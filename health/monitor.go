package health

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/pme-sh/pmesh/util"
	"github.com/pme-sh/pmesh/xlog"
)

type MonitorLoop struct {
	Interval  util.Duration `yaml:"interval"`
	Threshold int           `yaml:"threshold"`
}

func (m MonitorLoop) Or(d time.Duration, t int) MonitorLoop {
	m.Interval = m.Interval.Or(d)
	if m.Threshold <= 0 {
		m.Threshold = t
	}
	return m
}

type Monitor struct {
	Healthy   MonitorLoop        `yaml:"healthy"`   // The loop for healthy checks.
	Unhealthy MonitorLoop        `yaml:"unhealthy"` // The loop for unhealthy checks.
	Timeout   util.Duration      `yaml:"timeout"`   // The timeout for each check.
	Checks    map[string]Checker `yaml:"test"`      // The checks to perform.
}

type Observer interface {
	SetHealthy(healthy bool)
}
type ObserverFunc func(healthy bool)

func (f ObserverFunc) SetHealthy(healthy bool) { f(healthy) }

func (m *Monitor) Check(ctx context.Context, logger *xlog.Logger, address string) bool {
	// If there are no checks, the service is considered healthy
	if m.Checks == nil || len(m.Checks) == 0 {
		return true
	}

	// Create a new context for the checks
	timeout := m.Timeout.Or(5 * time.Second).Duration()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Perform the checks
	counter := &atomic.Int64{}
	counter.Store(int64(len(m.Checks)))
	result := make(chan error, 1)
	for name, check := range m.Checks {
		go func(name string, check Checker) {
			err := check.Perform(ctx, address)
			if err == nil {
				if counter.Add(-1) == 0 {
					select {
					case result <- nil:
					default:
					}
				}
			} else {
				if logger != nil {
					logger.Warn().Str("name", name).Err(err).Msg("Healthcheck failed")
				}
				select {
				case result <- err:
				default:
				}
			}
		}(name, check)
	}

	// Wait for the checks to complete or timeout
	select {
	case err := <-result:
		return err == nil // Success
	case <-ctx.Done():
		return false // Timeout
	}
}

type HealthState int8

const (
	Healthy   HealthState = 1
	Unhealthy HealthState = -1
	Unknown   HealthState = 0
)

func (s HealthState) String() string {
	switch s {
	case Healthy:
		return "Healthy"
	case Unhealthy:
		return "Unhealthy"
	default:
		return "Unknown"
	}
}

func (m *Monitor) observe(ctx context.Context, logger *xlog.Logger, address string, observer Observer) {
	// Wait for the address to become available for the first time
	for {
		chk := TcpCheck{}
		if chk.Perform(ctx, address) == nil {
			break
		}
		if ctx.Err() != nil {
			observer.SetHealthy(false)
			return
		}
	}

	// Get the adjustedthresholds and intervals
	lunhealthy := m.Unhealthy.Or(5*time.Second, 3)
	lhealthy := m.Healthy.Or(5*lunhealthy.Interval.Duration(), 1)

	prevHealthy := Unknown
	state := lhealthy.Threshold - 1 // Current state
	for {
		// Perform the checks and update the state
		healthy := m.Check(ctx, logger, address)
		if healthy {
			state = max(state, 0) + 1
		} else {
			state = min(state, 0) - 1
		}

		// Early exit if the context is done
		if ctx.Err() != nil {
			return
		}

		// Update the health state
		newHealthy := Unknown
		interval := lhealthy.Interval
		if state >= lhealthy.Threshold {
			newHealthy = Healthy
		} else if state <= -lunhealthy.Threshold {
			newHealthy = Unhealthy
			interval = lunhealthy.Interval
		}

		// If it changed, notify the observer and log the change
		if newHealthy != prevHealthy {
			prevHealthy = newHealthy
			if newHealthy != Unknown {
				logger.Info().Stringer("state", newHealthy).Msg("Health state changed")
				observer.SetHealthy(newHealthy == Healthy)
			}
		}

		// Wait for the next interval
		select {
		case <-interval.After():
		case <-ctx.Done():
			return
		}
	}
}
func (m *Monitor) Observe(ctx context.Context, logger *xlog.Logger, address string, observer Observer) {
	if m.Checks == nil || len(m.Checks) == 0 {
		observer.SetHealthy(true)
		return
	}
	go m.observe(ctx, logger, address, observer)
}
