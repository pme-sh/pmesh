package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"get.pme.sh/pmesh/pmtp"
	"get.pme.sh/pmesh/ray"
	"get.pme.sh/pmesh/xlog"
	"get.pme.sh/pmesh/xpost"

	"github.com/samber/lo"
)

func tail(ctx context.Context, dialer *pmtp.Dialer, tls bool, hostc string, to xlog.TailOptions, out io.Writer) error {
	to.Hostname = ""
	to.Viral = false
	body, err := json.Marshal(to)
	if err != nil {
		xlog.Err(err).Str("host", hostc).Msg("Failed to tail log, marshal error")
		return err
	}
	schema := "http"
	if tls {
		schema = "https"
	}
	req := &http.Request{
		Method: "POST",
		URL: &url.URL{
			Scheme: schema,
			Host:   hostc,
			Path:   "/tail",
		},
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
	}
	req = req.WithContext(ctx)
	conn, resp, err := dialer.RoundTrip(req)
	if err != nil {
		xlog.Err(err).Str("host", hostc).Msg("Failed to tail log, connection error")
		return err
	}
	defer conn.Close()
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		response, _ := io.ReadAll(resp.Body)
		xlog.Warn().Str("host", hostc).Str("status", resp.Status).Str("response", string(response)).Msg("Failed to tail log, unexpected status")
		return fmt.Errorf("unexpected status: %s", resp.Status)
	}
	context.AfterFunc(ctx, func() { conn.Close() })
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		xlog.Err(err).Str("host", hostc).Msg("Failed to tail log, copy error")
	}
	return err
}

func (c Client) tailBroadcast(ctx context.Context, dialer *pmtp.Dialer, u *pmtp.ConnURL, to xlog.TailOptions, out *xlog.MuxWriter) (errs []error) {
	peers, err := c.PeersAlive()
	if err != nil {
		xlog.Err(err).Msg("Failed to tail log, failed to get alive peers")
		return []error{err}
	}
	if to.Hostname != "" {
		h := ray.ParseHost(to.Hostname)
		peers = lo.Filter(peers, func(p xpost.Peer, _ int) bool {
			return h.EqualFold(p.Host)
		})
	}
	if len(peers) == 0 {
		xlog.Warn().Msg("Failed to tail log, no alive peers")
		return nil
	}

	wg := &sync.WaitGroup{}
	errs = make([]error, len(peers))
	for i, peer := range peers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			host := peer.IP
			if peer.Me {
				host = u.Host
			}
			err := tail(ctx, dialer, u.TLS, host, to, out.SubWriter(peer.Host))
			if err != nil {
				errs[i] = fmt.Errorf("host %s: %w", peer.Host, err)
			}
		}()
	}
	wg.Wait()
	return
}

func (c Client) TailContext(ctx context.Context, to xlog.TailOptions, out io.Writer) error {
	// Parse the URL, prepare the dialer
	url, err := pmtp.ParseURL(c.URL)
	if err != nil {
		return err
	}
	dialer := url.Dialer()

	// Create a new context for the tailing and a writer
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	mw := xlog.ToMuxWriter(out)
	defer mw.Flush()
	errs := c.tailBroadcast(ctx, dialer, url, to, mw)

	// If we're following, discard the errors and tail forever
	if to.Follow {
		to.IoLimit = -1
		for ctx.Err() == nil {
			errs = c.tailBroadcast(ctx, dialer, url, to, mw)
			time.Sleep(1 * time.Second)
		}
	}
	return errors.Join(errs...)
}
func (c Client) Tail(to xlog.TailOptions, out io.Writer) error {
	return c.TailContext(context.Background(), to, out)
}
