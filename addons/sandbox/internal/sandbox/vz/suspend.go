package vz

import (
	"context"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/guestagent"
)

// snapshotBackend is the Backend name of a snapshot reference of this backend.
const snapshotBackend = "vz"

// Suspend commits the changes of the guest and stops it. It does not save the
// memory of the guest: macOS refuses to save the state of a VM for a process
// without a graphical session, so the processes of the guest do not survive.
// Resume boots a new guest over the same files. Every process handle from
// before returns sandbox.ErrGone.
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
	// The guest stays frozen after the commit, so that nothing writes to the
	// upper image between the checkpoint and the stop of the VM.
	if _, _, err := s.checkpoint(ctx, "Checkpoint before suspend", true); err != nil {
		return sandbox.SnapshotRef{}, err
	}
	s.st.Suspended = true
	if err := s.saveSuspended(); err != nil {
		s.st.Suspended = false
		thaw := s.call(context.WithoutCancel(ctx), v, guestagent.TypeThaw)
		return sandbox.SnapshotRef{}, errors.Join(err, thaw)
	}
	s.shutdown(v)
	s.vm, s.gone, s.released = nil, true, true
	return sandbox.SnapshotRef{Backend: snapshotBackend, Path: s.dir}, nil
}

// saveSuspended writes state.json with Suspended set, through the test hook
// when set.
func (s *sbx) saveSuspended() error {
	if s.hooks.suspendSave != nil {
		if err := s.hooks.suspendSave(); err != nil {
			return fmt.Errorf("vz: %w", err)
		}
	}
	return s.st.save(s.dir)
}
