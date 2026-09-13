package whatsapp

import (
	"fmt"
	"log/slog"

	waLog "go.mau.fi/whatsmeow/util/log"
)

// slogAdapter bridges whatsmeow's logger interface onto slog.
type slogAdapter struct {
	l *slog.Logger
}

func newWALogger(l *slog.Logger) waLog.Logger {
	return slogAdapter{l: l}
}

func (a slogAdapter) Warnf(msg string, args ...any)  { a.l.Warn(fmt.Sprintf(msg, args...)) }
func (a slogAdapter) Errorf(msg string, args ...any) { a.l.Error(fmt.Sprintf(msg, args...)) }
func (a slogAdapter) Infof(msg string, args ...any)  { a.l.Info(fmt.Sprintf(msg, args...)) }
func (a slogAdapter) Debugf(msg string, args ...any) { a.l.Debug(fmt.Sprintf(msg, args...)) }
func (a slogAdapter) Sub(module string) waLog.Logger {
	return slogAdapter{l: a.l.With("module", module)}
}
