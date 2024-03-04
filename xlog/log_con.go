package xlog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"get.pme.sh/pmesh/config"
	"github.com/rs/zerolog"
	"golang.org/x/term"
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

func NewConsoleWriter(f io.Writer) LevelWriter {
	if file, ok := f.(*os.File); ok && term.IsTerminal(int(file.Fd())) && !*config.Dumb {
		consoleWriter := &zerolog.ConsoleWriter{
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
			FieldsExclude: []string{zerolog.ErrorStackFieldName},
			FormatExtra: func(m map[string]interface{}, b *bytes.Buffer) error {
				started := false
				if stack, ok := m[zerolog.ErrorStackFieldName]; ok {
					if arr, ok := stack.([]any); ok {
						for _, i := range arr {
							if data, ok := i.(map[string]any); ok {
								if !started {
									b.WriteString("\n│ \x1b[1mStack\x1b[0m\n")
									started = true
								}
								funcn, _ := data["func"].(string)
								line, _ := data["line"].(string)
								source, _ := data["source"].(string)
								source += ":" + line

								if len(source) < 24 {
									source += strings.Repeat(" ", 24-len(source))
								}
								b.WriteString(fmt.Sprintf("│ %s \x1b[1m%s()\x1b[0m\n", source, funcn))
							}
						}
					}
				}
				return nil
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
		if !*config.Verbose {
			return &zerolog.FilteredLevelWriter{
				Level:  LevelInfo,
				Writer: zerolog.LevelWriterAdapter{Writer: consoleWriter},
			}
		}
		return zerolog.LevelWriterAdapter{Writer: consoleWriter}
	}
	return zerolog.LevelWriterAdapter{Writer: f}
}
func StdoutWriter() LevelWriter { return NewConsoleWriter(os.Stdout) }
func StderrWriter() LevelWriter { return NewConsoleWriter(os.Stderr) }
