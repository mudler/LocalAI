// SPDX-License-Identifier: MIT

// Package manager keeps the sandboxes of the add-on. A sandbox belongs to one
// user and one conversation, and nobody else reaches it: the key is the pair,
// and the user comes from the host, never from the model. Every call commits
// what the code changed as one change set, so the sandbox itself holds no work
// that is not in the user's files.
package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/addons/sandbox/internal/codetools"
	"github.com/mudler/LocalAI/addons/sandbox/internal/remote"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The defaults of the limits.
const (
	DefaultPerUser   = 4
	DefaultTotal     = 16
	DefaultIdleAfter = 10 * time.Minute
	DefaultMaxAge    = 24 * time.Hour
)

// ErrBusy says the add-on holds as many sandboxes as it may.
var ErrBusy = errors.New("the add-on is running as many sandboxes as it may; try again later")

// Options configures a Manager.
type Options struct {
	// Backend returns the backend to use now. It may change between calls (the
	// administrator can switch it). codetools.ErrNoSandbox means there is none.
	Backend func() (sandbox.Backend, error)
	Store   *remote.Store
	// StateDir is the folder the backend keeps its sandboxes in. Whatever an
	// earlier run left in it is removed at start.
	StateDir string
	// KeepStateDir leaves StateDir as it is at start. Set it when the backends
	// keep their own folders in it: they stop the sandboxes an earlier process
	// left before they remove the folders, and emptying the folder first
	// would drop what they need to find those sandboxes.
	KeepStateDir      bool
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
}

// New returns a manager. It removes what an earlier run left in StateDir.
func New(o Options) (*Manager, error) {
	if o.Backend == nil || o.Store == nil {
		return nil, errors.New("manager: a backend and a store are required")
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
		return fmt.Errorf("manager: %q is not a folder the add-on may empty", dir)
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

func (e *entry) actor() workspace.Actor {
	return workspace.Actor{Principal: e.user, ChatID: e.conv}
}

// mounts is what a sandbox sees: the user's files, writable, under home.
var mounts = []workspace.MountRequest{{Path: "/", At: "home", Mode: workspace.ModeWrite}}

// ready makes sure the entry has a running sandbox that matches the user's
// files. The entry's lock is held.
func (m *Manager) ready(ctx context.Context, e *entry) error {
	b, err := m.o.Backend()
	if err != nil {
		return err
	}
	if e.trk == nil {
		e.trk = &tracker{Store: m.o.Store}
	}
	env := sandbox.Env{Store: e.trk, Actor: e.actor()}
	switch {
	case e.sb != nil:
		if e.trk.stale(ctx, e.actor()) {
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
	v, err := m.o.Store.View(ctx, e.actor(), mounts)
	if err != nil {
		return err
	}
	sb, err := b.Create(ctx, sandbox.Spec{Env: env, View: v})
	if err != nil {
		return err
	}
	e.sb, e.hasBuilt = sb, true
	return nil
}

// Run runs one tool call and returns its output. What the code changed in the
// user's files is committed before it returns.
func (m *Manager) Run(ctx context.Context, r Request) (string, error) {
	if r.User == "" || r.Conversation == "" || r.Operation == "" {
		return "", fmt.Errorf("%w: a call needs a user, a conversation and an operation", codetools.ErrInvalid)
	}
	var tool *codetools.Tool
	var runWith *sandbox.Sandbox
	var current sandbox.Sandbox
	runWith = &current
	for _, t := range codetools.NewTools(func(context.Context) (sandbox.Sandbox, func(), error) {
		return *runWith, func() {}, nil
	}) {
		if t.Name == r.Tool {
			t := t
			tool = &t
		}
	}
	if tool == nil {
		return "", fmt.Errorf("%w: %q is not a sandbox tool", codetools.ErrInvalid, r.Tool)
	}
	// Refuse early when the host cannot run sandboxes: nothing is made.
	if _, err := m.o.Backend(); err != nil {
		if errors.Is(err, codetools.ErrNoSandbox) {
			return codetools.NoSandboxText, nil
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
	ctx = remote.WithOperation(ctx, r.Operation)
	if err := m.ready(ctx, e); err != nil {
		if e.sb == nil && e.snap == nil {
			m.forget(e)
		}
		if errors.Is(err, codetools.ErrNoSandbox) {
			return codetools.NoSandboxText, nil
		}
		return "", err
	}
	current = e.sb
	out, runErr := tool.Run(ctx, r.Args)
	// The call's changes become one change set, whether or not the command
	// succeeded: a failing script may still have written files.
	cs, conflicts, cerr := e.sb.Checkpoint(ctx, "Sandbox: "+r.Tool)
	_ = cs
	switch {
	case cerr != nil && !errors.Is(cerr, workspace.ErrCommitted):
		out += "\n[the changes could not be saved to your files: " + cerr.Error() + "]"
	case len(conflicts) > 0:
		out += fmt.Sprintf("\n[%d file(s) changed in your files meanwhile; the sandbox's version was kept as a copy]", len(conflicts))
	}
	m.mu.Lock()
	e.lastUse = m.o.Now()
	m.mu.Unlock()
	return out, runErr
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
		case e.sb != nil && idle >= m.o.IdleAfter:
			sctx, cancel := context.WithTimeout(ctx, time.Minute)
			ref, err := e.sb.Suspend(sctx)
			cancel()
			if err != nil {
				xlog.Warn("sandbox: could not suspend an idle sandbox", "error", err)
			} else {
				e.sb, e.snap = nil, &ref
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

// Close destroys every sandbox. What the code did is already in the files.
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	list := make([]*entry, 0, len(m.entries))
	for _, e := range m.entries {
		list = append(list, e)
	}
	m.entries = map[string]*entry{}
	m.mu.Unlock()
	for _, e := range list {
		e.mu.Lock()
		e.gone = true
		m.destroy(ctx, e)
		e.mu.Unlock()
	}
	return nil
}

// tracker is the store of one sandbox. It remembers the revision the files
// were read at and the revisions the sandbox committed itself, so that only
// changes made by somebody else make the sandbox stale.
type tracker struct {
	*remote.Store
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
