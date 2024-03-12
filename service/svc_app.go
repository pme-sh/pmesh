package service

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"get.pme.sh/pmesh/config"
	"get.pme.sh/pmesh/cpuhist"
	"get.pme.sh/pmesh/glob"
	"get.pme.sh/pmesh/health"
	"get.pme.sh/pmesh/lb"
	"get.pme.sh/pmesh/security"
	"get.pme.sh/pmesh/util"
	"get.pme.sh/pmesh/vhttp"
	"get.pme.sh/pmesh/xlog"

	"github.com/samber/lo"
	"github.com/shirou/gopsutil/v3/process"
)

type AppService struct {
	Options
	Monitor          health.Monitor     `yaml:"monitor,omitempty"`           // The health monitor.
	LoadBalancerOpts lb.Options         `yaml:"lb,omitempty"`                // The load balancer.
	Root             string             `yaml:"root,omitempty"`              // The root directory of the app.
	Run              Command            `yaml:"run,omitempty"`               // The command to run the app.
	Build            util.Some[Command] `yaml:"build,omitempty"`             // The command to build the app.
	Shutdown         util.Some[Command] `yaml:"shutdown,omitempty"`          // The command to shutdown the app.
	Cluster          string             `yaml:"cluster,omitempty"`           // The number of instances to run.
	ClusterMin       string             `yaml:"cluster_min,omitempty"`       // The minimum number of instances to run.
	Env              map[string]string  `yaml:"env,omitempty"`               // The environment variables to set.
	EnvHost          string             `yaml:"env_host,omitempty"`          // The environment variable for the host.
	EnvPort          string             `yaml:"env_port,omitempty"`          // The environment variable for the port.
	EnvListen        string             `yaml:"env_listen,omitempty"`        // The environment variable for the address.
	ReadyTimeout     util.Duration      `yaml:"ready_timeout,omitempty"`     // The timeout for the app to become ready.
	StopTimeout      util.Duration      `yaml:"stop_timeout,omitempty"`      // The timeout for the app to stop.
	NoBuildControl   bool               `yaml:"no_build_control,omitempty"`  // If true, linking logic between .run and .build will be disabled.
	Background       bool               `yaml:"background,omitempty"`        // If true, the app is not a HTTP server.
	LogFile          string             `yaml:"log,omitempty"`               // The log file for stdout&stderr, default = app.log
	UnhealtyTimeout  util.Duration      `yaml:"unhealthy_timeout,omitempty"` // The timeout after which an unhealthy instance is killed.
	SlowStart        bool               `yaml:"slow_start,omitempty"`        // If true, instances are started one by one.
	MaxMemory        util.Size          `yaml:"max_memory,omitempty"`        // Maximum amount of memory the process is allowed to use, <= 0 means unlimited.
	AutoScale        bool               `yaml:"auto_scale,omitempty"`        // If true, the app will be auto-scaled.
	AutoScaleStreak  int                `yaml:"auto_scale_streak,omitempty"` // The number of consecutive ticks to trigger auto-scaling.
	AutoScaleDefer   util.Duration      `yaml:"auto_scale_defer,omitempty"`  // The time to wait until considering a process in auto-scaling.
	UpscalePercent   float64            `yaml:"upscale_percent,omitempty"`   // The percentage of CPU usage to trigger upscale.
	DownscalePercent float64            `yaml:"downscale_percent,omitempty"` // The percentage of CPU usage to trigger downscale.
	Stdin            bool               `yaml:"stdin,omitempty"`             // If true, the app will read from stdin.
	cluterN          int
	clusterMin       int
}

var DefaultRunEnv = map[string]string{
	"PM3":              "1",
	"DEBIAN_FRONTEND":  "noninteractive",
	"NODE_ENV":         "production",
	"npm_config_prod":  "1",
	"NODE_NO_WARNINGS": "1",
	"FLASK_DEBUG":      "0",
}
var DefaultBuildEnv = map[string]string{
	"YARN_PRODUCTION": "1",
	"PM3_BUILDING":    "1",
}

func (app *AppService) String() string {
	r := fmt.Sprintf("Exec{Root: %s, Run: %v, Build: %v}", app.Root, app.Run, app.Build)
	if app.cluterN > 1 {
		r += fmt.Sprintf("x%d", app.cluterN)
	}
	return r
}

const (
	lowercaseRunes    = "abcdefghijklmnopqrstuvwxyz"
	uppercaseRunes    = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	digitRunes        = "0123456789"
	alphanumericRunes = lowercaseRunes + uppercaseRunes + digitRunes
	nameRunes         = alphanumericRunes + "-_."
)

func (app *AppService) Prepare(opt Options) error {
	if opt.Name == "" {
		return fmt.Errorf("app name is required")
	}
	for _, chr := range opt.Name {
		if !strings.ContainsRune(nameRunes, chr) {
			return fmt.Errorf("invalid character in app name %q", opt.Name)
		}
	}

	app.Options = opt
	if app.LogFile == "" {
		app.LogFile = opt.Name + ".log"
	}
	app.EnvHost = cmp.Or(app.EnvHost, "HOST")
	app.EnvListen = cmp.Or(app.EnvListen, "LISTEN")
	app.EnvPort = cmp.Or(app.EnvPort, "PORT")
	if app.Root == "" {
		app.Root = filepath.Join(opt.ServiceRoot, opt.Name)
	} else if !filepath.IsAbs(app.Root) {
		app.Root = filepath.Join(opt.ServiceRoot, app.Root)
	}

	crange := [2]string{app.Cluster, app.ClusterMin}
	irange := [2]int{}
	for i, cluster := range crange {
		if cluster == "" {
			irange[i] = 1
		} else if percent, ok := strings.CutSuffix(cluster, "%"); ok {
			if n, err := strconv.Atoi(percent); err == nil {
				irange[i] = (n * cpuhist.NumCPU) / 100
			} else {
				return fmt.Errorf("invalid cluster value %q (%w)", cluster, err)
			}
		} else if n, err := strconv.Atoi(cluster); err == nil {
			irange[i] = n
		} else {
			return fmt.Errorf("invalid cluster value %q (%w)", cluster, err)
		}
		irange[i] = max(irange[i], 1)
	}
	app.cluterN, app.clusterMin = irange[0], irange[1]
	if app.cluterN < app.clusterMin {
		app.cluterN = app.clusterMin
	}

	// We need at least one monitor for startup checks.
	if len(app.Monitor.Checks) == 0 && !app.Background {
		app.Monitor.Checks = map[string]health.Checker{
			"tcp": health.NewChecker(&health.TcpCheck{}),
		}
	}
	app.ReadyTimeout = app.ReadyTimeout.Or(30 * time.Second)
	app.StopTimeout = app.StopTimeout.Or(10 * time.Second)

	// If we are auto-scaling, set the default values.
	if app.AutoScale {
		if app.cluterN == 1 {
			app.AutoScale = false
		} else {
			app.AutoScaleDefer = app.AutoScaleDefer.Or(30 * time.Second)
			if app.AutoScaleStreak <= 0 {
				app.AutoScaleStreak = 30
			}
			if app.UpscalePercent <= 0 {
				app.UpscalePercent = 0.8
			}
			if app.DownscalePercent == 0 {
				app.DownscalePercent = 0.02
			}
		}
	} else {
		app.clusterMin = app.cluterN
	}
	return nil
}

func (app *AppService) createCmd(c context.Context, cmd *Command, build bool, chk glob.Checksum) (g GluedCommand, err error) {
	cmd = cmd.Clone()
	cmd.Env["PM3_BUILD"] = chk.String()
	rootca := config.CertDir.File(security.GetSecretHash(config.Get().Secret) + "root.crt")
	cmd.Env["ROOT_CA"] = rootca
	cmd.Env["NODE_EXTRA_CA_CERTS"] = rootca

	if build {
		cmd.MergeEnv(DefaultBuildEnv)
	}
	cmd.MergeEnv(DefaultRunEnv)
	cmd.MergeEnv(app.Env)
	g.Cmd = cmd.Create(app.Root, c)

	if f := xlog.FileWriter(app.LogFile); f != nil {
		log := xlog.NewDomain(app.Options.Name, f)
		wstdout, enc := xlog.ToTextWriter(log, xlog.LevelInfo)
		wstderr, ence := xlog.ToTextWriter(log, xlog.LevelError)
		context.AfterFunc(c, func() {
			ence.Flush()
			enc.Flush()
			f.Close()
		})
		g.Stdout = wstdout
		g.Stderr = wstderr
	} else {
		g.Stdout = nil
		g.Stderr = nil
	}
	if app.Stdin && !build {
		g.Stdin = os.Stdin
	} else {
		g.Stdin = nil
	}
	return
}
func (app *AppService) execCmd(c context.Context, cmd *Command, build bool, chk glob.Checksum) (GluedCommand, error) {
	if cmd.IsZero() {
		return GluedCommand{}, nil
	}
	sub, cancel := context.WithCancel(c)
	defer cancel()
	x, err := app.createCmd(sub, cmd, build, chk)
	if err != nil {
		return GluedCommand{}, fmt.Errorf("failed to create command %q: %w", cmd.String(), err)
	}
	err = x.Run()
	if err != nil {
		err = fmt.Errorf("failed to run command %q: %w", cmd.String(), err)
	}
	return x, err
}

func (app *AppService) runBuilder(chk glob.Checksum, c context.Context) error {
	t0 := time.Now()
	for _, cmd := range app.Build {
		_, e := app.execCmd(c, &cmd, true, chk)
		if e != nil {
			return e
		}
	}
	app.Logger.Info().Dur("time", time.Since(t0)).Hex("chk", chk[:4]).Msg("Build finished")
	return nil
}
func (app *AppService) ShutdownApp(c context.Context, chk glob.Checksum) error {
	if len(app.Shutdown) == 0 {
		return nil
	}
	t0 := time.Now()
	for _, cmd := range app.Shutdown {
		_, e := app.execCmd(c, &cmd, false, chk)
		if e != nil {
			return e
		}
	}
	app.Logger.Info().Dur("time", time.Since(t0)).Msg("Shutdown finished")
	return nil
}

func (app *AppService) BuildApp(c context.Context, force bool) (chk glob.Checksum, err error) {
	if len(app.Build) == 0 {
		return
	}

	// If we are not controlling the build, just run it.
	//
	if app.NoBuildControl {
		err = app.runBuilder(chk, c)
		return
	}

	// Create a checksum.
	//
	chk = glob.Hash(app.Root, glob.IgnoreArtifacts(), glob.AddGitIgnores(app.Root)).All()

	// Check the cache.
	//
	bfs := BuildFS{Root: app.Root}
	if !force {
		prev, _ := bfs.ReadBuildId()
		if prev == chk {
			app.Logger.Info().Hex("chk", chk[:4]).Msg("Skipping build")
			return
		}
		app.Logger.Info().Hex("chk", chk[:4]).Hex("prev", prev[:4]).Msg("Building")
	} else {
		app.Logger.Info().Hex("chk", chk[:4]).Msg("Rebuilding")
	}

	// Build the app.
	//
	err = bfs.RunBuild(chk, func() error {
		return app.runBuilder(chk, c)
	})
	return
}

type noopServer struct{ *AppService }

func (noopServer) ServeHTTP(http.ResponseWriter, *http.Request) vhttp.Result {
	return vhttp.Continue
}
func (n noopServer) Stop(c context.Context) {
	if n.AppService != nil {
		n.ShutdownApp(c, glob.Checksum{})
	}
}

func (app *AppService) Start(c context.Context, invaliate bool) (instance Instance, err error) {
	var chk glob.Checksum
	for i := 0; i < 2; i++ {
		// Build the app.
		if chk, err = app.BuildApp(c, invaliate || i > 0); err != nil {
			return
		}

		// If there's nothing to run, return a null instance.
		if app.Run.IsZero() {
			return noopServer{app}, nil
		}

		// Create the app runner.
		runner := &AppServer{
			AppService: app,
			Checksum:   chk,
			Context:    c,
			ticker:     time.NewTicker(1 * time.Second),
		}
		cancel := context.AfterFunc(c, runner.ticker.Stop)
		if !app.Background {
			runner.LoadBalancer = &lb.LoadBalancer{
				Options: app.LoadBalancerOpts,
			}
			runner.LoadBalancer.SetLogger(xlog.NewDomain(app.Name + ".lb"))
		}

		// If we succeed, return the runner.
		if err = runner.init(); err == nil {
			instance = runner
			break
		}

		// If we fail, cancel the context and try again if we can.
		cancel()
	}
	return
}

type appProcessState struct {
	// Constant
	cfg      *AppService
	proc     *process.Process
	ctx      context.Context
	die      context.CancelCauseFunc
	upstream *lb.Upstream
	logger   *xlog.Logger

	// Shared variable state
	terminateDeadline atomic.Int64
	signalSent        atomic.Bool

	// Variable state exclusively for ticker
	downTicks int32
}

func (state *appProcessState) dead() bool {
	return state.ctx.Err() != nil
}
func (state *appProcessState) terminating() bool {
	return state.terminateDeadline.Load() != 0
}
func (state *appProcessState) bumpDeadline(intot time.Time) bool {
	// If the given deadline is in the past, return timeout.
	now := time.Now()
	if intot.Before(now) {
		state.terminateDeadline.Store(1)
		return true
	}

	// Constraint the deadline to the stop timeout.
	limit := now.Add(state.cfg.StopTimeout.Duration())
	if intot.After(limit) {
		intot = limit
	}

	// Try to set the deadline.
	into := intot.UnixMilli()
	for {
		prev := state.terminateDeadline.Load()
		if prev != 0 {
			// If previous deadline precedes the new deadline, return it's status.
			prevt := time.UnixMilli(prev)
			if !prevt.After(intot) {
				return prevt.Before(now)
			}
		}

		// Try to update the deadline.
		if state.terminateDeadline.CompareAndSwap(prev, into) {
			return false // No timeout.
		}
	}
}
func (state *appProcessState) requestTermination() {
	if state.dead() {
		return
	}
	intr := false
	if running, err := state.proc.IsRunning(); running || err != nil {
		pr, err := os.FindProcess(int(state.proc.Pid))
		if pr != nil && err == nil {
			intr = pr.Signal(os.Interrupt) == nil
		}
	}
	term := state.proc.Terminate()
	if !intr && term != nil {
		state.die(errors.New("killed (signalerr)"))
	}
}
func (state *appProcessState) drain() bool {
	for state.upstream.LoadFactor.Load() > 0 {
		deadline := time.UnixMilli(state.terminateDeadline.Load())
		halfDeadline := time.Until(deadline) / 2
		if halfDeadline < 200*time.Millisecond {
			return false // Already timed out.
		}
		select {
		case <-time.After(200 * time.Millisecond):
			continue
		case <-state.ctx.Done():
			return true // Process is dead so not much to do.
		}
	}
	return true // Drained.
}
func (state *appProcessState) tryTerminate(ctx context.Context) bool {
	if state.dead() {
		return true // Already terminated.
	}

	// Update upstream.
	if state.upstream != nil {
		state.upstream.SetHealthy(false)
	}

	// Update the deadline.
	var timeout bool
	if deadline, ok := ctx.Deadline(); ok {
		timeout = state.bumpDeadline(deadline)
	} else if dl := state.terminateDeadline.Load(); dl == 0 {
		deadline := time.Now().Add(state.cfg.StopTimeout.Duration())
		timeout = state.bumpDeadline(deadline)
	} else {
		timeout = dl < time.Now().UnixMilli()
	}
	if timeout {
		state.die(errors.New("killed (timeout)"))
		return true // Timeout.
	}

	// Send a signal if it hasn't been sent yet.
	if !state.signalSent.Swap(true) {
		// If there is an upstream serving requests, wait for it to drain, then signal.
		if state.upstream != nil {
			go func() {
				state.drain()
				state.requestTermination()
			}()
		} else {
			state.requestTermination()
		}
	}
	return false
}
func (state *appProcessState) terminate(ctx context.Context) {
	if state.tryTerminate(ctx) {
		return
	}
	for {
		select {
		case <-state.ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
			if state.tryTerminate(context.Background()) {
				return
			}
		}
	}
}
func (state *appProcessState) getMemoryUsage() uint64 {
	if state.dead() {
		return 0
	}
	rss := uint64(0)
	for _, proc := range NewProcessTree(state.proc).Tree {
		if mem, err := proc.MemoryInfo(); err == nil {
			rss += mem.RSS
		}
	}
	return rss
}
func (state *appProcessState) getCPUUsage() float64 {
	if state.dead() {
		return 0
	}
	cpu := 0.0
	for _, proc := range NewProcessTree(state.proc).Tree {
		cpu += cpuhist.GetUsePercentage(proc)
	}
	return cpu * 100
}

type AppServer struct {
	*AppService
	Checksum     glob.Checksum
	Context      context.Context
	LoadBalancer *lb.LoadBalancer
	ticker       *time.Ticker
	mu           sync.Mutex
	processes    []*appProcessState
}

func (run *AppServer) spawnProcess(initialProcess bool) (err error) {
	pctx, die := context.WithCancelCause(run.Context)
	defer func() {
		if err != nil {
			die(err)
		}
	}()

	cmd, err := run.createCmd(pctx, &run.Run, false, run.Checksum)
	if err != nil {
		return
	}

	// Allocate an IP address and create the upstream.
	var upstream *lb.Upstream
	if run.LoadBalancer != nil {
		const port = 70
		ip, err := SubnetAllocator().AllocateContext(pctx, port)
		if err != nil {
			return err
		}

		// Create the upstream.
		host := ip.String()
		address := fmt.Sprintf("%s:%d", host, port)
		upstream = lb.NewHttpUpstream(address)
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", run.EnvHost, host))
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", run.EnvListen, address))
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%d", run.EnvPort, port))
	}

	// Start the app.
	if err = cmd.Start(); err != nil {
		run.Logger.Err(err).Msg("Failed to start app")
		return
	}

	// If context is cancelled, kill the process tree.
	proc := cmd.Process
	pid := proc.Pid
	pproc := lo.Must(process.NewProcess(int32(pid)))
	context.AfterFunc(pctx, func() {
		NewProcessTree(pproc).Kill()
		proc.Kill()
	})

	// Create the state.
	//
	state := &appProcessState{
		cfg:      run.AppService,
		proc:     pproc,
		ctx:      pctx,
		die:      die,
		upstream: upstream,
		logger:   xlog.NewDomain(fmt.Sprintf("%s.%d", run.Name, pid)),
	}
	logger := state.logger
	logger.Info().Msg("Process started")
	run.mu.Lock()
	run.processes = append(run.processes, state)
	run.mu.Unlock()

	// Monitor the process exit.
	go func() {
		err := cmd.Wait()
		if err == nil {
			err = errors.New("success")
		}
		die(err)
		err = context.Cause(pctx)

		if upstream != nil {
			run.LoadBalancer.RemoveUpstream(upstream)
		}
		logger.Info().Err(err).Msg("Process exited")
	}()

	// If there's an upstream:
	if upstream != nil {
		// Wait until it becomes healthy.
		if initialProcess {
			readyCtx, cancel := context.WithTimeout(pctx, run.ReadyTimeout.Duration())
			defer cancel()
			for {
				logger.Info().Msg("Waiting for app to become healthy")
				healthy := run.Monitor.Check(readyCtx, logger, upstream.Address)
				if healthy {
					logger.Info().Str("address", upstream.Address).Msg("App started and healthy")
					upstream.SetHealthy(true)
					break
				}
				select {
				case <-pctx.Done():
					return context.Cause(pctx)
				case <-readyCtx.Done():
					err = errors.New("timed out waiting for app to become healthy")
					die(err)
					return
				case <-time.After(500 * time.Millisecond):
				}
			}
		} else {
			upstream.SetHealthy(false) // Assume unhealthy, let the monitor decide.
		}

		// Monitor the health of the instance.
		if timeout := run.UnhealtyTimeout.Or(10 * time.Second).Duration(); timeout > 0 {
			var timer *time.Timer
			run.Monitor.Observe(pctx, logger, upstream.Address, health.ObserverFunc(func(healthy bool) {
				upstream.SetHealthy(healthy)
				if !healthy {
					if timer == nil {
						timer = time.AfterFunc(timeout, func() {
							if !upstream.Healthy.Load() {
								logger.Warn().Str("address", upstream.Address).Msg("Unhealthy instance did not recover, killing it")
								state.tryTerminate(context.Background())
							}
						})
					}
				} else {
					if timer != nil {
						timer.Stop()
						timer = nil
					}
				}
			}))
		} else {
			run.Monitor.Observe(pctx, logger, upstream.Address, upstream)
		}

		// Add the upstream to the load balancer.
		run.LoadBalancer.AddUpstream(upstream)
	}
	return
}
func (run *AppServer) getProcesses() (res []*appProcessState) {
	run.mu.Lock()
	run.processes = lo.Filter(run.processes, func(state *appProcessState, _ int) bool {
		return !state.dead()
	})
	res = slices.Clone(run.processes)
	run.mu.Unlock()
	return
}

func (run *AppServer) tick(yield func() bool) {
	upTicks := 0
tick_loop:
	for yield() {
		list := run.getProcesses()

		// If there's no running instances, spawn one and continue.
		if _, anyRunning := lo.Find(list, func(proc *appProcessState) bool { return !proc.terminating() }); !anyRunning {
			// Wait for termination to complete.
			if len(list) != 0 {
				continue
			}
			if err := run.spawnProcess(true); err != nil {
				run.Logger.Err(err).Msg("Failed to spawn instance")
			}
			continue
		}

		// If we fail on memory overuse, terminate where relevant.
		if run.MaxMemory.IsPositive() {
			for _, proc := range list {
				if proc.terminating() {
					continue
				}
				if mem := proc.getMemoryUsage(); mem > uint64(run.MaxMemory) {
					proc.logger.Warn().Stringer("max", run.MaxMemory).Stringer("rss", util.Size(mem)).Msg("Memory usage exceeded")
					proc.tryTerminate(context.Background())
				}
			}
		}

		// If we're below the minimum amount, match it.
		for count := len(list); count < run.clusterMin; count++ {
			if err := run.spawnProcess(false); err != nil {
				run.Logger.Err(err).Msg("Failed to spawn instance")
				break
			}
			if run.SlowStart {
				continue tick_loop
			}
		}

		// If we're autoscaling:
		if run.AutoScale {
			var up, down, neutral int
			usageList := make([]float64, 0, len(list))
			for _, proc := range list {
				if proc.terminating() {
					continue
				}

				// If process is very recent, skip it.
				ct, _ := proc.proc.CreateTime()
				if ct > time.Now().Add(-run.AutoScaleDefer.Duration()).UnixMilli() {
					neutral++
					continue
				}

				// Update the ticks.
				usage := proc.getCPUUsage()
				usageList = append(usageList, usage)
				if usage > run.UpscalePercent {
					proc.downTicks = 0
					up++
				} else if usage < run.DownscalePercent && run.DownscalePercent >= 0 {
					proc.downTicks++
					down++
				} else {
					proc.downTicks = 0
					neutral++
				}
			}

			// If every instance is > downscale and most are > upscale, uptick.
			if down == 0 && up > neutral {
				upTicks++
			} else {
				upTicks = 0
			}

			// If upticks reached the threshold and we have less than N instances, spawn one.
			total := up + down + neutral
			if upTicks >= run.AutoScaleStreak && total < run.cluterN {
				run.Logger.Info().Int("total", total).Int("up", up).Int("down", down).Int("neutral", neutral).Floats64("usage", usageList).Msg("Auto-scaling up")
				if err := run.spawnProcess(false); err != nil {
					run.Logger.Err(err).Msg("Failed to spawn instance")
				}
				continue
			}

			// If total is above min, and we're in a down tick, terminate one.
			if total > run.clusterMin && down > neutral && up == 0 {
				for _, proc := range list {
					if proc.terminating() {
						continue
					}
					if proc.downTicks >= int32(run.AutoScaleStreak) {
						run.Logger.Info().Int("total", total).Int("up", up).Int("down", down).Int("neutral", neutral).Floats64("usage", usageList).Msg("Auto-scaling down")
						proc.tryTerminate(context.Background())
						break
					}
				}
			}
		}
	}
}
func (run *AppServer) init() error {
	// Spawn one instance.
	err := run.spawnProcess(true)
	if err != nil {
		return err
	}

	// Start the ticker.
	go func() {
		run.tick(func() bool {
			select {
			case <-run.Context.Done():
				return false
			case <-run.ticker.C:
				return true
			}
		})
	}()
	return nil
}

func (run *AppServer) ServeHTTP(w http.ResponseWriter, r *http.Request) vhttp.Result {
	if run.LoadBalancer == nil {
		vhttp.Error(w, r, http.StatusNotFound)
		return vhttp.Done
	}
	run.LoadBalancer.ServeHTTP(w, r)
	return vhttp.Done
}
func (run *AppServer) Stop(c context.Context) {
	// Stop the ticker and shutdown the app.
	run.ticker.Stop()
	run.ShutdownApp(c, run.Checksum)

	// Terminate all processes.
	wg := sync.WaitGroup{}
	ctx, cancel := context.WithTimeout(c, run.StopTimeout.Duration())
	defer cancel()
	for _, proc := range run.getProcesses() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			proc.terminate(ctx)
		}()
	}
	wg.Wait()
}
func (run *AppServer) GetLoadBalancer() *lb.LoadBalancer {
	return run.LoadBalancer
}
func (run *AppServer) GetProcessTrees() (res []ProcessTree) {
	return lo.Map(run.getProcesses(), func(s *appProcessState, _ int) (t ProcessTree) { return NewProcessTree(s.proc) })
}

func init() {
	Registry.Define("App", func() any { return &AppService{} })
}
