package xlog

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

var callerMaxWSeen atomic.Int32

const (
	callerMaxW = 30
	callerMinW = 10
)

func getCallerNameLen(n int) int {
	for {
		maxr := callerMaxWSeen.Load()
		if maxr >= int32(n) {
			n = int(maxr)
			break
		}
		if callerMaxWSeen.CompareAndSwap(maxr, int32(n)) {
			break
		}
	}
	return max(min(n, callerMaxW), callerMinW)
}

func NewConsoleWriter(f io.Writer) io.Writer {
	// file, ok := f.(*os.File); ok && term.IsTerminal(int(file.Fd())) && !*config.Dumb
	return &zerolog.ConsoleWriter{
		Out: f,
		FormatTimestamp: func(i any) string {
			ms, _ := i.(json.Number)
			msi, _ := ms.Int64()
			if msi == 0 {
				return ""
			}
			ts := time.UnixMilli(msi)
			if now := time.Now(); ts.Year() != now.Year() {
				return ts.Format("2006-01-02 15:04:05")
			} else if ts.YearDay() != now.YearDay() {
				return ts.Format("01-02 15:04:05")
			} else {
				return ts.Format(time.Kitchen)
			}
		},
		FormatCaller: func(i any) string {
			n, ok := i.(string)
			if !ok {
				return ""
			}

			preferw := getCallerNameLen(len(n))
			if x := preferw - len(n); x < 0 {
				n = n[:preferw-1] + "…"
			} else {
				n += strings.Repeat(" ", x)
			}
			return fmt.Sprintf("│ \x1b[1m%s\x1b[0m", n)
		},
	}
}
func StdoutWriter() io.Writer { return NewConsoleWriter(os.Stdout) }
func StderrWriter() io.Writer { return NewConsoleWriter(os.Stderr) }
