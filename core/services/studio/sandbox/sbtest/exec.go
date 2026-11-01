package sbtest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
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

func testExec(newBackend NewBackend) {
	It("exit code, stdout and stderr", func() {
		f := setup(newBackend, nil)
		r := f.run("echo out; echo err >&2; exit 3")
		Expect(r).To(Equal(result{code: 3, stdout: "out\n", stderr: "err\n"}))
	})
	It("stdin", func() {
		f := setup(newBackend, nil)
		r := f.runCmd(sandbox.Cmd{Args: []string{"cat"}, Stdin: strings.NewReader("hello\nworld\n")})
		Expect(r.code).To(Equal(0), "%+v", r)
		Expect(r.stdout).To(Equal("hello\nworld\n"))
	})
	It("env and dir", func() {
		f := setup(newBackend, nil)
		r := f.runCmd(sandbox.Cmd{Args: []string{"sh", "-c", `echo "$FOO"; basename "$PWD"`}, Env: []string{"FOO=bar"}, Dir: sandbox.DefaultDir + "/proj"})
		Expect(r.code).To(Equal(0), "%+v", r)
		Expect(r.stdout).To(Equal("bar\nproj\n"))
	})
	It("a signal stops a process", func() {
		f := setup(newBackend, nil)
		var out syncBuffer
		p, err := f.sb.Exec(ctx(), sandbox.Cmd{Args: []string{"sh", "-c", "echo started; exec sleep 60"}, Stdout: &out})
		Expect(err).NotTo(HaveOccurred())
		Expect(waitFor(10*time.Second, func() bool { return out.String() == "started\n" })).To(BeTrue(), "output = %q", out.String())
		Expect(p.Signal(syscall.SIGTERM)).To(Succeed())
		code, err := p.Wait()
		Expect(err).NotTo(HaveOccurred())
		Expect(code).To(Equal(128 + int(syscall.SIGTERM)))
	})
	It("a long-running process does not block another", func() {
		f := setup(newBackend, nil)
		var out syncBuffer
		long, err := f.sb.Exec(ctx(), sandbox.Cmd{Args: []string{"sh", "-c", "sleep 2; echo done"}, Stdout: &out})
		Expect(err).NotTo(HaveOccurred())
		Expect(f.mustRun("echo quick")).To(Equal("quick\n"))
		Expect(out.String()).To(BeEmpty(), "the long process ended too early")
		code, err := long.Wait()
		Expect(err).NotTo(HaveOccurred())
		Expect(code).To(Equal(0))
		Expect(out.String()).To(Equal("done\n"))
	})
	It("a background child does not hold the exit", func() {
		f := setup(newBackend, nil)
		start := time.Now()
		r := f.run("sleep 30 & echo started")
		Expect(r.code).To(Equal(0), "%+v", r)
		Expect(r.stdout).To(Equal("started\n"))
		Expect(time.Since(start)).To(BeNumerically("<=", 10*time.Second))
	})
	It("the end of the context kills the process", func() {
		f := setup(newBackend, nil)
		c, cancel := context.WithTimeout(ctx(), time.Second)
		defer cancel()
		p, err := f.sb.Exec(c, sandbox.Cmd{Args: []string{"sh", "-c", "sleep 2; echo late > proj/late.txt; sleep 60"}})
		Expect(err).NotTo(HaveOccurred())
		done := make(chan error, 1)
		go func() { _, err := p.Wait(); done <- err }()
		var waitErr error
		Eventually(done, 30*time.Second).Should(Receive(&waitErr), "the process outlived its context")
		Expect(errors.Is(waitErr, context.DeadlineExceeded)).To(BeTrue(), "wait err = %v", waitErr)
		// The marker is written two seconds after the start if the process lives.
		f.mustRun("sleep 3; test ! -e proj/late.txt")
	})
	It("home is writable", func() {
		f := setup(newBackend, nil)
		Expect(f.mustRun(`echo x > "$HOME/.probe" && cat "$HOME/.probe" && git config --global user.name probe && git config --global user.name`)).To(Equal("x\nprobe\n"))
	})
	It("a terminal", func() {
		f := setup(newBackend, nil)
		need(f.b, "tty")
		var out syncBuffer
		pr, pw := io.Pipe()
		p, err := f.sb.Exec(ctx(), sandbox.Cmd{
			Args:  []string{"sh", "-c", `test -t 0 && test -t 1 && echo is-a-tty; stty size; read -r line; stty size`},
			TTY:   &sandbox.TTY{Rows: 24, Cols: 80},
			Stdin: pr, Stdout: &out,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(waitFor(10*time.Second, func() bool { return strings.Contains(out.String(), "24 80") })).To(BeTrue(), "output = %q", out.String())
		Expect(p.Resize(30, 100)).To(Succeed())
		_, err = pw.Write([]byte("go\n"))
		Expect(err).NotTo(HaveOccurred())
		code, err := p.Wait()
		_ = pw.Close()
		got := out.String()
		Expect(err).NotTo(HaveOccurred())
		Expect(code).To(Equal(0))
		Expect(got).To(ContainSubstring("is-a-tty"))
		Expect(got).To(ContainSubstring("30 100"))
	})
	It("resize without a terminal fails", func() {
		f := setup(newBackend, nil)
		p, err := f.sb.Exec(ctx(), sandbox.Cmd{Args: []string{"sleep", "1"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(p.Resize(10, 10)).NotTo(Succeed(), "resize of a process without a terminal worked")
		_, _ = p.Wait()
	})
}
