//go:build linux

package chv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/guestagent"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// Resume restores a suspended sandbox from its session folder.
func (b *Backend) Resume(ctx context.Context, snap sandbox.SnapshotRef, env sandbox.Env) (sandbox.Sandbox, error) {
	if snap.Backend != "chv" {
		return nil, fmt.Errorf("chv: %w: snapshot of backend %q", workspace.ErrInvalid, snap.Backend)
	}
	dir := filepath.Clean(snap.Path)
	if rel, err := filepath.Rel(b.opts.StateDir, dir); err != nil || rel == "." || strings.ContainsRune(rel, filepath.Separator) || strings.HasPrefix(rel, "..") {
		return nil, fmt.Errorf("chv: %w: snapshot %q is not a session folder of this backend", workspace.ErrInvalid, snap.Path)
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
		return nil, fmt.Errorf("chv: %w: sandbox %s is not suspended", workspace.ErrInvalid, filepath.Base(dir))
	case env.Store == nil:
		return nil, fmt.Errorf("chv: %w: no store", workspace.ErrInvalid)
	case env.Actor.Principal != st.Actor.Principal || env.Actor.ChatID != st.Actor.ChatID:
		return nil, fmt.Errorf("chv: %w: resume needs the actor of the sandbox", workspace.ErrInvalid)
	case !samePorts(st.Ports, env.Ports):
		return nil, fmt.Errorf("chv: %w: resume needs handlers for ports %v", workspace.ErrInvalid, st.Ports)
	}
	if err := sandbox.CheckPorts(env.Ports); err != nil {
		return nil, err
	}
	img, err := b.image(ctx, st.Image)
	if err != nil {
		return nil, err
	}
	s := &sbx{b: b, id: filepath.Base(dir), dir: dir, img: img, env: env, st: st, epoch: st.Epoch}
	snapDir := filepath.Join(dir, "snapshot")
	// Restore the disk as it was at the snapshot, so the snapshot can be
	// resumed again.
	if err := cloneFile(filepath.Join(snapDir, "upper.ext4"), filepath.Join(dir, "upper.ext4")); err != nil {
		return nil, err
	}
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
	if s.ports, err = servePorts(dir, env.Ports); err != nil {
		return nil, err
	}
	fail := func(v *vm, err error) (sandbox.Sandbox, error) {
		if v != nil {
			v.stop()
		}
		s.ports.close()
		return nil, err
	}
	for _, f := range []string{"chv.sock", "vsock.sock"} {
		_ = os.Remove(filepath.Join(dir, f))
	}
	// Restore reopens and truncates the console file: keep the old one. One
	// copy is kept; the next resume replaces it.
	_ = os.Rename(filepath.Join(dir, "console.log"), filepath.Join(dir, "console.prev.log"))
	vfsd, vfsdDone, err := b.startVirtiofsd(dir)
	if err != nil {
		return fail(nil, err)
	}
	v := &vm{dir: dir, api: newAPI(filepath.Join(dir, "chv.sock")), vfsd: vfsd, vfsdDone: vfsdDone}
	v.ch, v.done, err = start([]string{b.bins.CloudHypervisor, "--api-socket", "path=" + filepath.Join(dir, "chv.sock")}, filepath.Join(dir, "chv.log"))
	if err != nil {
		return fail(v, err)
	}
	if err := waitAPI(ctx, v); err != nil {
		return fail(v, err)
	}
	if err := v.api.put(ctx, "vm.restore", restoreConfig{SourceURL: "file://" + snapDir}); err != nil {
		return fail(v, err)
	}
	if err := v.api.put(ctx, "vm.resume", nil); err != nil {
		return fail(v, err)
	}
	c, err := dialGuestRetry(ctx, v.vsock(), guestagent.AgentPort, b.opts.ReadyTimeout)
	if err == nil {
		err = guestagent.Call(ctx, c, guestagent.TypeHealth)
	}
	if err != nil {
		return fail(v, fmt.Errorf("chv: resumed guest does not answer: %w", err))
	}
	s.vm = v
	s.st.Suspended = false
	s.st.Epoch++
	s.epoch = s.st.Epoch
	if err := s.st.save(dir); err != nil {
		return fail(v, err)
	}
	// The restored VM writes a new console log, so the panic watch starts
	// now and never reads a line of the old VM.
	s.watch(v)
	return s, nil
}

func waitAPI(ctx context.Context, v *vm) error {
	end := time.Now().Add(5 * time.Second)
	for {
		c, cancel := context.WithTimeout(ctx, time.Second)
		_, err := v.api.ping(c)
		cancel()
		if err == nil {
			return nil
		}
		if v.exited() {
			return errors.New("chv: cloud-hypervisor exited: " + tail(filepath.Join(v.dir, "chv.log"), 1024))
		}
		if time.Now().After(end) {
			return fmt.Errorf("chv: API not up: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
