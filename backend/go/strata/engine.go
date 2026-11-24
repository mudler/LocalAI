package main

// engine drives one `strata --serve` child process over its line protocol.
//
// Protocol (verified against upstream src/program/generate.cpp, --serve block):
//
//	engine -> client  READY <ctx> [stop] [multi=N]   once, when the model is loaded
//	client -> engine  GEN <max_new> [key=value ...] <id,id,...>
//	client -> engine  GENI <max_new> [key=value ...] <embeddings file> <id,id,...>   (images; needs --vision)
//	engine -> client  T <id>                         one per generated token
//	engine -> client  LP <logprob> [id:logprob ...]  after each T when logprobs=K was asked for
//	engine -> client  DONE <produced> <prompt> <prompt_ms> <decode_ms> <finish> <accepted> <offered> <resumed> ...
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
	StopResend   time.Duration // how often to send STOP again while waiting for DONE (default 1.5s)
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

	pumps sync.WaitGroup // the stdout and stderr readers

	tailMu sync.Mutex
	tail   []string // last stderr lines, for error messages
	errs   []string // ERR lines seen on stdout before READY
}

// defaultStopTimeout is how long a stopped request may take to end. Upstream
// documents stops of about 24 s on a two-GPU split, and the engine is killed
// when the time is up, so the default leaves a wide margin.
const defaultStopTimeout = 2 * time.Minute

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
		cfg.StopTimeout = defaultStopTimeout
	}
	if cfg.StopResend <= 0 {
		cfg.StopResend = 1500 * time.Millisecond
	}
	path, err := cleanExecutable(cfg.Path)
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", cfg.Path, err)
	}
	args := append(append([]string{}, cfg.Args...), "--serve")
	cmd := exec.Command(path, args...) // #nosec G204 -- absolute regular file from the model's engine_path option, no shell, arguments passed as argv
	cmd.Env = append(os.Environ(), cfg.Env...)
	setChildAttrs(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	// Own pipes instead of StdoutPipe and StderrPipe: Wait closes those as
	// soon as the process has exited, which can cut off output still unread.
	// With plain files, Wait only reaps the process and the pumps read to EOF.
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		_ = outR.Close()
		_ = outW.Close()
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = outW, errW
	closeAll := func(fs ...*os.File) {
		for _, f := range fs {
			_ = f.Close()
		}
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
		closeAll(outR, outW, errR, errW)
		return nil, fmt.Errorf("start %s: %w", cfg.Path, err)
	}
	// The child has its own copies. Drop ours so the pumps see EOF when it exits.
	closeAll(outW, errW)
	e.pumps.Add(2)
	go e.pumpStdout(outR)
	go e.pumpStderr(errR)

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

func (e *engine) pumpStdout(r io.ReadCloser) {
	defer e.pumps.Done()
	defer close(e.lines)
	defer func() { _ = r.Close() }()
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

func (e *engine) pumpStderr(r io.ReadCloser) {
	defer e.pumps.Done()
	defer func() { _ = r.Close() }()
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

// drainTimeout bounds the wait for the output readers after the engine has
// exited. They end at once unless a grandchild still holds the pipes open.
const drainTimeout = 5 * time.Second

// drained waits until both output readers have seen EOF, so that the stderr
// tail and the ERR lines are complete.
func (e *engine) drained() {
	done := make(chan struct{})
	go func() { e.pumps.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(drainTimeout):
		xlog.Warn("strata: engine output did not close after the engine exited")
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
				e.drained()
				return fmt.Errorf("%w before READY (%v)%s", errEngineDied, e.waitErr, e.startReasons())
			}
			if strings.HasPrefix(l, "ERR") {
				// The engine may say why it cannot start before it exits.
				e.errs = append(e.errs, strings.TrimSpace(strings.TrimPrefix(l, "ERR")))
				continue
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

// startReasons is the engine's own account of a failed start: its ERR lines
// on stdout, then the tail of stderr.
func (e *engine) startReasons() string {
	var parts []string
	if len(e.errs) > 0 {
		parts = append(parts, strings.Join(e.errs, "; "))
	}
	if t := e.stderrTail(); t != "" {
		parts = append(parts, t)
	}
	if len(parts) == 0 {
		return ""
	}
	return ": " + strings.Join(parts, "\n")
}

func (e *engine) tailSuffix() string {
	if t := e.stderrTail(); t != "" {
		return ": " + t
	}
	return ""
}

// pid returns the engine's process id, or 0.
func (e *engine) pid() int {
	if e.cmd != nil && e.cmd.Process != nil {
		return e.cmd.Process.Pid
	}
	return 0
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

// genRequest is one GEN (or, with Embeddings set, GENI) call.
type genRequest struct {
	MaxNew     int
	Keys       []string // sampling keys, already formatted key=value
	IDs        []int
	Embeddings string // GENI: file of image embeddings, in prompt order. No spaces or '=' in the path.
	Logprobs   *int   // top alternatives per token (0..20); nil: no LP lines
}

func (r genRequest) line() string {
	var b strings.Builder
	if r.Embeddings != "" {
		b.WriteString("GENI ")
	} else {
		b.WriteString("GEN ")
	}
	b.WriteString(strconv.Itoa(r.MaxNew))
	for _, k := range r.Keys {
		b.WriteByte(' ')
		b.WriteString(k)
	}
	if r.Logprobs != nil {
		b.WriteString(" logprobs=")
		b.WriteString(strconv.Itoa(max(0, min(*r.Logprobs, maxLogprobs))))
	}
	if r.Embeddings != "" {
		b.WriteByte(' ')
		b.WriteString(r.Embeddings)
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

// maxLogprobs is the engine's limit for logprobs=K.
const maxLogprobs = 20

// lpAlt is one alternative token of an LP line.
type lpAlt struct {
	ID int
	LP float64
}

// genToken is one generated token. When logprobs were asked for, LP is the
// log probability of the token the engine chose and Top lists the K most
// likely tokens (the chosen one may or may not be among them). It is the
// model's own distribution, before sampling, penalties and temperature.
type genToken struct {
	ID    int
	HasLP bool
	LP    float64
	Top   []lpAlt
}

// genResult is the DONE line of a request.
type genResult struct {
	Finish       string  // "stop", "length", "cancel"
	Produced     int     // tokens the engine generated (including the end token)
	PromptTokens int     // prompt length the engine saw
	Resumed      int     // prompt tokens served from the engine's conversation cache
	PromptMS     float64 // time to read the prompt
	DecodeMS     float64 // time to generate
}

// parseDone reads: DONE <produced> <prompt> <prompt_ms> <decode_ms> <finish> <accepted> <offered> <resumed> ...
func parseDone(l string) genResult {
	f := strings.Fields(l)
	var r genResult
	atoi := func(i int) int {
		if i < len(f) {
			n, _ := strconv.Atoi(f[i])
			return n
		}
		return 0
	}
	atof := func(i int) float64 {
		if i < len(f) {
			v, _ := strconv.ParseFloat(f[i], 64)
			return v
		}
		return 0
	}
	r.Produced, r.PromptTokens = atoi(1), atoi(2)
	r.PromptMS, r.DecodeMS = atof(3), atof(4)
	if len(f) > 5 {
		r.Finish = f[5]
	}
	r.Resumed = atoi(8)
	return r
}

// parseLP reads: LP <logprob> [id:logprob ...] or LP nan.
func parseLP(l string, t *genToken) {
	f := strings.Fields(l)
	t.HasLP, t.LP, t.Top = false, 0, nil
	if len(f) < 2 || f[1] == "nan" {
		return
	}
	v, err := strconv.ParseFloat(f[1], 64)
	if err != nil {
		return
	}
	t.HasLP, t.LP = true, v
	for _, a := range f[2:] {
		idStr, lpStr, ok := strings.Cut(a, ":")
		if !ok {
			continue
		}
		id, err1 := strconv.Atoi(idStr)
		lp, err2 := strconv.ParseFloat(lpStr, 64)
		if err1 == nil && err2 == nil {
			t.Top = append(t.Top, lpAlt{ID: id, LP: lp})
		}
	}
}

// generate sends one request and calls onToken for each generated token. If
// onToken returns an error, or ctx ends, the engine is asked to STOP and the
// request is read to its end so the stream stays in step. It returns the
// engine's DONE line.
func (e *engine) generate(ctx context.Context, req genRequest, onToken func(genToken) error) (genResult, error) {
	e.reqMu.Lock()
	defer e.reqMu.Unlock()
	e.busy.Set(true)
	defer e.busy.Set(false)

	// A request that is already cancelled never reaches the engine.
	if err := ctx.Err(); err != nil {
		return genResult{}, err
	}
	if !e.alive() {
		e.drained()
		return genResult{}, fmt.Errorf("%w%s", errEngineDied, e.tailSuffix())
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
		return genResult{}, fmt.Errorf("write request: %w", err)
	}

	var (
		stopped bool
		cbErr   error
		stopBy  <-chan time.Time
		resend  <-chan time.Time
		pending *genToken // with logprobs, a token waits for its LP line

		stopTimer  *time.Timer
		resendTick *time.Ticker
	)
	done := ctx.Done()
	sendStop := func() {
		if !e.canStop {
			return
		}
		if err := e.send("STOP"); err != nil {
			xlog.Warn("strata: STOP write failed", "error", err)
		}
	}
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		sendStop()
		t := time.NewTimer(e.cfg.StopTimeout)
		stopTimer = t
		stopBy = t.C
		// The engine clears its stop flag when it takes the next request off
		// its queue, so a STOP that arrives right after GEN can be lost. Send
		// it again until the request ends.
		if e.canStop {
			tk := time.NewTicker(e.cfg.StopResend)
			resendTick = tk
			resend = tk.C
		}
	}
	defer func() {
		if stopTimer != nil {
			stopTimer.Stop()
		}
		if resendTick != nil {
			resendTick.Stop()
		}
	}()
	deliver := func(t genToken) {
		if stopped || cbErr != nil {
			return // late tokens of a cancelled request are discarded
		}
		if err := onToken(t); err != nil {
			cbErr = err
			stop()
		}
	}
	flush := func() {
		if pending != nil {
			t := *pending
			pending = nil
			deliver(t)
		}
	}
	for {
		select {
		case <-done:
			done = nil
			stop()
		case <-resend:
			sendStop()
		case <-stopBy:
			xlog.Warn("strata: engine did not finish after STOP, killing it")
			e.kill()
			return genResult{}, fmt.Errorf("strata engine did not finish within %s after STOP; it was killed", e.cfg.StopTimeout)
		case l, ok := <-e.lines:
			if !ok {
				<-e.exited
				e.drained()
				return genResult{}, fmt.Errorf("%w mid-request (%v)%s", errEngineDied, e.waitErr, e.tailSuffix())
			}
			switch {
			case strings.HasPrefix(l, "T "):
				flush()
				id, err := strconv.Atoi(strings.TrimSpace(l[2:]))
				if err != nil {
					return genResult{}, fmt.Errorf("malformed token line %q", l)
				}
				if req.Logprobs != nil {
					pending = &genToken{ID: id}
				} else {
					deliver(genToken{ID: id})
				}
			case strings.HasPrefix(l, "LP"):
				if pending != nil {
					parseLP(l, pending)
					flush()
				}
			case l == "DONE" || strings.HasPrefix(l, "DONE "):
				flush()
				res := parseDone(l)
				if cbErr != nil {
					return res, cbErr
				}
				if err := ctx.Err(); err != nil && stopped {
					return res, err
				}
				return res, nil
			case strings.HasPrefix(l, "ERR"):
				msg := strings.TrimSpace(strings.TrimPrefix(l, "ERR"))
				if cbErr != nil {
					return genResult{}, cbErr
				}
				return genResult{}, fmt.Errorf("strata engine: %s", msg)
			}
			// PP, INFO and anything unknown: skipped.
		}
	}
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
