package xlog

import (
	"context"
	"io"
	"net"
	"strings"
	"time"

	"get.pme.sh/pmesh/ray"

	"golang.org/x/sync/errgroup"
)

type TailOptions struct {
	Domain    string    `json:"domain,omitempty"`     // Domain to filter logs in
	MinLevel  Level     `json:"level,omitempty"`      // Level filter
	After     time.Time `json:"after,omitempty"`      // Time filter
	Before    time.Time `json:"before,omitempty"`     // Time filter
	Search    string    `json:"substr,omitempty"`     // Substring filter
	LineLimit int64     `json:"line_limit,omitempty"` // Max lines to emit from history
	IoLimit   int64     `json:"io_limit,omitempty"`   // Max bytes to read in history
	Follow    bool      `json:"follow,omitempty"`     // Follow logs in real time
	Hostname  string    `json:"hostname,omitempty"`   // Hostname to filter logs in (in public format). Not used in local implementation
	Viral     bool      `json:"viral,omitempty"`      // Viral logs, will spread to all nodes. Not used in local implementation
}

func (o TailOptions) WithRay(rayStr string) (opts TailOptions, err error) {
	// Parse ray
	rid, err := ray.Parse(rayStr)
	if err != nil {
		return
	}

	// Set options
	opts = o
	opts.Hostname = rid.Host.String()
	opts.Before = rid.Timestamp().Add(time.Minute)
	opts.After = rid.Timestamp().Add(-time.Minute)
	opts.Search = rid.String()
	opts.Follow = false
	return
}

func (o TailOptions) Filter() Filter {
	filter := MultiFilter{}
	if o.Domain != "" {
		filter = filter.Append(DomainFilter(o.Domain))
	}
	if o.MinLevel != LevelDebug {
		filter = filter.Append(LevelFilter(o.MinLevel))
	}
	if o.Search != "" {
		filter = filter.Append(SearchFilter(o.Search))
	}
	if !o.After.IsZero() || !o.Before.IsZero() {
		filter = filter.Append(TimeFilter{After: o.After, Before: o.Before})
	}
	return filter
}

func TailHistory(ctx context.Context, opts *TailOptions, out io.Writer) error {
	q := NewParserQuota(opts.IoLimit, opts.LineLimit)
	files, err := ReadDir()
	if err != nil {
		return err
	}
	eg, ctx := errgroup.WithContext(ctx)
	filter := opts.Filter()
	for _, file := range files {
		if !filter.TestFile(&file) {
			continue
		}
		eg.Go(func() (err error) {
			parser, err := NewFileParser(file.File.Name(), 0)
			if err != nil {
				return
			}
			parser = parser.WithFilter(filter).WithQuota(q)
			defer parser.Close()
			for {
				var line Line
				line, err = parser.NextContext(ctx)
				if err == io.EOF {
					return nil
				} else if err != nil {
					return nil // ignore errors, we may have been reading active files
				} else if _, err = out.Write(append(line.Raw, '\n')); err != nil {
					return
				}
			}
		})
	}
	e := eg.Wait()
	if e == ErrQuotaExceeded {
		e = nil
	}
	return e
}

type tailCollector struct {
	minLevel Level
	domain   string
	substr   string
	w        io.Writer
	cancel   context.CancelFunc
}

func (c *tailCollector) Write(raw []byte, level Level, domain string) {
	if level < c.minLevel || !strings.HasPrefix(domain, c.domain) {
		return
	}
	if c.substr != "" {
		if !strings.Contains(string(raw), c.substr) {
			return
		}
	}
	if _, err := c.w.Write(raw); err != nil {
		c.cancel()
	}
}
func tailFollow(ctx context.Context, opts *TailOptions) net.Conn {
	ctx, cancel := context.WithCancel(ctx)
	rcv, snd := net.Pipe()
	col := &tailCollector{
		minLevel: opts.MinLevel,
		domain:   opts.Domain,
		substr:   opts.Search,
		w:        snd,
		cancel:   cancel,
	}
	RegisterCollector(col)
	go func() {
		<-ctx.Done()
		snd.Close()
		RemoveCollector(col)
	}()
	return rcv
}

func TailContext(ctx context.Context, opts TailOptions, out io.Writer) error {
	if !opts.Follow {
		return TailHistory(ctx, &opts, out)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	now := time.Now()

	// If before time is zero, or in the future, start a collector
	var c net.Conn
	if opts.Before.IsZero() || opts.Before.After(now) {
		c = tailFollow(ctx, &opts)
		opts.Before = now
	}

	if opts.IoLimit >= 0 {
		// If after time is zero, or in the past, we need to read the logs
		if opts.After.IsZero() || opts.After.Before(now) {
			err := TailHistory(ctx, &opts, out)
			if err != nil {
				return err
			}
		}
	}
	if c == nil {
		return nil
	}
	_, err := io.Copy(out, c)
	return err
}
func Tail(opts TailOptions, out io.Writer) error {
	return TailContext(context.Background(), opts, out)
}
