package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The defaults of the limits.
const (
	DefaultPerUser   = 4
	DefaultTotal     = 16
	DefaultIdleAfter = 10 * time.Minute
	DefaultMaxAge    = 24 * time.Hour
)

// ErrBusy says the runner holds as many sandboxes as it may.
var ErrBusy = errors.New("as many sandboxes run as may; try again later")

// Store is the workspace of the sandboxes: the store itself in the server,
// or the host operations in the add-on (workspace.materialize,
// workspace.commit, workspace.changes). A sandbox reads the files with
// Materialize, commits with CommitUpper and asks ChangesSince whether somebody
// else changed them.
type Store = workspace.Store

// HomeMounts are the mounts the add-on asks for: the user's files, writable,
// under home. The host decides what they reach for the call's chat.
func HomeMounts(context.Context, workspace.Actor) ([]workspace.MountRequest, error) {
	return []workspace.MountRequest{{Path: "/", At: "home", Mode: workspace.ModeWrite}}, nil
}

// Options configures a Manager.
type Options struct {
	// Backend returns the backend to use now. It may change between calls (the
	// administrator can switch it). ErrNoSandbox means there is none.
	Backend func() (sandbox.Backend, error)
	Store   Store
	// Actor returns the actor a sandbox of a user and a conversation reads and
	// commits as: in the server, the actor of the chat's resolved access
	// (fenced for a chat whose agent has a folder). It is asked at every call,
	// so a changed access applies from the next call. Required.
	Actor func(ctx context.Context, user, conversation string) (workspace.Actor, error)
	// View returns the view a sandbox of a is made with: in the server, the
	// view of the chat's resolved access. Its actor must be a. It is asked at
	// every call too: a sandbox whose actor or mounts changed is made again.
	// Required.
	View func(ctx context.Context, a workspace.Actor) (workspace.View, error)
	// Tools returns the tools a sandbox runs. Nil: bash with its defaults.
	Tools func(acquire Acquire) []Tool
	// Prepare, when set, gives a call the context its store needs (the add-on
	// names the host operation of the call).
	Prepare func(ctx context.Context, r Request) context.Context
	// Title starts the title of the change set of a call, before the tool
	// name. Empty: "Sandbox: ".
	Title string
	// StateDir is the folder the backend keeps its sandboxes in. Whatever an
	// earlier run left in it is removed at start.
	StateDir string
	// KeepStateDir leaves StateDir as it is at start. Set it when the backends
	// keep their own folders in it: they stop the sandboxes an earlier process
	// left before they remove the folders, and emptying the folder first
	// would drop what they need to find those sandboxes.
	KeepStateDir bool
	// Ports returns the port handlers of the sandbox of a user and a
	// conversation, given at Create and again at Resume (which needs a
	// handler for every port the sandbox was made with, so the set must not
	// change). Nil: no network.
	Ports func(user, conv string) map[int]sandbox.PortHandler
	// Begin marks the call in progress of a sandbox, for the port handlers:
	// a connection the guest opens while no call runs belongs to nobody. It
	// is called with the entry locked, before the tool runs, and end after
	// the checkpoint; end may be called more than once. Nil: nothing is
	// marked.
	Begin             func(ctx context.Context, user, conv, operation string) (end func())
	PerUser, Total    int
	IdleAfter, MaxAge time.Duration
	Now               func() time.Time
}

// Request is one tool call of one user in one conversation.
type Request struct {
	User, Conversation, Operation, Tool string
	Args                                json.RawMessage
}

// Manager runs the tool calls in their sandboxes.
type Manager struct {
	o Options

	mu      sync.Mutex
	entries map[string]*entry
}

type entry struct {
	mu       sync.Mutex // one call at a time; guards sb, snap, gone
	user     string
	conv     string
	created  time.Time
	lastUse  time.Time // guarded by Manager.mu
	sb       sandbox.Sandbox
	snap     *sandbox.SnapshotRef
	trk      *tracker
	gone     bool
	hasBuilt bool
	// suspendFails counts the failed suspends since the last call, and
	// suspendAfter is the earliest time the sweep tries again. Both are
	// guarded by mu.
	suspendFails int
	suspendAfter time.Time
	// mounts are the folders of the sandbox's view, to name a path of a
	// commit as the guest sees it. A resumed sandbox has the view it was
	// made with.
	mounts []workspace.Mount
	// actor is the actor the sandbox was made with. A call whose resolved
	// actor or view differs makes the sandbox again.
	actor workspace.Actor
}

// New returns a manager. It removes what an earlier run left in StateDir.
func New(o Options) (*Manager, error) {
	if o.Backend == nil || o.Store == nil {
		return nil, errors.New("runner: a backend and a store are required")
	}
	// There is no default actor or view: a sandbox made with an actor that
	// is not the chat's would reach more than the chat may.
	if o.Actor == nil || o.View == nil {
		return nil, errors.New("runner: the actor and the view of a chat are required")
	}
	if o.PerUser <= 0 {
		o.PerUser = DefaultPerUser
	}
	if o.Total <= 0 {
		o.Total = DefaultTotal
	}
	if o.IdleAfter <= 0 {
		o.IdleAfter = DefaultIdleAfter
	}
	if o.MaxAge <= 0 {
		o.MaxAge = DefaultMaxAge
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Title == "" {
		o.Title = "Sandbox: "
	}
	if o.Tools == nil {
		o.Tools = func(acquire Acquire) []Tool { return []Tool{Bash(acquire, BashOptions{})} }
	}
	if o.StateDir != "" && !o.KeepStateDir {
		if err := clearDir(o.StateDir); err != nil {
			return nil, err
		}
	}
	return &Manager{o: o, entries: map[string]*entry{}}, nil
}

// clearDir removes the content of dir and keeps dir. It refuses a path that is
// not absolute or is the root.
func clearDir(dir string) error {
	dir = filepath.Clean(dir)
	if !filepath.IsAbs(dir) || dir == string(filepath.Separator) || dir == filepath.Dir(dir) {
		return fmt.Errorf("runner: %q is not a folder the runner may empty", dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	list, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range list {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func key(user, conv string) string { return user + "\x00" + conv }

// Count is the number of sandboxes of a user, or of everybody when user is "".
func (m *Manager) Count(user string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, e := range m.entries {
		if user == "" || e.user == user {
			n++
		}
	}
	return n
}

// Running is the number of sandboxes that are awake.
func (m *Manager) Running() int {
	m.mu.Lock()
	list := make([]*entry, 0, len(m.entries))
	for _, e := range m.entries {
		list = append(list, e)
	}
	m.mu.Unlock()
	n := 0
	for _, e := range list {
		if e.mu.TryLock() {
			if e.sb != nil {
				n++
			}
			e.mu.Unlock()
		} else {
			n++ // busy means running
		}
	}
	return n
}

// find returns the entry of a pair, making it when it is new. A new entry past
// the limits asks the oldest sandbox of the user to give way; past the total
// limit it is refused.
func (m *Manager) find(user, conv string) (*entry, error) {
	var victims []*entry
	m.mu.Lock()
	if e, ok := m.entries[key(user, conv)]; ok {
		e.lastUse = m.o.Now()
		m.mu.Unlock()
		return e, nil
	}
	if len(m.entries) >= m.o.Total {
		// Sandboxes that sleep for up to a day must not shut out a user who has
		// none: the oldest sleeper of anybody gives way. A running or busy one
		// is never touched.
		all := make([]*entry, 0, len(m.entries))
		for _, c := range m.entries {
			all = append(all, c)
		}
		sort.Slice(all, func(i, j int) bool { return all[i].lastUse.Before(all[j].lastUse) })
		for _, c := range all {
			if !c.mu.TryLock() {
				continue
			}
			if c.sb != nil {
				c.mu.Unlock()
				continue
			}
			victims = append(victims, c)
			delete(m.entries, key(c.user, c.conv))
			break
		}
		if len(victims) == 0 {
			m.mu.Unlock()
			return nil, ErrBusy
		}
	}
	own := make([]*entry, 0, m.o.PerUser)
	for _, e := range m.entries {
		if e.user == user {
			own = append(own, e)
		}
	}
	if len(own) >= m.o.PerUser {
		sort.Slice(own, func(i, j int) bool { return own[i].lastUse.Before(own[j].lastUse) })
		evicted := false
		for _, c := range own {
			if c.mu.TryLock() { // an idle one; a busy one is not touched
				victims = append(victims, c)
				delete(m.entries, key(c.user, c.conv))
				evicted = true
				break
			}
		}
		if !evicted {
			m.mu.Unlock()
			for _, v := range victims { // the total's victim is gone already
				v.gone = true
				m.destroy(context.Background(), v)
				v.mu.Unlock()
			}
			return nil, ErrBusy
		}
	}
	now := m.o.Now()
	e := &entry{user: user, conv: conv, created: now, lastUse: now}
	m.entries[key(user, conv)] = e
	m.mu.Unlock()
	for _, v := range victims {
		v.gone = true
		m.destroy(context.Background(), v)
		v.mu.Unlock()
	}
	return e, nil
}

// forget drops an entry that never got a sandbox.
func (m *Manager) forget(e *entry) {
	m.mu.Lock()
	if m.entries[key(e.user, e.conv)] == e {
		delete(m.entries, key(e.user, e.conv))
	}
	m.mu.Unlock()
}

// destroy stops a sandbox or removes its snapshot. The entry's lock is held.
func (m *Manager) destroy(ctx context.Context, e *entry) {
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if e.sb != nil {
		if err := e.sb.Destroy(dctx); err != nil {
			xlog.Warn("sandbox: destroy failed", "error", err)
		}
		e.sb = nil
	}
	if e.snap != nil {
		// Some backends keep more than a folder for a snapshot, so they are
		// asked first; the folder is removed after, if it is ours.
		if b, err := m.o.Backend(); err == nil {
			if d, ok := b.(sandbox.Discarder); ok {
				if err := d.Discard(dctx, *e.snap); err != nil {
					xlog.Warn("sandbox: discarding a snapshot failed", "error", err)
				}
			}
		}
		if p := e.snap.Path; p != "" && m.o.StateDir != "" && strings.HasPrefix(filepath.Clean(p), filepath.Clean(m.o.StateDir)+string(filepath.Separator)) {
			_ = os.RemoveAll(p)
		}
		e.snap = nil
	}
}

// access resolves the actor and the view of the entry's chat for this call.
// A view of another actor than the chat's is refused: a sandbox is never made
// with a view wider than the actor it commits as.
func (m *Manager) access(ctx context.Context, e *entry) (workspace.Actor, workspace.View, error) {
	a, err := m.o.Actor(ctx, e.user, e.conv)
	if err != nil {
		return workspace.Actor{}, workspace.View{}, err
	}
	if a.Principal != e.user || a.ChatID != e.conv {
		return workspace.Actor{}, workspace.View{}, fmt.Errorf("%w: the actor is not the one of this chat", ErrInvalid)
	}
	v, err := m.o.View(ctx, a)
	if err != nil {
		return workspace.Actor{}, workspace.View{}, err
	}
	if !sameActor(v.Actor, a) {
		return workspace.Actor{}, workspace.View{}, fmt.Errorf("%w: the view is not the one of this chat's actor", ErrInvalid)
	}
	if len(v.Mounts) == 0 {
		return workspace.Actor{}, workspace.View{}, fmt.Errorf("%w: the view of this chat has no folder", ErrInvalid)
	}
	return a, v, nil
}

// sameActor reports whether a and b are the same principal, chat, groups and
// fence.
func sameActor(a, b workspace.Actor) bool {
	if a.Principal != b.Principal || a.ChatID != b.ChatID || !a.SameFence(b) || len(a.Groups) != len(b.Groups) {
		return false
	}
	ga, gb := slices.Clone(a.Groups), slices.Clone(b.Groups)
	slices.Sort(ga)
	slices.Sort(gb)
	return slices.Equal(ga, gb)
}

// ready makes sure the entry has a running sandbox that matches the chat's
// access and the user's files. The entry's lock is held.
func (m *Manager) ready(ctx context.Context, e *entry) error {
	b, err := m.o.Backend()
	if err != nil {
		return err
	}
	a, v, err := m.access(ctx, e)
	if err != nil {
		return err
	}
	if (e.sb != nil || e.snap != nil) && (!sameActor(e.actor, a) || !slices.Equal(e.mounts, v.Mounts)) {
		// The chat's agent changed its folder or mode, or the chat's folders
		// changed: the sandbox must not keep the old ones. What the earlier
		// calls did is committed already.
		m.destroy(ctx, e)
		e.trk = nil
	}
	if e.trk == nil {
		e.trk = &tracker{Store: m.o.Store}
	}
	env := sandbox.Env{Store: e.trk, Actor: a}
	if m.o.Ports != nil {
		env.Ports = m.o.Ports(e.user, e.conv)
	}
	switch {
	case e.sb != nil:
		if e.trk.stale(ctx, a) {
			if err := e.sb.Refresh(ctx); err != nil {
				return fmt.Errorf("could not bring the sandbox up to date with your files: %w", err)
			}
		}
		return nil
	case e.snap != nil:
		sb, err := b.Resume(ctx, *e.snap, env)
		if err == nil {
			e.sb, e.snap = sb, nil
			return nil
		}
		// A snapshot that cannot wake is dropped. What the code did is in the
		// user's files, so a fresh sandbox loses nothing.
		xlog.Warn("sandbox: resume failed, starting a fresh one", "error", err)
		m.destroy(ctx, e)
	}
	sb, err := b.Create(ctx, sandbox.Spec{Env: env, View: v})
	if err != nil {
		return err
	}
	e.sb, e.hasBuilt, e.mounts, e.actor = sb, true, v.Mounts, a
	return nil
}

// Run runs one tool call and returns its output. What the code changed in the
// user's files is committed before it returns.
func (m *Manager) Run(ctx context.Context, r Request) (string, error) {
	if r.User == "" || r.Conversation == "" || r.Operation == "" {
		return "", fmt.Errorf("%w: a call needs a user, a conversation and an operation", ErrInvalid)
	}
	var tool *Tool
	var runWith *sandbox.Sandbox
	var current sandbox.Sandbox
	runWith = &current
	for _, t := range m.o.Tools(func(context.Context) (sandbox.Sandbox, func(), error) {
		return *runWith, func() {}, nil
	}) {
		if t.Name == r.Tool {
			t := t
			tool = &t
		}
	}
	if tool == nil {
		return "", fmt.Errorf("%w: %q is not a sandbox tool", ErrInvalid, r.Tool)
	}
	// Refuse early when the host cannot run sandboxes: nothing is made.
	if _, err := m.o.Backend(); err != nil {
		if errors.Is(err, ErrNoSandbox) {
			return NoSandboxText, nil
		}
		return "", err
	}
	e, err := m.find(r.User, r.Conversation)
	if err != nil {
		return "", err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.gone {
		return "", ErrBusy
	}
	ctx = context.WithValue(ctx, requestKey{}, r)
	if m.o.Prepare != nil {
		ctx = m.o.Prepare(ctx, r)
	}
	if err := m.ready(ctx, e); err != nil {
		if e.sb == nil && e.snap == nil {
			m.forget(e)
		}
		if errors.Is(err, ErrNoSandbox) {
			return NoSandboxText, nil
		}
		return "", err
	}
	current = e.sb
	end := func() {}
	if m.o.Begin != nil {
		end = m.o.Begin(ctx, r.User, r.Conversation, r.Operation)
		defer end() // a panic of the tool still ends the call
	}
	e.suspendFails, e.suspendAfter = 0, time.Time{}
	out, runErr := tool.Run(ctx, r.Args)
	// The call's changes become one change set, whether or not the command
	// succeeded: a failing script may still have written files.
	_, conflicts, cerr := e.sb.Checkpoint(ctx, m.o.Title+r.Tool)
	switch {
	case cerr != nil && !errors.Is(cerr, workspace.ErrCommitted):
		out += "\n[the changes could not be saved to the user's files: " + cerr.Error() + "]"
	case len(conflicts) > 0:
		out += ConflictNote(conflicts, e.mounts)
	}
	end()
	m.mu.Lock()
	e.lastUse = m.o.Now()
	m.mu.Unlock()
	return out, runErr
}

// suspendBackoff is the wait after n failed suspends of one sandbox: the
// sweep runs every minute, and a suspend that fails holds the entry for up to
// a minute, so it doubles from two minutes to half an hour.
func suspendBackoff(n int) time.Duration {
	d := time.Minute << min(n, 5)
	return min(d, 30*time.Minute)
}

// Sweep puts idle sandboxes to sleep and removes the old ones. It is called
// once a minute; tests call it by hand.
func (m *Manager) Sweep(ctx context.Context) {
	now := m.o.Now()
	m.mu.Lock()
	list := make([]*entry, 0, len(m.entries))
	for _, e := range m.entries {
		list = append(list, e)
	}
	m.mu.Unlock()
	for _, e := range list {
		if !e.mu.TryLock() {
			continue // in use
		}
		m.mu.Lock()
		idle := now.Sub(e.lastUse)
		m.mu.Unlock()
		switch {
		case now.Sub(e.created) >= m.o.MaxAge:
			e.gone = true
			m.destroy(ctx, e)
			m.forget(e)
		case e.sb != nil && idle >= m.o.IdleAfter && !now.Before(e.suspendAfter):
			sctx, cancel := context.WithTimeout(ctx, time.Minute)
			ref, err := e.sb.Suspend(sctx)
			cancel()
			switch {
			case err == nil:
				e.sb, e.snap = nil, &ref
				e.suspendFails = 0
			case errors.Is(err, sandbox.ErrGone):
				// The guest stopped by itself: keeping it would send every
				// next call of the chat to a dead sandbox.
				xlog.Warn("sandbox: an idle sandbox is gone, dropping it", "user", e.user, "conversation", e.conv, "error", err)
				m.destroy(ctx, e)
				e.suspendFails = 0
			default:
				e.suspendFails++
				e.suspendAfter = now.Add(suspendBackoff(e.suspendFails))
				xlog.Warn("sandbox: could not suspend an idle sandbox, trying again later", "user", e.user, "conversation", e.conv,
					"failures", e.suspendFails, "retry_after", e.suspendAfter, "error", err)
			}
		}
		e.mu.Unlock()
	}
}

// Start runs Sweep every minute until ctx ends.
func (m *Manager) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				m.Sweep(ctx)
			}
		}
	}()
}

// Reset destroys every sandbox, as Close does, and keeps the manager usable.
// The administrator changed the backend, so the old sandboxes are of no use.
func (m *Manager) Reset(ctx context.Context) { _ = m.Close(ctx) }

// Close destroys every sandbox, all at once, and returns when they are gone
// or ctx ends, whichever is first. What the code did is already in the files.
// On the deadline it returns the context error: the stops go on in the
// background, and a sandbox that a busy call still holds is left to the sweep
// the backend makes at the next start.
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	list := make([]*entry, 0, len(m.entries))
	for _, e := range m.entries {
		list = append(list, e)
	}
	m.entries = map[string]*entry{}
	m.mu.Unlock()
	var wg sync.WaitGroup
	for _, e := range list {
		wg.Go(func() {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.gone = true
			m.destroy(ctx, e)
		})
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// tracker is the store of one sandbox. It remembers the revision the files
// were read at and the revisions the sandbox committed itself, so that only
// changes made by somebody else make the sandbox stale.
type tracker struct {
	Store
	mu   sync.Mutex
	base int64
	own  map[int64]bool
}

func (t *tracker) Materialize(ctx context.Context, v workspace.View, dir string) (int64, error) {
	rev, err := t.Store.Materialize(ctx, v, dir)
	if err == nil {
		t.mu.Lock()
		t.base, t.own = rev, map[int64]bool{}
		t.mu.Unlock()
	}
	return rev, err
}

func (t *tracker) CommitUpper(ctx context.Context, v workspace.View, a workspace.Actor, base int64, up workspace.UpperSource, o workspace.UpperOptions) (workspace.ChangeSet, []workspace.Conflict, error) {
	cs, conflicts, err := t.Store.CommitUpper(ctx, v, a, base, up, o)
	if err == nil && cs.Rev != 0 {
		t.mu.Lock()
		if t.own == nil {
			t.own = map[int64]bool{}
		}
		t.own[cs.Rev] = true
		t.mu.Unlock()
	}
	return cs, conflicts, err
}

// stale reports whether somebody else changed the user's files after the
// sandbox read them. A host that cannot answer says "not stale": the sandbox
// keeps working with what it has.
func (t *tracker) stale(ctx context.Context, a workspace.Actor) bool {
	t.mu.Lock()
	base := t.base
	t.mu.Unlock()
	changes, err := t.Store.ChangesSince(ctx, a, base, 200)
	if err != nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, c := range changes {
		if !t.own[c.Rev] {
			return true
		}
	}
	return false
}

type requestKey struct{}

// RequestFrom returns the request of the call that ctx belongs to.
func RequestFrom(ctx context.Context) (Request, bool) {
	r, ok := ctx.Value(requestKey{}).(Request)
	return r, ok
}
