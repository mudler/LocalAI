package studio

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

// ringGrace is how long the ring of a run outlives the run, so a client that
// reconnects just after the end still gets the last events, turn_end included.
var ringGrace = 30 * time.Second

// stamp numbers an event of the turn that runs here and keeps it in the ring.
// The events that do not come from the turn (a file change, a goal, a card)
// have no id: they are hints to read the record again, which a reconnect does
// anyway.
func (s *Service) stamp(k string, ev session.Event) session.Event {
	s.mu.Lock()
	r := s.rings[k]
	s.mu.Unlock()
	if r == nil {
		return ev
	}
	return r.Add(ev)
}

// expireRing drops the ring of a finished run after the grace period, unless a
// newer run replaced it.
func (s *Service) expireRing(k string) {
	s.mu.Lock()
	r := s.rings[k]
	s.mu.Unlock()
	if r == nil {
		return
	}
	time.AfterFunc(ringGrace, func() {
		s.mu.Lock()
		if s.rings[k] == r {
			delete(s.rings, k)
		}
		s.mu.Unlock()
	})
}

func (s *Service) ringOf(k string) *eventRing {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rings[k]
}

// maxStreamsPerChat is the most event streams a user may have open on one chat
// on one frontend, as for the change feed of the workspace (8 per user). Each
// stream holds a subscription and a goroutine; a page that leaks them, or a
// script that opens them in a loop, is stopped here.
const maxStreamsPerChat = 8

// ErrTooManyStreams means the user has the most event streams allowed open on
// the chat; close one first.
var ErrTooManyStreams = errors.New("studio: too many event streams open on this chat")

// openStream counts a client stream of the chat k, or refuses one past the cap.
// The count is per process: it bounds what one frontend holds.
func (s *Service) openStream(k string) (release func(), err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.streams[k] >= maxStreamsPerChat {
		return nil, ErrTooManyStreams
	}
	s.streams[k]++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.streams[k]--; s.streams[k] <= 0 {
				delete(s.streams, k)
			}
		})
	}, nil
}

// EventsAfter is the event stream of a client (the events endpoint): Events,
// counted against maxStreamsPerChat, that first gives the events that follow the
// one with id after, then the live ones. The ring is on the process that runs
// the turn; a frontend that does not run it asks that process for them
// (eventremote.go). Where they cannot be given (the id is not of the current
// run, is older than the ring keeps, or the process that runs the turn is gone
// or does not answer) the stream starts with a resync event, which carries the
// newest id known; the client reads the transcript. A gap in the numbers of the
// live events is filled the same way. An empty after gives the live events only.
func (s *Service) EventsAfter(ctx context.Context, user, conv, after string) (<-chan session.Event, func(), error) {
	p := principal(user)
	if _, err := s.d.Store.GetChat(ctx, p, conv); err != nil {
		return nil, nil, err
	}
	k := key(p, conv)
	release, err := s.openStream(k)
	if err != nil {
		return nil, nil, err
	}
	if after == "" {
		live, cancelLive := s.bus.Subscribe(k)
		var once sync.Once
		return live, func() { once.Do(func() { cancelLive(); release() }) }, nil
	}
	// Subscribe before reading the ring: an event published in between is then
	// in both, and the numbers tell the copies apart.
	live, cancelLive := s.bus.Subscribe(k)
	out := make(chan session.Event, 64)
	done := make(chan struct{})
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			close(done)
			cancelLive()
			release()
		})
	}
	go s.resume(p, conv, after, live, out, done)
	return out, cancel, nil
}

// replayFrom gives the events that follow the id from, from this process's ring
// or, when it has none for the chat, from the process that runs the turn. When
// they cannot be given it is not ok, and head is the newest id known ("" when
// none).
func (s *Service) replayFrom(p, conv, from string) (evs []session.Event, head string, ok bool) {
	k := key(p, conv)
	if r := s.ringOf(k); r != nil {
		if evs, ok = r.After(from); ok {
			return evs, "", true
		}
		recordResync(resyncLocal)
		return nil, r.Head(), false
	}
	evs, head, ok, why := s.askOwner(p, conv, from)
	if !ok {
		recordResync(why)
	}
	return evs, head, ok
}

func (s *Service) resume(p, conv, after string, live <-chan session.Event, out chan<- session.Event, done <-chan struct{}) {
	defer close(out)
	send := func(ev session.Event) bool {
		select {
		case out <- ev:
			return true
		case <-done:
			return false
		}
	}
	var run string
	var seen int // the newest number sent in run
	advance := func(id string) {
		if r, n, ok := parseEventID(id); ok && (r != run || n > seen) {
			run, seen = r, n
		}
	}
	// catchUp sends what follows the id, or a resync.
	catchUp := func(from string) bool {
		evs, head, ok := s.replayFrom(p, conv, from)
		if !ok {
			// The client learns where the stream is now, to go on from there.
			ev := session.Event{Kind: session.KindResync, ID: head}
			advance(ev.ID)
			return send(ev)
		}
		for _, ev := range evs {
			advance(ev.ID)
			if !send(ev) {
				return false
			}
		}
		return true
	}
	if r, n, ok := parseEventID(after); ok {
		run, seen = r, n
	}
	if !catchUp(after) {
		return
	}
	for {
		select {
		case <-done:
			return
		case ev, open := <-live:
			if !open {
				return // fell behind: the client reconnects
			}
			r, n, ok := parseEventID(ev.ID)
			if !ok {
				if !send(ev) {
					return
				}
				continue
			}
			if r == run && n <= seen {
				continue // already sent from the ring
			}
			if r == run && n > seen+1 {
				// Events were lost between the owner and here. The ring of the
				// owner has them, and its tail ends with ev.
				if !catchUp(eventID(run, seen)) {
					return
				}
				if r == run && n <= seen {
					continue
				}
			}
			advance(ev.ID)
			if !send(ev) {
				return
			}
		}
	}
}
