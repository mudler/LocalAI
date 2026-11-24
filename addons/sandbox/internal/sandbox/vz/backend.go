package vz

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
)

var _ sandbox.Backend = (*Backend)(nil)

// lockName is the lock file in StateDir.
const lockName = ".lock"

// newBackend checks the options, takes the lock on StateDir and sweeps the
// folders that an earlier process left. mk makes the machines. New (one file
// per platform) calls it with the real machine.
func newBackend(o Options, mk machineFactory) (*Backend, error) {
	if o.StateDir == "" || o.CacheDir == "" {
		return nil, errors.New("vz: StateDir and CacheDir are required")
	}
	if o.ReadyTimeout == 0 {
		o.ReadyTimeout = 10 * time.Second
	}
	if o.StageLimit < 0 {
		return nil, errors.New("vz: StageLimit is negative")
	}
	if o.DefaultImage == "" {
		o.DefaultImage = DefaultImage
	}
	for _, d := range []*string{&o.StateDir, &o.CacheDir} {
		abs, err := filepath.Abs(*d)
		if err != nil {
			return nil, err
		}
		*d = abs
		if err := os.MkdirAll(abs, 0o700); err != nil {
			return nil, err
		}
	}
	b := &Backend{opts: o, mk: mk, live: map[*vmHandle]struct{}{}}
	var err error
	if b.lock, err = lockStateDir(o.StateDir); err != nil {
		return nil, err
	}
	if err := b.sweep(); err != nil {
		_ = b.Close()
		return nil, err
	}
	return b, nil
}

// lockStateDir takes the exclusive lock on StateDir without waiting. The lock
// belongs to the open file, so it ends when the file is closed or the process
// dies.
func lockStateDir(dir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, lockName), os.O_RDWR|os.O_CREATE, 0o600) // #nosec G304 -- a path the add-on builds under its own data or state folder
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("vz: StateDir %q is in use by another backend: use one StateDir per backend, or close the other one first", dir)
		}
		return nil, err
	}
	return f, nil
}

// sweep removes the session folders that an earlier process left in
// StateDir. A VM stops with the process that started it, so no folder has a
// running guest any more. A folder whose state.json says it is suspended is
// kept when Options.KeepSuspended is set.
func (b *Backend) sweep() error {
	ents, err := os.ReadDir(b.opts.StateDir)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if !e.IsDir() || !validID(e.Name()) {
			continue
		}
		dir := filepath.Join(b.opts.StateDir, e.Name())
		if st, err := loadState(dir); err == nil && st.Suspended && b.opts.KeepSuspended {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			log.Printf("vz: cannot remove the old folder %s: %v", e.Name(), err)
		}
	}
	return nil
}

// validID reports whether name looks like the id of a session folder, so that
// the sweep never touches another folder.
func validID(name string) bool {
	if len(name) != 12 {
		return false
	}
	_, err := hex.DecodeString(name)
	return err == nil
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// errClosed is the error of a backend after Close.
func errClosed() error {
	return fmt.Errorf("vz: %w: the backend is closed", sandbox.ErrUnsupported)
}

// isClosed reports whether Close ran.
func (b *Backend) isClosed() bool {
	b.liveMu.Lock()
	defer b.liveMu.Unlock()
	return b.closed
}

// track records a running VM, so that Close can stop it. It refuses a VM
// after Close: a boot that was already under way when Close ran would
// otherwise start a VM that nothing stops.
func (b *Backend) track(v *vmHandle) error {
	b.liveMu.Lock()
	defer b.liveMu.Unlock()
	if b.closed {
		return errClosed()
	}
	b.live[v] = struct{}{}
	return nil
}

func (b *Backend) untrack(v *vmHandle) {
	b.liveMu.Lock()
	defer b.liveMu.Unlock()
	delete(b.live, v)
}

// stopAll stops every VM that this backend started.
func (b *Backend) stopAll() {
	b.liveMu.Lock()
	vs := make([]*vmHandle, 0, len(b.live))
	for v := range b.live {
		vs = append(vs, v)
	}
	b.liveMu.Unlock()
	var wg sync.WaitGroup
	for _, v := range vs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v.stop()
		}()
	}
	wg.Wait()
}

// limits maps the CPUs and the memory of a spec to what the framework accepts
// on this Mac: at most the cores and the memory of the Mac, at least the
// framework's minimum, memory in whole MiB.
func (b *Backend) limits(cpus int, memory int64) (uint, uint64) {
	c, m := uint(max(cpus, 1)), uint64(max(memory, 0))
	m = (m + (1<<20 - 1)) &^ (1<<20 - 1)
	h := b.host
	if h.MaxCPUs > 0 {
		c = min(max(c, h.MinCPUs), h.MaxCPUs)
	}
	if h.MaxMemory > 0 {
		m = min(max(m, h.MinMemory), h.MaxMemory&^(1<<20-1))
	}
	return c, m
}

// Create materializes the view, starts a guest and waits for its agent.
func (b *Backend) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	if b.isClosed() {
		return nil, errClosed()
	}
	spec = spec.WithDefaults()
	if err := spec.Check(); err != nil {
		return nil, err
	}
	img, err := b.image(ctx, spec.Image)
	if err != nil {
		return nil, err
	}
	id := newID()
	dir := filepath.Join(b.opts.StateDir, id)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, err
	}
	s := &sbx{b: b, id: id, dir: dir, img: img, env: spec.Env}
	fail := func(err error) (sandbox.Sandbox, error) {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	base, err := spec.Store.Materialize(ctx, spec.View, filepath.Join(dir, "lower"))
	if err != nil {
		return fail(err)
	}
	if err := mkUpper(filepath.Join(dir, "upper.ext4"), spec.Disk); err != nil {
		return fail(err)
	}
	s.st = state{View: spec.View, Actor: spec.Actor, Base: base, Index: sandbox.UpperIndex{}, Image: img.Ref,
		CPUs: spec.CPUs, Memory: spec.Memory, Disk: spec.Disk, Ports: portList(spec.Ports)}
	if err := s.st.save(dir); err != nil {
		return fail(err)
	}
	if err := s.boot(ctx); err != nil {
		return fail(err)
	}
	return s, nil
}

// tail returns the last n bytes of a file.
func tail(path string, n int64) string {
	f, err := os.Open(path) // #nosec G304 -- a path the add-on builds under its own data or state folder
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	if st, err := f.Stat(); err == nil && st.Size() > n {
		_, _ = f.Seek(-n, io.SeekEnd)
	}
	b, _ := io.ReadAll(f)
	return strings.TrimSpace(string(b))
}
