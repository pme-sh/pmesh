package vhttp

import (
	"encoding"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/pme-sh/pmesh/netx"
	"github.com/pme-sh/pmesh/util"
	"github.com/pme-sh/pmesh/variant"
	"github.com/pme-sh/pmesh/xlog"
)

func makeParser(ty reflect.Type) func(string) (reflect.Value, error) {
	if ty == reflect.TypeOf(time.Duration(0)) {
		return func(s string) (reflect.Value, error) {
			var dur util.Duration
			err := dur.UnmarshalText([]byte(s))
			return reflect.ValueOf(dur.Duration()), err
		}
	}
	if ty.Implements(reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()) {
		return func(s string) (reflect.Value, error) {
			v := reflect.New(ty)
			err := v.Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(s))
			return v.Elem(), err
		}
	}
	switch ty.Kind() {
	case reflect.String:
		return func(s string) (reflect.Value, error) { return reflect.ValueOf(s), nil }
	case reflect.Bool:
		return func(s string) (reflect.Value, error) {
			b, err := strconv.ParseBool(s)
			return reflect.ValueOf(b), err
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return func(s string) (reflect.Value, error) {
			i, err := strconv.ParseInt(s, 10, 64)
			return reflect.ValueOf(i).Convert(ty), err
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return func(s string) (reflect.Value, error) {
			u, err := strconv.ParseUint(s, 10, 64)
			return reflect.ValueOf(u).Convert(ty), err
		}
	case reflect.Float32, reflect.Float64:
		return func(s string) (reflect.Value, error) {
			f, err := strconv.ParseFloat(s, 64)
			return reflect.ValueOf(f).Convert(ty), err
		}
	}
	panic(fmt.Sprintf("unsupported type %s", ty))
}

type directive struct {
	format  string
	name    string
	pfx     string
	fn      func(w http.ResponseWriter, r *http.Request, args []reflect.Value) Result
	parsers []func(string) (reflect.Value, error)
	err     error
}

func (d *directive) Instance() *directiveHandler {
	return &directiveHandler{d, nil}
}

type directiveHandler struct {
	*directive
	values []reflect.Value
}

func (p directiveHandler) vlist() []any {
	v := make([]any, len(p.values))
	for i, val := range p.values {
		v[i] = val.Interface()
	}
	return v
}

func (gd directiveHandler) String() string {
	return fmt.Sprintf(gd.format, gd.vlist()...)
}
func (gd *directiveHandler) UnmarshalText(text []byte) error {
	return gd.UnmarshalInline(string(text))
}
func (gd *directiveHandler) UnmarshalInline(text string) error {
	if !strings.HasPrefix(text, gd.pfx) {
		return variant.RejectMatch(gd)
	}

	prevSpace := false
	text = strings.Map(func(r rune) rune {
		switch r {
		case '\t', '\n', '\v', '\f', '\r', ' ', 0x85, 0xA0:
			if prevSpace {
				return -1
			}
			prevSpace = true
			return ' '
		default:
			prevSpace = false
		}
		return r
	}, text)

	strs := make([]any, len(gd.parsers))
	for i := range strs {
		strs[i] = new(string)
	}
	_, err := fmt.Sscanf(text, gd.format, strs...)
	if err != nil {
		return gd.err
	}
	gd.values = make([]reflect.Value, len(strs))
	for i, str := range strs {
		val, err := gd.parsers[i](*str.(*string))
		if err != nil {
			return gd.err
		}
		gd.values[i] = val
	}
	return nil
}
func (gd directiveHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) Result {
	return gd.fn(w, r, gd.values)
}

func registerDirective(format string, handlerFunction any) (d *directive) {
	before, _, _ := strings.Cut(format, " ")
	d = new(directive)
	d.name = before
	d.format = format
	d.err = fmt.Errorf("invalid %s directive", d.name)
	if before != format {
		d.pfx = before + " "
	} else {
		d.pfx = before
	}

	ty := reflect.TypeOf(handlerFunction)

	d.parsers = make([]func(string) (reflect.Value, error), ty.NumIn()-2)
	for i := 2; i < ty.NumIn(); i++ {
		d.parsers[i-2] = makeParser(ty.In(i))
	}
	d.fn = func(w http.ResponseWriter, r *http.Request, args []reflect.Value) Result {
		args = append([]reflect.Value{reflect.ValueOf(w), reflect.ValueOf(r)}, args...)
		retval := reflect.ValueOf(handlerFunction).Call(args)
		if len(retval) == 0 {
			return Continue
		}
		return retval[0].Interface().(Result)
	}
	Registry.Define(d.name, func() any { return d.Instance() })
	return
}

func init() {
	// Debug
	registerDirective("debug %q", func(w http.ResponseWriter, r *http.Request, message string) {
		xlog.Debug().EmbedObject(xlog.EnhanceRequest(r)).Msg(message)
	})
	registerDirective("log %q", func(w http.ResponseWriter, r *http.Request, message string) {
		xlog.Info().EmbedObject(xlog.EnhanceRequest(r)).Msg(message)
	})
	registerDirective("warn %q", func(w http.ResponseWriter, r *http.Request, message string) {
		xlog.Warn().EmbedObject(xlog.EnhanceRequest(r)).Msg(message)
	})
	registerDirective("err %q", func(w http.ResponseWriter, r *http.Request, message string) {
		xlog.Error().EmbedObject(xlog.EnhanceRequest(r)).Msg(message)
	})

	// Path directives
	registerDirective("proxy-host %s", func(w http.ResponseWriter, r *http.Request, host string) {
		r.Host = host
	})
	registerDirective("path %s", func(w http.ResponseWriter, r *http.Request, rel string) {
		r.URL.Path = rel
	})
	registerDirective("path-join %s", func(w http.ResponseWriter, r *http.Request, rel string) {
		r.URL.Path = RelativePath(r.URL.Path, rel)
	})
	registerDirective("path-ljoin %s", func(w http.ResponseWriter, r *http.Request, prefix string) {
		r.URL.Path = CleanPath(prefix + r.URL.Path)
	})
	registerDirective("path-trim %s", func(w http.ResponseWriter, r *http.Request, prefix, suffix string) {
		r.URL.Path = NormalPath(strings.TrimSuffix(r.URL.Path, suffix))
	})
	registerDirective("path-ltrim %s", func(w http.ResponseWriter, r *http.Request, prefix string) {
		r.URL.Path = NormalPath(strings.TrimPrefix(r.URL.Path, prefix))
	})

	// Portaling directives (use with caution)
	registerDirective("allow-internal", func(w http.ResponseWriter, r *http.Request) {
		r.Header["P-Internal"] = []string{"1"}
	})
	registerDirective("portal %s", func(w http.ResponseWriter, r *http.Request, nurl string) Result {
		if r.Header["P-Portal"] == nil {
			var err error
			r.URL, err = r.URL.Parse(nurl)
			if err != nil {
				xlog.ErrC(r.Context(), err).Str("nurl", nurl).Msg("Failed to parse portal url")
				Error(w, r, StatusSessionError)
				return Done
			}
			r.Host = r.URL.Host
			r.Header["P-Portal"] = []string{"1"}
			r.Header["P-Internal"] = []string{"1"}

			ctx := r.Context()
			session := ClientSessionFromContext(ctx)
			server := GetServerFromContext(ctx)
			server.ServeHTTPSession(w, r, session)
		} else {
			xlog.ErrStackC(r.Context(), errors.New("restart request loop")).
				Str("nurl", nurl).EmbedObject(xlog.EnhanceRequest(r)).Send()
			Error(w, r, StatusRestartLoop)
		}
		return Done
	})

	// Header directives
	registerDirective("header %s %q", func(w http.ResponseWriter, r *http.Request, key string, value string) { w.Header().Set(key, value) })
	registerDirective("proxy-header %s %q", func(w http.ResponseWriter, r *http.Request, key string, value string) { r.Header.Set(key, value) })
	registerDirective("add-header %s %q", func(w http.ResponseWriter, r *http.Request, key string, value string) { w.Header().Add(key, value) })
	registerDirective("add-proxy-header %s %q", func(w http.ResponseWriter, r *http.Request, key string, value string) { r.Header.Add(key, value) })
	registerDirective("del-header %s", func(w http.ResponseWriter, r *http.Request, key string) { w.Header().Del(key) })
	registerDirective("del-proxy-header %s", func(w http.ResponseWriter, r *http.Request, key string) { r.Header.Del(key) })

	// Response directives
	registerDirective("status %s", func(w http.ResponseWriter, r *http.Request, code int) Result {
		Error(w, r, code)
		return Done
	})
	registerDirective("return %s %q", func(w http.ResponseWriter, r *http.Request, code int, message string) Result {
		w.Header()["Content-Type"] = []string{"text/plain; charset=utf-8"}
		w.Header()["X-Content-Type-Options"] = []string{"nosniff"}
		w.Header()["Content-Length"] = []string{fmt.Sprint(len(message))}
		w.WriteHeader(code)
		fmt.Fprint(w, message)
		return Done
	})
	registerDirective("redirect %q", func(w http.ResponseWriter, r *http.Request, url string) Result {
		http.Redirect(w, r, url, http.StatusFound)
		return Done
	})
	registerDirective("abort", func(w http.ResponseWriter, r *http.Request) Result {
		netx.ResetRequestConn(w)
		return Done
	})
	registerDirective("drop", func(w http.ResponseWriter, r *http.Request) Result {
		return Drop
	})
	registerDirective("write-timeout %s", func(w http.ResponseWriter, r *http.Request, dur time.Duration) {
		rc := http.NewResponseController(w)
		var err error
		if dur <= 0 {
			err = rc.SetWriteDeadline(time.Time{})
		} else {
			err = rc.SetWriteDeadline(time.Now().Add(dur))
		}
		if err != nil {
			xlog.WarnC(r.Context()).Err(err).Msg("failed to set write deadline")
		}
	})
	registerDirective("read-timeout %s", func(w http.ResponseWriter, r *http.Request, dur time.Duration) {
		rc := http.NewResponseController(w)
		var err error
		if dur <= 0 {
			err = rc.SetReadDeadline(time.Time{})
		} else {
			err = rc.SetReadDeadline(time.Now().Add(dur))
		}
		if err != nil {
			xlog.WarnC(r.Context()).Err(err).Msg("failed to set read deadline")
		}
	})

	// Authentication directives
	registerDirective("auth %s %s:%s", func(w http.ResponseWriter, r *http.Request, realm string, u, p string) Result {
		user, pass, ok := r.BasicAuth()
		if !ok || user != u || pass != p {
			h := w.Header()
			h["WWW-Authenticate"] = []string{fmt.Sprintf("Basic realm=%q", realm)}
			h["Referrer-Policy"] = []string{"no-referrer"}
			Error(w, r, http.StatusUnauthorized)
			return Done
		}
		return Continue
	})
}
