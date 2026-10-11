// SPDX-License-Identifier: MIT
package nodes

import (
	"context"
	"github.com/mudler/LocalAI/pkg/diagnostics"
	"github.com/mudler/LocalAI/pkg/grpc"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sync"
	"sync/atomic"
	"time"
)

type delayedTracking struct {
	fakeInFlightTracker
	calls        []string
	decrementErr error
}

func (f *delayedTracking) IncrementInFlight(ctx context.Context, n, m string, i int) error {
	f.calls = append(f.calls, "increment")
	time.Sleep(time.Millisecond)
	return f.fakeInFlightTracker.IncrementInFlight(ctx, n, m, i)
}
func (f *delayedTracking) DecrementInFlight(ctx context.Context, n, m string, i int) error {
	f.calls = append(f.calls, "decrement")
	Expect(ctx.Err()).To(Succeed())
	deadline, ok := ctx.Deadline()
	Expect(ok).To(BeTrue())
	Expect(time.Until(deadline)).To(BeNumerically(">", 4*time.Second))
	time.Sleep(time.Millisecond)
	f.fakeInFlightTracker.DecrementInFlight(ctx, n, m, i)
	return f.decrementErr
}

type diagnosticHealthBackend struct {
	grpc.Backend
	calls   atomic.Int32
	entered chan struct{}
	finish  chan struct{}
}

func (b *diagnosticHealthBackend) HealthCheck(ctx context.Context) (bool, error) {
	b.calls.Add(1)
	close(b.entered)
	<-b.finish
	return true, nil
}

type diagnosticHealthFactory struct{ backend *diagnosticHealthBackend }

func (f diagnosticHealthFactory) NewClient(string, string, bool) grpc.Backend { return f.backend }

var _ = Describe("Tracking dispatch", func() {
	It("does not add probes for concurrent or cached health checks", func() {
		var mu sync.Mutex
		var events []diagnostics.Event
		ctx := diagnostics.NewRecorder(func(e diagnostics.Event) { mu.Lock(); defer mu.Unlock(); events = append(events, e) }).Request(context.Background())
		b := &diagnosticHealthBackend{entered: make(chan struct{}), finish: make(chan struct{})}
		r := NewSmartRouter(&fakeModelRouter{}, SmartRouterOptions{ClientFactory: diagnosticHealthFactory{b}})
		n := &BackendNode{ID: "n"}
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() { defer wg.Done(); r.probeHealth(ctx, n, "addr") }()
		}
		<-b.entered
		time.Sleep(time.Millisecond)
		close(b.finish)
		wg.Wait()
		Expect(r.probeHealth(ctx, n, "addr")).To(BeTrue())
		diagnostics.FlushDispatch(ctx)
		Expect(b.calls.Load()).To(Equal(int32(1)))
		var probes, caches, skips int
		for _, e := range events {
			if e.State != diagnostics.StateEnd {
				continue
			}
			if e.Phase == diagnostics.PhaseHealthProbe {
				probes++
				Expect(e.Count).To(Equal(1))
				Expect(e.Elapsed).To(BeNumerically(">=", time.Millisecond))
			}
			if e.Phase == diagnostics.PhaseHealthCache {
				caches++
				if e.Outcome == diagnostics.OutcomeSkipped {
					skips++
				}
			}
		}
		Expect(probes).To(Equal(1))
		Expect(caches).To(Equal(9))
		Expect(skips).To(Equal(8))
	})
	It("times delayed tracking and preserves failed increment, cancellation, and retry semantics", func() {
		for _, failed := range []bool{false, true} {
			var events []diagnostics.Event
			ctx, cancel := context.WithCancel(diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) }).Request(context.Background()))
			f := &delayedTracking{decrementErr: context.DeadlineExceeded}
			if failed {
				f.incrementErr = context.Canceled
			}
			c := NewInFlightTrackingClient(&fakeGRPCBackend{}, f, "n", "m", 0)
			releases := 0
			c.OnFirstComplete(func() { releases++; f.calls = append(f.calls, "release") })
			cancel()
			for range 2 {
				done := c.track(ctx)
				Expect(releases).To(BeNumerically("<=", 1))
				done()
			}
			var phases []diagnostics.Phase
			for _, e := range events {
				if e.State == diagnostics.StateEnd {
					phases = append(phases, e.Phase)
					Expect(e.Elapsed).To(BeNumerically(">=", time.Millisecond))
					Expect(e.Count).To(Equal(1))
					if failed || e.Phase == diagnostics.PhaseInflightDecrement {
						Expect(e.Outcome).To(Equal(diagnostics.OutcomeCanceled))
					}
				}
			}
			if failed {
				Expect(f.calls).To(Equal([]string{"increment", "increment"}))
				Expect(releases).To(BeZero())
				Expect(phases).To(HaveLen(2))
			} else {
				Expect(f.calls).To(Equal([]string{"increment", "decrement", "release", "increment", "decrement"}))
				Expect(releases).To(Equal(1))
				Expect(phases).To(HaveLen(4))
			}
		}
	})
	It("records warm routing and a single reservation release without extra probes", func() {
		var events []diagnostics.Event
		ctx := diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) }).Request(context.Background())
		f := &fakeModelRouter{findAndLockNode: &BackendNode{ID: "node", Address: "addr"}, findAndLockNM: &NodeModel{}}
		r := NewSmartRouter(f, SmartRouterOptions{ClientFactory: &stubClientFactory{client: &stubBackend{healthResult: true}}})
		result, err := r.Route(ctx, "model", "model", "llama-cpp", "", nil, false)
		Expect(err).NotTo(HaveOccurred())
		result.Release()
		result.Release()
		diagnostics.FlushDispatch(ctx)
		var phases []diagnostics.Phase
		for _, e := range events {
			if e.State == diagnostics.StateEnd {
				phases = append(phases, e.Phase)
			}
		}
		Expect(phases).To(ContainElements(diagnostics.PhaseRoute, diagnostics.PhaseRouteRevision, diagnostics.PhaseReservation, diagnostics.PhaseHealthCache, diagnostics.PhaseHealthProbe, diagnostics.PhaseLastUsed, diagnostics.PhaseReservationRelease))
		Expect(f.decrementCalls).To(HaveLen(1))
		Expect(f.findAndLockPrefs).To(HaveLen(1))
		Expect(f.touchCalls).To(HaveLen(1))
	})
	It("records synchronous increment and detached decrement and flushes completion", func() {
		var events []diagnostics.Event
		ctx := diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) }).Request(context.Background())
		f := &fakeInFlightTracker{}
		c := NewInFlightTrackingClient(&fakeGRPCBackend{}, f, "node", "model", 0)
		done := c.track(ctx)
		time.Sleep(time.Millisecond)
		done()
		var phases []diagnostics.Phase
		for _, e := range events {
			if e.State == diagnostics.StateEnd {
				phases = append(phases, e.Phase)
				Expect(e.Count).To(Equal(1))
			}
		}
		Expect(phases).To(Equal([]diagnostics.Phase{diagnostics.PhaseInflightIncrement, diagnostics.PhaseInflightDecrement}))
		Expect(f.increments).To(Equal(1))
		Expect(f.decrements).To(Equal(1))
	})
})
