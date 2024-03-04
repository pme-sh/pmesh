package xlog

import (
	"io/fs"
	"os"
	"slices"
	"strings"
	"time"

	"get.pme.sh/pmesh/config"
)

type FileKind uint8

const (
	ActiveLog     FileKind = iota // Active log     | hi.log
	InactiveLog                   // Inactive log   | hi-2024-02-29T00-00-23.275.log
	CompressedLog                 // Compressed log | hi-2024-02-29T00-22-01.211.log.gz
)

type FileInfo struct {
	File    fs.DirEntry // File info
	Name    string      // Name of the log file
	LastUse time.Time   // Upper bound (if inactive or compressed)
	Kind    FileKind    // Kind of log file
}

// ReadDir reads the log directory and returns a list of log files.
func ReadDir() ([]FileInfo, error) {
	files, err := os.ReadDir(config.LogDir.Path())
	if err != nil {
		return nil, err
	}

	var logs []FileInfo
	for _, file := range files {
		lf, ok := parseLogFile(file)
		if !ok {
			continue
		}
		logs = append(logs, lf)
	}
	slices.SortFunc(logs, func(a FileInfo, b FileInfo) int {
		if a.LastUse.Before(b.LastUse) {
			return -1
		}
		if a.LastUse.After(b.LastUse) {
			return 1
		}
		return 0
	})
	return logs, nil
}
func cutTimestamp(s string) (before string, timestamp time.Time) {
	const timestampFormat = "2006-01-02T15-04-05.000"
	it, itok := s, true
	for len(it) > len(timestampFormat) {
		_, it, itok = strings.Cut(it, "-")
		if !itok {
			break
		}
		if t, err := time.Parse(timestampFormat, it); err == nil {
			return s[:len(s)-len(it)-1], t
		}
	}
	return s, time.Time{}
}
func parseLogFile(e fs.DirEntry) (FileInfo, bool) {
	f := FileInfo{File: e}

	// Parse the name of the log file.
	name, compressed := strings.CutSuffix(e.Name(), ".gz")
	name, ok := strings.CutSuffix(name, ".log")
	if !ok || name == "" {
		return f, false
	}

	// Parse the timestamp of the log file.
	f.Name, f.LastUse = cutTimestamp(name)

	// If the timestamp is not valid, the log file is active.
	if f.LastUse.IsZero() {
		if compressed {
			return f, false
		}
		f.Kind = ActiveLog
		f.LastUse = time.Now()
		return f, true
	}

	// If the timestamp is valid, the log file is inactive or compressed.
	if compressed {
		f.Kind = CompressedLog
	} else {
		f.Kind = InactiveLog
	}
	return f, true
}
