package worker

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Worker heartbeat loop", func() {
	var (
		ctx    context.Context
		cancel context.CancelFunc
		tick   chan time.Time
		sent   chan struct{}
		done   chan struct{}
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		tick = make(chan time.Time)
		sent = make(chan struct{}, 8)
		done = make(chan struct{})
	})

	AfterEach(func() {
		cancel()
		Eventually(done, "5s").Should(BeClosed())
	})

	// tick delivers one tick, and fails the spec rather than parking forever if
	// the loop has stopped reading. A loop that returned early would otherwise
	// hang the suite instead of reporting a failure.
	fire := func() {
		select {
		case tick <- time.Now():
		case <-time.After(5 * time.Second):
			Fail("the heartbeat loop stopped reading its ticker")
		}
	}

	run := func(send func(context.Context) error) {
		go func() {
			defer close(done)
			heartbeatLoop(ctx, tick, send)
		}()
	}

	It("posts a heartbeat on every tick", func() {
		run(func(context.Context) error {
			sent <- struct{}{}
			return nil
		})
		for i := 0; i < 3; i++ {
			fire()
			Eventually(sent, "5s").Should(Receive(), "tick %d produced no heartbeat", i+1)
		}
	})

	// The heartbeat is the worker's own answer that its process is alive, and
	// the frontend reads absence from the tunnel it holds, aged against the
	// reconnect grace. A loop that gave up on a failing post would let one
	// frontend restart silence a worker for the rest of its life, and the
	// health monitor marks a silent node offline with no grace at all.
	It("keeps posting after a heartbeat fails", func() {
		run(func(context.Context) error {
			sent <- struct{}{}
			return errors.New("frontend unreachable")
		})
		for i := 0; i < 3; i++ {
			fire()
			Eventually(sent, "5s").Should(Receive(), "tick %d produced no heartbeat after a failure", i+1)
		}
	})

	It("stops once the shutdown context is cancelled", func() {
		run(func(context.Context) error {
			sent <- struct{}{}
			return nil
		})
		fire()
		Eventually(sent, "5s").Should(Receive())
		cancel()
		Eventually(done, "5s").Should(BeClosed())
	})
})
