// SPDX-License-Identifier: MIT
package grpc

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	d "github.com/mudler/LocalAI/pkg/diagnostics"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/stats"
)

var _ = Describe("Diagnostic flush admission", func() {
	It("bounds stalled owners and drops concurrent work without blocking, then recovers", func() {
		wait := func(check func() bool) {
			Eventually(check, 5*time.Second, time.Millisecond).Should(BeTrue())
		}
		wait(func() bool { return len(diagnosticFlushSlots) == 0 })
		before := DiagnosticFlushDrops()
		release := make(chan struct{})
		var once sync.Once
		DeferCleanup(func() {
			once.Do(func() { close(release) })
			Eventually(func() int { return len(diagnosticFlushSlots) }, 5*time.Second, time.Millisecond).Should(BeZero())
		})
		var entered atomic.Int32
		recorder := d.NewRecorder(func(d.Event) { entered.Add(1); <-release })
		// Distinct requests, not repeated flushes of a single bounded collection.
		for i := 0; i < maxDiagnosticFlushes; i++ {
			_, h := observeRPC(recorder.Request(context.Background()))
			h.finish()
		}
		wait(func() bool { return entered.Load() == maxDiagnosticFlushes })
		const producers = 128
		var wg sync.WaitGroup
		done := make(chan struct{})
		for i := 0; i < producers; i++ {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				ctx, h := observeRPC(recorder.Request(context.Background()))
				rpc := h.TagRPC(ctx, &stats.RPCTagInfo{FullMethodName: "/backend.Backend/Predict"})
				h.finish()
				var callbacks sync.WaitGroup
				for j := 0; j < 4; j++ {
					callbacks.Add(1)
					go func() {
						defer GinkgoRecover()
						defer callbacks.Done()
						h.HandleRPC(rpc, &stats.InPayload{})
						h.HandleRPC(rpc, &stats.End{})
						h.HandleConn(ctx, &stats.ConnBegin{})
						h.finish()
					}()
				}
				callbacks.Wait()
			}()
		}
		go func() { defer GinkgoRecover(); wg.Wait(); close(done) }()
		Eventually(done, time.Second, time.Millisecond).Should(BeClosed(), "submission or stats callback blocked on sink")
		Expect(DiagnosticFlushDrops()-before).To(Equal(uint64(producers)), "one drop per abandoned handler")
		Expect(len(diagnosticFlushSlots)).To(Equal(maxDiagnosticFlushes))
		Expect(entered.Load()).To(Equal(int32(maxDiagnosticFlushes)))
		once.Do(func() { close(release) })
		wait(func() bool { return len(diagnosticFlushSlots) == 0 })
		var recovered atomic.Bool
		_, h := observeRPC(d.NewRecorder(func(d.Event) { recovered.Store(true) }).Request(context.Background()))
		h.finish()
		wait(recovered.Load)
		wait(func() bool { return len(diagnosticFlushSlots) == 0 })
		Expect(DiagnosticFlushDrops()-before).To(Equal(uint64(producers)), "unexpected recovery drops")
	})

	It("bounds redrain passes and never retries an abandoned handler", func() {
		Eventually(func() int { return len(diagnosticFlushSlots) }, 5*time.Second, time.Millisecond).Should(BeZero())
		before := DiagnosticFlushDrops()
		var h *rpcDiagnostics
		var summaries atomic.Int32
		ctx := d.NewRecorder(func(e d.Event) {
			if e.Phase == d.PhaseDispatchSummary {
				summaries.Add(1)
				h.lateFlush()
			}
		}).Request(context.Background())
		_, h = observeRPC(ctx)
		h.finish()
		Eventually(func() bool {
			return DiagnosticFlushDrops() != before && len(diagnosticFlushSlots) == 0
		}, 5*time.Second, time.Millisecond).Should(BeTrue(), "redrain owner did not terminate")
		Expect(summaries.Load()).To(Equal(int32(d.MaxDispatchEvents + 1)))
		Expect(DiagnosticFlushDrops()).To(Equal(before+1), "expected one abandonment")
		h.finish()
		Expect(summaries.Load()).To(Equal(int32(d.MaxDispatchEvents+1)), "abandoned handler retried")
	})
})

var _ = Describe("Frontend completion drain admission", func() {
	It("shares RPC slots, drops each saturated completion request, and recovers", func() {
		Eventually(func() int { return len(diagnosticFlushSlots) }).Should(BeZero())
		release := make(chan struct{})
		var once sync.Once
		DeferCleanup(func() {
			once.Do(func() { close(release) })
			Eventually(func() int { return len(diagnosticFlushSlots) }).Should(BeZero())
		})
		var entered atomic.Int32
		recorder := d.NewRecorder(func(d.Event) { entered.Add(1); <-release })
		before := DiagnosticFlushDrops()
		for i := 0; i < maxDiagnosticFlushes; i++ {
			ctx := recorder.Request(context.Background())
			if i%2 == 0 {
				_, h := observeRPC(ctx)
				h.finish()
			} else {
				d.Dispatch(ctx).MarkOnce(d.PhaseSSERole, d.OutcomeOK)
				FlushDiagnosticsAsync(ctx)
			}
		}
		Eventually(entered.Load).Should(Equal(int32(maxDiagnosticFlushes)))
		for i := 0; i < 32; i++ {
			ctx := recorder.Request(context.Background())
			d.Dispatch(ctx).MarkOnce(d.PhaseSSEContent, d.OutcomeOK)
			FlushDiagnosticsAsync(ctx) // HTTP exit
			FlushDiagnosticsAsync(ctx) // detached producer exit
		}
		FlushDiagnosticsAsync(context.Background())
		Expect(DiagnosticFlushDrops() - before).To(Equal(uint64(64)))
		Expect(len(diagnosticFlushSlots)).To(Equal(maxDiagnosticFlushes))
		once.Do(func() { close(release) })
		Eventually(func() int { return len(diagnosticFlushSlots) }).Should(BeZero())
		events := &diagnosticEvents{}
		ctx := d.NewRecorder(events.add).Request(context.Background())
		d.Dispatch(ctx).MarkOnce(d.PhaseSSERole, d.OutcomeOK)
		FlushDiagnosticsAsync(ctx)
		Eventually(func() []d.Event { return events.find(d.PhaseSSERole) }).Should(HaveLen(1))
	})
	It("drains late producers after cancellation and an earlier completion drain", func() {
		events := &diagnosticEvents{}
		ctx, cancel := context.WithCancel(d.NewRecorder(events.add).Request(context.Background()))
		d.Dispatch(ctx).MarkOnce(d.PhaseSSERole, d.OutcomeOK)
		FlushDiagnosticsAsync(ctx)
		Eventually(func() []d.Event { return events.find(d.PhaseSSERole) }).Should(HaveLen(1))
		cancel()
		d.Dispatch(ctx).MarkOnce(d.PhaseFrontendCallback, d.OutcomeOK)
		FlushDiagnosticsAsync(ctx)
		Eventually(func() []d.Event { return events.find(d.PhaseFrontendCallback) }).Should(HaveLen(1))
		Expect(events.find(d.PhaseSSERole)).To(HaveLen(1))
	})
})
