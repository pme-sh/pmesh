package autonats

import (
	"strings"

	"get.pme.sh/pmesh/xlog"
)

type Logger struct {
	*xlog.Logger
}

func (l Logger) Debugf(format string, v ...any) {
	l.Logger.Trace().Msgf(format, v...)
}
func (l Logger) Tracef(format string, v ...any) {
	l.Logger.Trace().Msgf(format, v...)
}
func (l Logger) Errorf(format string, v ...any) {
	l.Logger.Error().Msgf(format, v...)
}
func (l Logger) Fatalf(format string, v ...any) {
	l.Logger.Fatal().Msgf(format, v...)
}
func (l Logger) Warnf(format string, v ...any) {
	// Not a real issue since we don't accept connections
	if strings.HasPrefix(format, "Plaintext passwords") {
		return
	}
	l.Logger.Warn().Msgf(format, v...)
}
func (l Logger) Noticef(format string, v ...any) {
	l.Logger.Debug().Msgf(format, v...)
}
