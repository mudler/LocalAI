//go:build linux || darwin

package sbtest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// NewFakeBackend returns a backend for tests of code that uses sandboxes. It
// runs commands on the host, in a copy of the lower tree under root, so a
// command sees the view's mounts as relative paths below its working
// directory (home/..., not /workspace/home/...). It has no terminal, no
// network isolation and no real suspend: a suspended sandbox stays in memory
// and its processes keep running. Every test of a backend that uses it needs
// sh on the host; the files that use it must build for linux or darwin.
func NewFakeBackend(root string) sandbox.Backend {
	return &fakeBackend{root: root, held: map[string]*fakeSandbox{}}
}

// fakeBackend is the backend that NewFakeBackend returns. The suite runs
// against it in this package's tests, so the suite compiles and runs without
// a VM.
type fakeBackend struct {
	root string
	mu   sync.Mutex
	held map[string]*fakeSandbox // suspended, by ID
}

func (b *fakeBackend) skips(feature string) bool { return feature == "tty" || feature == "network" }

func (b *fakeBackend) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	spec = spec.WithDefaults()
	if err := spec.Check(); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(b.root, "sb-")
	if err != nil {
		return nil, err
	}
	s := &fakeSandbox{b: b, id: filepath.Base(dir), dir: dir, env: spec.Env, view: spec.View}
	// #nosec G301 -- test fake of the host backend
	if err := os.MkdirAll(s.home(), 0o755); err != nil {
		return nil, err
	}
	if err := s.materialize(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (b *fakeBackend) Resume(ctx context.Context, snap sandbox.SnapshotRef, env sandbox.Env) (sandbox.Sandbox, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.held[snap.Path]
	if !ok || snap.Backend != "fake" {
		return nil, fmt.Errorf("fake: no snapshot %q", snap.Path)
	}
	delete(b.held, snap.Path)
	s.mu.Lock()
	s.env, s.gone = env, false
	s.mu.Unlock()
	return s, nil
}

type fakeSandbox struct {
	b    *fakeBackend
	id   string
	dir  string
	view workspace.View
	op   sync.Mutex // checkpoint, refresh, suspend

	mu    sync.Mutex
	env   sandbox.Env
	gone  bool
	procs []*fakeProc
	base  int64
	own   []int64
	index sandbox.UpperIndex
}

func (s *fakeSandbox) lower() string { return filepath.Join(s.dir, "lower") }
func (s *fakeSandbox) work() string  { return filepath.Join(s.dir, "work") }
func (s *fakeSandbox) home() string  { return filepath.Join(s.dir, "home") }

// materialize builds lower/ at the current revision and a writable copy in
// work/.
func (s *fakeSandbox) materialize(ctx context.Context) error {
	for _, d := range []string{s.lower(), s.work()} {
		if err := os.RemoveAll(d); err != nil {
			return err
		}
	}
	base, err := s.env.Store.Materialize(ctx, s.view, s.lower())
	if err != nil {
		return err
	}
	s.base, s.own, s.index = base, nil, sandbox.UpperIndex{}
	return filepath.WalkDir(s.lower(), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(s.lower(), p)
		dst := filepath.Join(s.work(), rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755) // #nosec G301 -- test fake of the host backend
		}
		b, err := os.ReadFile(p) // #nosec G122 G304 -- test fake of the host backend
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644) // #nosec G306 G703 -- test fake of the host backend
	})
}

func (s *fakeSandbox) ID() string { return s.id }

func (s *fakeSandbox) Exec(ctx context.Context, c sandbox.Cmd) (sandbox.Process, error) {
	s.mu.Lock()
	gone := s.gone
	s.mu.Unlock()
	if gone {
		return nil, sandbox.ErrGone
	}
	if c.TTY != nil {
		return nil, errors.New("fake: no terminal")
	}
	dir := c.Dir
	if dir == "" {
		dir = sandbox.DefaultDir
	}
	rel, ok := strings.CutPrefix(dir, sandbox.DefaultDir)
	if !ok {
		return nil, fmt.Errorf("fake: dir %q is outside %s", dir, sandbox.DefaultDir)
	}
	cmd := exec.Command(c.Args[0], c.Args[1:]...) // #nosec G204 -- test fake: it runs the command of the spec
	cmd.Dir = filepath.Join(s.work(), filepath.FromSlash(rel))
	cmd.Env = append(os.Environ(), "HOME="+s.home()) //nolint:forbidigo // the fake runs host commands with the host environment
	cmd.Env = append(cmd.Env, c.Env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c.Stdin, c.Stdout, c.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 200 * time.Millisecond
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &fakeProc{cmd: cmd, done: make(chan struct{}), gone: make(chan struct{})}
	s.mu.Lock()
	s.procs = append(s.procs, p)
	s.mu.Unlock()
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()
	go func() {
		select {
		case <-ctx.Done():
			p.ctxErr.Store(ctx.Err())
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		case <-p.done:
		}
	}()
	return p, nil
}

type fakeProc struct {
	cmd      *exec.Cmd
	done     chan struct{}
	err      error
	ctxErr   errBox
	gone     chan struct{} // closed by Suspend and Destroy
	goneOnce sync.Once
}

// markGone makes the handle return sandbox.ErrGone. The process itself keeps
// running after a Suspend, as a real suspend keeps it.
func (p *fakeProc) markGone() { p.goneOnce.Do(func() { close(p.gone) }) }

func (p *fakeProc) isGone() bool {
	select {
	case <-p.gone:
		return true
	default:
		return false
	}
}

type errBox struct {
	mu  sync.Mutex
	err error
}

func (b *errBox) Store(err error) { b.mu.Lock(); b.err = err; b.mu.Unlock() }
func (b *errBox) Load() error     { b.mu.Lock(); defer b.mu.Unlock(); return b.err }

func (p *fakeProc) Wait() (int, error) {
	select {
	case <-p.done:
	case <-p.gone:
	}
	if p.isGone() {
		return -1, sandbox.ErrGone
	}
	<-p.done
	if err := p.ctxErr.Load(); err != nil {
		return -1, err
	}
	ws := p.cmd.ProcessState.Sys().(syscall.WaitStatus)
	if ws.Signaled() {
		return 128 + int(ws.Signal()), nil
	}
	var ee *exec.ExitError
	if p.err != nil && !errors.As(p.err, &ee) && !errors.Is(p.err, exec.ErrWaitDelay) {
		return -1, p.err
	}
	return ws.ExitStatus(), nil
}

func (p *fakeProc) Signal(sig syscall.Signal) error {
	if p.isGone() {
		return sandbox.ErrGone
	}
	return syscall.Kill(-p.cmd.Process.Pid, sig)
}

func (p *fakeProc) Resize(rows, cols uint16) error {
	if p.isGone() {
		return sandbox.ErrGone
	}
	return errors.New("fake: no terminal")
}

// scan lists the work tree as an upper layer: every folder, every file that
// differs from the lower tree or was committed before, and a whiteout for
// every lower entry that is gone.
func (s *fakeSandbox) scan() ([]sandbox.IndexedEntry, error) {
	var out []sandbox.IndexedEntry
	err := filepath.WalkDir(s.work(), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(s.work(), p)
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case info.IsDir():
			out = append(out, sandbox.IndexedEntry{UpperEntry: workspace.FixtureDir(rel)})
		case info.Mode().IsRegular():
			if _, known := s.index[rel]; !known && sameContent(filepath.Join(s.lower(), rel), p) {
				return nil
			}
			st := info.Sys().(*syscall.Stat_t)
			path := p
			out = append(out, sandbox.IndexedEntry{
				UpperEntry: workspace.UpperEntry{Path: rel, Kind: workspace.EntryFile, Mode: info.Mode(), Open: func() (io.ReadCloser, error) { return os.Open(path) }}, // #nosec G304 -- test fake of the host backend
				Key:        fmt.Sprintf("%d/%d/%d/%d", st.Ino, info.Size(), statCtimeNano(st), info.ModTime().UnixNano()),
			})
		default:
			out = append(out, sandbox.IndexedEntry{UpperEntry: workspace.UpperEntry{Path: rel, Kind: workspace.EntryOther, Mode: info.Mode()}})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = filepath.WalkDir(s.lower(), func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(s.lower(), p)
		if rel == "." {
			return nil
		}
		if _, err := os.Lstat(filepath.Join(s.work(), rel)); errors.Is(err, fs.ErrNotExist) {
			out = append(out, sandbox.IndexedEntry{UpperEntry: workspace.FixtureWhiteout(filepath.ToSlash(rel))})
			if d.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	return out, err
}

func sameContent(a, b string) bool {
	x, err := os.ReadFile(a) // #nosec G304 -- test fake of the host backend
	if err != nil {
		return false
	}
	y, err := os.ReadFile(b) // #nosec G304 -- test fake of the host backend
	return err == nil && bytes.Equal(x, y)
}

func (s *fakeSandbox) checkpoint(ctx context.Context, title string) (workspace.ChangeSet, []workspace.Conflict, error) {
	entries, err := s.scan()
	if err != nil {
		return workspace.ChangeSet{}, nil, err
	}
	s.mu.Lock()
	env, base, own, index := s.env, s.base, s.own, s.index
	s.mu.Unlock()
	send, next := index.Plan(entries)
	cs, conflicts, err := env.Store.CommitUpper(ctx, s.view, env.Actor, base, workspace.FixtureSource(send), workspace.UpperOptions{Title: title, Own: own})
	if err != nil && !errors.Is(err, workspace.ErrCommitted) {
		return workspace.ChangeSet{}, nil, err
	}
	s.mu.Lock()
	if cs.Rev != 0 {
		s.own = append(s.own, cs.Rev)
	}
	s.index = next
	s.mu.Unlock()
	return cs, conflicts, nil
}

func (s *fakeSandbox) Checkpoint(ctx context.Context, title string) (workspace.ChangeSet, []workspace.Conflict, error) {
	if !s.op.TryLock() {
		return workspace.ChangeSet{}, nil, sandbox.ErrBusy
	}
	defer s.op.Unlock()
	return s.checkpoint(ctx, title)
}

func (s *fakeSandbox) Refresh(ctx context.Context) error {
	if !s.op.TryLock() {
		return sandbox.ErrBusy
	}
	defer s.op.Unlock()
	if _, _, err := s.checkpoint(ctx, "Refresh"); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.materialize(ctx)
}

func (s *fakeSandbox) Suspend(ctx context.Context) (sandbox.SnapshotRef, error) {
	if !s.op.TryLock() {
		return sandbox.SnapshotRef{}, sandbox.ErrBusy
	}
	defer s.op.Unlock()
	if _, _, err := s.checkpoint(ctx, "Suspend"); err != nil {
		return sandbox.SnapshotRef{}, err
	}
	s.mu.Lock()
	s.gone = true
	for _, p := range s.procs {
		p.markGone()
	}
	s.mu.Unlock()
	s.b.mu.Lock()
	s.b.held[s.id] = s
	s.b.mu.Unlock()
	return sandbox.SnapshotRef{Backend: "fake", Path: s.id}, nil
}

func (s *fakeSandbox) Destroy(ctx context.Context) error {
	s.mu.Lock()
	s.gone = true
	procs := s.procs
	s.procs = nil
	s.mu.Unlock()
	for _, p := range procs {
		p.markGone()
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	}
	return os.RemoveAll(s.dir)
}
