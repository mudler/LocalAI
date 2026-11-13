package vz

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Resume boots a new guest over the files of a suspended sandbox. The
// processes of the earlier guest are gone: Suspend did not save the memory.
//
// A snapshot whose folder is gone (the sweep of a new process removed it, or
// Destroy did) gives an error that wraps sandbox.ErrGone.
func (b *Backend) Resume(ctx context.Context, snap sandbox.SnapshotRef, env sandbox.Env) (sandbox.Sandbox, error) {
	if b.isClosed() {
		return nil, errClosed()
	}
	if snap.Backend != snapshotBackend {
		return nil, fmt.Errorf("vz: %w: snapshot of backend %q", workspace.ErrInvalid, snap.Backend)
	}
	dir := filepath.Clean(snap.Path)
	if rel, err := filepath.Rel(b.opts.StateDir, dir); err != nil || rel == "." || strings.ContainsRune(rel, filepath.Separator) || strings.HasPrefix(rel, "..") {
		return nil, fmt.Errorf("vz: %w: snapshot %q is not a session folder of this backend", workspace.ErrInvalid, snap.Path)
	}
	// The folder lock is held for the whole resume. Two resumes of one ref
	// then run one after the other, and the second sees the folder running.
	unlock, err := lockFile(ctx, filepath.Join(dir, "lock"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("vz: %w: the session folder is gone", sandbox.ErrGone)
		}
		return nil, err
	}
	defer unlock()
	st, err := loadState(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("vz: %w: the session folder is gone", sandbox.ErrGone)
		}
		return nil, err
	}
	switch {
	case !st.Suspended:
		return nil, fmt.Errorf("vz: %w: sandbox %s is not suspended", workspace.ErrInvalid, filepath.Base(dir))
	case env.Store == nil:
		return nil, fmt.Errorf("vz: %w: no store", workspace.ErrInvalid)
	case env.Actor.Principal != st.Actor.Principal || env.Actor.ChatID != st.Actor.ChatID:
		return nil, fmt.Errorf("vz: %w: resume needs the actor of the sandbox", workspace.ErrInvalid)
	case !samePorts(st.Ports, env.Ports):
		return nil, fmt.Errorf("vz: %w: resume needs handlers for ports %v", workspace.ErrInvalid, st.Ports)
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
	// other crash recovery. The .old files (lower.old, upper.old.ext4) stay:
	// after such a crash they may be the only copy of the previous layers.
	// Refresh removes them when it starts.
	for _, f := range []string{"lower.new", "upper.new.ext4"} {
		if err := os.RemoveAll(filepath.Join(dir, f)); err != nil {
			return nil, err
		}
	}
	if err := s.boot(ctx); err != nil {
		return nil, err
	}
	s.st.Suspended = false
	s.st.Epoch++
	s.epoch = s.st.Epoch
	if err := s.st.save(dir); err != nil {
		s.vm.stop()
		return nil, err
	}
	return s, nil
}
