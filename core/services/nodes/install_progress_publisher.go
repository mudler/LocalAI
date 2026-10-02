package nodes

import (
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/workerctl"
)

// DebouncedInstallProgressSink buffers backend-install download ticks and
// hands them to emit at most once per `interval`. Always emits the final
// event on Flush so the UI sees the terminal percentage. The debounce lives
// here rather than in the carrier behind emit, so every carrier sees the same
// bounded event rate.
//
// Behavior: leading-edge debounce. The first OnDownload after a quiet window
// emits immediately; subsequent ticks within `interval` only buffer the
// latest event, which is then emitted via a single trailing timer. This
// keeps the wire chatter bounded (~4 events per second at 250ms) while
// still surfacing every meaningful percentage jump.
//
// Lock ordering: never hold p.mu across an emit call. emit may block on a
// slow link, and we don't want a stalled network to stall the underlying
// gallery download loop.
type DebouncedInstallProgressSink struct {
	mu            sync.Mutex
	emit          func(workerctl.BackendInstallProgressEvent)
	nodeID        string
	opID          string
	backend       string
	interval      time.Duration
	lastEmittedAt time.Time
	pending       *workerctl.BackendInstallProgressEvent
	timer         *time.Timer
}

// NewDebouncedInstallProgressSink constructs a sink for one install
// operation. interval is the leading-edge debounce window (~250ms in
// production).
func NewDebouncedInstallProgressSink(emit func(workerctl.BackendInstallProgressEvent), nodeID, opID, backend string, interval time.Duration) *DebouncedInstallProgressSink {
	return &DebouncedInstallProgressSink{
		emit:     emit,
		nodeID:   nodeID,
		opID:     opID,
		backend:  backend,
		interval: interval,
	}
}

// OnDownload is the callback shape gallery.InstallBackendFromGallery and
// galleryop.InstallExternalBackend pass into the worker. Each invocation
// represents a single tick from the underlying io.Reader copy loop.
func (p *DebouncedInstallProgressSink) OnDownload(file, current, total string, percentage float64) {
	ev := workerctl.BackendInstallProgressEvent{
		OpID:       p.opID,
		NodeID:     p.nodeID,
		Backend:    p.backend,
		FileName:   file,
		Current:    current,
		Total:      total,
		Percentage: percentage,
		Phase:      workerctl.PhaseDownloading,
	}

	p.mu.Lock()
	now := time.Now()
	if p.lastEmittedAt.IsZero() || now.Sub(p.lastEmittedAt) >= p.interval {
		// Leading edge: emit immediately.
		p.lastEmittedAt = now
		p.pending = nil
		p.mu.Unlock()
		p.emit(ev)
		return
	}
	// Within the window: buffer the latest event and arm a trailing
	// emit. If a timer is already armed, we just overwrite p.pending so
	// the trailing emit carries the freshest data.
	p.pending = &ev
	if p.timer == nil {
		delay := p.interval - now.Sub(p.lastEmittedAt)
		p.timer = time.AfterFunc(delay, p.flushPending)
	}
	p.mu.Unlock()
}

// flushPending is the trailing-edge emitter fired by the AfterFunc timer.
// It clears the pending slot under the lock, then emits outside the lock so
// emit never blocks an in-progress OnDownload call.
func (p *DebouncedInstallProgressSink) flushPending() {
	p.mu.Lock()
	p.timer = nil
	pending := p.pending
	p.pending = nil
	if pending != nil {
		p.lastEmittedAt = time.Now()
	}
	p.mu.Unlock()
	if pending != nil {
		p.emit(*pending)
	}
}

// Flush emits any pending buffered event synchronously and stops the
// pending timer. Safe to call multiple times. Callers MUST defer Flush
// after constructing the sink so the terminal percentage reaches the
// master even on error returns.
func (p *DebouncedInstallProgressSink) Flush() {
	p.mu.Lock()
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	pending := p.pending
	p.pending = nil
	p.mu.Unlock()
	if pending != nil {
		p.emit(*pending)
	}
}
