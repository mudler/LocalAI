package chat

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
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
})

var _ = Describe("StartedServer.Stop", func() {
	It("is a no-op on a server that was never started", func() {
		var nilServer *StartedServer
		Expect(nilServer.Stop).NotTo(Panic())
		Expect((&StartedServer{}).Stop).NotTo(Panic())
	})
})

var _ = Describe("waitReady", func() {
	It("polls /readyz on the endpoint root and returns once it answers 200", func() {
		var ready atomic.Bool
		var paths atomic.Value
		paths.Store("")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			paths.Store(r.URL.Path)
			if !ready.Load() {
				// What LocalAI answers while startup is still in progress.
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		go func() {
			defer GinkgoRecover()
			time.Sleep(750 * time.Millisecond)
			ready.Store(true)
		}()

		Expect(waitReady(context.Background(), srv.URL, 20*time.Second, nil)).To(Succeed())
		Expect(paths.Load()).To(Equal("/readyz"), "readiness lives on the endpoint root, not under /v1")
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
