package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// DefaultIdleSuspend is how long a sandbox may sit unused before it is
// suspended.
const DefaultIdleSuspend = 10 * time.Minute

// Options configures a Manager.
type Options struct {
	Workspace workspace.Store
	// Sandbox runs the tools. Nil: chats run without tools that need a sandbox.
	Sandbox sandbox.Backend
	Store   Store // transcripts
	Harness Harness
	// Image is an OCI reference with digest; empty means the backend default.
	Image       string
	IdleSuspend time.Duration // default DefaultIdleSuspend
	MaxLiveVMs  int           // default 0: no cap
	// SystemPrompt is product text. The session appends the workspace layout.
	SystemPrompt string
	// StateDir is a folder for the harness's private files, one sub-folder
	// per chat. It must not be in the workspace.
	StateDir string
	// Shared lists the folders outside the home folder that the actor may
	// use, for example the folders shared with the user. It runs once per
	// Open. Nil: only the home folder.
	Shared func(ctx context.Context, a workspace.Actor) ([]SharedRoot, error)
	// ChatTools supplies host-side tools for one turn, for example the tools
	// of MCP servers that the user connected. It runs at the start of every
	// turn with the chat's actor. The tools are added to the six sandbox
	// tools. When the set of tools (names, descriptions, schemas) differs
	// from the one the harness runs with, the harness restarts from its saved
	// state before the turn. A tool whose name is the name of a sandbox tool
	// is dropped. An error from the hook does not fail the turn: it runs with
	// the sandbox tools only. Nil: only the sandbox tools.
	ChatTools func(ctx context.Context, a workspace.Actor) (TurnTools, error)
	// Inputs gives the harness of a chat its source of pending messages (see
	// HarnessEnv.Inputs). It runs once per harness start. Nil: no pending
	// messages.
	Inputs func(a workspace.Actor) func() ([]string, func(error))
	Logger *slog.Logger
}

// Manager opens chats and owns their sandboxes.
type Manager struct {
	o      Options
	log    *slog.Logger
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup // turn goroutines

	openMu   sync.Mutex // serializes Open and Delete
	layoutMu sync.Mutex // chat folder names
	// capMu serializes the creates and resumes of sandboxes under the cap.
	// Lock order: a chat's vmMu, then capMu. The evictor takes the vmMu of
	// other chats with TryLock only.
	capMu sync.Mutex

	mu     sync.Mutex
	chats  map[string]*Chat
	live   map[*Chat]struct{} // chats with a running sandbox
	gone   map[string]bool    // chats that Delete is removing
	closed bool
}

// New returns a Manager.
func New(o Options) (*Manager, error) {
	// Workspace may be nil: chats then have no files, only the harness and the
	// host tools.
	switch {
	case o.Store == nil:
		return nil, fmt.Errorf("%w: no store", ErrInvalid)
	case o.Harness == nil:
		return nil, fmt.Errorf("%w: no harness", ErrInvalid)
	case o.StateDir == "":
		return nil, fmt.Errorf("%w: no state folder", ErrInvalid)
	}
	if o.IdleSuspend <= 0 {
		o.IdleSuspend = DefaultIdleSuspend
	}
	if err := os.MkdirAll(o.StateDir, 0o700); err != nil {
		return nil, err
	}
	log := o.Logger
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{o: o, log: log, ctx: ctx, cancel: cancel, chats: map[string]*Chat{}, live: map[*Chat]struct{}{}, gone: map[string]bool{}}, nil
}

func (m *Manager) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

// userActor is the actor for the user's own actions: uploads and renames.
func userActor(a workspace.Actor) workspace.Actor {
	a.ChatID = ""
	return a
}

func (m *Manager) chatDir(chatID string) string {
	sum := sha256.Sum256([]byte(chatID))
	return filepath.Join(m.o.StateDir, "chats", hex.EncodeToString(sum[:8]))
}

// Open returns the chat, creating it on first use. It loads the transcript.
// A chat that is open already is returned as it is. A chat of another
// principal is ErrNotFound. The first tool call creates or resumes the
// sandbox.
func (m *Manager) Open(ctx context.Context, a workspace.Actor, chatID string) (*Chat, error) {
	if chatID == "" || a.Principal == "" || a.ChatID != chatID {
		return nil, fmt.Errorf("%w: the actor's ChatID must be the chat ID", ErrInvalid)
	}
	if !validPrincipal(a.Principal) {
		return nil, fmt.Errorf("%w: the principal must be one path segment", ErrInvalid)
	}
	m.openMu.Lock()
	defer m.openMu.Unlock()
	if m.isClosed() {
		return nil, ErrClosed
	}
	m.mu.Lock()
	c := m.chats[chatID]
	deleting := m.gone[chatID]
	m.mu.Unlock()
	if deleting {
		return nil, ErrNotFound
	}
	if c != nil {
		if c.principal() != a.Principal {
			return nil, ErrNotFound
		}
		view, err := m.buildView(ctx, a)
		if err != nil {
			return nil, err
		}
		if err := c.syncAccess(ctx, a, view); err != nil {
			return nil, err
		}
		if err := c.reopen(ctx); err != nil {
			return nil, err
		}
		return c, nil
	}

	rec, err := m.o.Store.GetChat(ctx, a.Principal, chatID)
	if errors.Is(err, ErrNotFound) {
		now := time.Now().UTC()
		rec = ChatRecord{ID: chatID, Principal: a.Principal, Title: DefaultTitle, Created: now, Updated: now}
		if m.o.Workspace != nil {
			if _, serr := m.o.Workspace.Stat(ctx, userActor(a), HomePath(a.Principal)); serr != nil {
				if errors.Is(serr, workspace.ErrNotFound) {
					return nil, fmt.Errorf("%w: %s", ErrNoHome, HomePath(a.Principal))
				}
				return nil, serr
			}
		}
		if err = m.o.Store.CreateChat(ctx, rec); errors.Is(err, ErrExists) {
			err = ErrNotFound // the ID belongs to another principal; do not say so
		}
	}
	if err != nil {
		return nil, err
	}
	view, err := m.buildView(ctx, a)
	if err != nil {
		return nil, err
	}
	c = newChat(m, a, rec, view)
	if err := c.startHarness(ctx); err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.chats[chatID] = c
	m.mu.Unlock()
	return c, nil
}

// Delete destroys the chat's sandbox and its transcript. Files stay in the
// workspace. A chat that does not exist, or belongs to another principal, is
// ErrNotFound.
func (m *Manager) Delete(ctx context.Context, a workspace.Actor, chatID string) error {
	if chatID == "" || a.Principal == "" || a.ChatID != chatID {
		return fmt.Errorf("%w: the actor's ChatID must be the chat ID", ErrInvalid)
	}
	if !validPrincipal(a.Principal) {
		return fmt.Errorf("%w: the principal must be one path segment", ErrInvalid)
	}
	m.openMu.Lock()
	if m.isClosed() {
		m.openMu.Unlock()
		return ErrClosed
	}
	rec, err := m.o.Store.GetChat(ctx, a.Principal, chatID)
	if err != nil {
		m.openMu.Unlock()
		return err
	}
	m.mu.Lock()
	c := m.chats[chatID]
	m.gone[chatID] = true // Open refuses the ID until the delete is over
	m.mu.Unlock()
	m.openMu.Unlock() // the close below is slow: let other chats open

	defer func() {
		m.mu.Lock()
		delete(m.gone, chatID)
		m.mu.Unlock()
	}()
	var errs []error
	if c != nil {
		// Go on after an error: the chat is being deleted, so a sandbox that
		// stays behind is worse than a failed close.
		if err := c.close(ctx); err != nil {
			errs = append(errs, err)
		}
		if err := c.destroySandbox(ctx); err != nil {
			errs = append(errs, err)
		}
	} else if rec.Snapshot != nil && m.o.Sandbox != nil {
		// The chat was never opened since the restart, but it has a
		// suspended sandbox.
		if err := m.destroySnapshot(ctx, a, *rec.Snapshot); err != nil {
			errs = append(errs, err)
		}
	}
	if errors.Is(errors.Join(errs...), errSnapshotLeft) {
		// Keep the record with its reference, so that a second Delete can
		// try again.
		return errors.Join(errs...)
	}
	if err := m.o.Store.DeleteChat(ctx, a.Principal, chatID); err != nil {
		return errors.Join(append(errs, err)...)
	}
	m.mu.Lock()
	delete(m.chats, chatID)
	m.mu.Unlock()
	if err := os.RemoveAll(m.chatDir(chatID)); err != nil {
		errs = append(errs, fmt.Errorf("session: remove the state folder: %w", err))
	}
	return errors.Join(errs...)
}

// Close stops every turn, suspends every live sandbox and stops background
// work. The chats stay stored.
func (m *Manager) Close(ctx context.Context) error {
	// Take openMu so that an Open in progress finishes and registers its chat
	// before we list the chats, and later Opens see the closed flag.
	m.openMu.Lock()
	m.mu.Lock()
	m.closed = true
	chats := make([]*Chat, 0, len(m.chats))
	for _, c := range m.chats {
		chats = append(chats, c)
	}
	m.mu.Unlock()
	m.openMu.Unlock()
	var errs []error
	for _, c := range chats {
		if err := c.close(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	m.wg.Wait()
	// No idle timer may suspend a sandbox from now on: Close does it.
	for _, c := range chats {
		c.stopTimer()
	}
	for _, c := range chats {
		if err := m.suspendForClose(ctx, c); err != nil {
			errs = append(errs, err)
		}
	}
	m.cancel()
	return errors.Join(errs...)
}

// Close retries a failed save this often, waiting a little longer each time.
const (
	closeAttempts = 3
	closeBackoff  = 100 * time.Millisecond
)

// suspendForClose suspends the sandbox of c, retrying a failed suspend. If it
// still fails, it destroys a live sandbox, so that no VM leaks, and returns
// the error.
func (m *Manager) suspendForClose(ctx context.Context, c *Chat) error {
	var err error
	for i := 0; i < closeAttempts; i++ {
		if i > 0 {
			select {
			case <-time.After(time.Duration(i) * closeBackoff):
			case <-ctx.Done():
				i = closeAttempts // no more attempts
			}
		}
		if err = c.suspendNow(ctx); err == nil {
			return nil
		}
	}
	m.log.Error("session: could not suspend a sandbox at close", "chat", c.id, "error", err)
	c.abandonLive(context.WithoutCancel(ctx))
	return err
}

// errSnapshotLeft marks an error after which a snapshot still exists.
var errSnapshotLeft = errors.New("session: the snapshot is still there")

// Waiting for a busy candidate at the cap.
const (
	evictPoll = 50 * time.Millisecond
	evictWait = 10 * time.Second
)

// makeRoom suspends idle chats until a sandbox may start under the cap. The
// caller holds capMu. When a candidate is busy (its lock is taken, for
// example because it is suspending itself), makeRoom polls for up to
// evictWait, and releases capMu while it waits so that a candidate that waits
// for capMu can go on. It never blocks on the lock of another chat.
func (m *Manager) makeRoom(ctx context.Context, self *Chat) error {
	deadline := time.Now().Add(evictWait)
	for m.liveCount() >= m.o.MaxLiveVMs {
		freed, busy := m.evictOne(ctx, self)
		if freed {
			continue
		}
		if busy == 0 || !time.Now().Before(deadline) {
			return ErrTooManyVMs
		}
		m.capMu.Unlock()
		select {
		case <-time.After(evictPoll):
			m.capMu.Lock()
		case <-ctx.Done():
			m.capMu.Lock()
			return ctx.Err()
		}
	}
	return nil
}

func (m *Manager) liveCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.live)
}

// evictOne suspends the live chat that has been idle the longest, except
// self. It reports whether it freed a sandbox, and how many candidates it
// skipped because their lock was taken. A chat that runs a tool or a turn is
// not idle and is skipped without counting.
func (m *Manager) evictOne(ctx context.Context, self *Chat) (freed bool, busy int) {
	m.mu.Lock()
	cands := make([]*Chat, 0, len(m.live))
	for c := range m.live {
		if c != self {
			cands = append(cands, c)
		}
	}
	m.mu.Unlock()
	sort.Slice(cands, func(i, j int) bool { return cands[i].lastUse.Load() < cands[j].lastUse.Load() })
	for _, c := range cands {
		if !c.vmMu.TryLock() {
			busy++ // a tool is starting, resuming or suspending it
			continue
		}
		if !c.idleLocked() {
			c.vmMu.Unlock()
			continue
		}
		err := c.suspendIdleLocked(ctx)
		c.vmMu.Unlock()
		if err == nil {
			return true, busy
		}
		if errors.Is(err, errNotIdle) {
			continue
		}
		m.log.Warn("session: could not suspend an idle sandbox", "chat", c.id, "error", err)
	}
	return false, busy
}

// destroySnapshot resumes a suspended sandbox and destroys it: a backend can
// destroy only a sandbox it runs. When the resume fails, the log names the
// snapshot so that an operator can remove it.
//
// A backend that refuses the reference with workspace.ErrInvalid does not
// know it: the product changed backends since the snapshot was taken, or the
// reference is damaged. No retry can change that, so the reference is
// dropped, with its path in the log, and the delete goes on. The folder that
// the path names stays on disk.
func (m *Manager) destroySnapshot(ctx context.Context, a workspace.Actor, snap sandbox.SnapshotRef) error {
	sb, err := m.o.Sandbox.Resume(ctx, snap, sandbox.Env{Store: m.o.Workspace, Actor: a})
	if errors.Is(err, workspace.ErrInvalid) {
		m.log.Warn("session: the backend does not know this snapshot (another backend made it, or it is damaged); dropping the reference and leaving the snapshot on disk",
			"chat", a.ChatID, "backend", snap.Backend, "snapshot", snap.Path, "error", err)
		return nil
	}
	if err != nil {
		m.log.Error("session: could not resume a sandbox to destroy it", "chat", a.ChatID, "backend", snap.Backend, "snapshot", snap.Path, "error", err)
		return fmt.Errorf("session: resume the sandbox to destroy it: %w", errors.Join(err, errSnapshotLeft))
	}
	if err := sb.Destroy(ctx); err != nil {
		m.log.Warn("session: destroy the sandbox", "chat", a.ChatID, "error", err)
		return fmt.Errorf("session: destroy the sandbox: %w", err)
	}
	return nil
}
