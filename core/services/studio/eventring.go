package studio

import (
	"strconv"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

// The ring of a running chat holds the live tail of its events, so a client that
// reconnects can be given what it missed. It is a cache of the tail: the
// transcript stays the record, and a client that asks for more than the ring
// holds is told to read the transcript (the resync event).
const (
	ringMaxEvents = 2048
	ringMaxBytes  = 4 << 20
)

// ringEntry is one event of the ring, or a run of consecutive text or
// reasoning deltas of one turn that were merged into one. first and last are
// the numbers of the first and the last event it stands for. pieces holds the
// size of each merged delta, so a client that has seen the first few of them
// is given only the rest.
type ringEntry struct {
	ev          session.Event
	first, last int
	pieces      []int
	size        int
}

// eventRing numbers the events of one run and keeps the last of them, bounded
// by count and by bytes, oldest out. dropped is the number of the newest event
// that was evicted: a client that has seen less than that cannot be served.
type eventRing struct {
	mu       sync.Mutex
	run      string
	last     int
	dropped  int
	entries  []ringEntry
	bytes    int
	maxCount int
	maxBytes int
}

func newEventRing(run string, maxCount, maxBytes int) *eventRing {
	return &eventRing{run: run, maxCount: maxCount, maxBytes: maxBytes}
}

// eventID is the id a client sees: the run and the number of the event in it.
func eventID(run string, n int) string { return run + "." + strconv.Itoa(n) }

// parseEventID splits an id into its run and number. The run may hold dots; the
// number is what follows the last one.
func parseEventID(id string) (run string, n int, ok bool) {
	i := strings.LastIndexByte(id, '.')
	if i <= 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(id[i+1:])
	if err != nil || n < 1 {
		return "", 0, false
	}
	return id[:i], n, true
}

// mergeable says whether a delta may join the entry before it.
func mergeable(prev, ev session.Event) bool {
	return (ev.Kind == session.KindText || ev.Kind == session.KindReasoning) &&
		prev.Kind == ev.Kind && prev.Turn == ev.Turn && ev.Tool == nil && prev.Tool == nil
}

func eventSize(ev session.Event) int {
	n := 64 + len(ev.Text) + len(ev.Detail)
	if ev.Tool != nil {
		n += len(ev.Tool.Name) + len(ev.Tool.Summary) + len(ev.Tool.Output)
	}
	for _, id := range ev.IDs {
		n += len(id)
	}
	return n
}

// Add numbers the event, keeps it, and returns it with its id set.
func (r *eventRing) Add(ev session.Event) session.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last++
	ev.ID = eventID(r.run, r.last)
	size := eventSize(ev)
	if n := len(r.entries); n > 0 && mergeable(r.entries[n-1].ev, ev) {
		e := &r.entries[n-1]
		e.ev.Text += ev.Text
		e.pieces = append(e.pieces, len(ev.Text))
		e.last = r.last
		e.size += len(ev.Text)
		r.bytes += len(ev.Text)
	} else {
		e := ringEntry{ev: ev, first: r.last, last: r.last, size: size}
		if ev.Kind == session.KindText || ev.Kind == session.KindReasoning {
			e.pieces = []int{len(ev.Text)}
		}
		r.entries = append(r.entries, e)
		r.bytes += size
	}
	for len(r.entries) > 1 && (len(r.entries) > r.maxCount || r.bytes > r.maxBytes) {
		r.dropped = r.entries[0].last
		r.bytes -= r.entries[0].size
		r.entries[0] = ringEntry{}
		r.entries = r.entries[1:]
	}
	// The slice only moves forward: copy it down now and then so its array
	// does not grow without bound.
	if cap(r.entries) > 4*r.maxCount && len(r.entries) < cap(r.entries)/4 {
		r.entries = append([]ringEntry(nil), r.entries...)
	}
	return ev
}

// Head is the id of the newest event, or "" when the ring is empty.
func (r *eventRing) Head() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last == 0 {
		return ""
	}
	return eventID(r.run, r.last)
}

// After returns the events that follow the one with id, each with its id (a
// merged one carries the id of its last delta), and ok. It is not ok when the
// ring cannot give them: the id is of another run, ahead of the ring, or older
// than the oldest event kept.
func (r *eventRing) After(id string) (events []session.Event, ok bool) {
	run, n, valid := parseEventID(id)
	r.mu.Lock()
	defer r.mu.Unlock()
	if !valid || run != r.run || n < r.dropped || n > r.last {
		return nil, false
	}
	for _, e := range r.entries {
		if e.last <= n {
			continue
		}
		ev := e.ev
		if e.first <= n { // the client has seen the first deltas of this entry
			skip := 0
			for _, p := range e.pieces[:n-e.first+1] {
				skip += p
			}
			ev.Text = ev.Text[skip:]
		}
		ev.ID = eventID(r.run, e.last)
		events = append(events, ev)
	}
	return events, true
}
