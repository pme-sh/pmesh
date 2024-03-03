package xlog

import (
	"reflect"
	"strings"
	"time"
)

type StreamFlag int

const (
	StreamTail     StreamFlag = 1 << 0
	StreamHead     StreamFlag = 1 << 1
	StreamGzip     StreamFlag = 1 << 2
	StreamBufferOk StreamFlag = 1 << 3 // Buffer is ok to use if we can't seek
)

type Filter interface {
	// TestRaw tests a raw log line for being a candidate for parsing
	// This is used to skip parsing lines that are known to be irrelevant
	TestRaw(string) (include bool)
	// Test tests a parsed log line for inclusion
	Test(line Line, flags StreamFlag) (include bool, stop bool)
	// Tests file info for inclusion
	TestFile(*FileInfo) (include bool)
}

// SearchFilter filters logs by substring
type SearchFilter string

func (f SearchFilter) TestRaw(line string) bool {
	return strings.Contains(line, string(f))
}
func (f SearchFilter) Test(line Line, flags StreamFlag) (bool, bool) {
	return true, false
}
func (f SearchFilter) TestFile(*FileInfo) bool {
	return true
}

// TimeFilter filters logs by time
type TimeFilter struct {
	After, Before time.Time
}

func (f TimeFilter) TestRaw(line string) bool {
	return true
}
func (f TimeFilter) Test(line Line, flags StreamFlag) (ok bool, stop bool) {
	if t := line.Time(); !t.IsZero() {
		if !f.After.IsZero() && t.Before(f.After) {
			// If we're looking for logs after X, and the log is before X, we're done
			stop = (flags & StreamTail) == StreamTail
		} else if !f.Before.IsZero() && t.After(f.Before) {
			// If we're looking for logs before X, and the log is after X, we're done
			stop = (flags & StreamHead) == StreamHead
		} else {
			ok = true
		}
	}
	return
}
func (f TimeFilter) TestFile(fi *FileInfo) bool {
	// If we're looking for logs after X, and the log was last used before X, we're done
	if !f.After.IsZero() && fi.LastUse.Before(f.After) {
		return false
	}
	return true
}

// DomainFilter filters logs by domain
type DomainFilter string

func (d DomainFilter) TestRaw(line string) bool {
	return strings.Contains(line, string(d))
}
func (d DomainFilter) Test(line Line, flags StreamFlag) (bool, bool) {
	return line.Domain() == string(d), false
}
func (d DomainFilter) TestFile(fi *FileInfo) bool {
	return fi.Name == "session" || strings.HasPrefix(fi.Name, string(d))
}

// LevelFilter filters logs by minimum level
type LevelFilter Level

func (f LevelFilter) TestRaw(line string) bool {
	return true
}
func (f LevelFilter) Test(line Line, flags StreamFlag) (bool, bool) {
	if l := line.Level(); l >= Level(f) {
		return true, false
	}
	return false, false
}
func (f LevelFilter) TestFile(*FileInfo) bool {
	return true
}

// MultiFilter is a collection of filters
type MultiFilter []Filter

func (f MultiFilter) TestRaw(line string) bool {
	for _, ff := range f {
		if !ff.TestRaw(line) {
			return false
		}
	}
	return true
}
func (f MultiFilter) Test(line Line, flags StreamFlag) (bool, bool) {
	for _, ff := range f {
		if inc, stop := ff.Test(line, flags); stop {
			return inc, stop
		} else if !inc {
			return false, false
		}
	}
	return true, false
}
func (f MultiFilter) TestFile(fi *FileInfo) bool {
	for _, ff := range f {
		if !ff.TestFile(fi) {
			return false
		}
	}
	return true
}
func (f MultiFilter) As(out any) bool {
	elem := reflect.ValueOf(out).Elem()
	ty := elem.Type()
	for _, ff := range f {
		if reflect.TypeOf(ff) == ty {
			elem.Set(reflect.ValueOf(ff))
			return true
		}
	}
	return false
}

func (f MultiFilter) Append(list ...Filter) MultiFilter {
	for _, ff := range list {
		if ff == nil {
			continue
		} else if m2, ok := ff.(MultiFilter); ok {
			f = append(f, m2...)
		} else {
			f = append(f, ff)
		}
	}
	return f
}
