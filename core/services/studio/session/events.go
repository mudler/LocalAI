package session

import "sync"

// Limits of the event queue of a chat.
const (
	// textRing is how many text events wait for a consumer. When it is full,
	// the oldest text event is dropped.
	textRing = 512
	// reasoningRing is how many reasoning events wait for a consumer. When it
	// is full, the oldest reasoning event is dropped. They count neither in
	// textRing nor in maxQueued.
	reasoningRing = 2048
	// maxQueued is how many other events wait for a consumer. When it is
	// full, tool_start and tool_end events are dropped, oldest first. The
	// other kinds are never dropped.
	maxQueued = 10000
	// eventBuffer is the buffer of the channel that Events returns.
	eventBuffer = 512
)

// eventBus holds the events of a chat until the consumer reads them. push
// never blocks. A goroutine moves the events to the channel in order.
type eventBus struct {
	ch   chan Event
	quit chan struct{}
	wake chan struct{}

	mu          sync.Mutex
	q           []Event
	texts       int // text events in q
	reasonings  int // reasoning events in q
	closed      bool
	droppedText int
	droppedTool int
	droppedReas int
}

func newEventBus() *eventBus {
	b := &eventBus{ch: make(chan Event, eventBuffer), quit: make(chan struct{}), wake: make(chan struct{}, 1)}
	go b.pump()
	return b
}

func isToolKind(k EventKind) bool { return k == KindToolStart || k == KindToolEnd }

// count adds d to the counter of e's kind. The caller holds b.mu.
func (b *eventBus) count(e Event, d int) {
	switch e.Kind {
	case KindText:
		b.texts += d
	case KindReasoning:
		b.reasonings += d
	}
}

// removeFirst deletes the first queued event for which match is true and
// reports whether there was one. The caller holds b.mu.
func (b *eventBus) removeFirst(match func(Event) bool) bool {
	for i, e := range b.q {
		if match(e) {
			b.count(e, -1)
			b.q = append(b.q[:i], b.q[i+1:]...)
			return true
		}
	}
	return false
}

func (b *eventBus) push(e Event) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	switch {
	case e.Kind == KindText:
		if b.texts >= textRing {
			b.removeFirst(func(x Event) bool { return x.Kind == KindText })
			b.droppedText++
		}
		b.texts++
	case e.Kind == KindReasoning:
		if b.reasonings >= reasoningRing {
			b.removeFirst(func(x Event) bool { return x.Kind == KindReasoning })
			b.droppedReas++
		}
		b.reasonings++
	case isToolKind(e.Kind) && len(b.q)-b.texts-b.reasonings >= maxQueued:
		if b.removeFirst(func(x Event) bool { return isToolKind(x.Kind) }) {
			b.droppedTool++
		}
	}
	b.q = append(b.q, e)
	b.mu.Unlock()
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// pop returns the first queued event.
func (b *eventBus) pop() (Event, bool, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.q) == 0 {
		return Event{}, false, b.closed
	}
	e := b.q[0]
	b.q[0] = Event{}
	b.q = b.q[1:]
	b.count(e, -1)
	return e, true, b.closed
}

func (b *eventBus) pump() {
	for {
		e, ok, closed := b.pop()
		if ok {
			select {
			case b.ch <- e:
			case <-b.quit:
				b.flush(e)
				return
			}
			continue
		}
		if closed {
			close(b.ch)
			return
		}
		select {
		case <-b.wake:
		case <-b.quit:
		}
	}
}

// flush moves what fits into the channel without waiting, then closes it.
func (b *eventBus) flush(first Event) {
	e, ok := first, true
	for ok {
		select {
		case b.ch <- e:
		default:
			close(b.ch)
			return
		}
		e, ok, _ = b.pop()
	}
	close(b.ch)
}

// close stops the bus. Events that fit in the channel stay readable; the
// channel is closed.
func (b *eventBus) close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	b.mu.Unlock()
	close(b.quit)
}

// dropped returns how many events the bus dropped.
func (b *eventBus) dropped() (text, tool int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.droppedText, b.droppedTool
}

// droppedReasoning returns how many reasoning events the bus dropped.
func (b *eventBus) droppedReasoning() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.droppedReas
}

// closedBus returns a bus that is closed already: Events of a closed chat
// returns its channel, which is closed, and emit does nothing.
func closedBus() *eventBus {
	b := &eventBus{ch: make(chan Event), quit: make(chan struct{}), wake: make(chan struct{}, 1), closed: true}
	close(b.ch)
	return b
}
