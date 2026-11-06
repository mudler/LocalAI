package studio

import (
	"context"
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

// EventsAfter is Events for a client that resumes: it first gives the events
// that follow the one with id after, then the live ones. The ring is on the
// frontend that runs the turn. Where it cannot give what follows (the id is
// not of the current run, is older than the ring keeps, or this frontend has no
// ring for the chat) the stream starts with a resync event, which carries the
// newest id this frontend knows of; the client reads the transcript. A gap in
// the numbers of the live events is filled from the ring the same way. An empty
// after is Events.
func (s *Service) EventsAfter(ctx context.Context, user, conv, after string) (<-chan session.Event, func(), error) {
	if after == "" {
		return s.Events(ctx, user, conv)
	}
	p := principal(user)
	if _, err := s.d.Store.GetChat(ctx, p, conv); err != nil {
		return nil, nil, err
	}
	k := key(p, conv)
	// Subscribe before reading the ring: an event published in between is then
	// in both, and the numbers tell the copies apart.
	live, cancelLive := s.bus.Subscribe(k)
	out := make(chan session.Event, 64)
	done := make(chan struct{})
	cancel := func() {
		select {
		case <-done:
		default:
			close(done)
		}
		cancelLive()
	}
	go s.resume(k, after, live, out, done)
	return out, cancel, nil
}

func (s *Service) resume(k, after string, live <-chan session.Event, out chan<- session.Event, done <-chan struct{}) {
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
	// catchUp sends what follows the id from the ring, or a resync.
	catchUp := func(from string) bool {
		var evs []session.Event
		ok := false
		if r := s.ringOf(k); r != nil {
			evs, ok = r.After(from)
		}
		if !ok {
			// The client learns where the stream is now, to go on from there.
			ev := session.Event{Kind: session.KindResync}
			if r := s.ringOf(k); r != nil {
				ev.ID = r.Head()
			}
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
				// Events were lost between the owner and here. The ring has them
				// when the owner is this frontend, and its tail ends with ev.
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
