package galleryop

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/modelartifacts"
)

type legacyProgressUpdate struct {
	fileName   string
	current    string
	total      string
	percentage float64
}

type legacyProgressCoalescer struct {
	mu        sync.Mutex
	forwardMu sync.Mutex
	closed    bool
	pending   *legacyProgressUpdate
	ticker    artifactProgressTicker
	done      chan struct{}
	forward   func(legacyProgressUpdate)
}

func newLegacyProgressCoalescer(interval time.Duration, forward func(legacyProgressUpdate)) *legacyProgressCoalescer {
	c := &legacyProgressCoalescer{
		ticker:  newArtifactProgressTicker(interval),
		done:    make(chan struct{}),
		forward: forward,
	}
	go c.run()
	return c
}

func (c *legacyProgressCoalescer) Sink(fileName, current, total string, percentage float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.pending = &legacyProgressUpdate{fileName: fileName, current: current, total: total, percentage: percentage}
}

func (c *legacyProgressCoalescer) Close() {
	c.forwardMu.Lock()
	defer c.forwardMu.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	pending := c.pending
	c.pending = nil
	close(c.done)
	c.ticker.Stop()
	c.mu.Unlock()
	c.forwardUpdate(pending)
}

func (c *legacyProgressCoalescer) run() {
	for {
		select {
		case <-c.ticker.Chan():
			c.flush()
		case <-c.done:
			return
		}
	}
}

func (c *legacyProgressCoalescer) flush() {
	c.forwardMu.Lock()
	defer c.forwardMu.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	pending := c.pending
	c.pending = nil
	c.mu.Unlock()
	c.forwardUpdate(pending)
}

func (c *legacyProgressCoalescer) forwardUpdate(update *legacyProgressUpdate) {
	if update != nil && c.forward != nil {
		c.forward(*update)
	}
}

func parseDisplayedBytes(value string) (int64, bool) {
	parts := strings.Fields(value)
	if len(parts) != 2 {
		return 0, false
	}
	number, err := strconv.ParseFloat(parts[0], 64)
	if err != nil || number < 0 {
		return 0, false
	}
	multipliers := map[string]float64{
		"B": 1, "KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30,
		"TiB": 1 << 40, "PiB": 1 << 50, "EiB": 1 << 60,
	}
	multiplier, ok := multipliers[parts[1]]
	if !ok {
		return 0, false
	}
	return int64(number * multiplier), true
}

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
