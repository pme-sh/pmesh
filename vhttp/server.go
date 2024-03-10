package vhttp

import (
	"cmp"
	"context"
	"crypto/tls"
	"html/template"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"get.pme.sh/pmesh/config"
	"get.pme.sh/pmesh/hosts"
	"get.pme.sh/pmesh/netx"
	"get.pme.sh/pmesh/security"
	"get.pme.sh/pmesh/urlsigner"
	"get.pme.sh/pmesh/xlog"
	"golang.org/x/crypto/acme"
)

type ipinfoWrapper struct{ netx.IPInfoProvider }
type Server struct {
	context.Context
	logger               *xlog.Logger
	Server               http.Server
	ipInfoProvider       atomic.Pointer[ipinfoWrapper]
	errTemplatesOverride atomic.Pointer[template.Template]

	TopLevelMux

	killed       atomic.Bool
	wg           sync.WaitGroup
	listenerInfo netx.ListenerInfo
	Signer       *urlsigner.Signer
}

func (s *Server) Value(key any) any {
	if _, ok := key.(serverKey); ok {
		return s
	}
	return s.Context.Value(key)
}

func (s *Server) GetIPInfoProvider() netx.IPInfoProvider {
	return s.ipInfoProvider.Load()
}
func (s *Server) SetIPInfoProvider(provider netx.IPInfoProvider) {
	s.ipInfoProvider.Store(&ipinfoWrapper{provider})
}
func (s *Server) SetErrorTemplates(t *template.Template) {
	s.errTemplatesOverride.Store(t)
}

type serverKey struct{}

func GetServerFromContext(ctx context.Context) *Server {
	sv, _ := ctx.Value(serverKey{}).(*Server)
	return sv
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTPSession(w http.ResponseWriter, r *http.Request, session *ClientSession) {
	t0 := time.Now()
	ordered, _ := s.TopLevelMux.getGroups()
	logger := xlog.Ctx(r.Context())
	isPortal := len(r.Header["P-Portal"]) != 0
	if !session.Local && !isPortal {
		logger.Debug().EmbedObject(xlog.EnhanceRequest(r)).Msg("Request")
	}

	// Walk through each group, and try to handle the request.
	handled := false
selector:
	for _, group := range ordered {
		switch group.ServeHTTP(w, r) {
		case Done:
			handled = true
			break selector
		case Drop:
			netx.ResetRequestConn(w)
			handled = true
			break selector
		}
	}
	logger.Trace().EmbedObject(xlog.EnhanceRequest(r)).Msgf("Request took %s", time.Since(t0))

	// If no match, 404
	if !handled {
		Error(w, r, http.StatusNotFound)
	}
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if idx := strings.IndexByte(r.Host, ':'); idx >= 0 {
		r.Host = r.Host[:idx]
	}

	// Reset the connection if the host is not in the list.
	_, unique := s.TopLevelMux.getGroups()
	if !hosts.TestMap(unique, r.Host) {
		r.URL.Host = r.Host
		xlog.WarnC(r.Context()).EmbedObject(xlog.EnhanceRequest(r)).Msg("Resetting connection, no matching host")
		netx.ResetRequestConn(w)
		return
	}

	// Start the request.
	r, session := StartClientRequest(r, s.GetIPInfoProvider())
	if session == nil {
		panic(http.ErrAbortHandler)
	}

	// Set the ray header.
	ray := r.Header[netx.HdrRay]
	w.Header()[netx.HdrRay] = ray

	// Stop if blocked.
	if session.IsBlocked() {
		Error(w, r, StatusWSFBlocked)
		return
	}

	// Replicate the GeneralOptionsHandler behavior after the host check.
	if r.Method == http.MethodOptions && r.URL.Path == "*" {
		defer r.Body.Close()
		w.Header()["Content-Length"] = []string{"0"}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Clean the path.
	originalPath := r.URL.Path
	r.URL.Path = CleanPath(originalPath)
	r.URL.Host = r.Host

	// Guard against panics.
	cw := NewConditionalResponse(w)
	defer func() {
		if err := recover(); err == http.ErrAbortHandler {
			panic(err)
		} else if err != nil {
			xlog.ErrStackC(r.Context(), err).EmbedObject(xlog.EnhanceRequest(r)).Msg("Panic serving request")
			if !cw.Touched {
				Error(w, r, StatusPanic)
			} else {
				panic(http.ErrAbortHandler)
			}
		}
	}()

	// Handle signed urls.
	signed, err := s.Signer.Authenticate(r)
	if err != nil {
		Error(cw, r, StatusSignatureError)
		return
	} else if signed {
		r.Header["P-Internal"] = []string{"1"}
	}
	s.ServeHTTPSession(cw, r, session)
}

// NewServer returns a new Server.
func NewServer(ctx context.Context) (s *Server) {
	logger := xlog.NewDomain("http")
	ctx = logger.WithContext(ctx)
	s = &Server{
		logger: logger,
		Signer: urlsigner.New(config.Get().Secret),
	}
	s.Context = context.WithValue(ctx, serverKey{}, s)

	mauth := security.CreateMutualAuthenticator(config.Get().Secret, "h2", "http/1.1")
	logf, logw := xlog.ToTextWriter(logger, xlog.LevelError)
	s.Server = http.Server{
		Handler:                      s,
		DisableGeneralOptionsHandler: true,
		ReadHeaderTimeout:            10 * time.Second,
		ReadTimeout:                  20 * time.Second,
		WriteTimeout:                 60 * time.Second,
		MaxHeaderBytes:               1 << 20,
		BaseContext: func(l net.Listener) context.Context {
			return s.Context
		},
		ErrorLog: log.New(logf, "", 0),
		TLSConfig: mauth.WrapServer(&tls.Config{
			GetCertificate:           s.GetCertificate,
			PreferServerCipherSuites: true,
			CurvePreferences:         []tls.CurveID{tls.CurveP256, tls.X25519},
			NextProtos:               []string{"h2", "http/1.1", acme.ALPNProto},
		}),
	}
	s.Server.RegisterOnShutdown(func() { logw.Flush() })
	s.SetIPInfoProvider(netx.NullIPInfoProvider)
	return
}

// Listen starts the Server listening on the given address.
func (s *Server) serveHttp(ln net.Listener) {
	logger := s.logger.With().Str("proto", "http").Stringer("addr", ln.Addr()).Logger()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		logger.Info().Msg("Server started")
		err := s.Server.Serve(ln)
		if err != nil && !s.killed.Load() {
			logger.Err(err).Msg("Server error")
		} else {
			logger.Info().Msg("Server closed")
		}
	}()
}
func (s *Server) serveHttps(ln net.Listener) {
	logger := s.logger.With().Str("proto", "https").Stringer("addr", ln.Addr()).Logger()
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		logger.Info().Msg("Server started")
		err := s.Server.ServeTLS(ln, "", "")
		if err != nil && !s.killed.Load() {
			logger.Err(err).Msg("Server error")
		} else {
			logger.Info().Msg("Server closed")
		}
	}()
}
func (s *Server) addLocalhostMappings(hostnames ...string) {
	if s.listenerInfo.LocalAddr.Port == 0 {
		return
	}
	localAddress := s.listenerInfo.LocalAddr.IP.String()
	localHostmap := hosts.Mapping{}
	for _, host := range hostnames {
		if _, found := localHostmap[host]; !found {
			if hosts.IsLocal(host) {
				localHostmap[host] = localAddress
				xlog.InfoC(s).Str("host", host).Str("addr", localAddress).Msg("Mapping")
			}
		}
	}
	if err := hosts.Insert(localHostmap); err != nil {
		xlog.WarnC(s).Err(err).Msg("Failed to add localhost mappings")
	}
}
func (s *Server) SetHosts(vhosts ...*VirtualHost) {
	s.TopLevelMux.SetHosts(vhosts...)
	s.addLocalhostMappings(s.TopLevelMux.Hostnames()...)
}
func (s *Server) Listen() (err error) {
	var http, https net.Listener

	if *config.HttpPort > 0 {
		http, err = net.Listen("tcp", net.JoinHostPort(*config.BindAddr, strconv.Itoa(*config.HttpPort)))
		if err != nil {
			return
		}
		defer func() {
			if err != nil {
				http.Close()
			}
		}()
	}

	if *config.HttpsPort > 0 {
		https, err = net.Listen("tcp", net.JoinHostPort(*config.BindAddr, strconv.Itoa(*config.HttpsPort)))
		if err != nil {
			return
		}
		defer func() {
			if err != nil {
				https.Close()
			}
		}()
	}

	if http == nil && https == nil {
		return
	}

	s.listenerInfo = netx.QueryListener(cmp.Or(http, https))
	xlog.InfoC(s).Stringer("local", s.listenerInfo.LocalAddr).Stringer("out", s.listenerInfo.OutboundAddr).Msg("Server starting")
	s.addLocalhostMappings(s.TopLevelMux.Hostnames()...)

	s.serveHttp(http)
	s.serveHttps(https)
	return
}
func (s *Server) Wait() {
	s.wg.Wait()
}
func (s *Server) Close() error {
	s.killed.Store(true)
	return s.Server.Close()
}
func (s *Server) Shutdown(ctx context.Context) error {
	s.killed.Store(true)
	return s.Server.Shutdown(ctx)
}
