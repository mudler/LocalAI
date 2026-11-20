// Package fanout delivers values to the live subscribers of a key. A
// subscriber that falls behind is closed, not waited for: it reconnects and
// reads the record, so one slow client never stalls the sender. Delivery is
// live only and at most once.
package fanout

import "sync"

// Pipe is a channel that closes itself when its reader falls behind. Its
// sender never blocks, and it is the only closer of the channel, so a send
// after the close is a no-op and never a panic.
type Pipe[T any] struct {
	ch     chan T
	mu     sync.Mutex
	closed bool
}

// NewPipe returns a Pipe that holds up to buffer values.
func NewPipe[T any](buffer int) *Pipe[T] { return &Pipe[T]{ch: make(chan T, buffer)} }

// C is the channel to read. It closes when the pipe closes.
func (p *Pipe[T]) C() <-chan T { return p.ch }

// Send queues v. It reports false when the pipe is closed, or when it was
// full: the pipe is closed then, and the reader sees the end of the channel.
func (p *Pipe[T]) Send(v T) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	select {
	case p.ch <- v:
		return true
	default:
		p.closed = true
		close(p.ch)
		return false
	}
}

// Close closes the channel. It is safe to call more than once.
func (p *Pipe[T]) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.closed {
		p.closed = true
		close(p.ch)
	}
}

// Hub fans the values published under a key out to the subscribers of that key.
type Hub[K comparable, T any] struct {
	buffer int
	mu     sync.Mutex
	subs   map[K]map[*Pipe[T]]struct{}
}

// NewHub returns a Hub whose subscribers each hold up to buffer values.
func NewHub[K comparable, T any](buffer int) *Hub[K, T] {
	return &Hub[K, T]{buffer: buffer, subs: map[K]map[*Pipe[T]]struct{}{}}
}

// Subscribe returns the values published under key from now on. Call cancel
// when done; the channel closes then, or when the subscriber falls behind.
func (h *Hub[K, T]) Subscribe(key K) (<-chan T, func()) {
	p := NewPipe[T](h.buffer)
	h.mu.Lock()
	if h.subs[key] == nil {
		h.subs[key] = map[*Pipe[T]]struct{}{}
	}
	h.subs[key][p] = struct{}{}
	h.mu.Unlock()
	return p.C(), func() { h.drop(key, p) }
}

func (h *Hub[K, T]) drop(key K, p *Pipe[T]) {
	h.mu.Lock()
	h.forget(key, p)
	h.mu.Unlock()
	p.Close()
}

// forget removes p, and the key when it has no subscriber left. The caller
// holds h.mu.
func (h *Hub[K, T]) forget(key K, p *Pipe[T]) {
	delete(h.subs[key], p)
	if len(h.subs[key]) == 0 {
		delete(h.subs, key)
	}
}

// Publish queues v for every subscriber of key. A subscriber with no room is
// closed and removed.
func (h *Hub[K, T]) Publish(key K, v T) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for p := range h.subs[key] {
		if !p.Send(v) {
			h.forget(key, p)
		}
	}
}
