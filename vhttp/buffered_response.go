package vhttp

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
)

type BufferedResponse struct {
	Status  int
	Headers http.Header
	Body    *bytes.Buffer
}

func NewBufferedResponse(buf *bytes.Buffer) *BufferedResponse {
	if buf == nil {
		buf = new(bytes.Buffer)
	}
	return &BufferedResponse{
		Status:  0,
		Headers: make(http.Header),
		Body:    buf,
	}
}

func (bw *BufferedResponse) Header() http.Header {
	return bw.Headers
}
func (bw *BufferedResponse) Write(b []byte) (int, error) {
	if bw.Status == 0 {
		bw.Status = http.StatusOK
	}
	return bw.Body.Write(b)
}
func (bw *BufferedResponse) WriteHeader(status int) {
	bw.Status = status
}

func (bw *BufferedResponse) Response() *http.Response {
	return &http.Response{
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		ContentLength: int64(bw.Body.Len()),
		Status:        http.StatusText(bw.Status),
		StatusCode:    bw.Status,
		Header:        bw.Headers,
		Body:          io.NopCloser(bw.Body),
	}
}

// Serves the buffered response to another writer.
func (bw *BufferedResponse) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if bw.Headers != nil {
		for k, v := range bw.Headers {
			w.Header()[k] = v
		}
	}
	if bw.Status != 0 {
		w.WriteHeader(bw.Status)
	}
	bw.Body.WriteTo(w)
}

type ConditionalResponse struct {
	rw      http.ResponseWriter
	Touched bool
}

func NewConditionalResponse(rw http.ResponseWriter) *ConditionalResponse {
	return &ConditionalResponse{rw: rw}
}

func (cr *ConditionalResponse) Header() http.Header {
	return cr.rw.Header()
}
func (cr *ConditionalResponse) Write(b []byte) (int, error) {
	cr.Touched = true
	return cr.rw.Write(b)
}
func (cr *ConditionalResponse) WriteHeader(status int) {
	cr.Touched = true
	cr.rw.WriteHeader(status)
}
func (cr *ConditionalResponse) Unwrap() http.ResponseWriter {
	return cr.rw
}
func (cr *ConditionalResponse) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	rc := http.NewResponseController(cr.rw)
	c, rw, e := rc.Hijack()
	if e == nil {
		cr.Touched = true
	}
	return c, rw, e
}
