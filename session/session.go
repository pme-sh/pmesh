package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"get.pme.sh/pmesh/concurrent"
	"get.pme.sh/pmesh/config"
	"get.pme.sh/pmesh/enats"
	"get.pme.sh/pmesh/lb"
	"get.pme.sh/pmesh/revision"
	"get.pme.sh/pmesh/rundown"
	"get.pme.sh/pmesh/security"
	"get.pme.sh/pmesh/service"
	"get.pme.sh/pmesh/snowflake"
	"get.pme.sh/pmesh/util"
	"get.pme.sh/pmesh/vhttp"
	"get.pme.sh/pmesh/xlog"
	"get.pme.sh/pmesh/xpost"

	"github.com/samber/lo"
)

type ServiceState struct {
	service.Instance
	name   string
	ctx    context.Context
	cancel context.CancelCauseFunc
	ID     snowflake.ID
}

func (s *ServiceState) Err() error {
	if s.ctx.Err() != nil {
		return context.Cause(s.ctx)
	}
	return nil
}
func (s *ServiceState) Shutdown(ctx context.Context) {
	if s.ctx.Err() != nil {
		return
	}
	xlog.InfoC(s.ctx).Msg("Service stopping")

	go func() {
		defer s.cancel(errors.New("shutdown"))
		s.Instance.Stop(ctx)
	}()

	select {
	case <-s.ctx.Done():
		xlog.InfoC(s.ctx).Msg("Service stopped")
	case <-ctx.Done():
		xlog.WarnC(s.ctx).Msg("Service took too long to stop")
		s.cancel(errors.New("shutdown"))
	}
}
func (s *ServiceState) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.Shutdown(ctx)
}
func (s *ServiceState) GetLoadBalancer() (*lb.LoadBalancer, bool) {
	if s.ctx.Err() == nil {
		if lb, ok := s.Instance.(service.InstanceLB); ok {
			return lb.GetLoadBalancer(), true
		}
	}
	return nil, false
}
func (s *ServiceState) GetProcessTrees() ([]service.ProcessTree, bool) {
	if s.ctx.Err() == nil {
		if proc, ok := s.Instance.(service.InstanceProc); ok {
			return proc.GetProcessTrees(), true
		}
	}
	return nil, false
}

type Session struct {
	ID      snowflake.ID
	Context context.Context
	Cancel  context.CancelFunc
	Server  *vhttp.Server

	Nats     *enats.Gateway
	Peerlist *xpost.Peerlist

	manifest          atomic.Pointer[Manifest]
	ManifestPath      string // immut
	ServiceMap        concurrent.Map[string, *ServiceState]
	TaskSubscriptions []context.CancelFunc
	util.TimedMutex
}

func (s *Session) Manifest() *Manifest {
	return s.manifest.Load()
}

func (s *Session) ResolveService(sv string) vhttp.Handler {
	service, ok := s.ServiceMap.Load(sv)
	if !ok {
		return nil
	}
	return service
}
func (s *Session) ResolveNats() *enats.Client {
	return s.Nats.Client
}

func New(path string) (s *Session, err error) {
	s = &Session{
		ManifestPath: path,
		ID:           snowflake.New(),
	}
	s.Context, s.Cancel = context.WithCancel(vhttp.WithStateResolver(context.Background(), s))

	// Acquire the lock
	err = config.TryLock()
	if err != nil {
		xlog.Err(err).Msg("Server already running")
		return
	}
	context.AfterFunc(s.Context, config.Unlock)

	// Kill any orphaned services
	service.KillOrphans()

	// Create the server
	s.Nats = enats.New()
	s.Server = vhttp.NewServer(s.Context)

	// Configure the logger
	level := xlog.LevelDebug
	if *config.Verbose {
		level = xlog.LevelTrace
	}
	xlog.SetLoggerLevel(level)
	xlog.SetDefaultOutput(xlog.StderrWriter(), xlog.FileWriter("session.log"))
	return
}
func (s *Session) StartService(name string, sv service.Service, invalidate bool) (*ServiceState, error) {
	uid := snowflake.New()
	logger := xlog.NewDomain(name)
	logger.UpdateContext(func(c xlog.Context) xlog.Context {
		return c.Stringer("uid", uid)
	})
	ctx, cancel := context.WithCancelCause(s.Context)
	ctx = logger.WithContext(ctx)

	xlog.InfoC(ctx).Msg("Service starting")
	instance, err := sv.Start(ctx, invalidate)
	state := &ServiceState{
		Instance: instance,
		name:     name,
		ctx:      ctx,
		cancel:   cancel,
		ID:       uid,
	}
	if err != nil {
		cancel(err)
		// If first instance, store anyway for observability
		s.ServiceMap.LoadOrStore(name, state)
		xlog.ErrC(ctx, err).Msg("Service failed to start")
		return nil, err
	}
	if prevState, ok := s.ServiceMap.Swap(name, state); ok {
		go prevState.Stop()
	}
	xlog.InfoC(ctx).Msg("Service started")
	return state, nil
}

func (s *Session) Reload(invalidate bool) error {
	s.Lock()
	defer s.Unlock()
	return s.ReloadLocked(invalidate)
}
func (s *Session) StopService(match *string) int {
	wg := &sync.WaitGroup{}
	n := 0
	s.ServiceMap.Range(func(name string, sv *ServiceState) bool {
		if match != nil && *match != name {
			return true
		}
		n++
		wg.Add(1)
		go func() {
			defer wg.Done()
			sv.Stop()
		}()
		return true
	})
	wg.Wait()
	return n
}
func (s *Session) RestartService(match *string, invalidate bool) int {
	wg := &sync.WaitGroup{}
	n := 0
	for _, t := range s.Manifest().Services {
		name, sv := t.A, t.B
		if match != nil && *match != name {
			continue
		}
		n++
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.StartService(name, sv, invalidate)
		}()
	}
	wg.Wait()
	return n
}
func (s *Session) ReloadLocked(invalidate bool) error {
	// Load the manifest
	manifest, err := LoadManifest(s.ManifestPath)
	if err != nil {
		return err
	}
	s.manifest.CompareAndSwap(nil, manifest)

	// Set revision data where relevant
	os.Setenv("PM3_COMMIT", "")
	os.Setenv("PM3_BRANCH", "")
	if repo, err := revision.Open(manifest.Root); err == nil {
		if ref, err := repo.Head(); err == nil {
			os.Setenv("PM3_COMMIT", ref.Hash)
			os.Setenv("PM3_BRANCH", ref.Branch)
		}
	}

	// Create the IP info provider
	s.Server.SetIPInfoProvider(manifest.IPInfo.CreateProvider())

	// Load custom error pages
	if errs := manifest.CustomErrors; errs != "" {
		if !filepath.IsAbs(errs) {
			errs = filepath.Join(manifest.Root, errs)
		}

		tmp, err := vhttp.ParseErrorTemplates(os.DirFS(errs), "")
		if err != nil {
			return fmt.Errorf("failed to load custom error pages: %w", err)
		}
		s.Server.SetErrorTemplates(tmp)
	}

	// Create the virtual hosts
	vhosts := []*vhttp.VirtualHost{CreateAPIHost(s)}
	for _, sv := range manifest.Server {
		vhosts = append(vhosts, sv.CreateVirtualHost())
	}
	s.Server.SetHosts(vhosts...)

	// Initialize the jet stream
	if err := manifest.Jet.Init(context.Background(), s.Nats.Jet); err != nil {
		return err
	}

	// First we need to stop all the services that are not in the new manifest
	s.ServiceMap.Range(func(name string, sv *ServiceState) bool {
		if _, ok := manifest.Services.Get(name); !ok {
			sv.Stop()
		}
		return true
	})

	// Start all the services that are in the new manifest
	manifest.Services.ForEach(func(name string, sv service.Service) {
		s.StartService(name, sv, invalidate)
	})

	// Stop the previous listeners
	for _, sub := range s.TaskSubscriptions {
		sub()
	}

	// Start the listeners
	s.TaskSubscriptions = nil
	for subject, task := range manifest.Runners {
		ctx, err := task.Listen(s.Context, s.Nats, subject)
		if err != nil {
			return err
		}
		s.TaskSubscriptions = append(s.TaskSubscriptions, ctx)
	}

	s.manifest.Store(manifest)
	return nil
}

func (s *Session) Open() error {
	xlog.Info().Stringer("id", s.ID).Msg("Session started")
	security.ObtainCertificate(config.Get().Secret) // Ensure the certificate is ready before starting the server
	if err := s.Nats.Open(context.Background()); err != nil {
		xlog.Err(err).Msg("Failed to open nats")
		return err
	}

	s.Peerlist = xpost.NewPeerlist(s.Nats)
	if err := s.Peerlist.Open(s.Context); err != nil {
		xlog.Err(err).Msg("Failed to open peer list")
		return err
	}
	if peers := s.Peerlist.List(true); len(peers) > 0 {
		for _, peer := range peers {
			xlog.Info().Str("host", peer.Host).Str("ip", peer.IP).Float64("lat", peer.Lat).Float64("lon", peer.Lon).Str("country", peer.Country).Str("isp", peer.ISP).Msg("Discovered peer")
		}
	}

	s.Peerlist.AddSDSource(func(out map[string]any) {
		out["commit"] = os.Getenv("PM3_COMMIT")
		out["branch"] = os.Getenv("PM3_BRANCH")
		var healthyServices []string
		for _, sv := range s.Manifest().Services {
			if svc, ok := s.ServiceMap.Load(sv.A); ok {
				if svc.ctx.Err() != nil {
					continue
				}
				if lb, ok := svc.Instance.(service.InstanceLB); ok {
					if l := lb.GetLoadBalancer(); l != nil {
						if l.Healthy() {
							healthyServices = append(healthyServices, sv.A)
						}
					}
				} else {
					healthyServices = append(healthyServices, sv.A)
				}
			}
		}
		out["services"] = healthyServices
	})

	err := s.Server.Listen()
	if err != nil {
		xlog.Err(err).Msg("Failed to start server")
		return err
	}
	return nil
}

func (s *Session) Close() error {
	defer s.Cancel()
	s.ServiceMap.Range(func(name string, sv *ServiceState) bool {
		sv.cancel(errors.New("session ended"))
		return true
	})
	s.Server.Close()
	xlog.Info().Stringer("id", s.ID).Msg("Session ended")
	return nil
}
func (s *Session) Shutdown(ctx context.Context) {
	defer s.Close()

	// Stop each service.
	wg := &sync.WaitGroup{}
	s.ServiceMap.Range(func(name string, sv *ServiceState) bool {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sv.Shutdown(ctx)
		}()
		return true
	})
	select {
	case <-ctx.Done():
	case <-s.Context.Done():
	case <-lo.Async0(func() { wg.Wait() }):
	}

	// Stop the server.
	if s.Server != nil {
		if err := s.Server.Shutdown(ctx); err != nil {
			xlog.Error().Err(err).Msg("Failed to shutdown server")
		}
	}
	if s.Peerlist != nil {
		if err := s.Peerlist.Close(ctx); err != nil {
			xlog.Error().Err(err).Msg("Failed to close peer list")
		}
	}
	if s.Nats != nil {
		if err := s.Nats.Close(ctx); err != nil {
			xlog.Error().Err(err).Msg("Failed to close nats")
		}
	}
}

func Start(manifestPath string) {
	xlog.Info().Str("manifest", manifestPath).Str("host", config.Get().Host).Msg("Starting node")

	s, err := New(manifestPath)
	if err != nil {
		xlog.Err(err).Msg("Failed to start session")
		return
	}

	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s.Shutdown(ctx)
	}()
	if err = s.Open(); err != nil {
		xlog.Err(err).Msg("Failed to open session")
		return
	}
	if err = s.Reload(false); err != nil {
		xlog.Err(err).Msg("Failed to load manifest")
		return
	}

	select {
	case <-rundown.Signal:
	case <-s.Context.Done():
	}
}
