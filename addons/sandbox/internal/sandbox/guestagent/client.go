package guestagent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"syscall"
	"time"
)

// stdinChunk is the largest stdin frame the host sends.
const stdinChunk = 32 << 10

// ErrDisconnected means the connection ended before the guest sent the exit
// frame.
var ErrDisconnected = errors.New("guestagent: connection lost before the exit")

// ErrNoTTY means a resize of a process without a terminal.
var ErrNoTTY = errors.New("guestagent: the process has no terminal")

// RemoteError is an error frame from the guest.
type RemoteError struct{ Message string }

func (e *RemoteError) Error() string { return "guestagent: guest: " + e.Message }

// ReadReady reads the ready frame from conn and checks the version.
func ReadReady(conn net.Conn, timeout time.Duration) (string, error) {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	f, err := ReadFrame(conn)
	if err != nil {
		return "", err
	}
	if f.Type != TypeReady {
		return "", fmt.Errorf("guestagent: expected ready, got %q", f.Type)
	}
	if !SameMajor(f.Version, Version) {
		return f.Version, fmt.Errorf("guestagent: guest agent version %q, host needs %s.x", f.Version, Version[:1])
	}
	return f.Version, nil
}

// SendReady writes the ready frame.
func SendReady(conn net.Conn) error {
	return WriteFrame(conn, Frame{Type: TypeReady, Version: Version})
}

// Call sends a request without fields (freeze, thaw, health, shutdown,
// detach) and waits for ok or error. It closes conn.
func Call(ctx context.Context, conn net.Conn, typ string) error {
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := WriteFrame(conn, Frame{Type: typ}); err != nil {
		return ctxErr(ctx, err)
	}
	f, err := ReadFrame(conn)
	if err != nil {
		return ctxErr(ctx, err)
	}
	return replyErr(f)
}

func ctxErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func replyErr(f Frame) error {
	switch f.Type {
	case TypeOK:
		return nil
	case TypeError:
		return &RemoteError{Message: f.Message}
	default:
		return fmt.Errorf("guestagent: expected ok, got %q", f.Type)
	}
}

// ExecRequest is the exec frame.
type ExecRequest struct {
	Args []string
	Env  []string
	Dir  string
	TTY  *TTY
}

// Process is a process started with Exec.
type Process struct {
	conn    net.Conn
	ctx     context.Context
	tty     bool
	wmu     sync.Mutex
	done    chan struct{}
	stop    func() bool
	code    int
	sig     int
	exited  bool
	readErr error

	// Stdin flow control: outstanding counts the stdin bytes sent and not
	// yet credited. finished is set when the read loop ends, so a feed
	// waiting for credit wakes up.
	fmu         sync.Mutex
	fcond       *sync.Cond
	outstanding int
	finished    bool
}

// Exec starts a process on conn and returns after the guest started it.
// stdin may be nil. The process owns conn. When ctx ends before the exit,
// Exec closes conn, the guest kills the process group, and Wait returns
// ctx.Err().
func Exec(ctx context.Context, conn net.Conn, req ExecRequest, stdin io.Reader, stdout, stderr io.Writer) (*Process, error) {
	if len(req.Args) == 0 {
		_ = conn.Close()
		return nil, errors.New("guestagent: exec without args")
	}
	if err := WriteFrame(conn, Frame{Type: TypeExec, Args: req.Args, Env: req.Env, Dir: req.Dir, TTY: req.TTY}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	f, err := ReadFrame(conn)
	if err == nil {
		err = replyErr(f)
	}
	if err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Time{})
	p := &Process{conn: conn, ctx: ctx, tty: req.TTY != nil, done: make(chan struct{})}
	p.fcond = sync.NewCond(&p.fmu)
	p.stop = context.AfterFunc(ctx, func() { _ = conn.Close() })
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	go p.read(stdout, stderr)
	go p.feed(stdin)
	return p, nil
}

func (p *Process) send(f Frame) error {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	return WriteFrame(p.conn, f)
}

// reserve waits until n more stdin bytes fit in StdinWindow, then counts
// them as outstanding. It returns false when the process or the connection
// ended first.
func (p *Process) reserve(n int) bool {
	p.fmu.Lock()
	defer p.fmu.Unlock()
	for !p.finished && p.outstanding+n > StdinWindow {
		p.fcond.Wait()
	}
	if p.finished {
		return false
	}
	p.outstanding += n
	return true
}

// credit releases n stdin bytes that the guest wrote into the child. The
// guest is not trusted with the counter: a negative n is ignored, and n is
// capped at the bytes outstanding.
func (p *Process) credit(n int) {
	p.fmu.Lock()
	if n > 0 {
		if n > p.outstanding {
			n = p.outstanding
		}
		p.outstanding -= n
	}
	p.fmu.Unlock()
	p.fcond.Broadcast()
}

// feed sends stdin in frames, then an empty frame to close it. It waits for
// credit when StdinWindow bytes are outstanding. With a terminal and no
// stdin it sends nothing: an end-of-file would end an interactive process at
// once, so its input stays open until it exits.
func (p *Process) feed(stdin io.Reader) {
	if stdin == nil && p.tty {
		return
	}
	if stdin != nil {
		buf := make([]byte, stdinChunk)
		for {
			n, err := stdin.Read(buf)
			if n > 0 {
				if !p.reserve(n) {
					return
				}
				if p.send(Frame{Type: TypeStdin, Data: append([]byte(nil), buf[:n]...)}) != nil {
					return
				}
			}
			if err != nil {
				break
			}
		}
	}
	_ = p.send(Frame{Type: TypeStdin})
}

func (p *Process) read(stdout, stderr io.Writer) {
	defer close(p.done)
	defer func() { _ = p.conn.Close() }()
	defer func() {
		p.fmu.Lock()
		p.finished = true
		p.fmu.Unlock()
		p.fcond.Broadcast()
	}()
	for {
		f, err := ReadFrame(p.conn)
		if err != nil {
			p.readErr = err
			return
		}
		switch f.Type {
		case TypeStdout:
			_, _ = stdout.Write(f.Data)
		case TypeStderr:
			_, _ = stderr.Write(f.Data)
		case TypeCredit:
			p.credit(f.N)
		case TypeExit:
			p.code, p.sig, p.exited = f.Code, f.Signal, true
			return
		}
	}
}

// Wait waits for the exit frame. A process killed by a signal exits with 128
// plus the signal number.
func (p *Process) Wait() (int, error) {
	<-p.done
	p.stop()
	if !p.exited {
		if err := p.ctx.Err(); err != nil {
			return -1, err
		}
		return -1, fmt.Errorf("%w: %v", ErrDisconnected, p.readErr)
	}
	if p.sig != 0 {
		return 128 + p.sig, nil
	}
	return p.code, nil
}

// Signal sends sig to the process group.
func (p *Process) Signal(sig syscall.Signal) error {
	return p.send(Frame{Type: TypeSignal, Signal: int(sig)})
}

// Resize changes the terminal size.
func (p *Process) Resize(rows, cols uint16) error {
	if !p.tty {
		return ErrNoTTY
	}
	return p.send(Frame{Type: TypeResize, Rows: rows, Cols: cols})
}
