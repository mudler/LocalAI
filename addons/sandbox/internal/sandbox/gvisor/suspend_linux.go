//go:build linux

package gvisor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/guestagent"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// agentCallBudget bounds one request to the guest agent while Suspend holds
// mu: an agent that does not answer must not block Destroy and Exec until the
// deadline of the caller.
var agentCallBudget = 30 * time.Second

// call sends one request without fields to the guest agent.
func (s *sbx) call(ctx context.Context, typ string) error {
	conn, err := net.Dial("unix", s.agentSock())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, agentCallBudget)
	defer cancel()
	return guestagent.Call(ctx, conn, typ)
}

// Suspend commits the changes and saves the whole sandbox, processes and open
// files included, with "runsc checkpoint". The sandbox stops. A process
// handle from before returns ErrGone.
//
// When Suspend fails after it detached the processes, the sandbox keeps
// running (or is started again from the checkpoint), but the Exec handles
// from before are gone: their connections are closed. The partial checkpoint
// folder is removed.
func (s *sbx) Suspend(ctx context.Context) (sandbox.SnapshotRef, error) {
	if !s.op.TryLock() {
		return sandbox.SnapshotRef{}, sandbox.ErrBusy
	}
	defer s.op.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	in := s.in
	if s.gone || in == nil || in.exited() {
		return sandbox.SnapshotRef{}, sandbox.ErrGone
	}
	unlock, err := s.lockOwned(ctx)
	if err != nil {
		return sandbox.SnapshotRef{}, err
	}
	defer unlock()
	if _, _, err := s.checkpoint(ctx, "Checkpoint before suspend", false); err != nil {
		return sandbox.SnapshotRef{}, err
	}
	snap := filepath.Join(s.dir, "checkpoint")
	if err := removeAll(snap); err != nil {
		return sandbox.SnapshotRef{}, err
	}
	if err := os.Mkdir(snap, 0o700); err != nil {
		return sandbox.SnapshotRef{}, err
	}
	// Processes keep running when their connection breaks. The agent closes its
	// listener, and the host closes its own connections: runsc cannot save a
	// sandbox that has a bound host socket or an open one to the host.
	if err := s.call(ctx, guestagent.TypeDetach); err != nil {
		_ = removeAll(snap)
		return sandbox.SnapshotRef{}, fmt.Errorf("gvisor: detach: %w", err)
	}
	if err := s.call(ctx, guestagent.TypeCheckpoint); err != nil {
		_ = removeAll(snap)
		return sandbox.SnapshotRef{}, s.listenAgain(fmt.Errorf("gvisor: checkpoint request: %w", err))
	}
	// Gate the port listeners first, so that no new forwarded connection can
	// open between the close of the old ones and "runsc checkpoint".
	s.ports.gate()
	s.conns.closeAll()
	s.ports.closeConns()
	// "runsc checkpoint" writes the memory of the sandbox, which takes seconds
	// for tens of MB. It gets the pause budget of the checkpoint read.
	cpCtx, cancelCP := context.WithTimeout(ctx, pauseBudget)
	defer cancelCP()
	if _, err := s.b.call(cpCtx, s.runscRoot(), "checkpoint", "--image-path="+snap, s.id); err != nil {
		_ = removeAll(snap)
		return sandbox.SnapshotRef{}, s.listenAgain(err)
	}
	// The sandbox is stopped now. stopInst deletes the runsc state and ends
	// the watchers of this instance; it runs before anything can start a sandbox
	// with the same id, because the delete would remove that one.
	s.stopInst(in)
	// The state is saved while the checkpoint is the only copy. A save that
	// fails starts the sandbox again, so no checkpoint is left behind that the
	// sandbox does not own.
	s.st.Suspended = true
	if err := s.saveSuspended(); err != nil {
		s.st.Suspended = false
		rerr := s.restart(ctx, snap)
		if rerr == nil {
			_ = removeAll(snap) // the sandbox runs again; the checkpoint is not needed
		}
		return sandbox.SnapshotRef{}, errors.Join(err, rerr)
	}
	s.ports.close()
	s.in, s.gone, s.released = nil, true, true
	return sandbox.SnapshotRef{Backend: "gvisor", Path: s.dir}, nil
}

// listenAgain is the undo of a failed suspend: it asks the agent to listen
// again. When the sandbox has stopped, it is gone. The result wraps cause.
func (s *sbx) listenAgain(cause error) error {
	s.ports.ungate()
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if !pidAlive(s.in.pid) {
		s.gone = true
		return errors.Join(sandbox.ErrGone, cause)
	}
	if _, err := s.b.call(c, s.runscRoot(), "kill", s.id, "USR1"); err != nil {
		s.gone = true
		return errors.Join(sandbox.ErrGone, cause, err)
	}
	return cause
}

// restart restores the sandbox from the checkpoint in snap, after a failed
// Suspend. The old instance is already stopped (Suspend did it right after
// "runsc checkpoint", so its watchers cannot touch the new sandbox, which has
// the same id). A restore that fails marks the sandbox gone. The caller holds mu.
func (s *sbx) restart(ctx context.Context, snap string) error {
	ctx = context.WithoutCancel(ctx)
	next, err := s.restore(ctx, snap)
	if err != nil {
		s.gone = true
		return errors.Join(sandbox.ErrGone, err)
	}
	s.in = next
	s.startWatchers(next)
	s.ports.ungate()
	return nil
}

// restore starts the sandbox from the checkpoint in snap, tells the agent to
// listen again and waits until it answers. On an error the new sandbox is
// stopped.
func (s *sbx) restore(ctx context.Context, snap string) (*inst, error) {
	_ = os.Remove(s.agentSock())
	_ = s.b.deleteForce(ctx, s.runscRoot(), s.id)
	_ = os.Rename(filepath.Join(s.dir, "console.log"), filepath.Join(s.dir, "console.prev.log"))
	pid, err := s.b.start(ctx, s.dir, s.id, s.st.CPUs, "restore", "--image-path="+snap)
	if err != nil {
		// A restore that fails may leave a container in runsc's state.
		_ = s.b.deleteForce(context.WithoutCancel(ctx), s.runscRoot(), s.id)
		return nil, err
	}
	in := newInst(pid)
	// The agent closed its listener for the checkpoint: SIGUSR1 opens it again.
	if _, err := s.b.call(ctx, s.runscRoot(), "kill", s.id, "USR1"); err != nil {
		s.stopInst(in)
		return nil, err
	}
	if err := s.waitReady(ctx, in); err != nil {
		s.stopInst(in)
		return nil, fmt.Errorf("gvisor: restored sandbox does not answer: %w\nconsole: %s", err, tail(filepath.Join(s.dir, "console.log"), 2048))
	}
	return in, nil // the caller sets s.in, then starts the watchers
}

// saveSuspended writes state.json with Suspended set, through the test hook
// when set.
func (s *sbx) saveSuspended() error {
	if s.hooks.suspendSave != nil {
		if err := s.hooks.suspendSave(); err != nil {
			return err
		}
	}
	return s.st.save(s.dir)
}

// Resume restores a suspended sandbox from its session folder.
func (b *Backend) Resume(ctx context.Context, snap sandbox.SnapshotRef, env sandbox.Env) (sandbox.Sandbox, error) {
	if snap.Backend != "gvisor" {
		return nil, fmt.Errorf("gvisor: %w: snapshot of backend %q", workspace.ErrInvalid, snap.Backend)
	}
	dir := filepath.Clean(snap.Path)
	if rel, err := filepath.Rel(b.opts.StateDir, dir); err != nil || rel == "." || strings.ContainsRune(rel, filepath.Separator) || strings.HasPrefix(rel, "..") {
		return nil, fmt.Errorf("gvisor: %w: snapshot %q is not a session folder of this backend", workspace.ErrInvalid, snap.Path)
	}
	// The folder lock is held for the whole resume. Two resumes of one ref
	// then run one after the other, and the second sees the folder running.
	unlock, err := lockFile(ctx, filepath.Join(dir, "lock"))
	if err != nil {
		return nil, err
	}
	defer unlock()
	st, err := loadState(dir)
	if err != nil {
		return nil, err
	}
	switch {
	case !st.Suspended:
		return nil, fmt.Errorf("gvisor: %w: sandbox %s is not suspended", workspace.ErrInvalid, filepath.Base(dir))
	case env.Store == nil:
		return nil, fmt.Errorf("gvisor: %w: no store", workspace.ErrInvalid)
	case env.Actor.Principal != st.Actor.Principal || env.Actor.ChatID != st.Actor.ChatID:
		return nil, fmt.Errorf("gvisor: %w: resume needs the actor of the sandbox", workspace.ErrInvalid)
	case !samePorts(st.Ports, env.Ports):
		return nil, fmt.Errorf("gvisor: %w: resume needs handlers for ports %v", workspace.ErrInvalid, st.Ports)
	}
	if err := sandbox.CheckPorts(env.Ports); err != nil {
		return nil, err
	}
	img, err := b.image(ctx, st.Image)
	if err != nil {
		return nil, err
	}
	s := &sbx{b: b, id: filepath.Base(dir), dir: dir, img: img, env: env, st: st, epoch: st.Epoch}
	// A crash in the middle of a refresh can leave the new lower and upper
	// beside the live ones. They are never live, so they go. Resume does no
	// other crash recovery. The .old folders stay: after such a crash they may
	// be the only copy of the previous layers. Refresh removes them when it
	// starts.
	for _, f := range []string{"lower.new", "upper.new", "stage"} {
		if err := removeAll(filepath.Join(dir, f)); err != nil {
			return nil, err
		}
	}
	if s.ports, err = servePorts(dir, env.Ports); err != nil {
		return nil, err
	}
	if err := s.writeBundle(); err != nil {
		s.ports.close()
		return nil, err
	}
	in, err := s.restore(ctx, filepath.Join(dir, "checkpoint"))
	if err != nil {
		s.ports.close()
		return nil, err // the checkpoint stays for a retry
	}
	s.st.Suspended = false
	s.st.Epoch++
	s.epoch = s.st.Epoch
	if err := s.st.save(dir); err != nil {
		s.stopInst(in)
		s.ports.close()
		return nil, err
	}
	s.mu.Lock()
	s.in = in
	s.mu.Unlock()
	s.startWatchers(in)
	// The checkpoint was used. A second resume of it would run a sandbox whose
	// memory does not match the upper layer any more.
	_ = removeAll(filepath.Join(dir, "checkpoint"))
	s.measureDisk(in, s.st.Disk)
	return s, nil
}
