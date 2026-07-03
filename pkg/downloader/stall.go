package downloader

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// DownloadStallTimeout bounds how long an in-flight download may receive no
// data before it is aborted. A silently-dropped TCP connection (no FIN/RST)
// would otherwise block the body read forever, freezing an install at N bytes
// until an external reaper kills it. Overridable (tests set it small); a value
// <= 0 disables the guard.
var DownloadStallTimeout = 60 * time.Second

// idleTimeoutReader wraps a streaming ReadCloser and aborts reads that make no
// progress within timeout. A standard io.Copy blocks indefinitely on a Read
// against a dead-but-unclosed socket; nothing in the copy loop can interrupt a
// blocked syscall. The watchdog timer closes the underlying reader on expiry,
// which unblocks the in-flight Read with an error. Each read that returns data
// resets the idle clock, so a slow-but-steady transfer never trips the guard.
type idleTimeoutReader struct {
	rc      io.ReadCloser
	timeout time.Duration

	mu    sync.Mutex
	timer *time.Timer
	fired bool
	done  bool
}

func newIdleTimeoutReader(rc io.ReadCloser, timeout time.Duration) *idleTimeoutReader {
	r := &idleTimeoutReader{rc: rc, timeout: timeout}
	r.timer = time.AfterFunc(timeout, r.onStall)
	return r
}

// onStall fires when no data has arrived within the timeout. Closing the
// underlying reader is what unblocks a Read parked in the kernel.
func (r *idleTimeoutReader) onStall() {
	r.mu.Lock()
	if r.done {
		r.mu.Unlock()
		return
	}
	r.fired = true
	r.mu.Unlock()
	_ = r.rc.Close()
}

func (r *idleTimeoutReader) Read(p []byte) (int, error) {
	n, err := r.rc.Read(p)
	if n > 0 {
		r.timer.Reset(r.timeout)
	}
	if err != nil {
		r.mu.Lock()
		fired := r.fired
		r.mu.Unlock()
		if fired {
			// Translate the "use of closed connection" the watchdog induced
			// into an actionable stall error. This is not context.Canceled,
			// so the caller keeps the .partial file for a later resume.
			return n, fmt.Errorf("download stalled: no data received for %s", r.timeout)
		}
	}
	return n, err
}

func (r *idleTimeoutReader) Close() error {
	r.mu.Lock()
	r.done = true
	r.mu.Unlock()
	r.timer.Stop()
	return r.rc.Close()
}
