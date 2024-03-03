package lb

import (
	"context"
	"errors"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/pme-sh/pmesh/retry"
	"github.com/pme-sh/pmesh/vhttp"
	"github.com/pme-sh/pmesh/xlog"

	"github.com/samber/lo"
)

type requestContext struct {
	Request      *http.Request
	LoadBalancer *LoadBalancer
	Upstream     *Upstream
	Retrier      retry.Retrier
	Session      *vhttp.ClientSession
}

type requestContextKey struct{}
type stickySessionKey struct{ *LoadBalancer }

func (ctx *requestContext) stickyLoad() *Upstream {
	key := stickySessionKey{ctx.LoadBalancer}
	res, ok := ctx.Session.Values.Load(key)
	if !ok {
		return nil
	}
	return res.(*Upstream)
}
func (ctx *requestContext) stickyCompareAndSwap(old, new *Upstream) bool {
	key := stickySessionKey{ctx.LoadBalancer}
	return ctx.Session.Values.CompareAndSwap(key, old, new)
}

type LoadBalancer struct {
	Options   `yaml:",inline"`
	logger    *xlog.Logger `yaml:"-"`
	upstreams []*Upstream
	mu        sync.RWMutex
	counter   atomic.Uint32
}

type LoadBalancerMetrics struct {
	Upstreams []UpstreamMetrics `json:"upstreams,omitempty"`
}

func (lb *LoadBalancer) Healthy() bool {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	for _, u := range lb.upstreams {
		if u.Healthy.Load() {
			return true
		}
	}
	return false
}

func (lb *LoadBalancer) Metrics() LoadBalancerMetrics {
	us := lb.Upstreams()
	upstreams := make([]UpstreamMetrics, len(us))
	for i, u := range us {
		upstreams[i] = u.Metrics()
	}
	return LoadBalancerMetrics{
		Upstreams: upstreams,
	}
}

func (lb *LoadBalancer) getLogger() *xlog.Logger {
	if lb.logger == nil {
		return xlog.Default()
	}
	return lb.logger
}
func (lb *LoadBalancer) SetLogger(logger *xlog.Logger) {
	lb.logger = logger
}
func (lb *LoadBalancer) Upstreams() (us []*Upstream) {
	lb.mu.RLock()
	defer lb.mu.RUnlock()
	us = make([]*Upstream, len(lb.upstreams))
	copy(us, lb.upstreams)
	return
}
func (lb *LoadBalancer) ClearUpstreams() {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.upstreams = nil
}
func (lb *LoadBalancer) AddUpstream(u *Upstream) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.upstreams = append(lb.upstreams, u)
}
func (lb *LoadBalancer) RemoveUpstream(u *Upstream) {
	lb.mu.Lock()
	lb.upstreams = lo.Without(lb.upstreams, u)
	lb.mu.Unlock()
}

var ErrNoHealthyUpstreams = errors.New("no healthy upstreams")

func (lb *LoadBalancer) getHealthyIdxLocked(n uint32, bad *Upstream) (res *Upstream) {
	for _, u := range lb.upstreams {
		if !u.Healthy.Load() {
			continue
		}
		// If we're at the bad index, ignore
		if u == bad {
			continue
		}
		// If we're at the index, return
		res = u
		if n == 0 {
			break
		}
		// Decrement the index
		n--
	}
	if res == nil {
		res = bad
	}
	return
}
func (lb *LoadBalancer) NextUpstream(ctx *requestContext) (result *Upstream, err error) {
	// If there is a bad upstream, we won't use least-conn
	strat := lb.Strategy
	bad := ctx.Upstream
	if bad != nil {
		strat = StrategyRandom
	}

	// Generate the "entropy"
	var entropy uint32
	switch strat {
	case StrategyHash:
		if ctx.Session.Local {
			entropy = lb.counter.Add(1)
		} else {
			entropy = ctx.Session.IPHash
		}
	case StrategyRandom:
		entropy = rand.Uint32()
	case StrategyRoundRobin:
		entropy = lb.counter.Add(1)
	}

	lb.mu.RLock()
	defer lb.mu.RUnlock()

	// Handle fixed cases.
	count := uint32(len(lb.upstreams))
	if count == 0 {
		return nil, nil
	} else if count == 1 {
		return lb.upstreams[0], nil
	}

	// If least-conn:
	if strat == StrategyLeastConn {
		result = lb.upstreams[0]
		best := result.LoadFactor.Load()
		if !result.Healthy.Load() {
			best = 0x7fffffff
		}
		for _, upstream := range lb.upstreams[1:] {
			if !upstream.Healthy.Load() {
				continue
			}
			conns := upstream.LoadFactor.Load()
			if conns < best {
				result, best = upstream, conns
			}
		}
		return
	}

	// Count the healthy upstreams, prefetch the first one.
	healthyN := uint32(0)
	var first *Upstream
	for _, upstream := range lb.upstreams {
		if upstream.Healthy.Load() {
			if healthyN == 0 {
				first = upstream
			}
			healthyN++
		}
	}

	// If there's just one healthy upstream, use it.
	if healthyN <= 1 {
		return first, nil
	}

	// Pick the entry given the entropy.
	result = lb.getHealthyIdxLocked(entropy%healthyN, bad)
	return
}
func (lb *LoadBalancer) PickUpstream(ctx *requestContext) (result *Upstream, err error) {
	defer func() {
		if err == nil && result == nil {
			err = ErrNoHealthyUpstreams
		}
	}()

	// If no state, just pick an upstream.
	if lb.State == StateNone {
		return lb.NextUpstream(ctx)
	}

	// Try the sticky session first.
	if result = ctx.stickyLoad(); result != nil {
		bad := ctx.Upstream
		if result != bad && result.Healthy.Load() {
			return result, nil
		}

		// If the last upstream was bad, clear it.
		ctx.stickyCompareAndSwap(result, nil)
	}

	// Pick the upstream and store it if it's good.
	//
	result, err = lb.NextUpstream(ctx)
	if result != nil {
		ctx.stickyCompareAndSwap(nil, result)
	}
	return
}

type lbRetryHandler struct {
	*requestContext
}

func (h lbRetryHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.LoadBalancer.serveHTTP(h.requestContext, w, r)
}

func (lb *LoadBalancer) OnErrorResponse(ctx *requestContext, r *http.Response) http.Handler {
	// Determine traits.
	retriable := false
	var opt *ErrorOptions
	switch {
	// 5xx
	case 500 <= r.StatusCode && r.StatusCode <= 599:
		opt = lb.Error5xx
		retriable = ctx.Request.Method == http.MethodGet
	// 4xx
	case r.StatusCode == 404:
		opt = lb.Error404
	case 400 <= r.StatusCode && r.StatusCode <= 499:
		opt = lb.Error4xx
	// ?
	default:
		break
	}

	if opt != nil {
		// If rate limited and this is not a retry:
		if !opt.Limit.IsZero() && ctx.Retrier.Step == 0 {
			// Enforce the rate limit, if it's exceeded return the error writer.
			if hnd := ctx.Session.EnforceRateReq(ctx.Request.Context(), r.Request, opt.Limit); hnd != nil {
				// Handled by the rate limiter.
				return hnd
			}
		}

		// If user has a special handler:
		buf := vhttp.NewBufferedResponse(nil)
		if opt.Handle != nil {
			switch opt.Handle.ServeHTTP(buf, ctx.Request) {
			// If handled, we're done.
			case vhttp.Done:
				return buf
			// Not handled.
			case vhttp.Continue:
			// Should not retry.
			case vhttp.Drop:
				retriable = false
			}
		}
	}

	// If bad request, we will never retry and it's not worth logging since it's the client's fault.
	if 400 <= r.StatusCode && r.StatusCode <= 499 {
		return nil
	}

	// If retriable:
	if retriable {
		// If we can retry:
		if retryError := ctx.Retrier.ConsumeAny(); retryError == nil {
			// Log and retry.
			lb.getLogger().Warn().Str("status", r.Status).Str("upstream", ctx.Upstream.Address).Msg("retriable server error")
			return lbRetryHandler{ctx}
		} else {
			// Log.
			lb.getLogger().Error().Str("status", r.Status).Err(retryError).Str("upstream", ctx.Upstream.Address).Msg("fatal server error")
		}
	}
	return nil
}
func (lb *LoadBalancer) OnError(ctx *requestContext, w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		return
	}
	// If we can retry:
	if retryError := ctx.Retrier.Consume(err); retryError == nil {
		// Log and retry.
		lb.getLogger().Warn().Err(err).Str("upstream", ctx.Upstream.Address).Msg("retriable upstream error")
		lb.serveHTTP(ctx, w, ctx.Request)
		return
	} else {
		lb.getLogger().Error().Err(err).Str("upstream", ctx.Upstream.Address).Msg("fatal upstream error")
	}
	vhttp.Error(w, ctx.Request, vhttp.StatusUpstreamError)
}

func (lb *LoadBalancer) serveHTTP(ctx *requestContext, w http.ResponseWriter, r *http.Request) {
	us, err := lb.PickUpstream(ctx)
	if err != nil {
		lb.OnError(ctx, w, r, err)
	} else {
		ctx.Upstream = us
		us.ServeHTTP(w, r)
	}
}

func (lb *LoadBalancer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := &requestContext{}
	defer func() {
		ctx.Request = nil
		ctx.Upstream = nil
		ctx.Session = nil
		ctx.LoadBalancer = nil
	}()

	cctx := context.WithValue(r.Context(), requestContextKey{}, ctx)
	r = r.WithContext(cctx)
	ctx.LoadBalancer = lb
	ctx.Retrier = lb.Retry.RetrierContext(cctx)
	ctx.Session = vhttp.ClientSessionFromContext(cctx)
	ctx.Upstream = nil
	ctx.Request = r
	lb.serveHTTP(ctx, w, r)
}
