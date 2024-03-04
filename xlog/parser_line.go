package xlog

import (
	"time"

	"get.pme.sh/pmesh/util"
	"github.com/rs/zerolog"
	"github.com/valyala/fastjson"
)

type Line struct {
	*fastjson.Value
	Raw []byte
}

func ParseLine(line []byte) (ll Line, err error) {
	v, err := fastjson.ParseBytes(line)
	if err != nil {
		return
	}
	return Line{v, line}, nil
}

func (ll Line) Domain() string {
	sb := ll.GetStringBytes(zerolog.CallerFieldName)
	return util.UnsafeString(sb)
}
func (ll Line) Time() time.Time {
	f := ll.GetInt64(zerolog.TimestampFieldName)
	if f == 0 {
		return time.Time{}
	}
	return time.UnixMilli(f)
}
func (ll Line) Level() Level {
	level := ll.Get(zerolog.LevelFieldName)
	if level != nil {
		if level.Type() == fastjson.TypeString {
			lstr := level.GetStringBytes()
			if v, ok := unformatLevel[util.UnsafeString(lstr)]; ok {
				return v
			}
		} else if level.Type() == fastjson.TypeNumber {
			return Level(level.GetInt())
		}
	}
	return LevelNone
}

var unformatLevel = map[string]Level{
	zerolog.LevelTraceValue: LevelTrace,
	zerolog.LevelDebugValue: LevelDebug,
	zerolog.LevelInfoValue:  LevelInfo,
	zerolog.LevelWarnValue:  LevelWarn,
	zerolog.LevelErrorValue: LevelError,
	zerolog.LevelFatalValue: LevelFatal,
	zerolog.LevelPanicValue: LevelPanic,
	"disabled":              LevelSuppress,
	"":                      LevelNone,
}
