package main

// engine drives one `strata --serve` child process over its line protocol.
//
// Protocol (verified against upstream src/program/generate.cpp, --serve block):
//
//	engine -> client  READY <ctx> [stop] [multi=N]   once, when the model is loaded
//	client -> engine  GEN <max_new> [key=value ...] <id,id,...>
//	engine -> client  T <id>                         one per generated token
//	engine -> client  LP ...                         optional logprobs, ignored here
//	engine -> client  DONE <produced> <prompt> <prompt_ms> <decode_ms> <finish> ...
//	engine -> client  ERR <message>                  ends the request
//	client -> engine  STOP                           cancel the request in flight
//	client -> engine  QUIT                           leave the serve loop
//
// Other lines (INFO, PP, YIELDED, ...) are progress or diagnostics and are
// skipped. The engine serves one request at a time unless it is started with
// --batch, which this backend does not use.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mudler/xlog"
)

// engineConfig is everything needed to start and talk to the engine.
type engineConfig struct {
	Path         string        // engine executable
	Args         []string      // arguments placed before --serve (--pack, --max-context, ...)
	Env          []string      // extra KEY=VALUE entries
	ReadyTimeout time.Duration // how long to wait for READY
	StopTimeout  time.Duration // how long to wait for DONE after STOP before killing the engine
}

type engine struct {
	cfg engineConfig
	cmd *exec.Cmd

	stdin   io.WriteCloser
	lines   chan string   // lines from stdout; closed when the stream ends
	exited  chan struct{} // closed when the process has been reaped
	waitErr error         // valid once exited is closed

	ctxLen  int
	canStop bool

	reqMu sync.Mutex // one request at a time
	wmu   sync.Mutex // serializes writes to stdin (STOP may race with a request line)

	busy atomicBool

	tailMu sync.Mutex
	tail   []string // last stderr lines, for error messages
}

var (
	errEngineDied = errors.New("strata engine exited")
	errNotReady   = errors.New("strata engine did not report READY")
)

// startEngine spawns the engine and waits for READY.
func startEngine(cfg engineConfig) (*engine, error) {
	if cfg.ReadyTimeout <= 0 {
		cfg.ReadyTimeout = 15 * time.Minute
	}
	if cfg.StopTimeout <= 0 {
		cfg.StopTimeout = 30 * time.Second
	}
	args := append(append([]string{}, cfg.Args...), "--serve")
	cmd := exec.Command(cfg.Path, args...)
	cmd.Env = append(os.Environ(), cfg.Env...)
	setChildAttrs(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	e := &engine{
		cfg:    cfg,
		cmd:    cmd,
		stdin:  stdin,
		lines:  make(chan string, 1024),
		exited: make(chan struct{}),
	}

	started := make(chan error, 1)
	go e.supervise(started)
	if err := <-started; err != nil {
		return nil, fmt.Errorf("start %s: %w", cfg.Path, err)
	}
	go e.pumpStdout(stdout)
	go e.pumpStderr(stderr)

	if err := e.waitReady(); err != nil {
		e.kill()
		return nil, err
	}
	return e, nil
}

// supervise starts the process and reaps it. The goroutine stays pinned to
// one OS thread for the life of the child: Linux ties PR_SET_PDEATHSIG to the
// thread that forked, so the thread must not exit while the child runs.
func (e *engine) supervise(started chan<- error) {
	lockThreadForChild()
	err := e.cmd.Start()
	started <- err
	if err != nil {
		close(e.exited)
		return
	}
	e.waitErr = e.cmd.Wait()
	close(e.exited)
}

func (e *engine) pumpStdout(r io.Reader) {
	defer close(e.lines)
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		s, err := br.ReadString('\n')
		if s != "" {
			e.lines <- strings.TrimRight(s, "\r\n")
		}
		if err != nil {
			return
		}
	}
}

func (e *engine) pumpStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		l := sc.Text()
		xlog.Debug("strata engine", "stderr", l)
		e.tailMu.Lock()
		e.tail = append(e.tail, l)
		if len(e.tail) > 20 {
			e.tail = e.tail[1:]
		}
		e.tailMu.Unlock()
	}
}

func (e *engine) stderrTail() string {
	e.tailMu.Lock()
	defer e.tailMu.Unlock()
	return strings.Join(e.tail, "\n")
}

func (e *engine) waitReady() error {
	timer := time.NewTimer(e.cfg.ReadyTimeout)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return fmt.Errorf("%w within %s", errNotReady, e.cfg.ReadyTimeout)
		case l, ok := <-e.lines:
			if !ok {
				<-e.exited
				return fmt.Errorf("%w before READY (%v)%s", errEngineDied, e.waitErr, e.tailSuffix())
			}
			if !strings.HasPrefix(l, "READY") {
				continue
			}
			f := strings.Fields(l)
			if len(f) < 2 {
				return fmt.Errorf("malformed READY line %q", l)
			}
			n, err := strconv.Atoi(f[1])
			if err != nil || n <= 0 {
				return fmt.Errorf("malformed READY line %q", l)
			}
			e.ctxLen = n
			for _, w := range f[2:] {
				if w == "stop" {
					e.canStop = true
				}
			}
			return nil
		}
	}
}

func (e *engine) tailSuffix() string {
	if t := e.stderrTail(); t != "" {
		return ": " + t
	}
	return ""
}

func (e *engine) alive() bool {
	select {
	case <-e.exited:
		return false
	default:
		return true
	}
}

func (e *engine) send(line string) error {
	e.wmu.Lock()
	defer e.wmu.Unlock()
	_, err := io.WriteString(e.stdin, line+"\n")
	return err
}

// genRequest is one GEN call.
type genRequest struct {
	MaxNew int
	Keys   []string // sampling keys, already formatted key=value
	IDs    []int
}

func (r genRequest) line() string {
	var b strings.Builder
	b.WriteString("GEN ")
	b.WriteString(strconv.Itoa(r.MaxNew))
	for _, k := range r.Keys {
		b.WriteByte(' ')
		b.WriteString(k)
	}
	b.WriteByte(' ')
	for i, id := range r.IDs {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(id))
	}
	return b.String()
}

// generate sends one request and calls onToken for each generated token. If
// onToken returns an error, or ctx ends, the engine is asked to STOP and the
// request is read to its end so the stream stays in step. It returns the
// engine's finish reason ("stop", "length", "cancel").
func (e *engine) generate(ctx context.Context, req genRequest, onToken func(id int) error) (string, error) {
	e.reqMu.Lock()
	defer e.reqMu.Unlock()
	e.busy.Set(true)
	defer e.busy.Set(false)

	if !e.alive() {
		return "", fmt.Errorf("%w%s", errEngineDied, e.tailSuffix())
	}
	// A previous request that was read to its end leaves nothing behind but
	// diagnostics; drop them so they cannot be mistaken for this one's output.
	for drained := false; !drained; {
		select {
		case <-e.lines:
		default:
			drained = true
		}
	}
	if err := e.send(req.line()); err != nil {
		return "", fmt.Errorf("write request: %w", err)
	}

	var (
		stopped bool
		cbErr   error
		stopBy  <-chan time.Time
	)
	done := ctx.Done()
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		if e.canStop {
			if err := e.send("STOP"); err != nil {
				xlog.Warn("strata: STOP write failed", "error", err)
			}
		}
		t := time.NewTimer(e.cfg.StopTimeout)
		stopBy = t.C
		// timer is collected with the process if it fires
	}
	for {
		select {
		case <-done:
			done = nil
			stop()
		case <-stopBy:
			xlog.Warn("strata: engine did not finish after STOP, killing it")
			e.kill()
			return "", fmt.Errorf("strata engine did not finish within %s after STOP; it was killed", e.cfg.StopTimeout)
		case l, ok := <-e.lines:
			if !ok {
				<-e.exited
				return "", fmt.Errorf("%w mid-request (%v)%s", errEngineDied, e.waitErr, e.tailSuffix())
			}
			switch {
			case strings.HasPrefix(l, "T "):
				if stopped {
					continue // late tokens of a cancelled request are discarded
				}
				id, err := strconv.Atoi(strings.TrimSpace(l[2:]))
				if err != nil {
					return "", fmt.Errorf("malformed token line %q", l)
				}
				if err := onToken(id); err != nil {
					cbErr = err
					stop()
				}
			case l == "DONE" || strings.HasPrefix(l, "DONE "):
				finish := doneFinish(l)
				if cbErr != nil {
					return finish, cbErr
				}
				if err := ctx.Err(); err != nil && stopped {
					return finish, err
				}
				return finish, nil
			case strings.HasPrefix(l, "ERR"):
				msg := strings.TrimSpace(strings.TrimPrefix(l, "ERR"))
				if cbErr != nil {
					return "", cbErr
				}
				return "", fmt.Errorf("strata engine: %s", msg)
			}
			// LP, PP, INFO and anything unknown: skipped.
		}
	}
}

// doneFinish extracts the finish reason: DONE <produced> <prompt> <prompt_ms> <decode_ms> <finish> ...
func doneFinish(l string) string {
	f := strings.Fields(l)
	if len(f) > 5 {
		return f[5]
	}
	return ""
}

// kill ends the engine at once and waits for it.
func (e *engine) kill() {
	if e.cmd.Process != nil {
		_ = e.cmd.Process.Kill()
	}
	<-e.exited
}

// close asks the engine to leave its serve loop and kills it if it does not.
func (e *engine) close() {
	if !e.alive() {
		return
	}
	_ = e.send("QUIT")
	_ = e.stdin.Close()
	select {
	case <-e.exited:
	case <-time.After(10 * time.Second):
		e.kill()
	}
}
