package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// acquireCreate is the Acquire of the tools: it creates the sandbox when the
// chat has none.
func (c *Chat) acquireCreate(ctx context.Context) (sandbox.Sandbox, func(), error) {
	return c.acquire(ctx, true)
}

// acquire returns the chat's sandbox and a release function. When the chat
// has none, it resumes the suspended one, or creates a new one if create is
// true. Without create it returns a nil sandbox and a nil error when there is
// nothing to resume.
//
// Lock order: a chat's vmMu first, then capMu. The evictor, which runs under
// capMu, takes the vmMu of other chats with TryLock only, so two chats that
// start at the same time cannot wait for each other.
func (c *Chat) acquire(ctx context.Context, create bool) (sandbox.Sandbox, func(), error) {
	if c.m.o.Sandbox == nil {
		if create {
			return nil, nil, ErrNoSandbox
		}
		return nil, nil, nil
	}
	c.vmMu.Lock()
	defer c.vmMu.Unlock()
	if c.vm.sb == nil {
		snap := c.snapshot()
		if snap == nil && !create {
			return nil, nil, nil
		}
		if c.vm.unsupported {
			return nil, nil, ErrNoSandbox
		}
		if err := c.startLocked(ctx, snap); err != nil {
			return nil, nil, err
		}
	}
	c.vm.busy++
	return c.vm.sb, c.release, nil
}

func (c *Chat) release() {
	c.vmMu.Lock()
	c.vm.busy--
	c.vmMu.Unlock()
	c.touch()
}

// refresh makes the sandbox see the files that were stored after it was
// created. Its processes end. It does nothing when the chat has no sandbox
// and no snapshot.
func (c *Chat) refresh(ctx context.Context) error {
	sb, release, err := c.acquire(ctx, false)
	if err != nil || sb == nil {
		return err
	}
	defer release()
	sweepTemp(ctx, sb)
	err = sb.Refresh(ctx)
	if errors.Is(err, workspace.ErrNotFound) {
		// A folder of the view is gone or no longer shared. The sandbox
		// cannot mount it again: build the view again and drop the sandbox
		// if the view changed. The next tool call creates one.
		if rerr := c.recoverView(ctx); rerr == nil {
			return nil
		}
	}
	return err
}

// recoverView builds the view of the chat again after a workspace
// ErrNotFound. When the view differs, it checkpoints and destroys the live
// sandbox, which has the old view, and reports nil.
func (c *Chat) recoverView(ctx context.Context) error {
	c.vmMu.Lock()
	defer c.vmMu.Unlock()
	_, _, old := c.access()
	if err := c.reloadView(ctx); err != nil {
		return err
	}
	if _, _, v := c.access(); sameView(old, v) {
		return workspace.ErrNotFound
	}
	if err := c.destroyLocked(ctx); err != nil {
		c.m.log.Warn("session: destroy the sandbox after a view change", "chat", c.id, "error", err)
	}
	return nil
}

// createLocked creates a sandbox with the view of the chat. A workspace
// ErrNotFound (a shared folder is gone or no longer shared) rebuilds the view
// once and tries again. The caller holds c.vmMu.
func (c *Chat) createLocked(ctx context.Context, env sandbox.Env) (sandbox.Sandbox, error) {
	_, _, view := c.access()
	sb, err := c.m.o.Sandbox.Create(ctx, sandbox.Spec{Env: env, Image: c.m.o.Image, View: view})
	if errors.Is(err, workspace.ErrNotFound) {
		if rerr := c.reloadView(ctx); rerr == nil {
			_, _, view = c.access()
			sb, err = c.m.o.Sandbox.Create(ctx, sandbox.Spec{Env: env, Image: c.m.o.Image, View: view})
		}
	}
	return sb, err
}

// startLocked resumes the sandbox from snap, or creates a new one. The caller
// holds c.vmMu.
//
// The snapshot reference is cleared only when a sandbox runs. If the resume
// fails, the reference stays until a create succeeds, and the log names the
// snapshot so that an operator can find it.
func (c *Chat) startLocked(ctx context.Context, snap *sandbox.SnapshotRef) error {
	m := c.m
	if m.o.MaxLiveVMs > 0 {
		m.capMu.Lock()
		defer m.capMu.Unlock()
		if err := m.makeRoom(ctx, c); err != nil {
			return err
		}
	}
	actor, _, _ := c.access()
	env := sandbox.Env{Store: m.o.Workspace, Actor: actor}
	var sb sandbox.Sandbox
	if snap != nil {
		var err error
		if sb, err = m.o.Sandbox.Resume(ctx, *snap, env); err != nil {
			m.log.Warn("session: could not resume the sandbox; creating a new one", "chat", c.id, "backend", snap.Backend, "snapshot", snap.Path, "error", err)
			sb = nil
		}
	}
	if sb == nil {
		var err error
		sb, err = c.createLocked(ctx, env)
		if errors.Is(err, sandbox.ErrUnsupported) {
			c.vm.unsupported = true
			m.log.Warn("session: this host cannot run sandboxes", "error", err)
			return ErrNoSandbox
		}
		if err != nil {
			return fmt.Errorf("session: create the sandbox: %w", err)
		}
		if snap != nil {
			m.log.Warn("session: the snapshot is dropped; it stays on disk", "chat", c.id, "backend", snap.Backend, "snapshot", snap.Path)
		}
	}
	if snap != nil {
		c.spendSnapshot()
	}
	c.vm.sb = sb
	m.mu.Lock()
	m.live[c] = struct{}{}
	m.mu.Unlock()
	c.lastUse.Store(time.Now().UnixNano())
	c.arm(m.o.IdleSuspend)
	return nil
}

// spendSnapshot clears the snapshot reference after a sandbox runs. The
// reference is cleared in memory even when the save fails; the next save of
// the record retries. Until then the old reference stays in the store. It is
// safe there: a restart that finds it tries a resume, and a failed resume
// falls back to a create (see startLocked).
func (c *Chat) spendSnapshot() {
	c.mu.Lock()
	c.rec.Snapshot = nil
	c.mu.Unlock()
	if err := c.persist(context.Background()); err != nil {
		c.m.log.Warn("session: could not clear the snapshot reference", "chat", c.id, "error", err)
	}
}

// idleLocked reports whether the sandbox may be suspended: it exists, no tool
// call holds it and no turn runs. The caller holds c.vmMu.
//
// The busy count is defense in depth: a tool call holds a turn, and a turn
// makes isRunning true. It keeps a tool that outlives its turn (for example
// after Stop) from losing its sandbox.
//
// A turn can start right after this check. Its first tool call, or its
// refresh, then waits for c.vmMu until the suspend is over and resumes the
// sandbox. No tool runs during a suspend.
func (c *Chat) idleLocked() bool {
	return c.vm.sb != nil && c.vm.busy == 0 && !c.isRunning()
}

// suspendLocked checkpoints and suspends the sandbox and stores the
// reference. The caller holds c.vmMu.
//
// If the reference cannot be saved, the sandbox goes on running (it is
// resumed from the snapshot that was just made) and the error is returned, so
// that the caller tries again later. A snapshot reference is never lost: if
// even the resume fails, the reference stays in memory and the next save of
// the record stores it.
func (c *Chat) suspendLocked(ctx context.Context) error { return c.suspendWith(ctx, false) }

// errNotIdle means a turn started while the sandbox was swept; it is not
// suspended.
var errNotIdle = errors.New("session: the chat is not idle")

// suspendIdleLocked is suspendLocked for the idle timer and the evictor: the
// chat must still be idle after the sweep, so that a turn that starts at that
// moment never meets a suspend.
func (c *Chat) suspendIdleLocked(ctx context.Context) error { return c.suspendWith(ctx, true) }

func (c *Chat) suspendWith(ctx context.Context, idleOnly bool) error {
	if c.vm.sb == nil {
		return nil
	}
	sweepTemp(ctx, c.vm.sb)
	if idleOnly && !c.idleLocked() {
		return errNotIdle
	}
	ref, err := c.vm.sb.Suspend(ctx)
	switch {
	case errors.Is(err, sandbox.ErrGone):
		c.m.log.Warn("session: the sandbox was gone at suspend", "chat", c.id)
	case err != nil:
		return err
	default:
		if serr := c.setSnapshot(&ref); serr != nil {
			return c.keepRunning(ctx, ref, serr)
		}
	}
	c.dropLocked()
	return nil
}

// keepRunning brings the sandbox back after a suspend whose reference could
// not be saved. The caller holds c.vmMu.
func (c *Chat) keepRunning(ctx context.Context, ref sandbox.SnapshotRef, saveErr error) error {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), checkpointLimit)
	defer cancel()
	actor, _, _ := c.access()
	sb, err := c.m.o.Sandbox.Resume(rctx, ref, sandbox.Env{Store: c.m.o.Workspace, Actor: actor})
	if err == nil {
		c.vm.sb = sb
		return saveErr
	}
	c.m.log.Error("session: the snapshot reference could not be saved and the sandbox could not be resumed", "chat", c.id, "backend", ref.Backend, "snapshot", ref.Path, "error", err)
	c.mu.Lock()
	c.rec.Snapshot = &ref
	c.mu.Unlock()
	c.dropLocked()
	return errors.Join(saveErr, fmt.Errorf("session: resume after a failed save: %w", err))
}

// dropLocked forgets the sandbox. The caller holds c.vmMu.
func (c *Chat) dropLocked() {
	c.vm.sb = nil
	c.stopTimer()
	c.m.mu.Lock()
	delete(c.m.live, c)
	c.m.mu.Unlock()
}

// suspendNow suspends the sandbox if the chat has one. A chat that has no
// sandbox but holds a snapshot reference that is only in memory (a failed save
// and a failed resume, see keepRunning) saves the record instead.
func (c *Chat) suspendNow(ctx context.Context) error {
	c.vmMu.Lock()
	defer c.vmMu.Unlock()
	if c.vm.sb == nil {
		if c.snapshot() != nil {
			return c.persist(ctx)
		}
		return nil
	}
	return c.suspendLocked(ctx)
}

// abandonLive destroys the live sandbox of a chat whose reference could not
// be saved, so that no VM leaks. The files are safe at the last checkpoint.
func (c *Chat) abandonLive(ctx context.Context) {
	c.vmMu.Lock()
	defer c.vmMu.Unlock()
	if c.vm.sb == nil {
		return
	}
	c.m.log.Error("session: destroying a live sandbox because its snapshot reference could not be saved; its files are at the last checkpoint", "chat", c.id)
	if err := c.vm.sb.Destroy(ctx); err != nil {
		c.m.log.Error("session: destroy the sandbox", "chat", c.id, "error", err)
	}
	c.dropLocked()
}

// destroySandbox destroys the sandbox, also a suspended one: it resumes the
// snapshot first, because a backend can destroy only a sandbox it runs. It
// returns the errors of the backend. The chat has no sandbox afterwards in
// either case.
func (c *Chat) destroySandbox(ctx context.Context) error {
	c.vmMu.Lock()
	defer c.vmMu.Unlock()
	return c.destroyLocked(ctx)
}

// destroyLocked is destroySandbox. The caller holds c.vmMu. Before it
// destroys a live sandbox it sweeps the temporary files and checkpoints, so
// that work the last turn did not commit reaches the workspace. A failed
// checkpoint is logged; the destroy goes on.
func (c *Chat) destroyLocked(ctx context.Context) error {
	c.stopTimer()
	var errs []error
	if c.vm.sb == nil && c.m.o.Sandbox != nil {
		if snap := c.snapshot(); snap != nil {
			actor, _, _ := c.access()
			if err := c.m.destroySnapshot(ctx, actor, *snap); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if c.vm.sb != nil {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), checkpointLimit)
		sweepTemp(cctx, c.vm.sb)
		if _, _, err := c.vm.sb.Checkpoint(cctx, "Before the sandbox ends"); err != nil {
			c.m.log.Warn("session: checkpoint before destroying the sandbox", "chat", c.id, "error", err)
		}
		cancel()
		if err := c.vm.sb.Destroy(ctx); err != nil {
			c.m.log.Warn("session: destroy the sandbox", "chat", c.id, "error", err)
			errs = append(errs, fmt.Errorf("session: destroy the sandbox: %w", err))
		}
		c.vm.sb = nil
	}
	c.m.mu.Lock()
	delete(c.m.live, c)
	c.m.mu.Unlock()
	return errors.Join(errs...)
}

// touch records a use of the sandbox and restarts the idle timer. It does not
// arm a timer: only a chat with a sandbox has one.
func (c *Chat) touch() {
	c.lastUse.Store(time.Now().UnixNano())
	c.tmu.Lock()
	defer c.tmu.Unlock()
	if c.timer != nil {
		c.timer.Reset(c.m.o.IdleSuspend)
	}
}

func (c *Chat) arm(d time.Duration) {
	c.tmu.Lock()
	defer c.tmu.Unlock()
	if c.timer == nil {
		c.timer = time.AfterFunc(d, c.idleCheck)
		return
	}
	c.timer.Reset(d)
}

// idleCheck suspends the sandbox when it has been idle for IdleSuspend. It
// re-arms the timer when the sandbox is in use, or when the suspend failed.
func (c *Chat) idleCheck() {
	c.vmMu.Lock()
	defer c.vmMu.Unlock()
	if c.vm.sb == nil || c.m.isClosed() {
		return
	}
	limit := c.m.o.IdleSuspend
	if !c.idleLocked() {
		c.arm(limit)
		return
	}
	if idle := time.Since(time.Unix(0, c.lastUse.Load())); idle < limit {
		c.arm(limit - idle)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), checkpointLimit)
	defer cancel()
	if err := c.suspendIdleLocked(ctx); errors.Is(err, errNotIdle) {
		c.arm(limit)
	} else if err != nil {
		c.m.log.Warn("session: idle suspend failed; the sandbox stays live", "chat", c.id, "error", err)
		c.arm(limit)
	}
}
