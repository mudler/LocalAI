package vz

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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/ext4image"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/guestagent"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/vz/vmm"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// guestImage is a converted image in the cache.
type guestImage = ext4image.Image

// vmHandle is one running VM and the port handlers that serve it.
type vmHandle struct {
	b     *Backend
	m     machine
	ports *portServer
}

func (v *vmHandle) exited() bool {
	select {
	case <-v.m.Done():
		return true
	default:
		return false
	}
}

func (v *vmHandle) done() <-chan struct{} { return v.m.Done() }

// stop ends the VM at once and cancels its port handlers.
func (v *vmHandle) stop() {
	v.ports.close()
	v.m.Stop()
	v.b.untrack(v)
}

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
	vm    *vmHandle
	env   sandbox.Env
	st    state
	epoch int64 // the ownership epoch of this handle (state.Epoch)
	gone  bool

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

// cmdline is the kernel command line of a boot.
func cmdline(st state) string {
	c := "console=hvc0 root=/dev/vda ro init=/sbin/sandbox-agent"
	if len(st.Ports) > 0 {
		ps := make([]string, len(st.Ports))
		for i, p := range st.Ports {
			ps[i] = strconv.Itoa(p)
		}
		c += " sandbox.ports=" + strings.Join(ps, ",")
	}
	return c
}

// boot starts a fresh guest over the files of the session folder and waits
// for the ready frame. On an error everything it started is stopped.
func (s *sbx) boot(ctx context.Context) error {
	if s.hooks.bootFail != nil {
		if err := s.hooks.bootFail(); err != nil {
			return err
		}
	}
	// A restarted VM must not inherit a kernel panic line from the old one,
	// which would fail its boot or mark it gone.
	console := filepath.Join(s.dir, "console.log")
	_ = os.Remove(console)
	cpus, mem := s.b.limits(s.st.CPUs, s.st.Memory)
	m, err := s.b.mk(vmm.Config{
		Kernel:      s.img.Kernel,
		Cmdline:     cmdline(s.st),
		Root:        s.img.Root,
		Upper:       filepath.Join(s.dir, "upper.ext4"),
		Lower:       filepath.Join(s.dir, "lower"),
		Console:     console,
		CPUs:        cpus,
		MemoryBytes: mem,
	})
	if err != nil {
		return fmt.Errorf("vz: %w", err)
	}
	v := &vmHandle{b: s.b, m: m}
	timeout := s.b.opts.ReadyTimeout
	ready := make(chan error, 1)
	// The listener is one-shot, as in the Cloud Hypervisor backend: the
	// first connection is the ready frame, and the listener goes as soon as
	// it is read. Any other connection that arrives meanwhile is closed
	// without a goroutine, so a guest process cannot pile up connections.
	var taken atomic.Bool
	err = m.Listen(guestagent.AgentPort, func(c net.Conn) {
		if !taken.CompareAndSwap(false, true) {
			_ = c.Close()
			return
		}
		go func() {
			defer func() { _ = c.Close() }()
			_, err := guestagent.ReadReady(c, timeout)
			// Unlisten waits for the queue of the VM, so it cannot run in
			// accept itself.
			m.Unlisten(guestagent.AgentPort)
			select {
			case ready <- err:
			default:
			}
		}()
	})
	if err == nil {
		v.ports, err = servePorts(m, s.portHandlers())
	}
	if err != nil {
		v.stop()
		return err
	}
	s.b.track(v)
	if err := m.Start(ctx); err != nil {
		v.stop()
		return fmt.Errorf("vz: guest not ready: %w", err)
	}
	// A kernel panic (for example an init that exits) leaves the VM up, so
	// the console log is watched too.
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
wait:
	for {
		select {
		case err = <-ready:
			break wait
		case <-v.done():
			err = errors.New("the VM stopped")
			break wait
		case <-timer.C:
			err = errors.New("the guest agent did not report ready in time")
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
		return fmt.Errorf("vz: guest not ready: %w\nconsole: %s", err, tail(console, 4096))
	}
	s.vm = v
	s.watch(v)
	return nil
}

// portHandlers returns the handlers of the ports of the sandbox.
func (s *sbx) portHandlers() map[int]sandbox.PortHandler { return s.env.Ports }

// markGone is the post-boot case of a kernel panic: the guest cannot run any
// more, so the sandbox is gone and its VM stops. Calls then return ErrGone.
// A watcher of a VM that a refresh has replaced does nothing.
func (s *sbx) markGone(v *vmHandle) {
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
func (s *sbx) watch(v *vmHandle) {
	console := filepath.Join(s.dir, "console.log")
	go watchConsole(console, v.done(), 250*time.Millisecond, consoleLimit, func() { s.markGone(v) }, func() { s.markFlooded(v) })
}

// markFlooded is the case of a console log over the cap: the VM is killed at
// once, the sandbox is gone, and the log is cut to its last bytes.
func (s *sbx) markFlooded(v *vmHandle) {
	// Kill before taking mu: a checkpoint can hold mu for a long time, and
	// the flood goes on until the VM is dead.
	v.stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.vm != v {
		return
	}
	s.gone = true
	if err := trimConsole(filepath.Join(s.dir, "console.log"), consoleKeep); err != nil {
		log.Printf("vz: trimming the console log: %v", err)
	}
}

// call sends one request to the guest agent, through the test hook when set.
func (s *sbx) call(ctx context.Context, v *vmHandle, typ string) error {
	if s.hooks.call != nil {
		return s.hooks.call(ctx, v, typ)
	}
	c, err := v.m.Dial(ctx, guestagent.AgentPort)
	if err != nil {
		return err
	}
	return guestagent.Call(ctx, c, typ)
}

func (s *sbx) Exec(ctx context.Context, c sandbox.Cmd) (sandbox.Process, error) {
	if len(c.Args) == 0 {
		return nil, fmt.Errorf("vz: %w: no args", workspace.ErrInvalid)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	v := s.vm
	if s.gone || v == nil || v.exited() {
		return nil, sandbox.ErrGone
	}
	conn, err := v.m.Dial(ctx, guestagent.AgentPort)
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

// Destroy stops the guest and deletes the session folder. A handle that no
// longer owns the folder (it was suspended and resumed through another
// handle) stops its own guest, if any, and leaves the folder alone.
func (s *sbx) Destroy(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.vm != nil {
		s.vm.stop()
		s.vm = nil
	}
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
