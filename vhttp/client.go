package vhttp

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"get.pme.sh/pmesh/config"
	"get.pme.sh/pmesh/netx"
	"get.pme.sh/pmesh/rate"
	"get.pme.sh/pmesh/ray"
	"get.pme.sh/pmesh/xlog"
)

type ClientMetrics struct {
	AvgRps       float64   `json:"avg_rps"`
	NumReqs      int32     `json:"num_reqs"`
	FirstSeen    time.Time `json:"first_seen"`
	BlockedUntil time.Time `json:"blocked_until,omitempty"`
}

var Raygen = ray.NewGenerator(config.Get().Host)

type ClientSession struct {
	IP             netx.IP
	IPHash         uint32
	RemoteAddr     string   // ip:0, for setting request.RemoteAddr
	Values         sync.Map // For handler's use.
	firstRequestMs int64
	lastRequestMs  atomic.Int64
	NumRequests    atomic.Int32
	BlockedUntilMs atomic.Int64
	IPInfo         http.Header
	Local          bool
}

var LocalClientSession = &ClientSession{
	IP:         netx.ParseIP("127.0.0.1"),
	IPHash:     0x7f000001,
	RemoteAddr: "127.0.0.1:0",
	IPInfo:     netx.LocalIPInfoHeaders,
	Local:      true,
}

func (s *ClientSession) FirstRequest() time.Time { return time.UnixMilli(s.firstRequestMs) }
func (s *ClientSession) LastRequest() time.Time  { return time.UnixMilli(s.lastRequestMs.Load()) }

func (s *ClientSession) BlockUntil(t time.Time) time.Time {
	if s.Local {
		return time.Time{}
	}
	at := t.UnixMilli()
	for {
		bt := s.BlockedUntilMs.Load()
		if bt >= at {
			return time.UnixMilli(bt)
		}
		if s.BlockedUntilMs.CompareAndSwap(bt, at) {
			xlog.Warn().Str("ip", s.IP.String()).Time("until", t).Msg("Client blocked")
			return t
		}
	}
}
func (s *ClientSession) BlockFor(d time.Duration) time.Time {
	return s.BlockUntil(time.Now().Add(d))
}

func (s *ClientSession) Unblock() {
	s.BlockedUntilMs.Store(0)
}
func (s *ClientSession) IsBlocked() bool {
	return s.BlockedUntilMs.Load() > time.Now().UnixMilli()
}

func (s *ClientSession) Metrics() ClientMetrics {
	firstReq := s.FirstRequest()
	lastReq := s.LastRequest()
	numReq := s.NumRequests.Load()
	rps := 0.0
	total := lastReq.Sub(firstReq).Milliseconds()
	if numReq > 1 && total > 0 {
		rps = (float64(numReq) / float64(total)) * 1000
	}
	bt := time.UnixMilli(s.BlockedUntilMs.Load())
	if bt.Before(time.Now()) {
		bt = time.Time{}
	}
	return ClientMetrics{
		AvgRps:       rps,
		NumReqs:      numReq,
		FirstSeen:    firstReq,
		BlockedUntil: bt,
	}
}
func GetClientMetrics() (metrics map[string]ClientMetrics) {
	metrics = make(map[string]ClientMetrics, NumClients())
	ForEachSession(func(s *ClientSession) bool {
		metrics[s.IP.String()] = s.Metrics()
		return true
	})
	return
}

type laterHandler struct {
	advised time.Time
	advise  bool
}

func (h laterHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.advise {
		w.Header()["Retry-After"] = []string{h.advised.Format(http.TimeFormat)}
	}
	Error(w, r, http.StatusTooManyRequests)
}

type blockedHandler struct{}

func (blockedHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	Error(w, r, StatusWSFBlocked)
}

type rstHandler struct{}

func (rstHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	netx.ResetRequestConn(w)
}

func (s *ClientSession) EnforceRateReq(c context.Context, r *http.Request, l rate.Limit) http.Handler {
	if s.Local {
		return nil
	}
	err := l.EnforceVar(c, &s.Values)
	if err == nil {
		return nil
	}

	var re rate.RateError
	if errors.As(err, &re) {
		now := time.Now()
		blocked := false
		if re.BlockUntil > 0 {
			s.BlockUntil(now.Add(re.BlockUntil))
			blocked = true
		}

		evt := xlog.WarnC(c)
		if r != nil {
			evt = evt.EmbedObject(xlog.EnhanceRequest(r))
		}
		evt.Stringer("limit", &l).Dur("block", re.BlockUntil).Dur("retry", re.RetryAfter).Msg("Rate limit exceeded")

		if blocked {
			return blockedHandler{}
		} else {
			advised, ok := re.AdviseClient(now)
			return laterHandler{advised, ok}
		}
	} else {
		return rstHandler{}
	}
}
func EnforceRateReq(r *http.Request, l rate.Limit) http.Handler {
	return ClientSessionFromContext(r.Context()).EnforceRateReq(r.Context(), r, l)
}

func newClientSession(ctx context.Context, px netx.ProxyTraits, t int64, infoProvider netx.IPInfoProvider) (session *ClientSession) {
	hash := sha1.Sum(px.Origin.ToSlice())
	session = &ClientSession{
		IP:             px.Origin,
		IPHash:         binary.LittleEndian.Uint32(hash[:]),
		firstRequestMs: t,
		IPInfo:         make(http.Header, 5),
	}
	session.Local = px.Origin.IsLoopback() || px.Origin.IsPrivate()
	if !session.Local {
		ipstr := px.Origin.String()
		ipinfo := infoProvider.LookupContext(ctx, px.Origin)
		netx.SetIPInfoHeaders(session.IPInfo, ipstr, ipinfo)
		if px.CountryHint.IsValid() {
			session.IPInfo[netx.HdrIPGeo] = []string{px.CountryHint.String()}
		}
	} else {
		session.IPInfo = netx.LocalIPInfoHeaders
	}
	session.lastRequestMs.Store(t)
	session.RemoteAddr = netx.IPPort{IP: px.Origin, Port: 0}.String()
	return
}

type sessionContextKey struct{}

func (s *ClientSession) SetOnContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, sessionContextKey{}, s)
}

func ClientSessionFromContext(ctx context.Context) (session *ClientSession) {
	session, _ = ctx.Value(sessionContextKey{}).(*ClientSession)
	return
}

var sessionMap sync.Map //map[ip?]*ClientSession
var sessionCount atomic.Int32

func NumClients() int {
	return int(sessionCount.Load())
}
func ForEachSession(f func(*ClientSession) bool) {
	sessionMap.Range(func(_, v any) bool {
		return f(v.(*ClientSession))
	})
}
func GetClientSession(ip netx.IP) (session *ClientSession) {
	key := ipToKey(ip)
	sv, loaded := sessionMap.Load(key)
	if loaded {
		session = sv.(*ClientSession)
	}
	return
}

func (session *ClientSession) startRequest(ctx context.Context, t int64, r *http.Request) (rctx *http.Request) {
	// Update the session and request context.
	session.lastRequestMs.Store(t)
	session.NumRequests.Add(1)
	ctx = session.SetOnContext(ctx)
	rctx = r.WithContext(ctx)

	// If remote connection, or the local connection is not impersonating a remote connection:
	if !session.Local || len(rctx.Header[netx.HdrIP]) == 0 {
		// Inherit IP info from the session.
		for k, v := range session.IPInfo {
			rctx.Header[k] = v
		}
	}

	// Remove the forwarding headers, we already handled it for the app.
	rctx.RemoteAddr = session.RemoteAddr
	delete(rctx.Header, "X-Forwarded-For")
	return
}
func StartInternalRequest(r *http.Request) (rctx *http.Request) {
	rctx = LocalClientSession.startRequest(r.Context(), time.Now().UnixMilli(), r)
	if len(rctx.Header[netx.HdrRay]) == 0 {
		rctx.Header[netx.HdrRay] = []string{Raygen.Next()}
	}
	return
}

func StartClientRequest(r *http.Request, infoProvider netx.IPInfoProvider) (rctx *http.Request, session *ClientSession) {
	t := time.Now().UnixMilli()
	startCleaner.Do(func() {
		go func() {
			for range time.Tick(10 * time.Minute) {
				cleanupClientSessions()
			}
		}()
	})
	ctx := r.Context()

	// Resolve the proxy traits, retrieve or create the session.
	px := netx.ResolveProxyTraits(r)
	key := ipToKey(px.Origin)
	sv, loaded := sessionMap.Load(key)
	if !loaded {
		session = newClientSession(ctx, px, t, infoProvider)
		if ctx.Err() != nil {
			panic(http.ErrAbortHandler)
		}
		sv, loaded = sessionMap.LoadOrStore(key, session)
	}
	if !loaded {
		sessionCount.Add(1)
	} else {
		session = sv.(*ClientSession)
	}

	// Start the request.
	ray := Raygen.Next()
	rctx = session.startRequest(ctx, t, r)

	// Set the ray ID.
	rctx.Header[netx.HdrRay] = []string{ray}

	// Normalize scheme
	scheme := rctx.URL.Scheme
	if scheme == "" {
		if rctx.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	if fw := rctx.Header["X-Forwarded-Proto"]; len(fw) > 0 {
		switch proto := fw[0]; proto {
		case "http", "https":
			scheme = proto
		}
	}
	rctx.URL.Scheme = scheme
	rctx.Header["X-Forwarded-Proto"] = []string{scheme}

	// Set internal flag, stip secret from headers ASAP.
	internal := session.Local
	if !internal {
		// If we verified the peer certificate using the mutual authenticator, it's internal.
		if rctx.TLS != nil && len(rctx.TLS.PeerCertificates) != 0 && rctx.TLS.ServerName == "pm3" {
			internal = true
		} else if scheme == "https" {
			// If the request is over HTTPS and the secret is in the basic auth, it's internal.
			if u, pw, ok := rctx.BasicAuth(); ok {
				if u == config.Get().Secret || pw == config.Get().Secret {
					internal = true
					delete(rctx.Header, "Authorization")
				}
			}
		}
	}
	if internal {
		rctx.Header["P-Internal"] = []string{"1"}
	} else {
		delete(rctx.Header, "P-Internal")
	}
	delete(rctx.Header, "P-Portal")
	return
}

func ipToKey(ip netx.IP) any {
	if ip.IsV4() {
		return uint32(ip.Low)
	} else {
		return [2]uint64{ip.Low, ip.High}
	}
}

var startCleaner sync.Once

func cleanupClientSessions() {
	threshold := time.Now().Add(-30 * time.Minute).UnixMilli()
	cleanupCount := int32(0)
	sessionMap.Range(func(key any, v any) bool {
		session := v.(*ClientSession)
		if session.lastRequestMs.Load() >= threshold {
			return true
		}
		sessionMap.Delete(key)
		cleanupCount++
		return true
	})
	if cleanupCount > 0 {
		rem := sessionCount.Add(-cleanupCount)
		xlog.Info().Int32("count", cleanupCount).Int32("remaining", rem).Msg("Cleaned up client sessions")
	}
}
