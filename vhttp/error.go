package vhttp

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"get.pme.sh/pmesh/config"
	"get.pme.sh/pmesh/netx"
	"get.pme.sh/pmesh/ray"
	"get.pme.sh/pmesh/util"
)

const (
	StatusSessionError   = 522
	StatusUpstreamError  = 1020
	StatusWSFBlocked     = 1021
	StatusMaintenance    = 1022
	StatusRestartLoop    = 1023
	StatusPanic          = 1024
	StatusSignatureError = 1025
	StatusPublishError   = 1026
)

type ErrorPageType uint8

const (
	ErrorPageAny ErrorPageType = iota
	ErrorPageHTML
	ErrorPageHead
	ErrorPagePlain
	ErrorPageJSON
)

type ErrorPageParams struct {
	Template    string
	StatusSent  int           // Status code to send if non-zero
	Code        int           // Error code, or http status code
	Title       string        // Title of the error page
	Explanation string        // Explanation of the error
	Solution    string        // Solution to the error
	Server      string        // Server name
	Host        string        // Host name
	Date        string        // Date
	Ray         string        // Ray ID
	IP          string        // Client IP
	Type        ErrorPageType // Type of error page
}

//go:embed tmp/*
var templates embed.FS

func ParseErrorTemplates(fs fs.FS, prefix string) (*template.Template, error) {
	files := [...]string{
		"4xx.html",
		"5xx.html",
		"internal.html",
	}
	res := template.New("")
	for _, v := range files {
		file, err := fs.Open(prefix + v)
		if err != nil {
			continue
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			return nil, err
		}
		res.New(v).Parse(string(data))
	}
	return res, nil
}

var errorTemplates = template.Must(ParseErrorTemplates(templates, "tmp/"))

var defaultErrorParams = map[int]ErrorPageParams{
	// Internal
	StatusSessionError: {
		Template:    "internal.html",
		StatusSent:  http.StatusBadGateway,
		Title:       "Internal Session Error",
		Explanation: "An internal server error occurred.",
		Solution:    "Please try again in a few minutes, or contact support if the problem persists.",
	},
	StatusUpstreamError: {
		Template:    "5xx.html",
		StatusSent:  http.StatusInternalServerError,
		Title:       "Internal Upstream Error",
		Explanation: "An internal server error occurred.",
		Solution:    "Please try again in a few minutes, or contact support if the problem persists.",
	},
	StatusWSFBlocked: {
		Template:    "4xx.html",
		StatusSent:  http.StatusForbidden,
		Title:       "Blocked by Web Service Firewall (WSF)",
		Explanation: "This website is using a Web Service Firewall (WSF) to protect against malicious requests. Your request has been blocked.",
		Solution:    "If you believe you are being blocked in error, contact the owner of this site for assistance.",
	},
	StatusMaintenance: {
		Template:    "5xx.html",
		StatusSent:  http.StatusServiceUnavailable,
		Title:       "Maintenance",
		Explanation: "The server is currently undergoing maintenance.",
		Solution:    "Please try again later.",
	},
	StatusRestartLoop: {
		Template:    "internal.html",
		StatusSent:  http.StatusBadGateway,
		Title:       "Internal Server Error",
		Explanation: "An internal server error occurred.",
		Solution:    "Please try again in a few minutes, or contact support if the problem persists.",
	},
	StatusPanic: {
		Template:    "internal.html",
		StatusSent:  http.StatusBadGateway,
		Title:       "Internal Server Error",
		Explanation: "An internal server error occurred.",
		Solution:    "Please try again in a few minutes, or contact support if the problem persists.",
	},
	StatusSignatureError: {
		Template:    "4xx.html",
		StatusSent:  http.StatusBadRequest,
		Title:       "Signature Mismatch",
		Explanation: "The request signature does not match the expected signature.",
		Solution:    "Please check the URL and try again.",
	},
	StatusPublishError: {
		Template:    "5xx.html",
		StatusSent:  http.StatusServiceUnavailable,
		Title:       "Publish Error",
		Explanation: "The server is currently unavailable.",
		Solution:    "Please try again in a few minutes, or contact support if the problem persists.",
	},
	// 4xx
	http.StatusBadRequest: {
		Template:    "4xx.html",
		Title:       "Bad Request",
		Explanation: "The request was invalid or cannot be fulfilled.",
		Solution:    "Please check the URL, clean your browser cache, and try again.",
	},
	http.StatusUnauthorized: {
		Template:    "4xx.html",
		Title:       "Unauthorized",
		Explanation: "You are not authorized to access this resource.",
		Solution:    "Please ensure you are logged in and have the necessary permissions.",
	},
	http.StatusForbidden: {
		Template:    "4xx.html",
		Title:       "Forbidden",
		Explanation: "You have been blocked from accessing this resource.",
		Solution:    "If you believe you are being blocked in error, contact the owner of this site for assistance.",
	},
	http.StatusTooManyRequests: {
		Template:    "4xx.html",
		Title:       "Too Many Requests",
		Explanation: "You have been blocked from accessing this resource due to too many requests.",
		Solution:    "Please try again in a few minutes, or contact support if the problem persists.",
	},
	http.StatusNotFound: {
		Template:    "4xx.html",
		Title:       "Not Found",
		Explanation: "The requested resource was not found.",
		Solution:    "Please check the URL and try again.",
	},
	http.StatusMethodNotAllowed: {
		Template:    "4xx.html",
		Title:       "Method Not Allowed",
		Explanation: "The request method is not allowed for this resource.",
		Solution:    "Please check the URL and try again.",
	},
	// 5xx
	http.StatusInternalServerError: {
		Template:    "5xx.html",
		Title:       "Internal Server Error",
		Explanation: "An internal server error occurred.",
		Solution:    "Please try again in a few minutes, or contact support if the problem persists.",
	},
	http.StatusServiceUnavailable: {
		Template:    "5xx.html",
		Title:       "Service Unavailable",
		Explanation: "The server is currently unavailable.",
		Solution:    "Please try again in a few minutes, or contact support if the problem persists.",
	},
	http.StatusGatewayTimeout: {
		Template:    "5xx.html",
		Title:       "Gateway Timeout",
		Explanation: "The server is currently unavailable.",
		Solution:    "Please try again in a few minutes, or contact support if the problem persists.",
	},
	http.StatusHTTPVersionNotSupported: {
		Template:    "4xx.html",
		Title:       "HTTP Version Not Supported",
		Explanation: "The server does not support the HTTP protocol version used in the request.",
		Solution:    "Please try again with a different protocol version.",
	},
	http.StatusBadGateway: {
		Template:    "5xx.html",
		Title:       "Bad Gateway",
		Explanation: "The server received an invalid response from an upstream server.",
		Solution:    "Please try again in a few minutes, or contact support if the problem persists.",
	},
}

func init() {
	// Fill the range for 400-599
	for i := 400; i <= 599; i++ {
		params, ok := defaultErrorParams[i]
		if !ok {
			if i < 500 {
				params = defaultErrorParams[400]
			} else {
				params = defaultErrorParams[500]
			}
			if msg := http.StatusText(i); msg != "" {
				params.Title = msg
			}
		}
		params.Code = i
		defaultErrorParams[i] = params
	}
	// Fill the range for 1000-1099
	for i := 1000; i <= 1099; i++ {
		params, ok := defaultErrorParams[i]
		if !ok {
			params = defaultErrorParams[StatusSessionError]
		}
		params.Code = i
		defaultErrorParams[i] = params
	}
}

func (params *ErrorPageParams) WriteTo(w http.ResponseWriter, sv *Server) (err error) {
	status := params.Code
	if params.StatusSent != 0 {
		status = params.StatusSent
	}
	hdrs := w.Header()
	hdrs["X-Content-Type-Options"] = []string{"nosniff"}
	switch params.Type {
	default:
		fallthrough
	case ErrorPageHTML:
		var tmp *template.Template
		if sv != nil {
			if ow := sv.errTemplatesOverride.Load(); ow != nil {
				tmp = ow.Lookup(params.Template)
			}
		}
		if tmp == nil {
			tmp = errorTemplates.Lookup(params.Template)
		}
		if tmp != nil {
			hdrs["Content-Type"] = []string{"text/html; charset=utf-8"}
			w.WriteHeader(status)
			err = tmp.Execute(w, params)
			return
		}
		fallthrough
	case ErrorPagePlain:
		written := fmt.Sprintf("%d %s: %s", params.Code, params.Title, params.Explanation)
		hdrs["Content-Type"] = []string{"text/plain; charset=utf-8"}
		hdrs["Content-Length"] = []string{fmt.Sprint(len(written))}
		w.WriteHeader(status)
		_, err = fmt.Fprint(w, written)
	case ErrorPageJSON:
		type errorJSON struct {
			Code        int    `json:"code"`
			Title       string `json:"error"`
			Explanation string `json:"message"`
		}
		hdrs["Content-Type"] = []string{"application/json; charset=utf-8"}
		w.WriteHeader(status)
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		err = enc.Encode(errorJSON{Code: params.Code, Title: params.Title, Explanation: params.Explanation})
	case ErrorPageHead:
		hdrs["Content-Length"] = []string{"0"}
		w.WriteHeader(status)
	}
	return
}
func (params *ErrorPageParams) WithRequest(r *http.Request) *ErrorPageParams {
	params.Server = ray.ToHostString(config.Get().Host)
	params.Date = time.Now().UTC().Format("2006-01-02 15:04:05 UTC")
	params.IP = ClientSessionFromContext(r.Context()).IP.String()
	params.Host = r.Host
	if ray := r.Header[netx.HdrRay]; len(ray) == 1 {
		params.Ray = ray[0]
	}
	if r.Method == http.MethodHead || params.Type == ErrorPageHead {
		params.Type = ErrorPageHead
	} else if params.Type == ErrorPageAny {
		ty := ErrorPageJSON
		for _, v := range r.Header["Accept"] {
			if strings.Contains(v, "text/html") {
				ty = ErrorPageHTML
				break
			} else if strings.Contains(v, "application/json") {
				ty = ErrorPageJSON
				break
			} else if strings.Contains(v, "text/plain") {
				ty = ErrorPagePlain
				break
			}
		}
		params.Type = ty
	}
	return params
}
func Error(w http.ResponseWriter, r *http.Request, code int, explanation ...string) {
	defer util.DrainClose(r.Body)

	if 200 <= code && code < 299 {
		w.Header()["Content-Length"] = []string{"0"}
		w.WriteHeader(code)
		return
	}

	if code == 0 {
		code = StatusSessionError
	}
	params, ok := defaultErrorParams[code]
	if !ok {
		panic(fmt.Errorf("invalid error code %d", code))
	}
	if len(explanation) > 0 {
		if len(explanation) == 1 {
			params.Explanation = explanation[0]
		} else {
			params.Explanation = strings.Join(explanation, " ")
		}
	}
	sv := GetServerFromContext(r.Context())
	_ = params.WithRequest(r).WriteTo(w, sv)
}
