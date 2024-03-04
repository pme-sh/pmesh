package xlog

import (
	"io"
	"path/filepath"
	"time"

	"get.pme.sh/pmesh/config"
	"get.pme.sh/pmesh/lru"

	"gopkg.in/natefinch/lumberjack.v2"
)

const (
	LogRetentionDays = 28
	LogMaxSizeMB     = 16
	LogCompress      = false
)

type logger = *lumberjack.Logger

func newLogger(s string) (l logger) {
	return &lumberjack.Logger{
		Filename: s,
		MaxSize:  LogMaxSizeMB,
		MaxAge:   LogRetentionDays,
		Compress: LogCompress,
	}
}

var loggers = lru.Cache[string, logger]{
	Expiry:          5 * time.Minute,
	CleanupInterval: 10 * time.Minute,
	New: func(s string, e *lru.Entry[logger]) error {
		e.Value = newLogger(s)
		return nil
	},
	Evict: func(s string, wc logger) {
		wc.Close()
	},
}

type sharedLogger struct {
	entry *lru.Entry[logger]
	err   error
}

func (s sharedLogger) Close() error {
	if s.err != nil {
		return s.err
	}
	s.entry.Release()
	return nil
}
func (s sharedLogger) Write(p []byte) (n int, e error) {
	if s.err != nil {
		return 0, s.err
	}
	return s.entry.Value.Write(p)
}

type noCloser struct {
	io.Writer
}

func (noCloser) Close() error { return nil }

func FileWriter(name string) (wc io.WriteCloser) {
	if name == "session" || name == "stdout" || name == "stderr" {
		return noCloser{DefaultWriter{}}
	}
	if name == "null" || name == "NUL" || name == "/dev/null" {
		return nil
	}
	if !filepath.IsAbs(name) {
		name = config.LogDir.File(name)
	}
	entry, err := loggers.GetEntry(name)
	return sharedLogger{entry, err}
}
