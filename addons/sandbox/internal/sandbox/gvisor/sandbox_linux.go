//go:build linux

package gvisor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/guestagent"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// ErrDiskFull is the error of Exec while the upper layer is over Spec.Disk.
// The disk limit is watched, not enforced by the kernel.
var ErrDiskFull = errors.New("The sandbox disk is full.") //nolint:staticcheck // ST1005: text is user-facing and fixed by the spec

// inst is one running runsc sandbox. A refresh or a resume makes a new one.
type inst struct {
	pid  int
	done chan struct{} // closed when the sandbox has stopped
	quit chan struct{} // closed to end the watchers
	once sync.Once
}

func newInst(pid int) *inst {
	return &inst{pid: pid, done: make(chan struct{}), quit: make(chan struct{})}
}

func (in *inst) exited() bool {
	select {
	case <-in.done:
		return true
	default:
		return false
	}
}

// sbx is one sandbox. Locks: op makes Checkpoint, Refresh and Suspend
// exclusive (TryLock, else ErrBusy). mu is read-held by Exec and Checkpoint
// and write-held by Refresh, Suspend and Destroy, which replace or remove
// the runsc sandbox.
type sbx struct {
	b   *Backend
	id  string
	dir string
	img guestImage

	op sync.Mutex

	mu    sync.RWMutex
	in    *inst
	env   sandbox.Env
	st    state
	epoch int64 // the ownership epoch of this handle (state.Epoch)
	gone  bool
	ports *portServer
	conns connSet // the connections of Exec

	// released is set when Suspend handed the folder over. Another handle
	// may have resumed it since: every use of the folder takes the folder
	// lock and compares epochs first (lockOwned).
	released bool

	diskFull atomic.Bool
	hooks    testHooks
}

var _ sandbox.Sandbox = (*sbx)(nil)

func (s *sbx) ID() string { return s.id }

func (s *sbx) runscRoot() string { return filepath.Join(s.dir, "runsc") }
func (s *sbx) ctl() string       { return filepath.Join(s.dir, "ctl") }
func (s *sbx) agentSock() string { return filepath.Join(s.ctl(), "agent.sock") }

// writeBundle writes bundle/config.json for the current state.
func (s *sbx) writeBundle() error {
	cfg, err := bundleConfig(bundleParams{
		Rootfs: s.img.Root,
		Lower:  filepath.Join(s.dir, "lower"), Upper: filepath.Join(s.dir, "upper"), Ctl: s.ctl(),
		HostUID: os.Getuid(), HostGID: os.Getgid(),
		Ports: s.st.Ports, Disk: s.st.Disk,
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(s.dir, "bundle"), 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.dir, "bundle", "config.json"), cfg, 0o600)
}

// boot starts a fresh runsc sandbox and waits until the agent answers. On an
// error everything it started is stopped.
func (s *sbx) boot(ctx context.Context) error {
	if s.hooks.bootFail != nil {
		if err := s.hooks.bootFail(); err != nil {
			return err
		}
	}
	_ = os.Remove(s.agentSock())
	_ = os.Remove(filepath.Join(s.dir, "console.log"))
	// A sandbox of an earlier run of this id may have left state.
	_ = s.b.deleteForce(ctx, s.runscRoot(), s.id)
	if err := s.writeBundle(); err != nil {
		return err
	}
	pid, err := s.b.start(ctx, s.dir, s.id, s.st.CPUs, "run")
	if err != nil {
		// runsc may have forked the sandbox and its gofer before it failed or
		// before ctx killed it. Delete by id while the runsc state exists: the
		// caller may remove the folder next, and the sandbox could not be
		// reached after that.
		s.cleanup()
		return err
	}
	return s.adopt(ctx, newInst(pid), "boot")
}

// cleanupBudget bounds the "runsc delete --force" after a failed start.
const cleanupBudget = 30 * time.Second

// cleanup deletes the runsc sandbox of this id. It ignores the cancellation of
// the caller: it runs after a cancelled call, which is when it matters.
func (s *sbx) cleanup() {
	c, cancel := context.WithTimeout(context.Background(), cleanupBudget)
	defer cancel()
	if err := s.b.deleteForce(c, s.runscRoot(), s.id); err != nil {
		log.Printf("gvisor: sandbox %s: cleanup after a failed start: %v", s.id, err)
	}
}

// adopt waits for the agent of in, then starts the watchers. On an error in
// is stopped.
func (s *sbx) adopt(ctx context.Context, in *inst, what string) error {
	if err := s.waitReady(ctx, in); err != nil {
		s.stopInst(in)
		return fmt.Errorf("gvisor: guest not ready after %s: %w\nconsole: %s", what, err, tail(filepath.Join(s.dir, "console.log"), 4096))
	}
	s.in = in // the caller holds mu, or the handle is not shared yet
	s.startWatchers(in)
	return nil
}

// startWatchers starts the watchers of in. s.in must be in already (under
// mu), so that markGone, which compares it, sees the instance.
func (s *sbx) startWatchers(in *inst) {
	go s.watchAlive(in)
	go s.watchMemory(in, s.st.Memory)
	go s.watchDisk(in, s.st.Disk)
}

// waitReady dials the agent socket until the agent answers a health request,
// for at most Options.ReadyTimeout.
func (s *sbx) waitReady(ctx context.Context, in *inst) error {
	timeout := s.b.opts.ReadyTimeout
	end := time.Now().Add(timeout)
	last := errors.New("no try")
	for {
		if !pidAlive(in.pid) {
			return errors.New("runsc exited")
		}
		remaining := time.Until(end)
		if remaining <= 0 {
			return fmt.Errorf("the agent did not answer in %s: %w", timeout, last)
		}
		c, cancel := context.WithTimeout(ctx, min(2*time.Second, remaining))
		conn, err := net.Dial("unix", s.agentSock())
		if err == nil {
			err = guestagent.Call(c, conn, guestagent.TypeHealth)
		}
		cancel()
		if err == nil {
			return nil
		}
		last = err
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(30 * time.Millisecond):
		}
	}
}

// stopInst stops the sandbox of in: runsc deletes it, which kills it, also
// when it is paused. It is safe to call twice.
func (s *sbx) stopInst(in *inst) {
	in.once.Do(func() {
		close(in.quit)
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.b.deleteForce(c, s.runscRoot(), s.id); err != nil {
			log.Printf("gvisor: sandbox %s: %v", s.id, err)
		}
		waitGone(in.pid, 5*time.Second)
		if pidAlive(in.pid) { // runsc could not delete it: do not leave it running
			_ = syscall.Kill(in.pid, syscall.SIGKILL)
			waitGone(in.pid, 5*time.Second)
		}
		close(in.done)
	})
}

// markGone is the case of a sandbox that stopped by itself or that a watcher
// stopped: it cannot run any more, so calls return ErrGone. A watcher of an
// instance that a refresh has replaced does nothing.
func (s *sbx) markGone(in *inst, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.in != in {
		return
	}
	s.gone = true
	log.Printf("gvisor: sandbox %s stopped: %s", s.id, reason)
	if f, err := os.OpenFile(filepath.Join(s.dir, "console.log"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600); err == nil {
		_, _ = fmt.Fprintf(f, "sandbox stopped: %s\n", reason)
		_ = f.Close()
	}
}

// watchAlive ends the sandbox state when the runsc process is gone: a crash,
// a kill, or the exit of the agent.
func (s *sbx) watchAlive(in *inst) {
	t := time.NewTicker(s.b.aliveEvery)
	defer t.Stop()
	for {
		select {
		case <-in.quit:
			return
		case <-t.C:
		}
		if !pidAlive(in.pid) {
			s.stopInst(in)
			s.markGone(in, "the runsc process ended")
			return
		}
	}
}

func (s *sbx) Exec(ctx context.Context, c sandbox.Cmd) (sandbox.Process, error) {
	if len(c.Args) == 0 {
		return nil, fmt.Errorf("gvisor: %w: no args", workspace.ErrInvalid)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	in := s.in
	if s.gone || in == nil || in.exited() {
		return nil, sandbox.ErrGone
	}
	if s.diskFull.Load() {
		return nil, ErrDiskFull
	}
	raw, err := net.Dial("unix", s.agentSock())
	if err != nil {
		if in.exited() || !pidAlive(in.pid) {
			return nil, sandbox.ErrGone
		}
		return nil, err
	}
	conn := s.conns.add(raw)
	dir := c.Dir
	if dir == "" {
		dir = sandbox.DefaultDir
	}
	req := guestagent.ExecRequest{Args: c.Args, Env: append(slices.Clone(s.img.Env), c.Env...), Dir: dir}
	if c.TTY != nil {
		req.TTY = &guestagent.TTY{Rows: c.TTY.Rows, Cols: c.TTY.Cols}
	}
	p, err := guestagent.Exec(ctx, conn, req, c.Stdin, c.Stdout, c.Stderr)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return proc{p: p, gone: in.exited}, nil
}

type proc struct {
	p    *guestagent.Process
	gone func() bool
}

func (p proc) Wait() (int, error) {
	code, err := p.p.Wait()
	if errors.Is(err, guestagent.ErrDisconnected) {
		err = fmt.Errorf("%w: %v", sandbox.ErrGone, err)
	}
	return code, err
}

// mapGone turns the error of a write to a stopped guest into ErrGone.
func (p proc) mapGone(err error) error {
	if err != nil && p.gone() {
		return fmt.Errorf("%w: %v", sandbox.ErrGone, err)
	}
	return err
}

func (p proc) Signal(sig syscall.Signal) error { return p.mapGone(p.p.Signal(sig)) }
func (p proc) Resize(rows, cols uint16) error  { return p.mapGone(p.p.Resize(rows, cols)) }

// lockOwned takes the folder lock and checks that this handle still owns the
// folder: the epoch in state.json is the one the handle holds. It returns an
// error that wraps sandbox.ErrGone when the folder is deleted or another
// handle resumed it. The caller keeps the lock for as long as it reads or
// writes the folder, and saves state.json with s.st.save while it holds it.
func (s *sbx) lockOwned(ctx context.Context) (func(), error) {
	unlock, err := lockFile(ctx, filepath.Join(s.dir, "lock"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: the session folder is gone", sandbox.ErrGone)
		}
		return nil, err
	}
	st, err := loadState(s.dir)
	if err != nil {
		unlock()
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: the session folder is gone", sandbox.ErrGone)
		}
		return nil, err
	}
	if st.Epoch != s.epoch {
		unlock()
		return nil, fmt.Errorf("%w: another handle resumed this sandbox (epoch %d, this handle %d)", sandbox.ErrGone, st.Epoch, s.epoch)
	}
	return unlock, nil
}

// Destroy stops the sandbox and deletes the session folder, with a
// checkpoint in it. A handle that no longer owns the folder (it was
// suspended and resumed through another handle) stops its own sandbox, if
// any, and leaves the folder alone.
func (s *sbx) Destroy(ctx context.Context) error {
	s.mu.Lock()
	hadInst := s.in != nil
	if hadInst {
		s.stopInst(s.in)
		s.in = nil
	}
	s.conns.closeAll()
	s.ports.close()
	s.gone = true
	// The folder lock can be held for a while by another handle. mu is not
	// held while Destroy waits for it, so Exec and the other calls return
	// ErrGone at once instead of queueing behind Destroy.
	s.mu.Unlock()
	if _, err := os.Stat(s.dir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	unlock, err := lockFile(ctx, filepath.Join(s.dir, "lock"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer unlock()
	// A folder without state.json (a failed Create) is deleted too.
	if st, err := loadState(s.dir); err == nil && st.Epoch != s.epoch {
		return nil
	}
	// A handle without an instance (a failed boot, a failed refresh) may still
	// own a runsc sandbox: delete it by id before the folder, and with it the
	// runsc state, goes.
	if !hadInst {
		s.cleanup()
	}
	return removeAll(s.dir)
}
