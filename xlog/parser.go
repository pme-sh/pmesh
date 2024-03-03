package xlog

import (
	"bufio"
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/pme-sh/pmesh/config"
	"github.com/pme-sh/pmesh/util"

	"github.com/icza/backscanner"
	"github.com/valyala/fastjson"
)

type ParserQuota struct {
	Io    atomic.Int64
	Lines atomic.Int64
}

var ErrQuotaExceeded = errors.New("quota exceeded")

func NewParserQuota(maxIo, maxLines int64) (q *ParserQuota) {
	q = new(ParserQuota)
	maxIo = cmp.Or(maxIo, 1024*1024*1024)
	maxLines = cmp.Or(maxLines, 1<<62)
	q.Io.Store(int64(maxIo))
	q.Lines.Store(int64(maxLines))
	return
}

func (r *ParserQuota) ConsumeIo(n int) bool {
	if r == nil {
		return true
	}
	return r.Io.Add(int64(-n)) >= 0
}
func (r *ParserQuota) ConsumeLine() bool {
	if r == nil {
		return true
	}
	n := r.Lines.Add(-1)
	if n == 0 {
		r.Io.Store(-1) // don't read any more
	}
	return n >= 0
}

type scanner interface {
	scan() (line []byte, err error)
}

type Parser struct {
	scanner scanner
	flags   StreamFlag
	filter  MultiFilter
	parser  fastjson.Parser
	quote   *ParserQuota
	closer  io.Closer
}

func (r *Parser) NextContext(ctx context.Context) (v Line, err error) {
	if r.scanner == nil {
		err = io.EOF
		return
	}
	var raw []byte
	for {
		if ctx.Err() != nil {
			err = ctx.Err()
			return
		}

		// Read next line
		raw, err = r.scanner.scan()
		if err != nil {
			return
		} else if len(raw) == 0 {
			continue
		}
		if !r.quote.ConsumeIo(len(raw)) {
			err = ErrQuotaExceeded
			r.Close()
			return
		}

		// Pre-filter line
		str := util.UnsafeString(raw)
		if !r.filter.TestRaw(str) {
			continue
		}

		// Parse line, suppress errors
		v.Value, err = r.parser.Parse(str)
		v.Raw = raw
		if err != nil {
			err = nil
			continue
		}

		// Post-filter line
		if inc, stop := r.filter.Test(v, r.flags); stop {
			err = io.EOF
			r.Close()
			return
		} else if !inc {
			continue
		}
		if !r.quote.ConsumeLine() {
			err = ErrQuotaExceeded
			r.Close()
			return
		}

		// Done
		return v, nil
	}
}
func (r *Parser) Next() (v Line, err error) {
	return r.NextContext(context.Background())
}
func (r *Parser) WithFilter(f ...Filter) *Parser {
	r.filter = r.filter.Append(f...)
	return r
}
func (r *Parser) Close() error {
	r.scanner = nil
	closer := r.closer
	r.closer = nil
	if closer == nil {
		return nil
	}
	return closer.Close()
}
func (r *Parser) WithQuota(q *ParserQuota) *Parser {
	r.quote = q
	return r
}
func (r *Parser) Filters() []Filter {
	return r.filter
}
func (r *Parser) Flags() StreamFlag {
	return r.flags
}
func (r *Parser) IsTail() bool {
	return r.flags&StreamTail == 0
}

type tailScanner struct {
	bs *backscanner.Scanner
}

func (r tailScanner) scan() (line []byte, err error) {
	line, _, err = r.bs.LineBytes()
	return
}

type headScanner struct {
	sc *bufio.Scanner
}

func (r headScanner) scan() (line []byte, err error) {
	if !r.sc.Scan() {
		if err = r.sc.Err(); err == nil {
			err = io.EOF
		}
		return
	}
	line = r.sc.Bytes()
	return
}

func newTailParser(r io.Reader, fl StreamFlag, closer io.Closer) (*Parser, error) {
	ra, rok := r.(io.ReaderAt)
	seeker, seekok := r.(io.Seeker)
	if !rok || !seekok {
		if fl&StreamBufferOk != 0 {
			data, err := io.ReadAll(r)
			if err != nil {
				return nil, err
			}
			bufr := bytes.NewReader(data)
			ra, seeker = bufr, bufr
			closer.Close()
			closer = multiCloser{}
		} else {
			return nil, errors.New("tail parser requires random access to the file")
		}
	}

	end, err := seeker.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}

	fl |= StreamTail
	fl &= ^StreamHead
	scanner := tailScanner{backscanner.New(ra, int(end))}
	return &Parser{
		scanner: scanner,
		flags:   fl,
		parser:  fastjson.Parser{},
		quote:   nil,
		closer:  closer,
	}, nil
}
func newHeadParser(r io.Reader, fl StreamFlag, closer io.Closer) *Parser {
	fl |= StreamHead
	fl &= ^StreamTail
	scanner := headScanner{bufio.NewScanner(r)}
	return &Parser{
		scanner: scanner,
		flags:   fl,
		parser:  fastjson.Parser{},
		quote:   nil,
		closer:  closer,
	}
}

type multiCloser []io.Closer

func (r multiCloser) Close() error {
	var err error
	for _, c := range r {
		if e := c.Close(); e != nil {
			err = e
		}
	}
	return err
}

func NewParser(r io.ReadCloser, fl StreamFlag) (p *Parser, err error) {
	dir := fl & (StreamTail | StreamHead)
	if dir == 0 {
		fl |= StreamTail | StreamHead
		fl ^= StreamBufferOk
	}

	if fl&StreamGzip != 0 {
		gz, err := gzip.NewReader(r)
		if err != nil {
			r.Close()
			return nil, err
		}
		closer := multiCloser{r, gz}
		if fl&StreamHead == 0 {
			return newTailParser(gz, fl, closer)
		} else {
			return newHeadParser(gz, fl, closer), nil
		}
	}

	if fl&StreamTail != 0 {
		p, err = newTailParser(r, fl, r)
		if err == nil || fl&StreamHead == 0 {
			return p, err
		}
	}
	return newHeadParser(r, fl, r), nil
}

// NewFileParser creates a new parser for the file at path.
// The file is opened in read-only mode.
// If the file is a gzip file, it is automatically decompressed.
// In which case, the returned parser will always be a head parser.
func NewFileParser(path string, fl StreamFlag) (*Parser, error) {
	if !filepath.IsAbs(path) {
		path = config.LogDir.File(path)
	}
	f, err := os.OpenFile(path, os.O_RDONLY, 0666)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(path, ".gz") {
		fl |= StreamGzip
	}
	return NewParser(f, fl)
}
