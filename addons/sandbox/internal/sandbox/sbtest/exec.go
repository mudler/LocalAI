package sbtest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
)

// syncBuffer is a bytes.Buffer that is safe to read while a process writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// waitFor polls cond every 50 ms until it is true or d passed.
func waitFor(d time.Duration, cond func() bool) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if cond() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return cond()
}

func testExec(t *testing.T, newBackend NewBackend) {
	t.Run("exit code, stdout and stderr", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		r := f.run("echo out; echo err >&2; exit 3")
		if r.code != 3 || r.stdout != "out\n" || r.stderr != "err\n" {
			t.Fatalf("result = %+v", r)
		}
	})
	t.Run("stdin", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		r := f.runCmd(sandbox.Cmd{Args: []string{"cat"}, Stdin: strings.NewReader("hello\nworld\n")})
		if r.code != 0 || r.stdout != "hello\nworld\n" {
			t.Fatalf("result = %+v", r)
		}
	})
	t.Run("env and dir", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		r := f.runCmd(sandbox.Cmd{Args: []string{"sh", "-c", `echo "$FOO"; basename "$PWD"`}, Env: []string{"FOO=bar"}, Dir: sandbox.DefaultDir + "/proj"})
		if r.code != 0 || r.stdout != "bar\nproj\n" {
			t.Fatalf("result = %+v", r)
		}
	})
	t.Run("a signal stops a process", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		var out syncBuffer
		p, err := f.sb.Exec(ctx(t), sandbox.Cmd{Args: []string{"sh", "-c", "echo started; exec sleep 60"}, Stdout: &out})
		if err != nil {
			t.Fatal(err)
		}
		if !waitFor(10*time.Second, func() bool { return out.String() == "started\n" }) {
			t.Fatalf("output = %q", out.String())
		}
		if err := p.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		code, err := p.Wait()
		if err != nil || code != 128+int(syscall.SIGTERM) {
			t.Fatalf("wait = %d, %v", code, err)
		}
	})
	t.Run("a long-running process does not block another", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		var out syncBuffer
		long, err := f.sb.Exec(ctx(t), sandbox.Cmd{Args: []string{"sh", "-c", "sleep 2; echo done"}, Stdout: &out})
		if err != nil {
			t.Fatal(err)
		}
		if got := f.mustRun("echo quick"); got != "quick\n" {
			t.Fatalf("quick = %q", got)
		}
		if out.String() != "" {
			t.Fatal("the long process ended too early")
		}
		if code, err := long.Wait(); err != nil || code != 0 || out.String() != "done\n" {
			t.Fatalf("long = %d, %v, %q", code, err, out.String())
		}
	})
	t.Run("a background child does not hold the exit", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		start := time.Now()
		r := f.run("sleep 30 & echo started")
		if r.code != 0 || r.stdout != "started\n" || time.Since(start) > 10*time.Second {
			t.Fatalf("result = %+v after %v", r, time.Since(start))
		}
	})
	t.Run("the end of the context kills the process", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		c, cancel := context.WithTimeout(ctx(t), time.Second)
		defer cancel()
		p, err := f.sb.Exec(c, sandbox.Cmd{Args: []string{"sh", "-c", "sleep 2; echo late > proj/late.txt; sleep 60"}})
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { _, err := p.Wait(); done <- err }()
		select {
		case err := <-done:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("wait err = %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("the process outlived its context")
		}
		// The marker is written two seconds after the start if the process lives.
		f.mustRun("sleep 3; test ! -e proj/late.txt")
	})
	t.Run("home is writable", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		if got := f.mustRun(`echo x > "$HOME/.probe" && cat "$HOME/.probe" && git config --global user.name probe && git config --global user.name`); got != "x\nprobe\n" {
			t.Fatalf("output = %q", got)
		}
	})
	t.Run("a terminal", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		need(t, f.b, "tty")
		var out syncBuffer
		pr, pw := io.Pipe()
		p, err := f.sb.Exec(ctx(t), sandbox.Cmd{
			Args:  []string{"sh", "-c", `test -t 0 && test -t 1 && echo is-a-tty; stty size; read -r line; stty size`},
			TTY:   &sandbox.TTY{Rows: 24, Cols: 80},
			Stdin: pr, Stdout: &out,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !waitFor(10*time.Second, func() bool { return strings.Contains(out.String(), "24 80") }) {
			t.Fatalf("output = %q", out.String())
		}
		if err := p.Resize(30, 100); err != nil {
			t.Fatal(err)
		}
		if _, err := pw.Write([]byte("go\n")); err != nil {
			t.Fatal(err)
		}
		code, err := p.Wait()
		_ = pw.Close()
		got := out.String()
		if err != nil || code != 0 || !strings.Contains(got, "is-a-tty") || !strings.Contains(got, "30 100") {
			t.Fatalf("wait = %d, %v, output %q", code, err, got)
		}
	})
	t.Run("resize without a terminal fails", func(t *testing.T) {
		f := setup(t, newBackend, nil)
		p, err := f.sb.Exec(ctx(t), sandbox.Cmd{Args: []string{"sleep", "1"}})
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Resize(10, 10); err == nil {
			t.Fatal("resize of a process without a terminal worked")
		}
		_, _ = p.Wait()
	})
}
