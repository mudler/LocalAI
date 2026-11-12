// SPDX-License-Identifier: MIT

// Package clientdemo is a small reference add-on for paired clients. It
// shows the three things an add-on that serves a client does: it declares
// routes in its manifest, it starts and reads the client's jobs through the
// client.jobs.* host operations, and it keeps a per-client outbox of events
// that it serves as server-sent events, so a client that lost its connection
// resumes from the last event it saw.
package clientdemo

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/mudler/LocalAI/addons/internal/atomicfile"
)

// Event types of the outbox.
const (
	EventJobProgress = "job.progress"
	EventJobDone     = "job.done"
	EventJobFailed   = "job.failed"
	EventNotice      = "notice"
)

const (
	// MaxEvents is the most events one client's outbox keeps; the oldest go
	// first. MaxTracked is the most jobs it watches at once.
	MaxEvents  = 100
	MaxTracked = 8
	// DefaultTTL is how long an event stays in the outbox.
	DefaultTTL = time.Hour
)

// Event is one entry of the outbox. ID is a decimal sequence number that only
// grows, also across restarts of the add-on, so it is a safe Last-Event-ID.
type Event struct {
	Seq  uint64          `json:"seq"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
	At   time.Time       `json:"at"`
}

// ID is the SSE id of the event.
func (e Event) ID() string { return strconv.FormatUint(e.Seq, 10) }

// outboxState is what the file of a client holds.
type outboxState struct {
	Next    uint64            `json:"next"`
	Events  []Event           `json:"events"`
	Tracked map[string]string `json:"tracked"` // job id -> last status sent
}

// Outbox is the event queue of one client. It is safe for concurrent use.
type Outbox struct {
	mu   sync.Mutex
	path string
	ttl  time.Duration
	now  func() time.Time
	st   outboxState
	// reserved counts the places taken with Reserve and not yet used.
	reserved int
	// changed is closed and replaced at each append, so every open stream of
	// the client wakes, not only one.
	changed chan struct{}
}

// OpenOutbox loads or creates the outbox file of a client.
func OpenOutbox(path string, ttl time.Duration, now func() time.Time) (*Outbox, error) {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if now == nil {
		now = time.Now
	}
	o := &Outbox{path: path, ttl: ttl, now: now, changed: make(chan struct{})}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(b, &o.st); err != nil {
			return nil, fmt.Errorf("outbox %s: %w", filepath.Base(path), err)
		}
	}
	if o.st.Tracked == nil {
		o.st.Tracked = map[string]string{}
	}
	return o, nil
}

// ErrTooManyTracked is the refusal of a job when the outbox watches MaxTracked.
var ErrTooManyTracked = errors.New("too many jobs watched")

// Append adds an event and returns it. It prunes the expired and the surplus.
// The state changes only when the file was written: a failed save leaves the
// outbox as it was and wakes no stream.
func (o *Outbox) Append(typ string, data any) (Event, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return Event{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	next := o.st
	next.Next++
	e := Event{Seq: next.Next, Type: typ, Data: raw, At: o.now().UTC()}
	next.Events = o.pruned(append(slices.Clone(o.st.Events), e))
	if err := o.commitLocked(next); err != nil {
		return Event{}, err
	}
	close(o.changed)
	o.changed = make(chan struct{})
	return e, nil
}

// pruned returns the events that have not expired, without the surplus. It
// never writes into its argument.
func (o *Outbox) pruned(events []Event) []Event {
	cut := o.now().Add(-o.ttl)
	keep := make([]Event, 0, len(events))
	for _, e := range events {
		if e.At.After(cut) {
			keep = append(keep, e)
		}
	}
	if n := len(keep) - MaxEvents; n > 0 {
		keep = keep[n:]
	}
	return keep
}

// commitLocked writes the state and, when that worked, makes it the current one.
func (o *Outbox) commitLocked(next outboxState) error {
	b, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(o.path, b, 0o600); err != nil {
		return err
	}
	o.st = next
	return nil
}

// withTracked starts a change of the watched jobs on a copy of the state.
func (o *Outbox) withTracked() outboxState {
	next := o.st
	next.Tracked = maps.Clone(o.st.Tracked)
	return next
}

// After returns the events with a sequence number above after. gap is true when
// events the caller has not seen are gone: they expired or the outbox was full.
// A caller that asks from 0 has seen nothing and gets no gap.
func (o *Outbox) After(after uint64) (events []Event, gap bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.st.Events = o.pruned(o.st.Events) // memory only: the file is written at the next change
	first := o.st.Next + 1
	if len(o.st.Events) > 0 {
		first = o.st.Events[0].Seq
	}
	// Events after+1 .. first-1 existed and were dropped.
	// A position above the last number given is as stale as one that fell off
	// the end: the outbox was lost or reset, and numbers will repeat.
	gap = after > 0 && (first > after+1 || after > o.st.Next)
	for _, e := range o.st.Events {
		if e.Seq > after {
			events = append(events, e)
		}
	}
	return events, gap
}

// Oldest is the sequence number of the oldest event held, or the next number
// when the outbox is empty.
func (o *Outbox) Oldest() uint64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.st.Events) > 0 {
		return o.st.Events[0].Seq
	}
	return o.st.Next + 1
}

// Changed returns a channel that is closed at the next append. Ask again after
// it fires.
func (o *Outbox) Changed() <-chan struct{} {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.changed
}

// Reserve takes one of the MaxTracked places before a job is started, so two
// parallel starts cannot both pass the check. Commit uses the place, Release
// gives it back.
func (o *Outbox) Reserve() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.st.Tracked)+o.reserved >= MaxTracked {
		return ErrTooManyTracked
	}
	o.reserved++
	return nil
}

// Release gives back a place taken with Reserve and not used.
func (o *Outbox) Release() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.reserved > 0 {
		o.reserved--
	}
}

// Commit starts watching a job on a place taken with Reserve.
func (o *Outbox) Commit(job, status string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	next := o.withTracked()
	next.Tracked[job] = status
	if err := o.commitLocked(next); err != nil {
		return err
	}
	if o.reserved > 0 {
		o.reserved--
	}
	return nil
}

// Track starts watching a job. It refuses more than MaxTracked at once, the
// places held with Reserve included.
func (o *Outbox) Track(job, status string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.st.Tracked[job]; !ok && len(o.st.Tracked)+o.reserved >= MaxTracked {
		return ErrTooManyTracked
	}
	next := o.withTracked()
	next.Tracked[job] = status
	return o.commitLocked(next)
}

// Tracked lists the watched jobs and the last status sent for each.
func (o *Outbox) Tracked() map[string]string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make(map[string]string, len(o.st.Tracked))
	for k, v := range o.st.Tracked {
		out[k] = v
	}
	return out
}

// Transition records a new status of a watched job and reports whether it is
// a change, in one step, so two streams of one client that see the same change
// send one event. An ended job is forgotten.
func (o *Outbox) Transition(job, status string, ended bool) (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	last, ok := o.st.Tracked[job]
	if !ok || last == status {
		return false, nil
	}
	next := o.withTracked()
	if ended {
		delete(next.Tracked, job)
	} else {
		next.Tracked[job] = status
	}
	if err := o.commitLocked(next); err != nil {
		return false, err
	}
	return true, nil
}
