//go:build linux

package gvisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// testHooks are seams for the tests. A nil field means the real behaviour.
type testHooks struct {
	afterRead   func() // the sandbox is paused and the upper layer is read
	afterCommit func()
	suspendSave func() error // a non-nil error fails the save of the suspended state
	bootFail    func() error
	renameFail  func(from, to string) error // a non-nil error fails that rename
}

// bootBudget bounds the start of a sandbox after a refresh, which runs
// without the cancellation of the caller: a cancel there would leave the
// sandbox gone for good.
const bootBudget = 2 * time.Minute

// pauseBudget is how long a checkpoint may keep the sandbox paused.
const pauseBudget = 45 * time.Second

// errPauseOverrun means a checkpoint read the upper layer for longer than
// pauseBudget. Nothing was committed and the sandbox runs again.
var errPauseOverrun = errors.New("gvisor: the sandbox was paused for too long")

// Checkpoint commits the changes of the guest to the workspace. It returns
// ErrBusy when another checkpoint or refresh runs.
func (s *sbx) Checkpoint(ctx context.Context, title string) (workspace.ChangeSet, []workspace.Conflict, error) {
	if !s.op.TryLock() {
		return workspace.ChangeSet{}, nil, sandbox.ErrBusy
	}
	defer s.op.Unlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, err := os.Stat(filepath.Join(s.dir, "upper")); err != nil {
		return workspace.ChangeSet{}, nil, sandbox.ErrGone
	}
	unlock, err := s.lockOwned(ctx)
	if err != nil {
		return workspace.ChangeSet{}, nil, err
	}
	defer unlock()
	return s.checkpoint(ctx, title, false)
}

// checkpoint pauses the sandbox, reads the upper layer, resumes it and
// commits. With keepPaused it leaves the sandbox paused when the commit and
// the state save worked; the caller then stops it. The sandbox runs again on
// every other path, with a context of its own, so a cancelled ctx cannot
// leave it paused. A resume error is joined into the returned error, never
// dropped. The caller holds op, mu and the folder lock (lockOwned).
func (s *sbx) checkpoint(ctx context.Context, title string, keepPaused bool) (cs workspace.ChangeSet, conflicts []workspace.Conflict, err error) {
	in := s.in
	paused := false
	resume := func() error {
		if !paused {
			return nil
		}
		// One try only: the flag is cleared first, so the deferred call below
		// does not repeat a try that the main path made.
		paused = false
		if in == nil || !pidAlive(in.pid) {
			return nil // died while paused: nothing to resume; later calls return ErrGone
		}
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if _, err := s.b.call(c, s.runscRoot(), "resume", s.id); err != nil {
			if !pidAlive(in.pid) {
				return nil // it died during the resume
			}
			return fmt.Errorf("gvisor: resume: %w", err)
		}
		return nil
	}
	stayPaused := false
	// The deferred resume covers the paths that return before the main resume.
	defer func() {
		if stayPaused && err == nil {
			return
		}
		err = errors.Join(err, resume())
	}()
	stage := filepath.Join(s.dir, "stage")
	_ = removeAll(stage)
	if err = os.Mkdir(stage, 0o700); err != nil {
		return workspace.ChangeSet{}, nil, err
	}
	defer removeAll(stage)

	readCtx, cancel := context.WithTimeout(ctx, pauseBudget)
	defer cancel()
	if in != nil && !in.exited() {
		// A pause that fails may still have paused the sandbox (a lost reply),
		// so the resume is sent after any failure.
		paused = true
		if _, perr := s.b.call(readCtx, s.runscRoot(), "pause", s.id); perr != nil {
			if !in.exited() {
				return workspace.ChangeSet{}, nil, fmt.Errorf("gvisor: pause: %w", perr)
			}
			paused = false // the sandbox is gone: nothing to resume
		}
	}
	stageLimit := s.b.opts.StageLimit
	if stageLimit == 0 {
		// The disk limit is watched, not enforced: the upper layer can pass it
		// before the watcher reacts, and Refresh, which is how a full disk is
		// cleared, must still be able to commit that work first.
		stageLimit = 2 * s.st.Disk
	}
	src, next, err := readUpper(readCtx, filepath.Join(s.dir, "upper", "data"), stage, s.st.Index, readLimits{stage: stageLimit})
	if err != nil {
		if readCtx.Err() != nil && ctx.Err() == nil {
			err = fmt.Errorf("%w: reading the upper layer took more than %s, nothing was committed; retry: %v", errPauseOverrun, pauseBudget, err)
		}
		return workspace.ChangeSet{}, nil, err
	}
	if s.hooks.afterRead != nil {
		s.hooks.afterRead()
	}
	// A resume error does not stop the commit: the read is complete and the
	// commit is good. It is returned with the result.
	var resumeErr error
	if !keepPaused {
		resumeErr = resume()
	}
	cs, conflicts, err = s.env.Store.CommitUpper(ctx, s.st.View, s.env.Actor, s.st.Base, src,
		workspace.UpperOptions{Title: title, Own: s.st.Own})
	if err != nil && !errors.Is(err, workspace.ErrCommitted) {
		return workspace.ChangeSet{}, nil, errors.Join(err, resumeErr)
	}
	err = nil
	if s.hooks.afterCommit != nil {
		s.hooks.afterCommit()
	}
	if cs.Rev != 0 {
		s.st.Own = append(s.st.Own, cs.Rev)
	}
	s.st.Index = next
	if serr := s.st.save(s.dir); serr != nil {
		return cs, conflicts, errors.Join(resumeErr,
			fmt.Errorf("gvisor: committed revision %d, but saving the state failed: %w", cs.Rev, serr))
	}
	s.measureDisk(in, s.st.Disk)
	stayPaused = keepPaused
	return cs, conflicts, resumeErr
}

// Refresh commits the changes, then replaces the lower layer with the
// current content of the view and starts a fresh upper layer. The sandbox is
// stopped while paused, so nothing writes between the commit and the stop.
// Processes do not survive. A refresh also clears a full disk.
//
// Failures before the swap keep the old lower and upper and start them
// again. Failures after it put the old folders back and mark the sandbox
// gone; the returned error then wraps sandbox.ErrGone.
func (s *sbx) Refresh(ctx context.Context) error {
	if !s.op.TryLock() {
		return sandbox.ErrBusy
	}
	defer s.op.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gone {
		return sandbox.ErrGone
	}
	unlock, err := s.lockOwned(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	lower, fresh, lowerOld := filepath.Join(s.dir, "lower"), filepath.Join(s.dir, "lower.new"), filepath.Join(s.dir, "lower.old")
	upper, upperNew, upperOld := filepath.Join(s.dir, "upper"), filepath.Join(s.dir, "upper.new"), filepath.Join(s.dir, "upper.old")
	discard := func() {
		_ = removeAll(fresh)
		_ = removeAll(upperNew)
	}
	// Leftovers of an earlier refresh, whose removal failed, would block the
	// renames below.
	if err := errors.Join(removeAll(lowerOld), removeAll(upperOld)); err != nil {
		return fmt.Errorf("gvisor: refresh: removing the folders of an earlier refresh: %w", err)
	}
	if _, _, err := s.checkpoint(ctx, "Checkpoint before refresh", true); err != nil {
		return err
	}
	old := s.in
	s.stopInst(old)
	s.in = nil
	s.conns.closeAll()
	discard()
	// Build the new lower and upper beside the live ones.
	base, err := s.env.Store.Materialize(ctx, s.st.View, fresh)
	if err == nil {
		err = os.Mkdir(upperNew, 0o700)
	}
	if err != nil {
		discard()
		return errors.Join(fmt.Errorf("gvisor: refresh: %w", err), s.revive(ctx))
	}

	// The point of no return: the old folders are renamed, not removed, until
	// the new sandbox is ready.
	moves := [][2]string{{lower, lowerOld}, {fresh, lower}, {upper, upperOld}, {upperNew, upper}}
	if renameErr, undoErr := s.renameAll(moves); renameErr != nil {
		if undoErr != nil {
			// The folders are in a mixed state (an old upper may sit next to a
			// new lower). Starting that pair could commit wrong deletes, so
			// the sandbox stops here, and nothing is removed.
			s.gone = true
			return errors.Join(sandbox.ErrGone, fmt.Errorf("gvisor: refresh: %w", renameErr), undoErr)
		}
		discard()
		return errors.Join(fmt.Errorf("gvisor: refresh: %w", renameErr), s.revive(ctx))
	}
	oldSt := s.st
	s.st.Base, s.st.Own, s.st.Index = base, nil, sandbox.UpperIndex{}
	if err := s.st.save(s.dir); err != nil {
		return s.abandon(moves, oldSt, fmt.Errorf("gvisor: refresh: saving the state: %w", err))
	}
	bootCtx, cancelBoot := context.WithTimeout(context.WithoutCancel(ctx), bootBudget)
	defer cancelBoot()
	if err := s.boot(bootCtx); err != nil {
		return s.abandon(moves, oldSt, fmt.Errorf("gvisor: refresh: the new sandbox did not start: %w", err))
	}
	s.diskFull.Store(false)
	if err := errors.Join(removeAll(lowerOld), removeAll(upperOld)); err != nil {
		return fmt.Errorf("gvisor: refresh: the new sandbox runs, but the old folders stay: %w", err)
	}
	return nil
}

// revive starts the sandbox again from the folders on disk. A start that
// fails marks the sandbox gone.
func (s *sbx) revive(ctx context.Context) error {
	bootCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), bootBudget)
	defer cancel()
	if err := s.boot(bootCtx); err != nil {
		s.gone = true
		return errors.Join(sandbox.ErrGone, fmt.Errorf("gvisor: refresh: the old sandbox did not start: %w", err))
	}
	return nil
}

// abandon puts the old folders and state back after a failure past the swap,
// and marks the sandbox gone. The caller holds mu.
func (s *sbx) abandon(moves [][2]string, old state, cause error) error {
	s.st = old
	saveErr := s.st.save(s.dir)
	restoreErr := s.undoMoves(moves)
	// The new lower and upper are not used any more.
	_ = removeAll(moves[1][0])
	_ = removeAll(moves[3][0])
	s.gone = true
	return errors.Join(sandbox.ErrGone, cause, saveErr, restoreErr)
}

// rename is os.Rename, through the test hook when set.
func (s *sbx) rename(from, to string) error {
	if s.hooks.renameFail != nil {
		if err := s.hooks.renameFail(from, to); err != nil {
			return err
		}
	}
	return os.Rename(from, to)
}

// renameAll renames each from to to, in order. On a rename failure it undoes
// the renames it made. It returns the rename error and, separately, the undo
// error: an undo error means the folders may be in a mixed state.
func (s *sbx) renameAll(moves [][2]string) (renameErr, undoErr error) {
	for i, m := range moves {
		if err := s.rename(m[0], m[1]); err != nil {
			return err, s.undoMoves(moves[:i])
		}
	}
	return nil, nil
}

// undoMoves reverses renameAll on moves that were made.
func (s *sbx) undoMoves(moves [][2]string) error {
	var errs []error
	for j := len(moves) - 1; j >= 0; j-- {
		if err := s.rename(moves[j][1], moves[j][0]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
