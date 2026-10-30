//go:build linux

package chv

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/guestagent"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// testHooks are seams for the tests. A nil field means the real behaviour.
type testHooks struct {
	call        func(ctx context.Context, v *vm, typ string) error
	now         func() time.Time
	afterRead   func()
	afterCommit func()
	suspendSave func() error // a non-nil error fails the save of the suspended state
	bootFail    func() error
	renameFail  func(from, to string) error // a non-nil error fails that rename
}

// errFreezeOverrun means a checkpoint read the upper image for longer than
// the guest's own thaw allows, so the read may be torn. Nothing was committed.
var errFreezeOverrun = errors.New("chv: the guest was frozen for too long")

// now is the clock of the sandbox.
func (s *sbx) now() time.Time {
	if s.hooks.now != nil {
		return s.hooks.now()
	}
	return time.Now()
}

// freezeBudget is how long a freeze may last before a refresh gives up. The
// guest thaws itself after 60 s.
const freezeBudget = 45 * time.Second

// Checkpoint commits the changes of the guest to the workspace. It returns
// ErrBusy when another checkpoint or refresh runs.
func (s *sbx) Checkpoint(ctx context.Context, title string) (workspace.ChangeSet, []workspace.Conflict, error) {
	if !s.op.TryLock() {
		return workspace.ChangeSet{}, nil, sandbox.ErrBusy
	}
	defer s.op.Unlock()
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, err := os.Stat(filepath.Join(s.dir, "upper.ext4")); err != nil {
		return workspace.ChangeSet{}, nil, sandbox.ErrGone
	}
	unlock, err := s.lockOwned(ctx)
	if err != nil {
		return workspace.ChangeSet{}, nil, err
	}
	defer unlock()
	return s.checkpoint(ctx, title, false)
}

// checkpoint freezes the guest file system, reads the upper image, thaws and
// commits. With keepFrozen it commits before the thaw and leaves the guest
// frozen when the commit and the state save worked; the caller then stops
// the guest. The guest is thawed on every other path, with a context of its
// own, so a cancelled ctx cannot leave it frozen. A thaw error is joined into
// the returned error, never dropped. The caller holds op, mu and the folder lock (lockOwned).
func (s *sbx) checkpoint(ctx context.Context, title string, keepFrozen bool) (cs workspace.ChangeSet, conflicts []workspace.Conflict, err error) {
	v := s.vm
	frozen := false
	thaw := func() error {
		if !frozen {
			return nil
		}
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := s.call(c, v, guestagent.TypeThaw); err != nil {
			return fmt.Errorf("chv: thaw: %w", err)
		}
		frozen = false
		return nil
	}
	stayFrozen := false
	// The deferred thaw also retries a thaw that failed before the commit.
	// Thaw does nothing when the guest is not frozen.
	defer func() {
		if stayFrozen && err == nil {
			return
		}
		err = errors.Join(err, thaw())
	}()
	if v != nil && !v.exited() {
		// A freeze that fails may still have frozen the guest (a lost reply),
		// so the thaw is sent after any failure. A thaw of a guest that is not
		// frozen does nothing.
		frozen = true
		s.frozenAt = s.now()
		if ferr := s.call(ctx, v, guestagent.TypeFreeze); ferr != nil {
			if !v.exited() {
				return workspace.ChangeSet{}, nil, fmt.Errorf("chv: freeze: %w", ferr)
			}
			frozen = false // the guest is gone: nothing to thaw
		}
	}
	stage := filepath.Join(s.dir, "stage")
	_ = os.RemoveAll(stage)
	if err = os.Mkdir(stage, 0o700); err != nil {
		return workspace.ChangeSet{}, nil, err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	// The stage limit defaults to the size of the upper image, read here on
	// the host: debugfs may see the image under another path.
	limit := s.b.opts.StageLimit
	if limit == 0 {
		st, err := os.Stat(filepath.Join(s.dir, "upper.ext4"))
		if err != nil {
			return workspace.ChangeSet{}, nil, fmt.Errorf("chv: upper image size: %w", err)
		}
		limit = st.Size()
	}
	argv, inStage := s.b.debugfsArgv(s.dir)
	reader := upperReader{argv: argv, stage: inStage, host: stage, lim: readLimits{stage: limit}}
	src, next, err := reader.read(ctx, s.st.Index)
	if err != nil {
		return workspace.ChangeSet{}, nil, err
	}
	if s.hooks.afterRead != nil {
		s.hooks.afterRead()
	}
	// The guest thaws itself after 60 s, so a read that took longer may have
	// seen a changing image. Nothing is committed: the deferred thaw runs and
	// the caller retries.
	if frozen && s.now().Sub(s.frozenAt) > freezeBudget {
		return workspace.ChangeSet{}, nil, fmt.Errorf("%w: the upper image was read for more than %s, nothing was committed; retry", errFreezeOverrun, freezeBudget)
	}
	// A thaw error does not stop the commit: the read is complete and the
	// commit is good. It is returned with the result.
	var thawErr error
	if !keepFrozen {
		thawErr = thaw()
	}
	cs, conflicts, err = s.env.Store.CommitUpper(ctx, s.st.View, s.env.Actor, s.st.Base, src,
		workspace.UpperOptions{Title: title, Own: s.st.Own})
	if err != nil && !errors.Is(err, workspace.ErrCommitted) {
		return workspace.ChangeSet{}, nil, errors.Join(err, thawErr)
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
		return cs, conflicts, errors.Join(thawErr,
			fmt.Errorf("chv: committed revision %d, but saving the state failed: %w", cs.Rev, serr))
	}
	stayFrozen = keepFrozen
	return cs, conflicts, thawErr
}

// shutdown stops the guest: a shutdown request, then the API after 5 s.
func (s *sbx) shutdown(v *vm) {
	if v == nil {
		return
	}
	if !v.exited() {
		c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := s.call(c, v, guestagent.TypeShutdown); err != nil {
			log.Printf("chv: shutdown request failed, waiting for the VM: %v", err)
		}
		cancel()
		select {
		case <-v.done:
		case <-time.After(5 * time.Second):
			c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			if err := v.api.put(c, "vm.shutdown", nil); err != nil {
				log.Printf("chv: shutdown through the API failed, killing the VM: %v", err)
			}
			cancel()
		}
	}
	v.stop()
}

// Refresh commits the changes, then replaces the lower layer with the
// current content of the view and starts a fresh upper layer. The guest is
// stopped while frozen, which is safe: the freeze syncs the disks, nothing
// writes after it, the lower layer is read-only, and the committed changes
// are in the workspace. The guest skips its own sync when frozen (see
// cmd/sandbox-agent), so a frozen power-off does not block.
//
// Failures before the swap keep the old lower and upper and boot them again.
// Failures after it put the old files back and mark the sandbox gone; the
// returned error then wraps sandbox.ErrGone.
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
	upper, upperNew, upperOld := filepath.Join(s.dir, "upper.ext4"), filepath.Join(s.dir, "upper.new.ext4"), filepath.Join(s.dir, "upper.old.ext4")
	discard := func() {
		_ = os.RemoveAll(fresh)
		_ = os.RemoveAll(upperNew)
	}
	// Leftovers of an earlier refresh, whose removal failed, would block the
	// renames below.
	if err := errors.Join(os.RemoveAll(lowerOld), os.RemoveAll(upperOld)); err != nil {
		return fmt.Errorf("chv: refresh: removing the files of an earlier refresh: %w", err)
	}
	if _, _, err := s.checkpoint(ctx, "Checkpoint before refresh", true); err != nil {
		return err
	}
	// The guest thaws itself after 60 s of freeze. Past freezeBudget its
	// writes would land in the upper and be dropped by the shutdown, so the
	// refresh stops here: the guest is thawed, the checkpoint is committed,
	// and the sandbox keeps its lower and upper.
	if v := s.vm; v != nil && !v.exited() && s.now().Sub(s.frozenAt) > freezeBudget {
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		terr := s.call(c, v, guestagent.TypeThaw)
		cancel()
		err := fmt.Errorf("chv: refresh did not happen: the guest was frozen for more than %s and was thawed; the checkpoint is committed, retry the refresh", freezeBudget)
		if terr != nil {
			err = errors.Join(err, fmt.Errorf("chv: thaw: %w", terr))
		}
		return err
	}
	s.shutdown(s.vm)
	s.vm = nil
	discard()
	// Build the new lower and upper beside the live ones.
	base, err := s.env.Store.Materialize(ctx, s.st.View, fresh)
	if err == nil {
		err = s.b.mkUpper(ctx, upperNew, s.st.Disk)
	}
	if err != nil {
		discard()
		return errors.Join(fmt.Errorf("chv: refresh: %w", err), s.revive(ctx))
	}

	// The point of no return: the old files are renamed, not removed, until
	// the new guest is ready.
	moves := [][2]string{{lower, lowerOld}, {fresh, lower}, {upper, upperOld}, {upperNew, upper}}
	if renameErr, undoErr := s.renameAll(moves); renameErr != nil {
		if undoErr != nil {
			// The files are in a mixed state (an old upper may sit next to a
			// new lower). Reviving that pair could commit wrong deletes, so
			// the sandbox stops here, and nothing is removed.
			s.gone = true
			return errors.Join(sandbox.ErrGone, fmt.Errorf("chv: refresh: %w", renameErr), undoErr)
		}
		discard()
		return errors.Join(fmt.Errorf("chv: refresh: %w", renameErr), s.revive(ctx))
	}
	oldSt := s.st
	s.st.Base, s.st.Own, s.st.Index = base, nil, sandbox.UpperIndex{}
	if err := s.st.save(s.dir); err != nil {
		return s.abandon(moves, oldSt, fmt.Errorf("chv: refresh: saving the state: %w", err))
	}
	if err := s.boot(ctx); err != nil {
		return s.abandon(moves, oldSt, fmt.Errorf("chv: refresh: the new guest did not boot: %w", err))
	}
	if err := errors.Join(os.RemoveAll(lowerOld), os.Remove(upperOld)); err != nil {
		return fmt.Errorf("chv: refresh: the new guest runs, but the old files stay: %w", err)
	}
	return nil
}

// revive boots the sandbox again from the files on disk. A boot that fails
// marks the sandbox gone.
func (s *sbx) revive(ctx context.Context) error {
	if err := s.boot(context.WithoutCancel(ctx)); err != nil {
		s.gone = true
		return errors.Join(sandbox.ErrGone, fmt.Errorf("chv: refresh: the old guest did not boot: %w", err))
	}
	return nil
}

// abandon puts the old files and state back after a failure past the swap,
// and marks the sandbox gone. The caller holds mu.
func (s *sbx) abandon(moves [][2]string, old state, cause error) error {
	s.st = old
	saveErr := s.st.save(s.dir)
	restoreErr := s.undoMoves(moves)
	// The new lower and upper are not used any more.
	_ = os.RemoveAll(moves[1][0])
	_ = os.RemoveAll(moves[3][0])
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
// error: an undo error means the files may be in a mixed state.
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
	for _, move := range slices.Backward(moves) {
		if err := s.rename(move[1], move[0]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
