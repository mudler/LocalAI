package chat

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// unusedPort is a loopback address nothing listens on, used wherever a spec
// needs a readiness poll to keep failing. Port 1 is privileged, so no test
// process could have bound it.
const unusedPort = "http://127.0.0.1:1"

var _ = Describe("OfferToStart", func() {
	It("never spawns anything when there is no confirmer", func() {
		started, err := OfferToStart(context.Background(), StartOptions{
			Endpoint:   "http://127.0.0.1:59999",
			Confirm:    nil,
			Stderr:     io.Discard,
			Executable: "/nonexistent/binary-that-must-not-run",
		})
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrDeclined)).To(BeTrue(), "want ErrDeclined, got %v", err)
		Expect(started).To(BeNil())
	})

	It("does not spawn when the user declines", func() {
		asked := false
		started, err := OfferToStart(context.Background(), StartOptions{
			Endpoint: "http://127.0.0.1:59999",
			Confirm: func(string) (bool, error) {
				asked = true
				return false, nil
			},
			Stderr:     io.Discard,
			Executable: "/nonexistent/binary-that-must-not-run",
		})
		Expect(asked).To(BeTrue(), "the user should have been asked")
		Expect(errors.Is(err, ErrDeclined)).To(BeTrue())
		Expect(started).To(BeNil())
	})

	It("names the endpoint in the question", func() {
		var question string
		_, _ = OfferToStart(context.Background(), StartOptions{
			Endpoint: "http://example.invalid:9090",
			Confirm: func(q string) (bool, error) {
				question = q
				return false, nil
			},
			Stderr:     io.Discard,
			Executable: "/nonexistent/binary-that-must-not-run",
		})
		Expect(question).To(ContainSubstring("http://example.invalid:9090"))
	})

	It("propagates a confirmer error", func() {
		boom := errors.New("boom")
		_, err := OfferToStart(context.Background(), StartOptions{
			Endpoint:   "http://127.0.0.1:59999",
			Confirm:    func(string) (bool, error) { return false, boom },
			Stderr:     io.Discard,
			Executable: "/nonexistent/binary-that-must-not-run",
		})
		Expect(errors.Is(err, boom)).To(BeTrue())
	})

	It("reports which binary it failed to launch", func() {
		started, err := OfferToStart(context.Background(), StartOptions{
			Endpoint:   "http://127.0.0.1:59999",
			Confirm:    func(string) (bool, error) { return true, nil },
			Stderr:     io.Discard,
			Executable: "/nonexistent/binary-that-must-not-run",
		})
		Expect(started).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("starting a LocalAI server")))
		Expect(err).To(MatchError(ContainSubstring("/nonexistent/binary-that-must-not-run")))
	})

	It("stops waiting as soon as the process it started exits", func() {
		// A harmless no-op binary rather than a real server: this exercises the
		// early-exit path without starting LocalAI, binding a port, or running
		// 'local-ai run'. Without early-exit detection the call would sit here
		// polling until ReadyTimeout.
		bin, lookErr := exec.LookPath("true")
		if lookErr != nil {
			Skip("no 'true' binary on PATH to stand in for a server that dies at once")
		}

		start := time.Now()
		started, err := OfferToStart(context.Background(), StartOptions{
			Endpoint:     unusedPort,
			Confirm:      func(string) (bool, error) { return true, nil },
			Stderr:       io.Discard,
			Executable:   bin,
			ReadyTimeout: 30 * time.Second,
		})
		Expect(started).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("exited before it became ready")))
		Expect(time.Since(start)).To(BeNumerically("<", 10*time.Second),
			"the wait should end with the process, not with the readiness budget")
	})

	It("gives up on a child whose grandchildren still hold its output pipe", func() {
		// The real LocalAI shape: 'local-ai run' exits but a backend
		// subprocess it spawned inherited the stderr pipe and keeps it open.
		// Without cmd.WaitDelay, cmd.Wait blocks on the copy goroutine, exited
		// never closes, and the readiness wait runs out the full budget instead
		// of reporting that the server died.
		sh, lookErr := exec.LookPath("sh")
		if lookErr != nil {
			Skip("no 'sh' binary on PATH to stand in for a server with a lingering child")
		}

		dir := GinkgoT().TempDir()
		pidFile := filepath.Join(dir, "grandchild.pid")
		script := filepath.Join(dir, "server-with-lingering-child")
		// #nosec G306 -- this has to be executable to stand in for a binary.
		Expect(os.WriteFile(script,
			[]byte("#!"+sh+"\nsleep 30 &\necho $! > "+pidFile+"\nexit 0\n"),
			0o700)).To(Succeed())

		// Reap the grandchild whatever happens: it outlives its own parent by
		// design, so nothing else will clean it up.
		DeferCleanup(func() {
			raw, err := os.ReadFile(pidFile)
			if err != nil {
				return
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
			if err != nil {
				return
			}
			proc, err := os.FindProcess(pid)
			if err != nil {
				return
			}
			_ = proc.Kill()
			_, _ = proc.Wait()
		})

		start := time.Now()
		started, err := OfferToStart(context.Background(), StartOptions{
			Endpoint:     unusedPort,
			Confirm:      func(string) (bool, error) { return true, nil },
			Stderr:       io.Discard,
			Executable:   script,
			ReadyTimeout: 25 * time.Second,
		})
		elapsed := time.Since(start)

		Expect(started).To(BeNil())
		Expect(err).To(MatchError(ContainSubstring("exited before it became ready")),
			"an unbounded cmd.Wait would report a readiness timeout instead")
		Expect(elapsed).To(BeNumerically("<", 20*time.Second),
			"the wait must be bounded by the output drain, not by the readiness budget")

		// This is the case where cmd.Wait returns exec.ErrWaitDelay, whose own
		// text names a struct field of os/exec. Users get told what happened
		// instead.
		Expect(err).NotTo(MatchError(ContainSubstring("WaitDelay")),
			"os/exec plumbing must not reach the user")
		Expect(err).NotTo(MatchError(ContainSubstring("exec:")))
		Expect(err).To(MatchError(ContainSubstring("left a subprocess of its own still running")))
	})

	It("reports the exit status of a server that failed outright", func() {
		// The counterpart to the case above: translating ErrWaitDelay must not
		// cost a real exit status, which is the one diagnostic worth having.
		bin, lookErr := exec.LookPath("false")
		if lookErr != nil {
			Skip("no 'false' binary on PATH to stand in for a server that fails")
		}

		_, err := OfferToStart(context.Background(), StartOptions{
			Endpoint:     unusedPort,
			Confirm:      func(string) (bool, error) { return true, nil },
			Stderr:       io.Discard,
			Executable:   bin,
			ReadyTimeout: 30 * time.Second,
		})
		Expect(err).To(MatchError(ContainSubstring("exited before it became ready")))
		Expect(err).To(MatchError(ContainSubstring("exit status 1")))
	})
})

var _ = Describe("StartedServer.Stop", func() {
	It("is a no-op on a server that was never started", func() {
		var nilServer *StartedServer
		Expect(nilServer.Stop).NotTo(Panic())
		Expect((&StartedServer{}).Stop).NotTo(Panic())
	})

	It("interrupts the child exactly once however often it is called", func() {
		s, proc := stoppableServer()

		s.Stop()
		s.Stop()
		s.Stop()

		Expect(proc.interrupts.Load()).To(Equal(int32(1)),
			"a second Stop must not signal the child again")
		Expect(proc.kills.Load()).To(BeZero(), "a child that already exited must not be killed")
	})

	It("interrupts the child exactly once when called concurrently", func() {
		// The realistic double-Stop: a deferred Stop on the way out racing the
		// signal handler that also owns shutting the server down.
		const callers = 8

		s, proc := stoppableServer()

		var wg sync.WaitGroup
		wg.Add(callers)
		for range callers {
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				s.Stop()
			}()
		}
		wg.Wait()

		Expect(proc.interrupts.Load()).To(Equal(int32(1)))
		Expect(proc.kills.Load()).To(BeZero())
	})

	It("asks the child to interrupt rather than killing it outright", func() {
		// The escalation order is the whole point of the grace period: SIGKILL
		// first would strand the backend subprocesses local-ai run owns.
		s, proc := stoppableServer()

		s.Stop()

		Expect(proc.lastSignal.Load()).To(Equal(os.Interrupt))
		Expect(proc.kills.Load()).To(BeZero())
	})
})

// countingProcess stands in for the *os.Process that Stop drives, recording
// what it was asked to do.
type countingProcess struct {
	interrupts atomic.Int32
	kills      atomic.Int32
	lastSignal atomic.Value
}

func (p *countingProcess) Signal(sig os.Signal) error {
	p.interrupts.Add(1)
	p.lastSignal.Store(sig)
	return nil
}

func (p *countingProcess) Kill() error {
	p.kills.Add(1)
	return nil
}

// stoppableServer builds a StartedServer whose child has already exited, driven
// by a countingProcess rather than a real one. Nothing is spawned.
func stoppableServer() (*StartedServer, *countingProcess) {
	proc := &countingProcess{}
	exited := make(chan struct{})
	close(exited)
	return &StartedServer{exited: exited, proc: proc}, proc
}

var _ = Describe("newServerCommand", func() {
	It("bounds how long it will wait for the child's output pipes", func() {
		cmd := newServerCommand("/nonexistent/binary-that-must-not-run", io.Discard)

		// An unbounded wait is the failure mode: backend subprocesses inherit
		// the child's stderr pipe and can hold it open long after the server
		// itself is gone.
		Expect(cmd.WaitDelay).To(BeNumerically(">", 0), "cmd.Wait must not be unbounded")
		Expect(cmd.WaitDelay).To(BeNumerically("<", shutdownGrace),
			"a drain longer than the shutdown grace would kill a cleanly exited server")
	})

	It("runs the server subcommand without giving it the terminal", func() {
		cmd := newServerCommand("/nonexistent/binary-that-must-not-run", io.Discard)

		Expect(cmd.Args).To(Equal([]string{"/nonexistent/binary-that-must-not-run", "run"}))
		Expect(cmd.Stdin).To(BeNil(), "the child must not compete with the agent for stdin")
		Expect(cmd.Stdout).NotTo(BeNil())
		Expect(cmd.Stderr).NotTo(BeNil())
	})
})

var _ = Describe("waitReady", func() {
	It("polls /readyz on the endpoint root and returns only once it answers 200", func() {
		// readyOnPoll is deliberately above 1. A handler that answers 200 to the
		// first poll cannot tell a correct implementation apart from one that
		// treats 503 as ready, because both return after a single request; the
		// poll count is what makes 503-as-ready observable.
		const readyOnPoll = 3

		var polls atomic.Int32
		var paths atomic.Value
		paths.Store("")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			paths.Store(r.URL.Path)
			if polls.Add(1) < readyOnPoll {
				// What LocalAI answers while startup is still in progress.
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		Expect(waitReady(context.Background(), srv.URL, 20*time.Second, nil)).To(Succeed())
		Expect(paths.Load()).To(Equal("/readyz"), "readiness lives on the endpoint root, not under /v1")
		Expect(polls.Load()).To(BeNumerically(">=", readyOnPoll),
			"503 means startup is still in progress and must never be accepted as ready")
	})

	It("tolerates a trailing slash on the endpoint", func() {
		var path atomic.Value
		path.Store("")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path.Store(r.URL.Path)
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		Expect(waitReady(context.Background(), srv.URL+"/", 20*time.Second, nil)).To(Succeed())
		Expect(path.Load()).To(Equal("/readyz"))
	})

	It("reports a timeout, not a cancellation, when the budget runs out", func() {
		err := waitReady(context.Background(), unusedPort, 1200*time.Millisecond, nil)
		Expect(err).To(HaveOccurred())
		// A budget built from context.WithCancel plus a timer would surface as
		// context.Canceled, which downstream code reads as "the caller gave up"
		// and would stop classifying a hung server as unreachable.
		Expect(errors.Is(err, context.Canceled)).To(BeFalse(), "got %v", err)
		Expect(err).To(MatchError(ContainSubstring("did not become ready")))
	})

	It("returns the caller's cancellation when the caller gives up", func() {
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			defer GinkgoRecover()
			time.Sleep(200 * time.Millisecond)
			cancel()
		}()
		defer cancel()

		err := waitReady(ctx, unusedPort, time.Minute, nil)
		Expect(errors.Is(err, context.Canceled)).To(BeTrue(), "got %v", err)
	})

	It("gives up when the process it is waiting on has exited", func() {
		exited := make(chan struct{})
		close(exited)

		err := waitReady(context.Background(), unusedPort, time.Minute, exited)
		Expect(err).To(MatchError(ContainSubstring("exited before it became ready")))
	})
})
