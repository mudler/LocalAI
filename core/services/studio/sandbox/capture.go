package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
)

// CapWriter keeps the first Max bytes and counts the rest. It is safe for
// the two writers (stdout and stderr) of one process. When the cap is
// reached, Full is called once, so that the process can be stopped.
type CapWriter struct {
	Max  int
	Full func()

	mu    sync.Mutex
	buf   bytes.Buffer
	extra int64
}

func (w *CapWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	room := min(w.Max-w.buf.Len(), len(p))
	if room > 0 {
		w.buf.Write(p[:room])
	}
	if w.extra == 0 && room < len(p) && w.Full != nil {
		w.Full()
	}
	w.extra += int64(len(p) - max(room, 0))
	return len(p), nil
}

// Cut reports whether output was dropped at the cap.
func (w *CapWriter) Cut() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.extra > 0
}

// String returns the kept output as valid UTF-8, with a marker that tells how
// many bytes were dropped at the cap.
func (w *CapWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := strings.ToValidUTF8(w.buf.String(), "�")
	if w.extra > 0 {
		s += fmt.Sprintf("\n[output cut: %d more bytes]", w.extra)
	}
	return s
}

// RunCapped runs args in sb and returns the output of stdout and stderr in one
// string, and the exit code. When the output reaches limit, it stops the
// process at once and returns the output with its cut marker, and no error.
func RunCapped(ctx context.Context, sb Sandbox, args []string, stdin io.Reader, limit int) (string, int, error) {
	rctx, stop := context.WithCancel(ctx)
	defer stop()
	out := &CapWriter{Max: limit, Full: stop}
	p, err := sb.Exec(rctx, Cmd{Args: args, Stdin: stdin, Stdout: out, Stderr: out})
	if err != nil {
		return "", 0, err
	}
	code, err := p.Wait()
	if ctx.Err() == nil && rctx.Err() != nil && out.Cut() {
		return out.String(), 0, nil // stopped by us at the cap
	}
	return out.String(), code, err
}
