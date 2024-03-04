package health

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"get.pme.sh/pmesh/netx"
)

type HttpCheck struct {
	Method          string            `yaml:"method"`           // The HTTP method to use
	Path            string            `yaml:"path"`             // The path to request
	FollowRedirects bool              `yaml:"follow_redirects"` // Follow redirects
	Header          map[string]string `yaml:"header"`           // The headers to send with the request
	Code            int               `yaml:"code"`             // Expected status code
	Body            string            `yaml:"body"`             // Expected body
}

func (t *HttpCheck) UnmarshalInline(text string) error {
	_, err := fmt.Sscanf(text, "%s %s %d", &t.Method, &t.Path, &t.Code)
	if err != nil {
		return fmt.Errorf("invalid inline http check: %w", err)
	}
	switch t.Method {
	case "GET", "HEAD", "OPTIONS", "DELETE", "TRACE", "CONNECT", "PATCH", "POST", "PUT":
		return nil
	default:
		return fmt.Errorf("invalid method: %s", t.Method)
	}
}

func (t *HttpCheck) Perform(ctx context.Context, addr string) error {
	cli := &http.Client{
		Transport: netx.LocalTransport,
	}
	if deadline, ok := ctx.Deadline(); ok {
		cli.Timeout = time.Until(deadline)
	}
	if !t.FollowRedirects {
		cli.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	url := &url.URL{}
	if strings.HasPrefix(t.Path, "http://") || strings.HasPrefix(t.Path, "https://") {
		url, _ = url.Parse(t.Path)
	} else if strings.HasPrefix(t.Path, "/") {
		url.Scheme = "http"
		url.Host = addr
		url.Path = t.Path
	} else {
		url, _ = url.Parse("http://" + t.Path)
	}
	method := t.Method
	if method == "" {
		method = "GET"
	}
	req, err := http.NewRequestWithContext(ctx, method, url.String(), nil)
	if err != nil {
		return err
	}
	if t.Header != nil {
		for k, v := range t.Header {
			if k == "Host" {
				req.Host = v
			} else {
				req.Header.Set(k, v)
			}
		}
	}
	req.Host = url.Host
	req.URL.Host = addr
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	all, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading body: %w", err)
	}

	if t.Code > 0 && resp.StatusCode != t.Code {
		return fmt.Errorf("unexpected status code: %d, expected %d", resp.StatusCode, t.Code)
	}

	if t.Body != "" {
		allStr := strings.ToLower(string(all))
		bodyStr := strings.ToLower(t.Body)
		if !strings.Contains(allStr, bodyStr) {
			return fmt.Errorf("unexpected body: %s", all)
		}
	}
	return nil
}
func init() {
	Registry.Define("Http", func() any {
		return &HttpCheck{
			Method:          "GET",
			Path:            "/",
			FollowRedirects: false,
			Header:          map[string]string{},
			Code:            http.StatusOK,
		}
	})
}
