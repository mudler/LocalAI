package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mudler/xlog"
)

const tailLines = 40

// lineTail keeps the last lines a process wrote, for error messages.
type lineTail struct {
	mu    sync.Mutex
	lines []string
}

func (t *lineTail) add(l string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lines = append(t.lines, l)
	if len(t.lines) > tailLines {
		t.lines = t.lines[len(t.lines)-tailLines:]
	}
}

func (t *lineTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.lines, "\n")
}

// sidecar is one running `coli serve` on a loopback port.
type sidecar struct {
	baseURL string
	apiKey  string
	client  *http.Client

	cmd  *exec.Cmd
	pid  int
	tail lineTail

	done    chan struct{} // closed when the process has exited and its output is read
	exitErr error         // valid after done is closed

	stopOnce sync.Once
}

// freePort asks the kernel for a free loopback port. Another process can
// take it before coli binds it; the start then fails with the bind error in
// the output tail and the caller tries again.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func randomKey() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// baseEnv is this process's environment without the backend's own
// COLIBRI_* settings: coli checks every variable that starts with COLI and
// warns about the ones it does not know.
func baseEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "COLIBRI_") {
			env = append(env, kv)
		}
	}
	return env
}

// childEnv is the environment of coli. The API key keeps other local users
// off the loopback port. TMPDIR is where coli keeps its pid and stop files.
func childEnv(c loadConfig, key, stateDir string) []string {
	env := append(baseEnv(),
		"COLI_API_KEY="+key,
		"PYTHONUNBUFFERED=1",
		"PYTHONDONTWRITEBYTECODE=1",
	)
	if stateDir != "" {
		env = append(env, "TMPDIR="+stateDir)
	}
	return append(env, c.env...)
}

// startSidecar starts coli serve and waits until its /health answers ok.
// A start that fails reports the exit status and the last lines coli wrote.
func startSidecar(ctx context.Context, c loadConfig, stateDir string) (*sidecar, error) {
	port, err := freePort()
	if err != nil {
		return nil, fmt.Errorf("colibri: no free loopback port: %w", err)
	}
	key, err := randomKey()
	if err != nil {
		return nil, err
	}
	args := append(c.serveArgs(), "--host", "127.0.0.1", "--port", strconv.Itoa(port))
	prog, argv := c.command(args...)

	s := &sidecar{
		baseURL: "http://127.0.0.1:" + strconv.Itoa(port),
		apiKey:  key,
		client:  &http.Client{},
		done:    make(chan struct{}),
	}
	// #nosec G204 -- prog is the packaged coli (or the coli_path/python model options an admin sets); argv is passed without a shell
	cmd := exec.Command(prog, argv...)
	cmd.Env = childEnv(c, key, stateDir)
	cmd.Dir = c.modelDir
	setChildAttrs(cmd)
	s.cmd = cmd

	// One pipe for stdout and stderr: coli prints its start errors on either.
	// The read end sees EOF only when every writer is gone, the engine
	// grandchild included, so the supervisor waits for it with a bound.
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout, cmd.Stderr = w, w

	started := make(chan error, 1)
	go s.supervise(r, w, started)
	if err := <-started; err != nil {
		return nil, fmt.Errorf("colibri: cannot start %s: %w", prog, err)
	}
	xlog.Info("colibri: coli serve started", "pid", s.pid, "port", port, "model", c.modelDir)

	if err := s.waitReady(ctx, c.startTimeout); err != nil {
		s.stop(c.stopTimeout)
		return nil, err
	}
	return s, nil
}

// supervise starts the process and stays with it until it exits. On Linux
// the child dies with the thread that started it (PDEATHSIG), so this
// goroutine keeps its OS thread for the life of the child.
func (s *sidecar) supervise(r, w *os.File, started chan<- error) {
	lockThreadForChild()

	pumped := make(chan struct{})
	err := s.cmd.Start()
	_ = w.Close()
	if err != nil {
		_ = r.Close()
		s.exitErr = err
		close(s.done)
		started <- err
		return
	}
	s.pid = s.cmd.Process.Pid
	started <- nil

	go func() {
		defer close(pumped)
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for sc.Scan() {
			l := sc.Text()
			s.tail.add(l)
			xlog.Debug("colibri: " + l)
		}
	}()

	s.exitErr = s.cmd.Wait()
	// The gateway is gone; anything it started (the engine) goes with it.
	killGroup(s.pid)
	// Read what is left in the pipe before anyone reports the exit, or the
	// reason coli printed last is missing from the error.
	select {
	case <-pumped:
	case <-time.After(3 * time.Second):
	}
	_ = r.Close()
	close(s.done)
}

func (s *sidecar) alive() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
}

// exitReason describes how the process ended, with its output tail.
func (s *sidecar) exitReason() string {
	reason := "coli serve exited"
	if s.exitErr != nil {
		reason += ": " + s.exitErr.Error()
	}
	if t := s.tail.String(); t != "" {
		reason += "\n--- last output of coli serve ---\n" + t
	}
	return reason
}

// health is the body of GET /health.
type health struct {
	Status string `json:"status"`
	Arch   string `json:"arch"`
}

// probe asks /health once. ok is true when the engine is up; status is the
// gateway's own word for its state ("ok", "restarting", ...).
func (s *sidecar) probe(ctx context.Context) (status string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+"/health", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var h health
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&h)
	if h.Status == "" {
		h.Status = strconv.Itoa(resp.StatusCode)
	}
	return h.Status, nil
}

func (s *sidecar) waitReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-s.done:
			return errors.New("colibri: " + s.exitReason())
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("colibri: coli serve was not ready after %s\n--- last output of coli serve ---\n%s", timeout, s.tail.String())
		case <-tick.C:
			if st, err := s.probe(ctx); err == nil && st == "ok" {
				return nil
			}
		}
	}
}

// stop ends the process group: SIGTERM first, so the engine can save its
// state, then SIGKILL after the timeout.
func (s *sidecar) stop(timeout time.Duration) {
	s.stopOnce.Do(func() {
		if !s.alive() {
			return
		}
		termGroup(s.pid)
		select {
		case <-s.done:
			return
		case <-time.After(timeout):
		}
		xlog.Warn("colibri: coli serve did not stop in time, killing it", "pid", s.pid)
		killGroup(s.pid)
		<-s.done
	})
}

// post sends a JSON request to the gateway.
func (s *sidecar) post(ctx context.Context, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+path, strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	return s.client.Do(req)
}
