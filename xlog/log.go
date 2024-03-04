package xlog

import (
	"context"
	"fmt"
	"io"
	llog "log"
	"log/slog"

	pkgerr "github.com/pkg/errors"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/rs/zerolog/pkgerrors"
)

type Logger = zerolog.Logger
type Level = zerolog.Level
type LevelWriter = zerolog.LevelWriter
type Context = zerolog.Context
type Event = zerolog.Event
type Hook = zerolog.Hook
type Sampler = zerolog.Sampler

const (
	LevelDebug    = zerolog.DebugLevel
	LevelInfo     = zerolog.InfoLevel
	LevelWarn     = zerolog.WarnLevel
	LevelError    = zerolog.ErrorLevel
	LevelFatal    = zerolog.FatalLevel
	LevelPanic    = zerolog.PanicLevel
	LevelNone     = zerolog.NoLevel
	LevelSuppress = zerolog.Disabled
	LevelTrace    = zerolog.TraceLevel
)

var defaultOutput = StderrWriter()

type DefaultWriter struct{}

func (DefaultWriter) Write(p []byte) (n int, err error) { return defaultOutput.Write(p) }

func Default() *Logger { return &log.Logger }

// Not safe for concurrent use.
func SetDefaultOutput(w ...io.Writer) {
	defaultOutput = zerolog.MultiLevelWriter(w...)
}

func WrapStackError(err error) error {
	return pkgerr.WithStack(err)
}
func NewStackError(msg string) error {
	return pkgerr.New(msg)
}
func NewStackErrorf(fmt string, args ...any) error {
	return pkgerr.Errorf(fmt, args...)
}

// Replaces all defaults.
func init() {
	log.Logger = *NewDomain("pmesh", DefaultWriter{})

	passthrough, _ := ToTextWriter(&log.Logger, LevelInfo)
	slog.SetDefault(ToSlog(&log.Logger))
	llog.Default().SetOutput(passthrough)

	zerolog.LevelFieldName = "l"
	zerolog.TimestampFieldName = "t"
	zerolog.MessageFieldName = "msg"
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnixMs
	zerolog.CallerFieldName = DomainFieldName
	zerolog.DefaultContextLogger = &log.Logger
	zerolog.ErrorStackMarshaler = pkgerrors.MarshalStack
}

// SetLoggerLevel sets the global logger level.
func SetLoggerLevel(level Level) {
	zerolog.SetGlobalLevel(level)
}

// Output duplicates the global logger and sets w as its output.
func Output(w io.Writer) Logger {
	return log.Logger.Output(w)
}

// With creates a child logger with the field added to its context.
func With() Context {
	return log.Logger.With()
}

// Err starts a new message with error level with err as a field if not nil or
// with info level if err is nil.
//
// You must call Msg on the returned event in order to send the event.
func Err(err error) *Event {
	return log.Logger.Err(err)
}
func ErrC(ctx context.Context, err error) *Event { return Ctx(ctx).Err(err) }

// ErrStack starts a new message with error level with err as a field if not nil or
// with info level if err is nil. The stack trace is attached to the event.
//
// You must call Msg on the returned event in order to send the event.
func ErrStack(err any) *Event {
	if err != nil {
		e, ok := err.(error)
		if !ok {
			e = NewStackError(fmt.Sprint(err))
		} else {
			e = WrapStackError(e)
		}
		return log.Logger.Error().Stack().Err(e)
	}
	return log.Logger.Info()
}
func ErrStackC(ctx context.Context, err any) *Event {
	if err != nil {
		e, ok := err.(error)
		if !ok {
			e = NewStackError(fmt.Sprint(err))
		} else {
			e = WrapStackError(e)
		}
		return Ctx(ctx).Error().Stack().Err(e)
	}
	return Ctx(ctx).Info()
}

// Trace starts a new message with trace level.
//
// You must call Msg on the returned event in order to send the event.
func Trace() *Event {
	return log.Logger.Trace()
}
func TraceC(ctx context.Context) *Event { return Ctx(ctx).Trace() }

// Debug starts a new message with debug level.
//
// You must call Msg on the returned event in order to send the event.
func Debug() *Event {
	return log.Logger.Debug()
}
func DebugC(ctx context.Context) *Event { return Ctx(ctx).Debug() }

// Info starts a new message with info level.
//
// You must call Msg on the returned event in order to send the event.
func Info() *Event {
	return log.Logger.Info()
}
func InfoC(ctx context.Context) *Event { return Ctx(ctx).Info() }

// Warn starts a new message with warn level.
//
// You must call Msg on the returned event in order to send the event.
func Warn() *Event {
	return log.Logger.Warn()
}
func WarnC(ctx context.Context) *Event { return Ctx(ctx).Warn() }

// Error starts a new message with error level.
//
// You must call Msg on the returned event in order to send the event.
func Error() *Event {
	return log.Logger.Error()
}
func ErrorC(ctx context.Context) *Event { return Ctx(ctx).Error() }

// Fatal starts a new message with fatal level. The os.Exit(1) function
// is called by the Msg method.
//
// You must call Msg on the returned event in order to send the event.
func Fatal() *Event {
	return log.Logger.Fatal()
}
func FatalC(ctx context.Context) *Event { return Ctx(ctx).Fatal() }

// Panic starts a new message with panic level. The message is also sent
// to the panic function.
//
// You must call Msg on the returned event in order to send the event.
func Panic() *Event {
	return log.Logger.Panic()
}
func PanicC(ctx context.Context) *Event { return Ctx(ctx).Panic() }

// WithLevel starts a new message with level.
//
// You must call Msg on the returned event in order to send the event.
func WithLevel(level Level) *Event {
	return log.Logger.WithLevel(level)
}
func WithLevelC(ctx context.Context, level Level) *Event { return Ctx(ctx).WithLevel(level) }

// Log starts a new message with no level. Setting GlobalLevel to
// Disabled will still disable events produced by this method.
//
// You must call Msg on the returned event in order to send the event.
func Log() *Event {
	return log.Logger.Log()
}
func LogC(ctx context.Context) *Event { return Ctx(ctx).Log() }

// Print sends a log event using debug level and no extra field.
// Arguments are handled in the manner of fmt.Print.
func Print(v ...any) {
	log.Logger.Debug().CallerSkipFrame(1).Msg(fmt.Sprint(v...))
}
func PrintC(ctx context.Context, v ...any) { Ctx(ctx).Print(v...) }

// Printf sends a log event using debug level and no extra field.
// Arguments are handled in the manner of fmt.Printf.
func Printf(format string, v ...any) {
	log.Logger.Debug().CallerSkipFrame(1).Msgf(format, v...)
}
func PrintfC(ctx context.Context, format string, v ...any) { Ctx(ctx).Printf(format, v...) }

// Ctx returns the Logger associated with the ctx. If no logger
// is associated, a disabled logger is returned.
func Ctx(ctx context.Context) *Logger {
	return zerolog.Ctx(ctx)
}
