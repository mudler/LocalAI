package galleryop

import (
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/modelartifacts"
)

type artifactProgressTicker interface {
	Chan() <-chan time.Time
	Stop()
}

type realArtifactProgressTicker struct {
	ticker *time.Ticker
}

func (t *realArtifactProgressTicker) Chan() <-chan time.Time { return t.ticker.C }

func (t *realArtifactProgressTicker) Stop() { t.ticker.Stop() }

func newRealArtifactProgressTicker(interval time.Duration) artifactProgressTicker {
	return &realArtifactProgressTicker{ticker: time.NewTicker(interval)}
}

var newArtifactProgressTicker = newRealArtifactProgressTicker

type artifactProgressCoalescer struct {
	mu        sync.Mutex
	forwardMu sync.Mutex
	closed    bool
	pending   *modelartifacts.ProgressEvent
	ticker    artifactProgressTicker
	done      chan struct{}
	forward   modelartifacts.ProgressSink
}

func newArtifactProgressCoalescer(interval time.Duration, forward modelartifacts.ProgressSink) *artifactProgressCoalescer {
	c := &artifactProgressCoalescer{
		ticker:  newArtifactProgressTicker(interval),
		done:    make(chan struct{}),
		forward: forward,
	}
	go c.run()
	return c
}

func (c *artifactProgressCoalescer) Sink(event modelartifacts.ProgressEvent) {
	if event.Phase == modelartifacts.PhaseDownloading {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.closed {
			return
		}
		c.pending = &event
		return
	}

	c.forwardMu.Lock()
	defer c.forwardMu.Unlock()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	pending := c.takePendingLocked()
	c.mu.Unlock()

	c.forwardEvent(pending)
	c.forwardEvent(&event)
}

func (c *artifactProgressCoalescer) Close() {
	c.forwardMu.Lock()
	defer c.forwardMu.Unlock()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	pending := c.takePendingLocked()
	close(c.done)
	c.ticker.Stop()
	c.mu.Unlock()

	c.forwardEvent(pending)
}

func (c *artifactProgressCoalescer) run() {
	for {
		select {
		case <-c.ticker.Chan():
			c.flush()
		case <-c.done:
			return
		}
	}
}

func (c *artifactProgressCoalescer) flush() {
	c.forwardMu.Lock()
	defer c.forwardMu.Unlock()

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	pending := c.takePendingLocked()
	c.mu.Unlock()

	c.forwardEvent(pending)
}

func (c *artifactProgressCoalescer) takePendingLocked() *modelartifacts.ProgressEvent {
	pending := c.pending
	c.pending = nil
	return pending
}

func (c *artifactProgressCoalescer) forwardEvent(event *modelartifacts.ProgressEvent) {
	if event != nil && c.forward != nil {
		c.forward(*event)
	}
}
