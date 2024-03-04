package xlog

import (
	"bytes"
	"io"
	"log/slog"

	"get.pme.sh/pmesh/textproc"
	"get.pme.sh/pmesh/util"

	slogzerolog "github.com/samber/slog-zerolog/v2"
)

type TextAdapter struct {
	logger       *Logger
	defaultLevel Level
	buf          bytes.Buffer
	e            *Event
}

func (w *TextAdapter) Write(p []byte) (n int, err error) {
	return w.WriteLevel(w.defaultLevel, p)
}
func (w *TextAdapter) WriteLevel(lv Level, p []byte) (n int, err error) {
	if w.e == nil {
		e := w.logger.WithLevel(lv)
		if !e.Enabled() {
			return len(p), nil
		}
		w.e = e
	}
	return w.buf.Write(p)
}
func (w *TextAdapter) Flush() error {
	if e := w.e; e != nil {
		w.e = nil
		buf := w.buf.Bytes()
		e.Msg(util.UnsafeString(buf))
		w.buf.Reset()
	}
	return nil
}

var textToLine = textproc.NewEncoding().
	WithNoANSI().
	WithDiscardSet("\r\x00\f").
	WithLineFlush()

// Creates a new writer that writes valid JSON objects to the logger.
func ToTextWriter(logger *Logger, level Level) (w io.Writer, te *TextAdapter) {
	te = &TextAdapter{logger: logger, defaultLevel: level}
	return textToLine.NewEncoder(te), te
}

// Creates a new slog.Logger that writes to the logger.
func ToSlog(logger *Logger) *slog.Logger {
	return slog.New(slogzerolog.Option{
		Logger: logger,
	}.NewZerologHandler())
}
