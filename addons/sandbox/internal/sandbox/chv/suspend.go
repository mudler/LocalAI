//go:build linux

package chv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/guestagent"
)

func (s *sbx) Suspend(ctx context.Context) (sandbox.SnapshotRef, error) {
	if !s.op.TryLock() {
		return sandbox.SnapshotRef{}, sandbox.ErrBusy
	}
	defer s.op.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.vm
	if s.gone || v == nil || v.exited() {
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
	if err := s.call(ctx, v, guestagent.TypeDetach); err != nil {
		return sandbox.SnapshotRef{}, fmt.Errorf("chv: detach: %w", err)
	}
	snap := filepath.Join(s.dir, "snapshot")
	if err := os.RemoveAll(snap); err != nil {
		return sandbox.SnapshotRef{}, err
	}
	if err := os.Mkdir(snap, 0o700); err != nil {
		return sandbox.SnapshotRef{}, err
	}
	if err := v.api.put(ctx, "vm.pause", nil); err != nil {
		return sandbox.SnapshotRef{}, err
	}
	err = v.api.put(ctx, "vm.snapshot", snapshotConfig{DestinationURL: "file://" + snap})
	if err == nil {
		err = cloneFile(filepath.Join(s.dir, "upper.ext4"), filepath.Join(snap, "upper.ext4"))
	}
	if err != nil {
		_ = v.api.put(context.WithoutCancel(ctx), "vm.resume", nil)
		return sandbox.SnapshotRef{}, err
	}
	// The state is saved while the VM is still paused. A save that fails
	// resumes the VM, so no snapshot is left behind that the sandbox does
	// not own.
	s.st.Suspended = true
	if err := s.saveSuspended(); err != nil {
		s.st.Suspended = false
		resumeErr := v.api.put(context.WithoutCancel(ctx), "vm.resume", nil)
		return sandbox.SnapshotRef{}, errors.Join(err, resumeErr)
	}
	v.stop()
	s.ports.close()
	s.vm, s.gone, s.released = nil, true, true
	return sandbox.SnapshotRef{Backend: "chv", Path: s.dir}, nil
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
