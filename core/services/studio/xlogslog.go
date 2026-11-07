package studio

import (
	"context"
	"log/slog"

	"github.com/mudler/xlog"
)

// xlogHandler sends slog records to the server's own log, so they follow
// --log-level and --log-format. slog.Default() would write its own
// "2006/01/02 15:04:05 INFO ..." line whatever level the server was given.
type xlogHandler struct{ attrs []any }

func (xlogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h xlogHandler) Handle(_ context.Context, r slog.Record) error {
	args := append([]any(nil), h.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		args = append(args, a.Key, a.Value.Any())
		return true
	})
	switch {
	case r.Level >= slog.LevelError:
		xlog.Error(r.Message, args...)
	case r.Level >= slog.LevelWarn:
		xlog.Warn(r.Message, args...)
	case r.Level >= slog.LevelInfo:
		xlog.Info(r.Message, args...)
	default:
		xlog.Debug(r.Message, args...)
	}
	return nil
}

func (h xlogHandler) WithAttrs(as []slog.Attr) slog.Handler {
	n := xlogHandler{attrs: append([]any(nil), h.attrs...)}
	for _, a := range as {
		n.attrs = append(n.attrs, a.Key, a.Value.Any())
	}
	return n
}

func (h xlogHandler) WithGroup(string) slog.Handler { return h }

// serverLogger is the logger a migration uses when its caller passes none.
func serverLogger() *slog.Logger { return slog.New(xlogHandler{}) }
