//go:build linux

package chv

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
	"syscall"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/guestagent"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// sbx is one sandbox. Locks: op makes Checkpoint, Refresh and Suspend
// exclusive (TryLock, else ErrBusy). mu is read-held by Exec and Checkpoint
// and write-held by Refresh, Suspend and Destroy, which replace or remove
// the VM.
type sbx struct {
	b   *Backend
	id  string
	dir string
	img guestImage

	op sync.Mutex

	mu    sync.RWMutex
	vm    *vm
	env   sandbox.Env
	st    state
	epoch int64 // the ownership epoch of this handle (state.Epoch)
	gone  bool
	ports *portServer

	// frozenAt is when the last freeze of this sandbox was requested.
	frozenAt time.Time
	// released is set when Suspend handed the folder over. Another handle
	// may have resumed it since: every use of the folder takes the folder
	// lock and compares epochs first (lockOwned).
	released bool
	hooks    testHooks
}

var _ sandbox.Sandbox = (*sbx)(nil)

func (s *sbx) ID() string { return s.id }

// boot starts virtiofsd and a fresh guest and waits for the ready frame. On
// an error everything it started is stopped.
func (s *sbx) boot(ctx context.Context) error {
	if s.hooks.bootFail != nil {
		if err := s.hooks.bootFail(); err != nil {
			return err
		}
	}
	readySock := filepath.Join(s.dir, "vsock.sock_"+itoa(uint64(guestagent.AgentPort)))
	// console.log goes too: a restarted VM must not inherit a kernel panic
	// line from the old one, which would fail its boot or mark it gone.
	for _, f := range []string{"chv.sock", "vsock.sock", filepath.Base(readySock), "console.log"} {
		_ = os.Remove(filepath.Join(s.dir, f))
	}
	rl, err := net.Listen("unix", readySock)
	if err != nil {
		return err
	}
	defer func() { _ = rl.Close(); _ = os.Remove(readySock) }()
	vfsd, vfsdDone, err := s.b.startVirtiofsd(s.dir)
	if err != nil {
		return err
	}
	v := &vm{dir: s.dir, api: newAPI(filepath.Join(s.dir, "chv.sock")), vfsd: vfsd, vfsdDone: vfsdDone}
	argv := append([]string{s.b.bins.CloudHypervisor}, chArgs(s.dir, s.img, s.st)...)
	v.ch, v.done, err = start(argv, filepath.Join(s.dir, "chv.log"))
	if err != nil {
		v.stop()
		return err
	}
	timeout := s.b.opts.ReadyTimeout
	ready := make(chan error, 1)
	go func() {
		_ = rl.(*net.UnixListener).SetDeadline(time.Now().Add(timeout))
		c, err := rl.Accept()
		if err != nil {
			ready <- err
			return
		}
		defer func() { _ = c.Close() }()
		_, err = guestagent.ReadReady(c, timeout)
		ready <- err
	}()
	// A kernel panic (for example an init that exits) leaves the VM up, so
	// the console log is watched too.
	console := filepath.Join(s.dir, "console.log")
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
wait:
	for {
		select {
		case err = <-ready:
			break wait
		case <-v.done:
			err = errors.New("cloud-hypervisor exited")
			break wait
		case <-ctx.Done():
			err = ctx.Err()
			break wait
		case <-tick.C:
			if consoleHasPanic(console) {
				err = errors.New("kernel panic during boot")
				break wait
			}
		}
	}
	if err != nil {
		v.stop()
		return fmt.Errorf("chv: guest not ready: %w\nconsole: %s\ncloud-hypervisor: %s", err,
			tail(filepath.Join(s.dir, "console.log"), 4096), tail(filepath.Join(s.dir, "chv.log"), 1024))
	}
	s.vm = v
	s.watch(v)
	return nil
}

// markGone is the post-boot case of a kernel panic: the guest cannot run any
// more, so the sandbox is gone and its VM stops. Calls then return ErrGone.
// A watcher of a VM that a refresh has replaced does nothing.
func (s *sbx) markGone(v *vm) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.vm != v {
		return
	}
	s.gone = true
	v.stop()
}

// watch starts the post-boot console watch of v: a panic line or a log over
// the cap marks the sandbox gone.
func (s *sbx) watch(v *vm) {
	console := filepath.Join(s.dir, "console.log")
	go watchConsole(console, v.done, 250*time.Millisecond, consoleLimit, func() { s.markGone(v) }, func() { s.markFlooded(v) })
}

// markFlooded is the case of a console log over the cap: the VM is killed at
// once, the sandbox is gone, and the log is cut to its last bytes.
func (s *sbx) markFlooded(v *vm) {
	// Kill before taking mu: a checkpoint can hold mu for a long time, and
	// the flood goes on until the VM is dead. A watcher of a replaced VM is
	// stopped by its done channel, so v is the current VM or about to be
	// stopped anyway.
	if v.ch != nil {
		_ = v.ch.Process.Kill()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.vm != v {
		return
	}
	s.gone = true
	v.stop()
	if err := trimConsole(filepath.Join(s.dir, "console.log"), consoleKeep); err != nil {
		log.Printf("chv: trimming the console log: %v", err)
	}
}

// call sends one request to the guest agent, through the test hook when set.
func (s *sbx) call(ctx context.Context, v *vm, typ string) error {
	if s.hooks.call != nil {
		return s.hooks.call(ctx, v, typ)
	}
	return s.request(ctx, v, typ)
}

// request sends one request to the guest agent.
func (s *sbx) request(ctx context.Context, v *vm, typ string) error {
	c, err := dialGuest(ctx, v.vsock(), guestagent.AgentPort)
	if err != nil {
		return err
	}
	return guestagent.Call(ctx, c, typ)
}

func (s *sbx) Exec(ctx context.Context, c sandbox.Cmd) (sandbox.Process, error) {
	if len(c.Args) == 0 {
		return nil, fmt.Errorf("chv: %w: no args", workspace.ErrInvalid)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	v := s.vm
	if s.gone || v == nil || v.exited() {
		return nil, sandbox.ErrGone
	}
	conn, err := dialGuest(ctx, v.vsock(), guestagent.AgentPort)
	if err != nil {
		if v.exited() {
			return nil, sandbox.ErrGone
		}
		return nil, err
	}
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
		return nil, err
	}
	// gone reports that this VM stopped: Suspend and Destroy stop it before
	// they return. A write that fails after that is ErrGone, not a socket error.
	return proc{p: p, gone: v.exited}, nil
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

// Destroy stops the guest and deletes the session folder, with a snapshot
// in it. A handle that no longer owns the folder (it was suspended and
// resumed through another handle) stops its own guest, if any, and leaves
// the folder alone.
func (s *sbx) Destroy(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.vm != nil {
		s.vm.stop()
		s.vm = nil
	}
	s.ports.close()
	s.gone = true
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
	return os.RemoveAll(s.dir)
}
