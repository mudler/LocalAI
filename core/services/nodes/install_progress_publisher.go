package nodes

import (
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// DebouncedInstallProgressPublisher buffers backend-install download ticks
// and hands them to its emit sink at most once per `interval`. Always emits the
// final event on Flush so the UI sees the terminal percentage.
//
// The sink is a function rather than a NATS subject because the same debounce
// now bounds two carriers: the NATS progress subject an agent node still
// publishes on, and a line written into the streaming HTTP response a worker
// serves over its tunnel. Keeping one debouncer keeps the ~4/s tick bound a
// single fact instead of one per carrier.
//
// Behavior: leading-edge debounce. The first OnDownload after a quiet window
// publishes immediately; subsequent ticks within `interval` only buffer the
// latest event, which is then emitted via a single trailing timer. This
// keeps the wire chatter bounded (~4 events per second at 250ms) while
// still surfacing every meaningful percentage jump.
//
// Lock ordering: never hold p.mu across an emit call. The sink writes to the
// network, which may block on a slow link, and we don't want a stalled network
// to stall the underlying gallery download loop.
type DebouncedInstallProgressPublisher struct {
	mu              sync.Mutex
	emit            func(messaging.BackendInstallProgressEvent)
	nodeID          string
	opID            string
	backend         string
	interval        time.Duration
	lastPublishedAt time.Time
	pending         *messaging.BackendInstallProgressEvent
	timer           *time.Timer
}

// NewDebouncedInstallProgressPublisher constructs a publisher for one install
// operation that publishes to the per-op NATS progress subject. interval is the
// leading-edge debounce window (~250ms in production).
func NewDebouncedInstallProgressPublisher(client messaging.MessagingClient, nodeID, opID, backend string, interval time.Duration) *DebouncedInstallProgressPublisher {
	subject := messaging.SubjectNodeBackendInstallProgress(nodeID, opID)
	return NewDebouncedInstallProgressSink(func(ev messaging.BackendInstallProgressEvent) {
		_ = client.Publish(subject, ev)
	}, nodeID, opID, backend, interval)
}

// NewDebouncedInstallProgressSink constructs a publisher for one install
// operation that hands each debounced event to emit.
//
// emit is called with p.mu released, so a sink that blocks on a slow link
// cannot stall the gallery download loop that feeds it.
func NewDebouncedInstallProgressSink(emit func(messaging.BackendInstallProgressEvent), nodeID, opID, backend string, interval time.Duration) *DebouncedInstallProgressPublisher {
	return &DebouncedInstallProgressPublisher{
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
func (p *DebouncedInstallProgressPublisher) OnDownload(file, current, total string, percentage float64) {
	ev := messaging.BackendInstallProgressEvent{
		OpID:       p.opID,
		NodeID:     p.nodeID,
		Backend:    p.backend,
		FileName:   file,
		Current:    current,
		Total:      total,
		Percentage: percentage,
		Phase:      messaging.PhaseDownloading,
	}

	p.mu.Lock()
	now := time.Now()
	if p.lastPublishedAt.IsZero() || now.Sub(p.lastPublishedAt) >= p.interval {
		// Leading edge: publish immediately.
		p.lastPublishedAt = now
		p.pending = nil
		p.mu.Unlock()
		p.emit(ev)
		return
	}
	// Within the window: buffer the latest event and arm a trailing
	// publish. If a timer is already armed, we just overwrite p.pending so
	// the trailing publish carries the freshest data.
	p.pending = &ev
	if p.timer == nil {
		delay := p.interval - now.Sub(p.lastPublishedAt)
		p.timer = time.AfterFunc(delay, p.flushPending)
	}
	p.mu.Unlock()
}

// flushPending is the trailing-edge publisher fired by the AfterFunc timer.
// It clears the pending slot under the lock, then emits outside the lock so the
// sink never blocks an in-progress OnDownload call.
func (p *DebouncedInstallProgressPublisher) flushPending() {
	p.mu.Lock()
	p.timer = nil
	pending := p.pending
	p.pending = nil
	if pending != nil {
		p.lastPublishedAt = time.Now()
	}
	p.mu.Unlock()
	if pending != nil {
		p.emit(*pending)
	}
}

// Flush emits any pending buffered event synchronously and stops the pending
// timer. Safe to call multiple times. Callers MUST defer Flush after
// constructing the publisher so the terminal percentage reaches the master even
// on error returns.
func (p *DebouncedInstallProgressPublisher) Flush() {
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
