//go:build linux || darwin

// Package host is the unsafe sandbox backend: it runs commands on the host,
// as the add-on's own user, in a copy of the files of the sandbox. Nothing
// isolates them from the rest of the host. It exists for hosts that can run
// neither gVisor nor KVM, and an administrator has to ask for it by name and
// confirm it; it is never chosen for them.
package host

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// geteuid is os.Geteuid. It is a variable so that a test can check the refusal
// of root.
var geteuid = os.Geteuid

// commandPath is the PATH of every command.
const commandPath = "/usr/local/bin:/usr/bin:/bin"

// lookPath finds a program in commandPath. A name with a slash is used as it is.
func lookPath(name string) (string, error) {
	if strings.Contains(name, "/") {
		return name, nil
	}
	for _, dir := range filepath.SplitList(commandPath) {
		p := filepath.Join(dir, name)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("host: %q not found in %s", name, commandPath)
}

// Options configures the backend.
type Options struct {
	// Root is the folder the sandboxes live in. It must be an absolute path.
	Root string
	// Log is called before every command with the principal the sandbox runs
	// for and the command line. It is the record of what ran on the host.
	Log func(principal, command string)
}

// Backend runs commands on the host.
type Backend struct {
	root string
	log  func(principal, command string)
	mu   sync.Mutex
	held map[string]*hostSandbox // suspended, by ID
}

// New returns the backend. It refuses to run as root: a command that runs on
// the host as root could do anything.
func New(o Options) (*Backend, error) {
	if geteuid() == 0 {
		return nil, fmt.Errorf("%w: the host backend must not run as root: run LocalAI as an unprivileged user", sandbox.ErrUnsupported)
	}
	if !filepath.IsAbs(o.Root) {
		return nil, errors.New("host: Root must be an absolute path")
	}
	if err := os.MkdirAll(o.Root, 0o700); err != nil {
		return nil, err
	}
	// A host sandbox does not survive its process, so what an earlier run left
	// is garbage.
	if left, err := filepath.Glob(filepath.Join(o.Root, "sb-*")); err == nil {
		for _, d := range left {
			_ = os.RemoveAll(d)
		}
	}
	return &Backend{root: filepath.Clean(o.Root), log: o.Log, held: map[string]*hostSandbox{}}, nil
}

// Skips lists what this backend cannot do, for the conformance suite: it has
// no terminal and no network isolation.
func (b *Backend) Skips(feature string) bool { return feature == "tty" || feature == "network" }

func (b *Backend) Create(ctx context.Context, spec sandbox.Spec) (_ sandbox.Sandbox, err error) {
	spec = spec.WithDefaults()
	if err := spec.Check(); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(b.root, "sb-")
	if err != nil {
		return nil, err
	}
	// A failed create must not leave a copy of the workspace behind.
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	s := &hostSandbox{b: b, id: filepath.Base(dir), dir: dir, env: spec.Env, view: spec.View}
	// #nosec G301 -- the home folder of the commands, as on the user's own machine
	if err := os.MkdirAll(s.home(), 0o755); err != nil {
		return nil, err
	}
	if err := s.materialize(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (b *Backend) Resume(ctx context.Context, snap sandbox.SnapshotRef, env sandbox.Env) (sandbox.Sandbox, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, ok := b.held[snap.Path]
	if !ok || snap.Backend != "host" {
		return nil, fmt.Errorf("host: no snapshot %q", snap.Path)
	}
	delete(b.held, snap.Path)
	s.mu.Lock()
	s.env, s.gone = env, false
	s.mu.Unlock()
	return s, nil
}

// Discard drops a suspended sandbox: its processes are killed and its folder
// is removed. A suspended host sandbox keeps its processes running, so without
// this they would run until the add-on stops. A snapshot this backend does not
// hold is ignored, which makes a second discard harmless.
func (b *Backend) Discard(ctx context.Context, snap sandbox.SnapshotRef) error {
	if snap.Backend != "host" {
		return nil
	}
	b.mu.Lock()
	s, ok := b.held[snap.Path]
	delete(b.held, snap.Path)
	b.mu.Unlock()
	if !ok {
		return nil
	}
	return s.Destroy(ctx)
}

// Close discards every suspended sandbox the backend still holds. The selector
// calls it when the administrator moves to another backend.
func (b *Backend) Close() error {
	b.mu.Lock()
	held := b.held
	b.held = map[string]*hostSandbox{}
	b.mu.Unlock()
	var first error
	for _, s := range held {
		if err := s.Destroy(context.Background()); err != nil && first == nil {
			first = err
		}
	}
	return first
}

type hostSandbox struct {
	b    *Backend
	id   string
	dir  string
	view workspace.View
	op   sync.Mutex // checkpoint, refresh, suspend

	mu    sync.Mutex
	env   sandbox.Env
	gone  bool
	procs []*hostProc
	base  int64
	own   []int64
	index sandbox.UpperIndex
}

func (s *hostSandbox) lower() string { return filepath.Join(s.dir, "lower") }
func (s *hostSandbox) work() string  { return filepath.Join(s.dir, "work") }
func (s *hostSandbox) home() string  { return filepath.Join(s.dir, "home") }

// materialize builds lower/ at the current revision and a writable copy in
// work/.
func (s *hostSandbox) materialize(ctx context.Context) error {
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
			return os.MkdirAll(dst, 0o755) // #nosec G301 -- the work copy is the user's folder, as on the user's own machine
		}
		b, err := os.ReadFile(p) // #nosec G122 G304 -- the walk reads the lower folder that the add-on itself wrote
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644) // #nosec G306 G703 -- the destination is inside the work folder of the add-on; the work copy is the user's folder
	})
}

func (s *hostSandbox) ID() string { return s.id }

func (s *hostSandbox) Exec(ctx context.Context, c sandbox.Cmd) (sandbox.Process, error) {
	s.mu.Lock()
	gone := s.gone
	s.mu.Unlock()
	if gone {
		return nil, sandbox.ErrGone
	}
	if c.TTY != nil {
		return nil, errors.New("host: no terminal")
	}
	dir := c.Dir
	if dir == "" {
		dir = sandbox.DefaultDir
	}
	// Clean first: "/workspace/../.." must not leave the workspace, and
	// "/workspacefoo" is not a folder of it.
	clean := path.Clean(dir)
	rel, ok := strings.CutPrefix(clean, sandbox.DefaultDir+"/")
	if clean == sandbox.DefaultDir {
		rel, ok = "", true
	}
	if !ok {
		return nil, fmt.Errorf("host: dir %q is outside %s", dir, sandbox.DefaultDir)
	}
	if s.b.log != nil {
		line := strings.Join(c.Args, " ")
		if len(line) > 4096 {
			line = line[:4096] + "..."
		}
		s.b.log(s.env.Actor.Principal, line)
	}
	// The program is looked up in the path the command gets, not in the
	// add-on's own: the supervisor starts the add-on with no PATH at all.
	prog, err := lookPath(c.Args[0])
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(prog, c.Args[1:]...) // #nosec G204 -- the host backend runs the command of the user by design
	cmd.Dir = filepath.Join(s.work(), filepath.FromSlash(rel))
	// A fixed environment: nothing of the add-on's own reaches the command.
	cmd.Env = append([]string{"PATH=" + commandPath, "HOME=" + s.home(), "LANG=C", "TZ=UTC"}, c.Env...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c.Stdin, c.Stdout, c.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 200 * time.Millisecond
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	p := &hostProc{cmd: cmd, done: make(chan struct{}), gone: make(chan struct{})}
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

type hostProc struct {
	cmd      *exec.Cmd
	done     chan struct{}
	err      error
	ctxErr   errBox
	gone     chan struct{} // closed by Suspend and Destroy
	goneOnce sync.Once
}

// markGone makes the handle return sandbox.ErrGone. The process itself keeps
// running after a Suspend, as a real suspend keeps it.
func (p *hostProc) markGone() { p.goneOnce.Do(func() { close(p.gone) }) }

func (p *hostProc) isGone() bool {
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

func (p *hostProc) Wait() (int, error) {
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

func (p *hostProc) Signal(sig syscall.Signal) error {
	if p.isGone() {
		return sandbox.ErrGone
	}
	return syscall.Kill(-p.cmd.Process.Pid, sig)
}

func (p *hostProc) Resize(rows, cols uint16) error {
	if p.isGone() {
		return sandbox.ErrGone
	}
	return errors.New("host: no terminal")
}

// scan lists the work tree as an upper layer: every folder, every file that
// differs from the lower tree or was committed before, and a whiteout for
// every lower entry that is gone.
func (s *hostSandbox) scan() ([]sandbox.IndexedEntry, error) {
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
				UpperEntry: workspace.UpperEntry{Path: rel, Kind: workspace.EntryFile, Mode: info.Mode(), Open: func() (io.ReadCloser, error) { return os.Open(path) }}, // #nosec G304 -- a file of the work folder of the add-on
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
	x, err := os.ReadFile(a) // #nosec G304 -- files of the lower and work folders of the add-on
	if err != nil {
		return false
	}
	y, err := os.ReadFile(b) // #nosec G304 -- files of the lower and work folders of the add-on
	return err == nil && bytes.Equal(x, y)
}

func (s *hostSandbox) checkpoint(ctx context.Context, title string) (workspace.ChangeSet, []workspace.Conflict, error) {
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

func (s *hostSandbox) Checkpoint(ctx context.Context, title string) (workspace.ChangeSet, []workspace.Conflict, error) {
	if !s.op.TryLock() {
		return workspace.ChangeSet{}, nil, sandbox.ErrBusy
	}
	defer s.op.Unlock()
	return s.checkpoint(ctx, title)
}

func (s *hostSandbox) Refresh(ctx context.Context) error {
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

func (s *hostSandbox) Suspend(ctx context.Context) (sandbox.SnapshotRef, error) {
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
	return sandbox.SnapshotRef{Backend: "host", Path: s.id}, nil
}

func (s *hostSandbox) Destroy(ctx context.Context) error {
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
