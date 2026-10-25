package session

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The actor and the view of a chat come from the product (the actor) and
// from Options.Shared (the shared folders) at each Open. A chat that stays in
// memory keeps the access of its first Open unless it is checked again, so
// every Open of a cached chat compares the new access with the old one
// (syncAccess), and a failed mount of a shared folder rebuilds the view
// (reloadView).

func copyActor(a workspace.Actor) workspace.Actor {
	a.Groups = append([]string(nil), a.Groups...)
	return a
}

// validPrincipal reports whether p can be one path segment of a home folder.
func validPrincipal(p string) bool {
	return p != "" && p != "." && p != ".." && !strings.ContainsAny(p, "/\\\x00")
}

// sameActor reports whether two actors have the same principal, chat and set
// of groups.
func sameActor(a, b workspace.Actor) bool {
	if a.Principal != b.Principal || a.ChatID != b.ChatID {
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

// buildView lists the shared folders of the actor and builds its view.
func (m *Manager) buildView(ctx context.Context, a workspace.Actor) (workspace.View, error) {
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
	return BuildView(ctx, m.o.Workspace, a, shared, m.log)
}

// reloadView builds the view of the chat again, for the actor that the chat
// has now, and stores it. The caller holds vmMu, so no sandbox is created
// with the old view meanwhile.
func (c *Chat) reloadView(ctx context.Context) error {
	actor, _, _ := c.access()
	v, err := c.m.buildView(ctx, actor)
	if err != nil {
		return err
	}
	c.accMu.Lock()
	c.view = v
	c.accMu.Unlock()
	return nil
}

// syncAccess is called by Open for a chat that is in memory. a is the actor
// and view the new view of this Open. When neither differs from what the chat
// has, it does nothing: the sandbox and its processes stay. Otherwise the
// chat takes its access again:
//
//  1. a live sandbox is swept and checkpointed, then destroyed, and a
//     suspended one is discarded (a sandbox resumes only with the same actor);
//  2. the chat adopts the new actor and view;
//  3. an open chat restarts its harness session from the exported state, so
//     that the system prompt names the new folders.
//
// A chat that runs a turn is not changed: Open returns ErrTurnRunning.
func (c *Chat) syncAccess(ctx context.Context, a workspace.Actor, view workspace.View) error {
	c.accMu.RLock()
	oldActor, oldView, oldPrompt := c.actor, c.view, c.harnessView
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
		c.actor, c.user, c.view = copyActor(a), userActor(copyActor(a)), view
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
	c.evMu.RLock()
	b := c.bus
	c.evMu.RUnlock()
	b.close()
	c.releaseMemory(ctx)
	return err
}
