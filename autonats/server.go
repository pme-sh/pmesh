package autonats

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"get.pme.sh/pmesh/tlsmux"

	natssrv "get.pme.sh/pnats/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/samber/lo"
)

type Server struct {
	s      atomic.Pointer[natssrv.Server]
	mux    *http.ServeMux
	cliurl string

	readych chan struct{}
	donech  chan struct{}

	ready, done bool
	mu          sync.Mutex
}

func (s *Server) Ready() <-chan struct{} { return s.readych }
func (s *Server) Done() <-chan struct{}  { return s.donech }

func (s *Server) markDead() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.done {
		close(s.donech)
	}
	if !s.ready {
		close(s.readych)
	}
	s.done = true
	s.ready = true
}
func (s *Server) markAlive() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ready {
		return
	}
	close(s.readych)
	s.ready = true
}

func (s *Server) ClientURL() string {
	if s.cliurl != "" {
		return s.cliurl
	}
	sv := s.Server()
	if sv == nil {
		return ""
	}
	return sv.ClientURL()
}
func (s *Server) InProcessConn() (net.Conn, error) {
	srv := s.Server()
	if srv == nil || !srv.Running() || !srv.ReadyForConnections(5*time.Second) {
		return nil, errors.New("nats server not ready")
	}
	return srv.InProcessConn()
}
func (s *Server) Connect(opts ...nats.Option) (*nats.Conn, error) {
	opts = append(opts, nats.InProcessServer(s))
	return nats.Connect("", opts...)
}
func (s *Server) Server() *natssrv.Server {
	return s.s.Load()
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if srv := s.Server(); srv == nil || !srv.Running() {
		http.Error(w, "Server not ready", http.StatusServiceUnavailable)
	} else {
		s.mux.ServeHTTP(w, r)
	}
}
func (s *Server) Shutdown(ctx context.Context) error {
	sv := s.Server()
	if sv == nil {
		return nil
	}

	sv.Shutdown()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-lo.Async0(sv.WaitForShutdown):
		s.s.Store(nil)
		return nil
	}
}
func (s *Server) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.Shutdown(ctx)
}

const ServerStartTimeout = 5 * time.Minute

type natsNetworkIntercept struct {
	cfg *tls.Config
}

func (i natsNetworkIntercept) DialTimeoutCause(network, address string, timeout time.Duration, cause string) (net.Conn, error) {
	d := net.Dialer{
		Timeout:   timeout,
		KeepAlive: -1,
	}
	netconn, err := d.Dial(network, address)
	if err != nil {
		return nil, err
	}
	cli := tlsmux.Client(netconn, i.cfg, "nats-"+cause)
	if err := cli.Handshake(); err != nil {
		netconn.Close()
		return nil, err
	}
	return cli, nil
}
func (i natsNetworkIntercept) ListenCause(network, address, cause string) (net.Listener, error) {
	return tlsmux.Listen(network, address, i.cfg, "nats-"+cause)
}

func StartServer(opts Options) (srv *Server, err error) {
	opts.SetDefaults()
	logger := opts.Logger

	// Create the base options
	systemAccount := natssrv.NewAccount("$SYS")
	defaultAccount := natssrv.NewAccount("$G")
	defaultAccount.EnableJetStream(nil)
	base := &natssrv.Options{
		Trace:                  false,
		Debug:                  opts.Debug,
		TraceVerbose:           opts.Debug,
		Host:                   opts.Addr,
		Port:                   opts.Port,
		ClientAdvertise:        opts.Advertise,
		ServerName:             opts.ServerName,
		StoreDir:               opts.StoreDir,
		DisableJetStreamBanner: true,
		JetStream:              true,
		JetStreamMaxMemory:     -1,
		JetStreamMaxStore:      -1,
		Cluster: natssrv.ClusterOpts{
			Name:      opts.ClusterName,
			Host:      opts.Addr,
			Port:      opts.Port,
			Advertise: opts.Advertise,
		},
		LeafNode: natssrv.LeafNodeOpts{
			Host:      opts.Addr,
			Port:      opts.Port,
			Advertise: opts.Advertise,
		},
		Gateway: natssrv.GatewayOpts{
			Name:      opts.ClusterName,
			Host:      opts.Addr,
			Port:      opts.Port,
			Advertise: opts.Advertise,
		},
		Users: []*natssrv.User{
			{
				Username: "sys",
				Password: "sys",
				Account:  systemAccount,
			},
			{
				Username: "usr",
				Password: "usr",
				Account:  defaultAccount,
			},
		},
		Accounts: []*natssrv.Account{
			systemAccount,
			defaultAccount,
		},
		NoAuthUser:    "usr",
		SystemAccount: "$SYS",
	}
	base.NetworkIntercept = natsNetworkIntercept{
		cfg: opts.TLSConfig,
	}

	if opts.ClusterName == "" {
		logger.Info().Msg("Starting leaf node")
		for clusterName, servers := range opts.Topology {
			// skip seeding partition.
			if clusterName == "" {
				continue
			}
			rlo := &natssrv.RemoteLeafOpts{}
			for _, s := range servers {
				rlo.URLs = append(rlo.URLs, &url.URL{
					Scheme: "nats",
					Host:   net.JoinHostPort(s, strconv.Itoa(opts.Port)),
				})
			}
			base.LeafNode.Remotes = append(base.LeafNode.Remotes, rlo)
		}
		base.Routes = nil
		base.Gateway = natssrv.GatewayOpts{}
		base.Cluster = natssrv.ClusterOpts{}
	} else {
		logger.Info().Msg("Starting cluster node")
		for clusterName, servers := range opts.Topology {
			// skip seeding partition.
			if clusterName == "" {
				continue
			}
			if clusterName == opts.ClusterName {
				for _, s := range servers {
					base.Routes = append(base.Routes, &url.URL{
						Scheme: "nats",
						Host:   net.JoinHostPort(s, strconv.Itoa(opts.Port)),
					})
				}
			} else {
				gwo := &natssrv.RemoteGatewayOpts{
					Name: clusterName,
				}
				for _, s := range servers {
					gwo.URLs = append(gwo.URLs, &url.URL{
						Scheme: "nats",
						Host:   net.JoinHostPort(s, strconv.Itoa(opts.Port)),
					})
				}
				base.Gateway.Gateways = append(base.Gateway.Gateways, gwo)
			}
		}
	}

	// Create the server
	natss, err := natssrv.NewServer(base)
	if err != nil {
		return
	}
	natss.SetLogger(Logger{Logger: logger}, base.Debug, base.Trace)
	natss.Start()

	// Fill the HTTP Server.
	srv = &Server{
		readych: make(chan struct{}),
		donech:  make(chan struct{}),
	}
	srv.s.Store(natss)
	srv.mux = http.NewServeMux()
	srv.mux.HandleFunc(natssrv.RootPath, natss.HandleRoot)
	srv.mux.HandleFunc(natssrv.VarzPath, natss.HandleVarz)
	srv.mux.HandleFunc(natssrv.ConnzPath, natss.HandleConnz)
	srv.mux.HandleFunc(natssrv.RoutezPath, natss.HandleRoutez)
	srv.mux.HandleFunc(natssrv.GatewayzPath, natss.HandleGatewayz)
	srv.mux.HandleFunc(natssrv.LeafzPath, natss.HandleLeafz)
	srv.mux.HandleFunc(natssrv.SubszPath, natss.HandleSubsz)
	srv.mux.HandleFunc("/subscriptionsz", natss.HandleSubsz)
	srv.mux.HandleFunc(natssrv.StackszPath, natss.HandleStacksz)
	srv.mux.HandleFunc(natssrv.AccountzPath, natss.HandleAccountz)
	srv.mux.HandleFunc(natssrv.AccountStatzPath, natss.HandleAccountStatz)
	srv.mux.HandleFunc(natssrv.JszPath, natss.HandleJsz)
	srv.mux.HandleFunc(natssrv.HealthzPath, natss.HandleHealthz)
	srv.mux.HandleFunc(natssrv.IPQueuesPath, natss.HandleIPQueuesz)

	// Create the local listener, this is completely optional so we will not abort if it fails
	var localListener net.Listener
	if opts.LocalPort != -1 && opts.LocalAddr != "" {
		cfg := net.ListenConfig{
			KeepAlive: -1,
		}
		var lnErr error
		if opts.LocalPort == 0 {
			localListener, lnErr = cfg.Listen(context.Background(), "tcp", net.JoinHostPort(opts.LocalAddr, "4222"))
			if err != nil {
				localListener, lnErr = cfg.Listen(context.Background(), "tcp", net.JoinHostPort(opts.LocalAddr, "0"))
			}
		} else {
			localListener, lnErr = cfg.Listen(context.Background(), "tcp", net.JoinHostPort(opts.LocalAddr, strconv.Itoa(opts.LocalPort)))
		}
		if lnErr != nil {
			logger.Error().Err(lnErr).Msg("Failed to start local listener")
		} else {
			srv.cliurl = fmt.Sprintf("nats://%s", localListener.Addr())
			logger.Info().Msgf("Listening for client connections on %s", srv.cliurl)
			go func() {
				defer localListener.Close()
				for {
					conn, err := localListener.Accept()
					if err != nil {
						logger.Error().Err(err).Msg("Failed to accept local connection")
						select {
						case <-srv.donech:
							return
						case <-time.After(500 * time.Millisecond):
							continue
						}
					}
					go func() {
						err := natss.RegisterExternalConn(conn)
						if err != nil {
							logger.Error().Err(err).Msg("Failed to register external connection")
							conn.Close()
							return
						}
					}()
				}
			}()
		}
	}

	// Wait for the server to be done
	go func() {
		natss.WaitForShutdown()
		srv.markDead()
		if localListener != nil {
			localListener.Close()
		}
	}()

	// Wait for the server to be ready
	go func() {
		deadline := time.Now().Add(ServerStartTimeout)

		var conn *nats.Conn
		defer func() {
			if conn != nil {
				conn.Close()
			}
		}()

		for i := 0; ; i++ {
			if !natss.ReadyForConnections(5 * time.Second) {
				logger.Info().Err(err).Msg("Init: Waiting for NATS server to accept connections")
				if time.Now().After(deadline) {
					logger.Error().Msg("Timeout waiting for NATS server to become ready")
					srv.markDead()
					natss.Shutdown()
					return
				}
				continue
			}

			if conn == nil {
				var err error
				conn, err = srv.Connect(nats.Timeout(time.Until(deadline)))
				if err != nil {
					logger.Info().Err(err).Msg("Init: Waiting for NATS server to become ready")
					continue
				}
			}

			// Test jetstream cluster
			jsc, err := jetstream.New(conn)
			if err != nil {
				logger.Info().Err(err).Msg("Init: Waiting for Jetstream to become ready")
				continue
			}
			_, err = jsc.AccountInfo(context.Background())
			if err != nil {
				logger.Info().Err(err).Msg("Init: Waiting for Jetstream to become ready")
				continue
			}

			// Test jetstream node placement

			kv, err := jsc.CreateOrUpdateKeyValue(context.Background(), jetstream.KeyValueConfig{
				Bucket:      "pmesh-probe",
				Description: "pmesh-probe",
				TTL:         1 * time.Minute,
				Storage:     jetstream.MemoryStorage,
			})
			if err != nil {
				logger.Info().Err(err).Msg("Init: Waiting for Jetstream RAFT log to become ready (propose)")
				time.Sleep(1 * time.Second)
				continue
			}

			if _, err := kv.Put(context.Background(), "test", []byte{0x1}); err != nil {
				logger.Info().Err(err).Msg("Init: Waiting for Jetstream RAFT log to become ready (write)")
				time.Sleep(1 * time.Second)
				continue
			}

			logger.Info().Msg("Init: NATS server ready")
			srv.markAlive()
			return
		}
	}()
	return
}
