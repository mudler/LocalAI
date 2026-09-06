package model

import (
	"bytes"
	"context"
	"log/slog"
	"sync"

	"github.com/mudler/xlog"
)

// xlog keeps its logger in a package-level variable that SetLogger writes
// without a lock, so every xlog.Info in the process reads that variable
// concurrently with the write. A spec that swapped the logger to capture output
// and swapped it back on cleanup therefore raced with any goroutine this
// package had left logging: the backend process watcher, which logs while a
// process is stopping, is one of those on every run.
//
// So SetLogger is called exactly ONCE for this whole test binary, from init,
// before a goroutine exists to race with. What a spec swaps afterwards is the
// destination, under a mutex, through the routing handler below. That keeps the
// per-spec level filtering intact, which matters: at least one spec asserts
// that a debug emission is filtered OUT, and would pass vacuously against a
// handler that simply recorded everything.
type routingLogHandler struct {
	mu sync.Mutex
	to slog.Handler
}

// sharedLogHandler is the one handler xlog is given for this binary.
var sharedLogHandler = &routingLogHandler{}

func init() {
	xlog.SetLogger(xlog.NewLoggerWithHandler(sharedLogHandler, xlog.LogLevelInfo))
}

func (h *routingLogHandler) current() slog.Handler {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.to
}

// arm points the shared handler at inner, or discards everything when inner is
// nil. Returns nothing: a spec restores by arming nil, because xlog exposes no
// getter and there is no previous value to hand back.
func (h *routingLogHandler) arm(inner slog.Handler) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.to = inner
}

func (h *routingLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	inner := h.current()
	return inner != nil && inner.Enabled(ctx, level)
}

func (h *routingLogHandler) Handle(ctx context.Context, r slog.Record) error {
	inner := h.current()
	if inner == nil {
		return nil
	}
	return inner.Handle(ctx, r)
}

// WithAttrs and WithGroup hand back the router itself. xlog never calls either
// (it has no With), and a copy would be a second handler holding the same
// mutex by value.
func (h *routingLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *routingLogHandler) WithGroup(string) slog.Handler      { return h }

var _ slog.Handler = (*routingLogHandler)(nil)

// syncBuffer is a bytes.Buffer that a log handler and a poller may share.
//
// The diagnostic under test is written by a goroutine and read by an Eventually
// on the spec goroutine, which is a plain concurrent use of a bytes.Buffer.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLogs sends everything logged through xlog at or above level into a
// fresh buffer, until stopCapturingLogs is called.
func captureLogs(level slog.Level) *syncBuffer {
	captured := &syncBuffer{}
	sharedLogHandler.arm(slog.NewTextHandler(captured, &slog.HandlerOptions{Level: level}))
	return captured
}

// stopCapturingLogs sends everything logged afterwards nowhere, which is what
// a test binary wants of a package whose goroutines outlive their spec.
func stopCapturingLogs() { sharedLogHandler.arm(nil) }
