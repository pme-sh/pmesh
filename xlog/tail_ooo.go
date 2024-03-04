package xlog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"get.pme.sh/pmesh/ray"

	"github.com/valyala/fastjson"
)

type oooLog struct {
	Line []byte
	Time time.Time
}

// Mux writer is a writer that buffers lines and writes them in order
// of their timestamps. It is used to write logs from multiple sources
// to a single output, while preserving the order of the logs.
// Additionally, it inserts sending host information into the caller data.
type MuxWriter struct {
	mu        sync.Mutex
	out       io.Writer    // The underlying writer
	lnbuf     bytes.Buffer // Buffer for incomplete lines
	buf       []oooLog     // Buffer for complete lines, preceding the flush time
	flushTime time.Time    // The time when the logger was created
}

func ToMuxWriter(out io.Writer) *MuxWriter {
	if mw, ok := out.(*MuxWriter); ok {
		return mw
	}
	lo := &MuxWriter{
		out:       out,
		buf:       make([]oooLog, 0, 100),
		flushTime: time.Now().Add(500 * time.Millisecond),
	}
	time.AfterFunc(500*time.Millisecond, lo.Flush)
	return lo
}

func (o *MuxWriter) flushLocked() {
	sort.Slice(o.buf, func(i, j int) bool {
		return o.buf[i].Time.Before(o.buf[j].Time)
	})
	for _, l := range o.buf {
		o.out.Write(l.Line)
	}
	o.buf = nil
}
func (o *MuxWriter) Flush() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.flushLocked()
}

func (o *MuxWriter) WriteAs(p []byte, host string) (n int, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n = len(p)

	// Write the input to the line buffer
	o.lnbuf.Write(p)

	maxTime := time.Time{}
	for {
		// Read the next line from the buffer
		p, e := o.lnbuf.ReadBytes('\n')
		if e == io.EOF {
			break
		} else if e != nil {
			return n, e
		}

		// Parse the line
		ln, err := ParseLine(p)
		if err != nil {
			// Invalid lines are written directly to the output
			_, err := o.out.Write(p)
			if err != nil {
				return 0, err
			}
			continue
		}

		// Insert the host into the caller data
		if host != "" {
			dom := ln.GetStringBytes(DomainFieldName)
			newDom, _ := json.Marshal(fmt.Sprintf("%s/%s", host, dom))
			ln.Set(DomainFieldName, fastjson.MustParseBytes(newDom))
			p = ln.MarshalTo(p[:0])
		}

		// If we're buffering, append the line, else write it directly
		if o.buf != nil {
			t := ln.Time()
			o.buf = append(o.buf, oooLog{Line: p, Time: t})
			if t.After(maxTime) {
				maxTime = t
			}
		} else {
			_, err := o.out.Write(p)
			if err != nil {
				return 0, err
			}
		}
	}

	// If we are past the flush time, flush the buffer
	if maxTime.After(o.flushTime) {
		o.flushLocked()
	}
	return n, nil
}
func (o *MuxWriter) Write(p []byte) (n int, err error) {
	return o.WriteAs(p, "")
}

type subWriter struct {
	mux  *MuxWriter
	host string
}

func (o *subWriter) Write(p []byte) (n int, err error) {
	return o.mux.WriteAs(p, o.host)
}
func (o *MuxWriter) SubWriter(host string) io.Writer {
	return &subWriter{mux: o, host: ray.ToHostString(host)}
}
