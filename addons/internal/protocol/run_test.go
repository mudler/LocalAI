// SPDX-License-Identifier: MIT
package protocol

import (
	"context"
	"io"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Run", func() {
	It("refuses without the protocol flag and builds no runtime", func() {
		built := false
		factory := func(Startup) contract.Runtime { built = true; return nil }
		for _, args := range [][]string{{"x"}, {"x", "--other"}, {"x", "--localai-addon-v1", "extra"}} {
			Expect(Run(args, factory)).To(Equal(2), "args %v", args)
		}
		Expect(built).To(BeFalse(), "the factory ran before the protocol flag was checked")
	})
})

// closingRuntime is a runtime that has work to do before the process exits.
type closingRuntime struct {
	contract.Runtime
	closeFn func(context.Context) error
}

func (c closingRuntime) Close(ctx context.Context) error { return c.closeFn(ctx) }

var _ = Describe("the shutdown of an add-on", func() {
	It("closes the runtime with a bounded context before it exits, once", func() {
		var calls, exits atomic.Int32
		var bounded atomic.Bool
		rt := closingRuntime{closeFn: func(ctx context.Context) error {
			calls.Add(1)
			_, ok := ctx.Deadline()
			bounded.Store(ok)
			return nil
		}}
		code := make(chan int, 2)
		stop := newShutdown(rt, func(c int) { exits.Add(1); code <- c })
		stop()
		stop()
		Expect(<-code).To(Equal(0))
		Expect(calls.Load()).To(Equal(int32(1)))
		Expect(bounded.Load()).To(BeTrue())
		Expect(exits.Load()).To(Equal(int32(1)))
	})

	It("exits when the owner closes the startup channel", func() {
		r, w := io.Pipe()
		done := make(chan struct{})
		var closed atomic.Bool
		rt := closingRuntime{closeFn: func(context.Context) error { closed.Store(true); return nil }}
		exitOnParentLoss(r, newShutdown(rt, func(int) { close(done) }))
		Expect(w.Close()).To(Succeed())
		Eventually(done).Should(BeClosed())
		Expect(closed.Load()).To(BeTrue())
	})

	It("still exits when the runtime does not return in time", func() {
		old := shutdownBudget
		shutdownBudget = 100 * time.Millisecond
		DeferCleanup(func() { shutdownBudget = old })
		release := make(chan struct{})
		DeferCleanup(func() { close(release) })
		rt := closingRuntime{closeFn: func(context.Context) error { <-release; return nil }}
		done := make(chan struct{})
		go newShutdown(rt, func(int) { close(done) })()
		Eventually(done, 2*time.Second).Should(BeClosed())
	})

	It("exits at once for a runtime with nothing to close", func() {
		done := make(chan struct{})
		go newShutdown(nil, func(int) { close(done) })()
		Eventually(done).Should(BeClosed())
	})
})
