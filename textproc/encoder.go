package textproc

import (
	"bytes"
	"fmt"
	"io"
)

type WriteFlusher interface {
	Write(p []byte) (nn int, err error)
	Flush() error
}

type ByteEncoder interface {
	Encode(rest []byte, fw WriteFlusher) (cnt []byte, e error)
}
type FuncEncoder func(rest []byte, fw WriteFlusher) (cnt []byte, e error)

func (f FuncEncoder) Encode(rest []byte, fw WriteFlusher) (cnt []byte, e error) {
	return f(rest, fw)
}

type FlushEncoder struct{}

func (FlushEncoder) Encode(rest []byte, fw WriteFlusher) (cnt []byte, e error) {
	e = fw.Flush()
	return rest, e
}

type LiteralEncoder struct {
	Value []byte
}

func (r LiteralEncoder) Encode(rest []byte, fw WriteFlusher) (cnt []byte, e error) {
	_, e = fw.Write(r.Value)
	return rest, e
}

type DiscardEncoder struct{}

func (r DiscardEncoder) Encode(rest []byte, fw WriteFlusher) (cnt []byte, e error) {
	return rest, nil
}

var jsonEscaped = map[byte]ByteEncoder{}

func init() {
	jsonEscaped['"'] = LiteralEncoder{[]byte("\\\"")}
	jsonEscaped['\\'] = LiteralEncoder{[]byte("\\\\")}
	jsonEscaped['\b'] = LiteralEncoder{[]byte("\\b")}
	jsonEscaped['\f'] = LiteralEncoder{[]byte("\\f")}
	jsonEscaped['\n'] = LiteralEncoder{[]byte("\\n")}
	jsonEscaped['\r'] = LiteralEncoder{[]byte("\\r")}
	jsonEscaped['\t'] = LiteralEncoder{[]byte("\\t")}
	for i := range 0x20 {
		if jsonEscaped[byte(i)] == nil {
			jsonEscaped[byte(i)] = LiteralEncoder{[]byte(fmt.Sprintf("\\u%04x", i))}
		}
	}
}

type Encoding struct {
	encoders [256]ByteEncoder
}

func (r *Encoding) ForEach(f func(b byte, r ByteEncoder)) {
	for i, e := range r.encoders {
		if e != nil {
			f(byte(i), e)
		}
	}
}
func (r *Encoding) Index(b []byte) (idx int, e ByteEncoder) {
	for i, c := range b {
		if e = r.encoders[c]; e != nil {
			return i, e
		}
	}
	return -1, nil
}
func (r *Encoding) With(b byte, h ByteEncoder) *Encoding {
	if r.encoders[b] == nil {
		r.encoders[b] = h
	}
	return r
}
func (r *Encoding) WithFunc(b byte, h FuncEncoder) *Encoding {
	return r.With(b, h)
}
func (r *Encoding) WithLiteral(b byte, d []byte) *Encoding {
	return r.With(b, LiteralEncoder{d})
}
func (r *Encoding) WithString(b byte, with string) *Encoding {
	return r.WithLiteral(b, []byte(with))
}
func (r *Encoding) WithFlush(b byte) *Encoding {
	return r.With(b, FlushEncoder{})
}
func (r *Encoding) WithDiscard(b byte) *Encoding {
	return r.With(b, DiscardEncoder{})
}
func (r *Encoding) WithDiscardSet(k string) *Encoding {
	for _, c := range k {
		r.WithDiscard(byte(c))
	}
	return r
}
func (r *Encoding) WithJSONEscaped(b ...byte) *Encoding {
	if len(b) == 0 {
		for i := range 0x20 {
			if e, ok := jsonEscaped[byte(i)]; ok {
				r.With(byte(i), e)
			}
		}
	} else {
		for _, c := range b {
			if e, ok := jsonEscaped[c]; ok {
				r.With(c, e)
			}
		}
	}
	return r
}
func (r *Encoding) WithNoANSI() *Encoding {
	return r.WithFunc('\x1b', func(after []byte, fw WriteFlusher) ([]byte, error) {
		end := bytes.IndexByte(after, 'm')
		if end == -1 {
			return nil, nil
		}
		return after[end+1:], nil
	})
}
func (r *Encoding) WithLineFlush() *Encoding {
	return r.WithFlush('\n')
}
func (r *Encoding) WithTabWidth(n int) *Encoding {
	return r.WithLiteral('\t', bytes.Repeat([]byte(" "), n))
}
func (r *Encoding) Clone() *Encoding {
	n := NewEncoding()
	*n = *r
	return n
}
func (r *Encoding) EncodePart(b []byte, w WriteFlusher) (e error) {
	for {
		i, c := r.Index(b)
		if i == -1 {
			if len(b) != 0 {
				_, e = w.Write(b)
			}
			return
		}
		if i != 0 {
			_, e = w.Write(b[:i])
			if e != nil {
				break
			}
		}
		b = b[i+1:]
		b, e = c.Encode(b, w)
		if e != nil {
			break
		}
	}
	return
}

func NewEncoding() *Encoding {
	return &Encoding{}
}

type Encoder struct {
	Encoding *Encoding
	Writer   WriteFlusher
}

func (r *Encoder) Write(p []byte) (n int, err error) {
	err = r.Encoding.EncodePart(p, r.Writer)
	n = len(p)
	return
}
func (e *Encoding) NewEncoder(w WriteFlusher) *Encoder {
	return &Encoder{Encoding: e, Writer: w}
}

type bufferedWriter struct {
	buf bytes.Buffer
	w   io.Writer
}

func (b *bufferedWriter) Write(p []byte) (n int, err error) {
	return b.buf.Write(p)
}
func (b *bufferedWriter) Flush() error {
	_, err := b.buf.WriteTo(b.w)
	return err
}
func (e *Encoding) NewBufferedWriter(w io.Writer) *Encoder {
	return e.NewEncoder(&bufferedWriter{w: w})
}
