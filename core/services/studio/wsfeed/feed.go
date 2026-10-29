// Package wsfeed tells the live clients of a user which files of the user's
// home folder changed, whoever changed them: a chat tool, an upload, a trigger
// run, a connector or another API client, on this frontend or on another one.
//
// The record is the workspace journal. Every change set has a revision, and
// the revisions of one workspace are committed in order (the store serializes
// its writers), so a reader that remembers the last revision it read misses
// nothing. A Feed polls the journal while at least one client listens, and
// sends each subscriber one event per poll with the paths that changed since
// its last event.
//
// Distributed mode: the feed keeps no shared state. In a cluster every
// frontend reads the same journal in the shared database, so a change that any
// process commits reaches the clients of every frontend within one poll
// interval, and no message on the bus is needed or trusted (bus delivery is at
// most once; the journal is not). The cursor of each frontend and of each
// subscriber lives only in that process and is derived from the journal again
// after a restart or a reconnect (the client sends the last revision it saw).
// The JSON store runs in one process only, so there the feed is local too.
//
// Access: the feed finds the users a change set touches by reading the journal
// as the administrator, but what a subscriber receives comes from a read of the
// journal as that user, so a client only ever learns the paths its user may
// read, as with every other call of the store. Paths outside the user's home
// folder are left out.
package wsfeed

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Errors of Subscribe.
var (
	// ErrTooManySubscribers means the user has the most live streams allowed.
	ErrTooManySubscribers = errors.New("wsfeed: too many streams for this user")
	// ErrClosed means the feed was closed.
	ErrClosed = errors.New("wsfeed: closed")
)

// Event is what one subscriber receives.
type Event struct {
	// Revision is the newest revision the subscriber has been told about. A
	// client that reconnects gives it back to resume after it.
	Revision int64
	// Paths are the files and folders that changed, relative to the home
	// folder, without a leading slash, sorted. A move gives both paths.
	Paths []string
	// Kinds are the kinds of the change sets (user, agent, upload, undo).
	Kinds []string
	// Resync means changes were left out: the subscriber fell behind, or its
	// resume point was too far back. The client reads again whatever it shows.
	Resync bool
}

// Options configure a Feed.
type Options struct {
	Store workspace.Store
	// Admin is the store's admin actor. It reads the journal to find which
	// users a change set touches.
	Admin workspace.Actor
	// Interval is the time between two reads of the journal. Zero: one second.
	Interval time.Duration
	// MaxPerUser bounds the live streams of one user. Zero: 8.
	MaxPerUser int
	// Buffer is how many events a subscriber may fall behind by before its
	// queue collapses into one resync event. Zero: 16.
	Buffer int
	// MaxCatchUp is the most change sets a subscriber is caught up with in
	// one read. More than that sends a resync. Zero: 500.
	MaxCatchUp int
	Logger     *slog.Logger
}

// Feed delivers the changes of the users' home folders to live subscribers.
type Feed struct {
	o   Options
	log *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	closed  bool
	running bool
	subs    map[string]map[*sub]struct{}

	// Only the loop goroutine uses these.
	cur      int64
	curKnown bool
}

type sub struct {
	principal string
	ch        chan Event
	cursor    int64 // the newest revision the subscriber has been told about
	catchUp   bool  // read the user's journal from cursor at the next poll
	closed    bool
}

// New returns a Feed. It starts no goroutine until the first subscriber.
func New(o Options) *Feed {
	if o.Interval <= 0 {
		o.Interval = time.Second
	}
	if o.MaxPerUser <= 0 {
		o.MaxPerUser = 8
	}
	if o.Buffer <= 0 {
		o.Buffer = 16
	}
	if o.MaxCatchUp <= 0 {
		o.MaxCatchUp = 500
	}
	o.MaxCatchUp = min(o.MaxCatchUp, 1000) // the most the store returns at once
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Feed{o: o, log: log, ctx: ctx, cancel: cancel, subs: map[string]map[*sub]struct{}{}}
}

// Subscribe returns the changes of the principal's home folder. With resume
// set they start after revision after; without it, from now on. The channel
// closes when cancel is called or the feed closes.
func (f *Feed) Subscribe(ctx context.Context, principal string, after int64, resume bool) (<-chan Event, func(), error) {
	if !resume {
		head, err := f.head(ctx)
		if err != nil {
			return nil, nil, err
		}
		after = head
	}
	s := &sub{principal: principal, ch: make(chan Event, f.o.Buffer), cursor: max(after, 0), catchUp: true}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, nil, ErrClosed
	}
	if len(f.subs[principal]) >= f.o.MaxPerUser {
		return nil, nil, ErrTooManySubscribers
	}
	if f.subs[principal] == nil {
		f.subs[principal] = map[*sub]struct{}{}
	}
	f.subs[principal][s] = struct{}{}
	if !f.running {
		f.running = true
		f.wg.Add(1)
		go f.loop()
	}
	var once sync.Once
	return s.ch, func() { once.Do(func() { f.drop(s) }) }, nil
}

func (f *Feed) drop(s *sub) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.subs[s.principal], s)
	if len(f.subs[s.principal]) == 0 {
		delete(f.subs, s.principal)
	}
	f.closeSub(s)
}

// closeSub closes the channel of s. The caller holds f.mu, under which every
// send happens too, so a send never meets a closed channel.
func (f *Feed) closeSub(s *sub) {
	if !s.closed {
		s.closed = true
		close(s.ch)
	}
}

// Close stops the feed and closes every stream. It is safe to call more than once.
func (f *Feed) Close() {
	f.mu.Lock()
	f.closed = true
	for _, set := range f.subs {
		for s := range set {
			f.closeSub(s)
		}
	}
	f.subs = map[string]map[*sub]struct{}{}
	f.mu.Unlock()
	f.cancel()
	f.wg.Wait()
}

// loop polls the journal until the last subscriber leaves or the feed closes.
func (f *Feed) loop() {
	defer f.wg.Done()
	t := time.NewTicker(f.o.Interval)
	defer t.Stop()
	for {
		f.pass(f.ctx)
		select {
		case <-f.ctx.Done():
			return
		case <-t.C:
		}
		f.mu.Lock()
		if len(f.subs) == 0 {
			// Idle: the next subscriber starts a new loop, which reads the
			// head again, so nothing is read for nobody.
			f.running, f.curKnown = false, false
			f.mu.Unlock()
			return
		}
		f.mu.Unlock()
	}
}

// head is the newest revision of the workspace, 0 for an empty one.
func (f *Feed) head(ctx context.Context) (int64, error) {
	h, err := f.o.Store.History(ctx, f.o.Admin, workspace.HistoryQuery{Path: "/", Limit: 1})
	if err != nil || len(h) == 0 {
		return 0, err
	}
	return h[0].Rev, nil
}

// pageSize is how many change sets the administrator's scan reads at once.
const pageSize = 200

// pass reads the journal once and delivers what it found.
func (f *Feed) pass(ctx context.Context) {
	if !f.curKnown {
		head, err := f.head(ctx)
		if err != nil {
			f.logErr("could not read the newest revision", err)
			return
		}
		f.cur, f.curKnown = head, true
	}
	f.mu.Lock()
	touched := map[string]bool{}
	for p, set := range f.subs {
		for s := range set {
			if s.catchUp {
				touched[p] = true
			}
		}
	}
	f.mu.Unlock()
	for {
		page, err := f.o.Store.ChangesSince(ctx, f.o.Admin, f.cur, pageSize)
		if err != nil {
			f.logErr("could not read the journal", err)
			break
		}
		for _, cs := range page {
			for _, op := range cs.Ops {
				for _, p := range []string{op.OldPath, op.NewPath} {
					if owner, ok := ownerOf(p); ok {
						touched[owner] = true
					}
				}
			}
			f.cur = cs.Rev
		}
		if len(page) < pageSize {
			break
		}
	}
	for _, p := range slices.Sorted(maps.Keys(touched)) {
		f.deliver(ctx, p)
	}
	f.advanceIdle(touched)
}

// advanceIdle moves the cursor of every subscriber whose user no change set
// touched up to the scanned revision, so that a later read starts close to the head.
func (f *Feed) advanceIdle(touched map[string]bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for p, set := range f.subs {
		if touched[p] {
			continue
		}
		for s := range set {
			s.cursor = max(s.cursor, f.cur)
		}
	}
}

// ownerOf returns the principal whose home folder holds p.
func ownerOf(p string) (string, bool) {
	rest, ok := strings.CutPrefix(p, session.UsersRoot+"/")
	if !ok {
		return "", false
	}
	owner, _, _ := strings.Cut(rest, "/")
	return owner, owner != ""
}

// deliver reads the journal as one user and sends each of the user's
// subscribers the changes after its cursor.
func (f *Feed) deliver(ctx context.Context, principal string) {
	f.mu.Lock()
	set := f.subs[principal]
	if len(set) == 0 {
		f.mu.Unlock()
		return
	}
	from := int64(-1)
	for s := range set {
		if from < 0 || s.cursor < from {
			from = s.cursor
		}
	}
	f.mu.Unlock()

	sets, err := f.o.Store.ChangesSince(ctx, workspace.Actor{Principal: principal}, from, f.o.MaxCatchUp)
	if err != nil {
		f.logErr("could not read the journal of a user", err)
		f.mu.Lock()
		for s := range f.subs[principal] {
			s.catchUp = true // read again at the next poll
		}
		f.mu.Unlock()
		return
	}
	full := len(sets) == f.o.MaxCatchUp
	last := f.cur
	if len(sets) > 0 {
		last = max(last, sets[len(sets)-1].Rev)
	}
	home := session.HomePath(principal) + "/"

	f.mu.Lock()
	defer f.mu.Unlock()
	for s := range f.subs[principal] {
		s.catchUp = false
		if full {
			// The read stopped at the limit: what lies beyond it is unknown
			// here, so the client reads everything again.
			f.send(s, Event{Revision: last, Resync: true})
			s.cursor = last
			continue
		}
		if ev, ok := eventFor(sets, s.cursor, home); ok {
			f.send(s, ev)
		}
		s.cursor = max(s.cursor, last)
	}
}

// eventFor merges the change sets after cursor into one event, with the
// paths inside home. ok is false when none of them touched home.
func eventFor(sets []workspace.ChangeSet, cursor int64, home string) (Event, bool) {
	paths, kinds := map[string]bool{}, map[string]bool{}
	var rev int64
	for _, cs := range sets {
		if cs.Rev <= cursor {
			continue
		}
		hit := false
		for _, op := range cs.Ops {
			for _, p := range []string{op.OldPath, op.NewPath} {
				if rel, ok := strings.CutPrefix(p, home); ok && rel != "" {
					paths[rel], hit = true, true
				}
			}
		}
		if hit {
			kinds[string(cs.Kind)] = true
			rev = cs.Rev
		}
	}
	if len(paths) == 0 {
		return Event{}, false
	}
	return Event{Revision: rev, Paths: slices.Sorted(maps.Keys(paths)), Kinds: slices.Sorted(maps.Keys(kinds))}, true
}

// send queues ev without waiting. A full queue is dropped and replaced by one
// resync event: the writer of the journal and the other subscribers never
// wait for a slow client. The caller holds f.mu; the loop goroutine is the
// only sender, so after the drain the send has room.
func (f *Feed) send(s *sub, ev Event) {
	if s.closed {
		return
	}
	select {
	case s.ch <- ev:
		return
	default:
	}
	for {
		select {
		case <-s.ch:
			continue
		default:
		}
		break
	}
	select {
	case s.ch <- Event{Revision: ev.Revision, Resync: true}:
	default:
	}
}

func (f *Feed) logErr(msg string, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	f.log.Warn("wsfeed: "+msg, "error", err)
}
