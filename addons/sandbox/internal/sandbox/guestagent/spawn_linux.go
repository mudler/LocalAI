package guestagent

import (
	"errors"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

// Linux capabilities that the agent user keeps, so it can edit workspace
// files that the lower layer holds read-only (mode 0444, another owner).
const (
	CapDACOverride = 1
	CapFowner      = 3
)

// LinuxSpawner starts processes on Linux.
type LinuxSpawner struct {
	// Credential is the user of every process. Nil keeps the agent's user.
	Credential *syscall.Credential
	// AmbientCaps are raised in every process. They need a root agent.
	AmbientCaps []uintptr
	// Env is the base environment. The request's entries win.
	Env []string
	// Dir is used when a request has no dir.
	Dir string
	// Reap makes the spawner reap every child of this process, also orphans
	// that the kernel gives to PID 1. Set it only in PID 1: it takes the exit
	// status of every child.
	Reap bool

	once  sync.Once
	mu    sync.Mutex
	waits map[int]chan syscall.WaitStatus
	// reapHook runs before each reap; tests use it to make a reap fail.
	reapHook func()
}

// Spawn starts a process with pipes or a terminal.
func (sp *LinuxSpawner) Spawn(req ExecRequest) (Proc, error) {
	if len(req.Args) == 0 {
		return nil, errors.New("guestagent: no args")
	}
	dir := req.Dir
	if dir == "" {
		dir = sp.Dir
	}
	attr := &syscall.SysProcAttr{Credential: sp.Credential, AmbientCaps: sp.AmbientCaps}
	p := &linuxProc{}
	var child []*os.File
	if req.TTY != nil {
		master, slave, err := openPTY()
		if err != nil {
			return nil, err
		}
		if err := setWinsize(master, req.TTY.Rows, req.TTY.Cols); err != nil {
			_ = master.Close()
			_ = slave.Close()
			return nil, err
		}
		p.tty = master
		p.files = []*os.File{master}
		p.stdin = ptyInput{master}
		p.stdout = ptyOutput{master}
		child = []*os.File{slave, slave, slave}
		attr.Setsid, attr.Setctty, attr.Ctty = true, true, 0
	} else {
		inR, inW, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		outR, outW, err := os.Pipe()
		if err != nil {
			_ = inR.Close()
			_ = inW.Close()
			return nil, err
		}
		errR, errW, err := os.Pipe()
		if err != nil {
			_ = inR.Close()
			_ = inW.Close()
			_ = outR.Close()
			_ = outW.Close()
			return nil, err
		}
		p.stdin, p.stdout, p.stderr = inW, outR, errR
		p.files = []*os.File{inW, outR, errR}
		child = []*os.File{inR, outW, errW}
		attr.Setpgid = true
	}
	env := mergeEnv(sp.Env, req.Env)
	if req.TTY != nil && lookupEnv(env, "TERM") == "" {
		env = append(env, "TERM=xterm-256color")
	}
	path := req.Args[0]
	if !strings.Contains(path, "/") {
		lp, err := lookPath(path, lookupEnv(env, "PATH"))
		if err != nil {
			_ = p.Close()
			closeFiles(child)
			return nil, err
		}
		path = lp
	}
	if sp.Reap {
		sp.once.Do(sp.startReaper)
		sp.mu.Lock() // the reaper must not see the exit before the pid is known
	}
	proc, err := os.StartProcess(path, req.Args, &os.ProcAttr{Dir: dir, Env: env, Files: child, Sys: attr})
	closeFiles(child)
	if err != nil {
		if sp.Reap {
			sp.mu.Unlock()
		}
		_ = p.Close()
		return nil, err
	}
	p.pid = proc.Pid
	// os.StartProcess opens a pidfd. The reaper and Wait4 work on the pid,
	// so the pidfd is released here rather than left to a finalizer.
	_ = proc.Release()
	p.done = make(chan syscall.WaitStatus, 1)
	if sp.Reap {
		sp.waits[p.pid] = p.done
		sp.mu.Unlock()
	} else {
		go func() {
			var ws syscall.WaitStatus
			for {
				_, err := syscall.Wait4(p.pid, &ws, 0, nil)
				if !errors.Is(err, syscall.EINTR) {
					break
				}
			}
			p.done <- ws
		}()
	}
	return p, nil
}

// maxReapRetries bounds the runs of one SIGCHLD that a panic cuts short.
const maxReapRetries = 8

// startReaper reaps every child on SIGCHLD and hands the status of a
// started process to its Wait.
func (sp *LinuxSpawner) startReaper() {
	sp.waits = map[int]chan syscall.WaitStatus{}
	ch := make(chan os.Signal, 16)
	signal.Notify(ch, syscall.SIGCHLD)
	go func() {
		for range ch {
			// A panic in one drain must not stop the reaper, or every later
			// child stays a zombie and its Wait never returns. The drain runs
			// again at once: a child it did not reach is a zombie until the
			// next drain, and there may be no next SIGCHLD.
			for tries := 0; tries < maxReapRetries && !sp.drain(); tries++ {
			}
		}
	}()
}

// drain reaps every child that has exited and delivers its status. It
// returns false when a panic stopped it part-way.
func (sp *LinuxSpawner) drain() (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("reaper: recovered panic: %v", r)
			ok = false
		}
	}()
	for {
		if sp.reapHook != nil {
			sp.reapHook()
		}
		var ws syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &ws, syscall.WNOHANG, nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil || pid <= 0 {
			return true
		}
		sp.mu.Lock()
		done, found := sp.waits[pid]
		delete(sp.waits, pid)
		sp.mu.Unlock()
		if found {
			// done is buffered with capacity 1 and gets exactly one status
			// (the pid leaves the map here), so this send never blocks.
			done <- ws
		}
	}
}

func closeFiles(fs []*os.File) {
	seen := map[*os.File]bool{}
	for _, f := range fs {
		if !seen[f] {
			seen[f] = true
			_ = f.Close()
		}
	}
}

type linuxProc struct {
	pid    int
	tty    *os.File
	files  []*os.File // the parent's ends: every descriptor Close must release
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
	done   chan syscall.WaitStatus
	once   sync.Once
	ws     syscall.WaitStatus
	closed sync.Once
}

// Close releases the parent's descriptors. The stdin writer and the output
// readers fail with an error once it runs, so they can stop.
func (p *linuxProc) Close() error {
	var first error
	p.closed.Do(func() {
		for _, f := range p.files {
			if err := f.Close(); err != nil && first == nil {
				first = err
			}
		}
	})
	return first
}

func (p *linuxProc) Stdin() io.WriteCloser { return p.stdin }
func (p *linuxProc) Stdout() io.ReadCloser { return p.stdout }
func (p *linuxProc) Stderr() io.ReadCloser {
	if p.stderr == nil {
		return nil
	}
	return p.stderr
}

func (p *linuxProc) Wait() (int, int, error) {
	p.once.Do(func() { p.ws = <-p.done })
	if p.ws.Signaled() {
		return -1, int(p.ws.Signal()), nil
	}
	return p.ws.ExitStatus(), 0, nil
}

func (p *linuxProc) Signal(sig syscall.Signal) error { return syscall.Kill(-p.pid, sig) }
func (p *linuxProc) Kill() error                     { return syscall.Kill(-p.pid, syscall.SIGKILL) }

func (p *linuxProc) Resize(rows, cols uint16) error {
	if p.tty == nil {
		return ErrNoTTY
	}
	return setWinsize(p.tty, rows, cols)
}

// ptyInput writes to the terminal. Close sends end-of-file (Ctrl-D) and
// keeps the terminal open.
type ptyInput struct{ f *os.File }

func (t ptyInput) Write(b []byte) (int, error) { return t.f.Write(b) }
func (t ptyInput) Close() error                { _, err := t.f.Write([]byte{4}); return err }

// ptyOutput reads the terminal. EIO, which the kernel returns when the last
// process closed the terminal, is the end of the output.
type ptyOutput struct{ f *os.File }

func (t ptyOutput) Read(b []byte) (int, error) {
	n, err := t.f.Read(b)
	if errors.Is(err, syscall.EIO) {
		err = io.EOF
	}
	return n, err
}
func (t ptyOutput) Close() error { return t.f.Close() }

// openPTY opens a new terminal pair.
func openPTY() (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	var unlock int32
	if err := fileIoctl(master, syscall.TIOCSPTLCK, unsafe.Pointer(&unlock)); err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	var n uint32
	if err := fileIoctl(master, syscall.TIOCGPTN, unsafe.Pointer(&n)); err != nil {
		_ = master.Close()
		return nil, nil, err
	}
	slave, err = os.OpenFile("/dev/pts/"+itoa(int(n)), os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}

type winsize struct{ Row, Col, X, Y uint16 }

func setWinsize(f *os.File, rows, cols uint16) error {
	ws := winsize{Row: rows, Col: cols}
	return fileIoctl(f, syscall.TIOCSWINSZ, unsafe.Pointer(&ws))
}

// fileIoctl runs an ioctl on f without f.Fd, which would put f in blocking
// mode and stop Close from interrupting a Read. arg stays a pointer until the
// call: the conversion to uintptr happens only inside the syscall.Syscall
// argument list, which is the form the unsafe rules allow.
func fileIoctl(f *os.File, req uintptr, arg unsafe.Pointer) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var e syscall.Errno
	if err := rc.Control(func(fd uintptr) {
		_, _, e = syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg))
	}); err != nil {
		return err
	}
	if e != 0 {
		return e
	}
	return nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
