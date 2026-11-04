package session

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The access of a chat (its actor and its view) comes from the product at
// each Open: the product resolves it for every turn, so an edit of the chat's
// agent applies from the next turn. A chat that stays in memory keeps the
// access of its last Open, so every Open of a cached chat compares the new
// access with the old one (syncAccess), and a failed mount of a shared folder
// rebuilds the view (reloadView).

func copyActor(a workspace.Actor) workspace.Actor {
	a.Groups = append([]string(nil), a.Groups...)
	return a
}

// validPrincipal reports whether p can be one path segment of a home folder.
// The workspace administrator is not a user.
func validPrincipal(p string) bool {
	return p != "" && p != workspace.StudioAdmin && p != "." && p != ".." && !strings.ContainsAny(p, "/\\\x00")
}

// sameActor reports whether two actors have the same principal, chat, set
// of groups and fence.
func sameActor(a, b workspace.Actor) bool {
	if a.Principal != b.Principal || a.ChatID != b.ChatID || !a.SameFence(b) {
		return false
	}
	ga, gb := append([]string(nil), a.Groups...), append([]string(nil), b.Groups...)
	sort.Strings(ga)
	sort.Strings(gb)
	return len(ga) == len(gb) && strings.Join(ga, "\x00") == strings.Join(gb, "\x00")
}

// sameView reports whether two views have the same mounts with the same modes.
func sameView(a, b workspace.View) bool {
	if len(a.Mounts) != len(b.Mounts) {
		return false
	}
	for i := range a.Mounts {
		if a.Mounts[i] != b.Mounts[i] {
			return false
		}
	}
	return true
}

// buildView lists the shared folders of the actor and builds its view with
// primary at "home" and, when chat is not "", the chat folder at "chat". It
// is the one place a chat's view is built: UserAccess (the home) and
// ScopedAccess (the agent's folder and the chat folder) both call it, so an
// unscoped and a scoped chat get the same layout rules.
func (m *Manager) buildView(ctx context.Context, a workspace.Actor, primary string, extra []string, chat string) (workspace.View, error) {
	if m.o.Workspace == nil {
		return workspace.View{Actor: a}, nil
	}
	var shared []SharedRoot
	if m.o.Shared != nil {
		var err error
		if shared, err = m.o.Shared(ctx, a); err != nil {
			return workspace.View{}, fmt.Errorf("session: list shared folders: %w", err)
		}
	}
	return BuildViewFolders(ctx, m.o.Workspace, a, primary, extra, chat, shared, m.log)
}

// UserAccess is the access of a chat that works on the whole home: the
// actor as given and the view of its home and its shared folders. The actor
// must not be fenced: a fenced actor cannot mount the home.
func (m *Manager) UserAccess(ctx context.Context, a workspace.Actor) (Access, error) {
	if _, fenced := a.Fence(); fenced {
		return Access{}, fmt.Errorf("%w: a fenced actor has no view of the home", ErrInvalid)
	}
	v, err := m.buildView(ctx, a, HomePath(a.Principal), nil, "")
	if err != nil {
		return Access{}, err
	}
	return NewAccess(a, v)
}

// ScopedAccess is the access of a chat whose agent has a folder: the fenced
// actor, primary (the agent's folder, or a sub-agent's folder inside it) at
// "home" capped at the fence's mode, the chat folder at "chat" and the
// shared folders read only. The actor must be fenced, and both folders must
// be inside its fence: a folder the store cannot mount is ErrNoHome (the
// primary) or an error that wraps workspace.ErrNotFound (the chat folder),
// never a view of the home.
func (m *Manager) ScopedAccess(ctx context.Context, a workspace.Actor, primary, chat string) (Access, error) {
	return m.ScopedAccessFolders(ctx, a, primary, nil, chat)
}

// ScopedAccessFolders is ScopedAccess for an agent with several folders:
// primary is the first one, at "home", and extra are the others, at
// "folder-<name>" (see FolderMountNames).
func (m *Manager) ScopedAccessFolders(ctx context.Context, a workspace.Actor, primary string, extra []string, chat string) (Access, error) {
	if !workspace.HasFence(a) {
		return Access{}, fmt.Errorf("%w: a scoped access needs a fenced actor", ErrInvalid)
	}
	if primary == "" || chat == "" {
		return Access{}, fmt.Errorf("%w: a scoped access needs its folder and its chat folder", ErrInvalid)
	}
	if m.o.Workspace == nil {
		return Access{}, ErrNoWorkspace
	}
	v, err := m.buildView(ctx, a, primary, extra, chat)
	if err != nil {
		return Access{}, err
	}
	return NewAccess(a, v)
}

// reloadView builds the view of the chat again and stores it. The caller
// holds vmMu, so no sandbox is created with the old view meanwhile. When the
// product resolves the access to another actor (the chat's agent changed),
// the view is kept and the error says so: the next Open takes the new access.
func (c *Chat) reloadView(ctx context.Context) error {
	actor, _, _ := c.access()
	var x Access
	var err error
	if c.m.o.Resolve != nil {
		x, err = c.m.o.Resolve(ctx, actor)
	} else {
		x, err = c.m.UserAccess(ctx, actor)
	}
	if err != nil {
		return err
	}
	if !sameActor(actor, x.actor) {
		return fmt.Errorf("%w: the access of the chat changed; it applies at the next turn", ErrTurnRunning)
	}
	c.accMu.Lock()
	c.acc = x
	c.accMu.Unlock()
	return nil
}

// syncAccess is called by Open for a chat that is in memory, with the access
// of this Open. When neither its actor nor its view differs from what the
// chat has, it does nothing: the sandbox and its processes stay. Otherwise
// the chat takes its access again:
//
//  1. a live sandbox is swept and checkpointed, then destroyed, and a
//     suspended one is discarded (a sandbox resumes only with the same actor);
//  2. the chat adopts the new actor and view;
//  3. an open chat restarts its harness session from the exported state, so
//     that the system prompt names the new folders.
//
// A chat that runs a turn is not changed: Open returns ErrTurnRunning.
func (c *Chat) syncAccess(ctx context.Context, x Access) error {
	a, view := x.actor, x.view
	c.accMu.RLock()
	oldActor, oldView, oldPrompt := c.acc.actor, c.acc.view, c.harnessView
	c.accMu.RUnlock()
	boxStale := !sameActor(oldActor, a) || !sameView(oldView, view)
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	promptStale := !closed && !sameView(oldPrompt, view)
	if !boxStale && !promptStale {
		return nil
	}
	// Hold the chat like SetTitle does: Send waits and nothing else starts.
	c.mu.Lock()
	if c.running || c.renaming != nil {
		c.mu.Unlock()
		return fmt.Errorf("%w: the access of the chat changed; open it again when the turn has ended", ErrTurnRunning)
	}
	ch := make(chan struct{})
	c.renaming = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.renaming = nil
		c.mu.Unlock()
		close(ch)
	}()

	if boxStale {
		if err := c.destroySandbox(ctx); err != nil {
			c.m.log.Warn("session: could not discard the sandbox of a chat whose access changed", "chat", c.id, "error", err)
		}
		c.mu.Lock()
		had := c.rec.Snapshot != nil
		c.rec.Snapshot = nil
		c.mu.Unlock()
		if had {
			if err := c.persist(ctx); err != nil {
				c.m.log.Warn("session: could not clear the snapshot reference", "chat", c.id, "error", err)
			}
		}
		c.accMu.Lock()
		c.acc = x
		c.accMu.Unlock()
	}
	if closed {
		return nil // reopen starts the harness with the new prompt
	}
	return c.restartHarness(ctx)
}

// swapHarness closes the harness session and starts a new one from its
// exported state. On an error no harness runs and the chat is still open.
func (c *Chat) swapHarness(ctx context.Context) error {
	c.mu.Lock()
	hs := c.hs
	c.hs = nil
	c.mu.Unlock()
	if hs != nil {
		if state, err := hs.Export(); err == nil {
			c.mu.Lock()
			c.rec.HarnessState = state
			c.stateSeq++
			c.mu.Unlock()
		} else {
			c.m.log.Warn("session: export the harness state before a restart", "chat", c.id, "error", err)
		}
		if err := hs.Close(); err != nil {
			c.m.log.Warn("session: close the harness before a restart", "chat", c.id, "error", err)
		}
	}
	return c.startHarness(ctx)
}

// restartHarness is swapHarness. If the start fails the chat is left closed,
// and the next Open tries again.
func (c *Chat) restartHarness(ctx context.Context) error {
	err := c.swapHarness(ctx)
	if err == nil {
		return nil
	}
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	// Say why before the stream ends: a consumer that only sees the channel
	// close cannot tell a failure from a normal end, and a message that was
	// accepted would never get an answer.
	c.emit(Event{Kind: KindError, Text: reasonAgent, Fatal: true})
	c.evMu.RLock()
	b := c.bus
	c.evMu.RUnlock()
	b.close()
	c.releaseMemory(ctx)
	return err
}

// EnsureChatFolder makes the folder of a chat (Chats/<folder>/) when it is
// missing, gives the chat its folder name first when it has none, and
// returns the folder. owner is the user's own actor: it must not be fenced,
// because the product calls this before it fences the chat to that folder,
// and a fenced actor cannot create a folder outside its fence. A chat of
// another principal is ErrNotFound.
func (m *Manager) EnsureChatFolder(ctx context.Context, owner workspace.Actor, chatID string) (workspace.Node, error) {
	if m.o.Workspace == nil {
		return workspace.Node{}, ErrNoWorkspace
	}
	if _, fenced := owner.Fence(); fenced || owner.ChatID != "" || !validPrincipal(owner.Principal) {
		return workspace.Node{}, fmt.Errorf("%w: the chat folder is made as the user", ErrInvalid)
	}
	c, err := m.folderChat(ctx, owner, chatID)
	if err != nil {
		return workspace.Node{}, err
	}
	name, err := c.ensureFolder(ctx)
	if err != nil {
		return workspace.Node{}, err
	}
	return m.o.Workspace.Stat(ctx, owner, ChatsPath(owner.Principal)+"/"+name)
}

// folderChat returns the chat in memory, or a chat value that is only used to
// name and make the folder of a chat that is not open here. Open is held off
// meanwhile, so the record it saves is not saved over by an Open that read
// it before.
func (m *Manager) folderChat(ctx context.Context, owner workspace.Actor, chatID string) (*Chat, error) {
	m.openMu.Lock()
	defer m.openMu.Unlock()
	m.mu.Lock()
	c := m.chats[chatID]
	m.mu.Unlock()
	if c != nil {
		if c.principal() != owner.Principal {
			return nil, ErrNotFound
		}
		return c, nil
	}
	rec, err := m.o.Store.GetChat(ctx, owner.Principal, chatID)
	if err != nil {
		return nil, err
	}
	a := owner
	a.ChatID = chatID
	x, err := NewAccess(a, workspace.View{})
	if err != nil {
		return nil, err
	}
	c = newChat(m, x, rec)
	if rec.Folder == "" {
		// The name is saved here, under openMu, so that the chat value can
		// be dropped afterwards.
		if _, err := c.reserveFolder(ctx); err != nil {
			return nil, err
		}
		// reserveFolder keeps a name it could not save, for the save at the
		// end of a turn; this chat value has no turn, so the save must work.
		if err := c.persist(ctx); err != nil {
			return nil, err
		}
	}
	return c, nil
}
