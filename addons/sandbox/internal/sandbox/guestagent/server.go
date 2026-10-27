package guestagent

import (
	"errors"
	"io"
	"log"
	"net"
	"sync"
	"syscall"
	"time"
)

// Proc is a process that a Spawner started.
type Proc interface {
	// Stdin writes to the process. Close sends end-of-file (with a terminal,
	// Ctrl-D) and keeps the process's descriptors open.
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser // with a terminal: the terminal
	Stderr() io.ReadCloser // nil with a terminal
	// Wait waits for the exit. sig is the signal that killed the process, or 0.
	Wait() (code, sig int, err error)
	Signal(sig syscall.Signal) error // to the process group
	Resize(rows, cols uint16) error
	Kill() error // SIGKILL to the process group
	// Close closes every descriptor the spawner opened for the process. It
	// does not signal the process, and it is safe to call more than once.
	Close() error
}

// Spawner starts processes.
type Spawner interface {
	Spawn(req ExecRequest) (Proc, error)
}

// Server answers host requests.
type Server struct {
	Spawner Spawner
	// Freeze, Thaw, Health and Shutdown answer the requests of the same name.
	// A nil function answers ok.
	Freeze, Thaw, Health, Shutdown func() error
	// Checkpoint answers a checkpoint request: the host is about to save the
	// state of the sandbox. A backend that cannot save open listeners uses
	// it to close them. A nil function answers ok.
	Checkpoint func() error
	// Linger is how long the server waits for the output of a process after
	// it exited. A background child that keeps the output open does not hold
	// the exit frame longer. Zero means 200 ms.
	Linger time.Duration

	mu       sync.Mutex
	sessions map[*session]bool
}

type session struct {
	mu       sync.Mutex
	detached bool
}

func (s *session) isDetached() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.detached
}

// Serve accepts connections until l fails.
func (s *Server) Serve(l net.Listener) error {
	for {
		c, err := l.Accept()
		if err != nil {
			return err
		}
		go func() {
			defer logPanic("connection")
			s.ServeConn(c)
		}()
	}
}

// ServeConn handles one request connection and closes it.
func (s *Server) ServeConn(c net.Conn) {
	defer c.Close()
	_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
	f, err := ReadFrame(c)
	if err != nil {
		if errors.Is(err, ErrFrameTooLarge) {
			_ = WriteFrame(c, Frame{Type: TypeError, Message: err.Error()})
		}
		return
	}
	_ = c.SetReadDeadline(time.Time{})
	var fn func() error
	switch f.Type {
	case TypeExec:
		s.exec(c, f)
		return
	case TypeFreeze:
		fn = s.Freeze
	case TypeThaw:
		fn = s.Thaw
	case TypeHealth:
		fn = s.Health
	case TypeShutdown:
		fn = s.Shutdown
	case TypeDetach:
		fn = s.detach
	case TypeCheckpoint:
		fn = s.Checkpoint
	default:
		_ = WriteFrame(c, Frame{Type: TypeError, Message: "unknown frame type " + f.Type})
		return
	}
	_ = reply(c, fn)
}

func reply(c net.Conn, fn func() error) error {
	if fn != nil {
		if err := fn(); err != nil {
			return WriteFrame(c, Frame{Type: TypeError, Message: err.Error()})
		}
	}
	return WriteFrame(c, Frame{Type: TypeOK})
}

// detach marks every running process: when its connection breaks, it keeps
// running.
func (s *Server) detach() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ss := range s.sessions {
		ss.mu.Lock()
		ss.detached = true
		ss.mu.Unlock()
	}
	return nil
}

func (s *Server) track(ss *session, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions == nil {
		s.sessions = map[*session]bool{}
	}
	if on {
		s.sessions[ss] = true
	} else {
		delete(s.sessions, ss)
	}
}

// frameConn serializes frame writes. After the first write error it drops
// every frame, so output keeps draining when the host is gone.
type frameConn struct {
	mu     sync.Mutex
	c      net.Conn
	broken bool
}

func (w *frameConn) write(f Frame) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.broken {
		return
	}
	if err := WriteFrame(w.c, f); err != nil {
		w.broken = true
	}
}

type exitStatus struct{ code, sig int }

// stdinQueue holds the stdin the child has not taken yet. It is bounded by
// bytes: a frame counts from the moment it is queued until the writer has
// drained it, the same span the host counts as outstanding. A host that
// keeps to StdinWindow never overflows it; a frame that would overflow it is
// refused, and the frame loop then stops the process. The frame loop never
// waits on the queue.
type stdinQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	chunks [][]byte
	queued int  // bytes queued and not yet drained
	closed bool // no more input: the host closed stdin, or the connection ended
}

func newStdinQueue() *stdinQueue {
	q := &stdinQueue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// push queues a frame. It returns false when the frame would take the queue
// past StdinWindow. Frames after the close are ignored.
func (q *stdinQueue) push(b []byte) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return true
	}
	if q.queued+len(b) > StdinWindow {
		return false
	}
	q.queued += len(b)
	q.chunks = append(q.chunks, b)
	q.cond.Signal()
	return true
}

// close ends the input. The writer drains what is queued, then stops.
func (q *stdinQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.cond.Broadcast()
}

// pop waits for the next chunk. It returns false once the input ended and
// everything queued was taken.
func (q *stdinQueue) pop() ([]byte, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.chunks) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.chunks) == 0 {
		return nil, false
	}
	b := q.chunks[0]
	q.chunks[0] = nil
	q.chunks = q.chunks[1:]
	return b, true
}

// drained releases n bytes after the writer has taken them from the queue.
func (q *stdinQueue) drained(n int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.queued -= n
}

// finish waits, for at most linger, for the output pumps of a process that
// exited. A descendant that keeps the output open cannot hold it longer. It
// then closes the process's descriptors.
func finish(p Proc, c net.Conn, out *sync.WaitGroup, outputs []io.ReadCloser, linger time.Duration) {
	drained := make(chan struct{})
	go func() { out.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(linger):
		for _, o := range outputs {
			_ = o.Close()
		}
		select {
		case <-drained:
		case <-time.After(linger):
			_ = c.Close() // a pump blocked on a host that does not read
			<-drained
		}
	}
	_ = p.Close()
}

func (s *Server) exec(c net.Conn, f Frame) {
	if s.Spawner == nil || len(f.Args) == 0 {
		_ = WriteFrame(c, Frame{Type: TypeError, Message: "nothing to run"})
		return
	}
	p, err := s.Spawner.Spawn(ExecRequest{Args: f.Args, Env: f.Env, Dir: f.Dir, TTY: f.TTY})
	if err != nil {
		_ = WriteFrame(c, Frame{Type: TypeError, Message: err.Error()})
		return
	}
	ss := &session{}
	s.track(ss, true)
	defer s.track(ss, false)
	w := &frameConn{c: c}
	w.write(Frame{Type: TypeOK})
	linger := s.Linger
	if linger == 0 {
		linger = 200 * time.Millisecond
	}

	var out sync.WaitGroup
	pump := func(r io.Reader, typ string) {
		defer out.Done()
		buf := make([]byte, 32<<10)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				w.write(Frame{Type: typ, Data: append([]byte(nil), buf[:n]...)})
			}
			if err != nil {
				return
			}
		}
	}
	outputs := []io.ReadCloser{p.Stdout()}
	out.Add(1)
	go pump(p.Stdout(), TypeStdout)
	if e := p.Stderr(); e != nil {
		outputs = append(outputs, e)
		out.Add(1)
		go pump(e, TypeStderr)
	}

	exited := make(chan exitStatus, 1)
	go func() {
		code, sig, _ := p.Wait()
		exited <- exitStatus{code, sig}
	}()

	// Stdin goes through a bounded queue to one writer goroutine. The frame
	// loop only pushes to the queue and never waits for the child, so a
	// signal, a resize or a close is handled while the child ignores stdin.
	in := newStdinQueue()
	go func() {
		sw := p.Stdin()
		var werr error
		for {
			chunk, ok := in.pop()
			if !ok {
				break
			}
			if werr == nil {
				_, werr = sw.Write(chunk)
			}
			in.drained(len(chunk))
			// Credit even after an error, so the host never waits on a
			// child that is gone.
			w.write(Frame{Type: TypeCredit, N: len(chunk)})
		}
		_ = sw.Close()
	}()

	lost := make(chan struct{})
	go func() {
		defer close(lost)
		defer in.close()
		for {
			m, err := ReadFrame(c)
			if err != nil {
				return
			}
			switch m.Type {
			case TypeStdin:
				if len(m.Data) == 0 {
					in.close()
					continue
				}
				if !in.push(m.Data) {
					// The host ignored the window. The frame loop cannot
					// wait for the child, so the process is stopped.
					log.Printf("guestagent: host sent stdin beyond the window; killing the process")
					in.close()
					_ = p.Kill()
				}
			case TypeSignal:
				_ = p.Signal(syscall.Signal(m.Signal))
			case TypeResize:
				_ = p.Resize(m.Rows, m.Cols)
			}
		}
	}()

	select {
	case st := <-exited:
		finish(p, c, &out, outputs, linger)
		w.write(Frame{Type: TypeExit, Code: st.code, Signal: st.sig})
	case <-lost:
		if ss.isDetached() {
			// The process runs on. Its output keeps draining, and its
			// descriptors close when it exits.
			go func() {
				<-exited
				finish(p, c, &out, outputs, linger)
			}()
			return
		}
		_ = p.Kill()
		_ = p.Close() // unblocks a stdin writer that waits on a descendant
		<-exited
		finish(p, c, &out, outputs, linger)
	}
}
