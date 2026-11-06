package signals

import (
	"context"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("terminate", func() {
	It("runs drain handlers before the hooks", func() {
		var order []string
		drain := func(ctx context.Context) { order = append(order, "drain") }
		hook := func() { order = append(order, "hook") }
		terminate([]func(context.Context){drain}, []func(){hook}, time.Second, time.Second)
		Expect(order).To(Equal([]string{"drain", "hook"}))
	})

	It("gives a drain handler its timeout and then moves on", func() {
		var hooked atomic.Bool
		drain := func(ctx context.Context) { <-ctx.Done() }
		start := time.Now()
		terminate([]func(context.Context){drain}, []func(){func() { hooked.Store(true) }}, 100*time.Millisecond, time.Second)
		Expect(hooked.Load()).To(BeTrue())
		Expect(time.Since(start)).To(BeNumerically("<", 900*time.Millisecond))
	})

	It("does not wait for a drain handler that ignores its context", func() {
		release := make(chan struct{})
		defer close(release)
		drain := func(context.Context) { <-release }
		start := time.Now()
		terminate([]func(context.Context){drain}, nil, 50*time.Millisecond, time.Second)
		Expect(time.Since(start)).To(BeNumerically("<", 2*time.Second))
	})

	It("does not wait forever for a hook", func() {
		release := make(chan struct{})
		defer close(release)
		start := time.Now()
		terminate(nil, []func(){func() { <-release }}, 0, 100*time.Millisecond)
		Expect(time.Since(start)).To(BeNumerically("<", time.Second))
	})

	It("takes no longer than WorstCase with handlers that never return", func() {
		release := make(chan struct{})
		defer close(release)
		stuck := func(context.Context) { <-release }
		start := time.Now()
		// The same phases as a shutdown, scaled down: drain, slack, hooks.
		terminate([]func(context.Context){stuck}, []func(){func() { <-release }}, 50*time.Millisecond, 100*time.Millisecond)
		Expect(time.Since(start)).To(BeNumerically("<", 50*time.Millisecond+drainSlack+100*time.Millisecond+500*time.Millisecond))
	})
})

var _ = Describe("the shutdown budget", func() {
	It("fits a 60 s grace period with a 10 s preStop hook, with a margin", func() {
		const afterSigterm = 60*time.Second - 10*time.Second
		Expect(WorstCase(DefaultDrainTimeout)).To(Equal(41 * time.Second))
		Expect(WorstCase(DefaultDrainTimeout)).To(BeNumerically("<=", afterSigterm-5*time.Second))
	})

	It("counts a drain of zero as no wait", func() {
		Expect(WorstCase(0)).To(Equal(drainSlack + DefaultHookTimeout))
		Expect(WorstCase(-time.Second)).To(Equal(WorstCase(0)))
	})
})
