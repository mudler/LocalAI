// SPDX-License-Identifier: MIT
package diagnostics

import (
	"context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Dispatch completion ownership", func() {
	It("hands concurrent completion to the active owner", func() {
		entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var events []Event
		ctx := NewRecorder(func(e Event) {
			events = append(events, e)
			if e.Phase == PhaseHTTPEntry {
				close(entered)
				<-release
			}
		}).Request(context.Background())
		Dispatch(ctx).MarkOnce(PhaseHTTPEntry, OutcomeOK)
		go func() { FlushDispatch(ctx); close(done) }()
		<-entered
		Dispatch(ctx).Record(PhaseInflightDecrement, 0, OutcomeOK, 1)
		FlushDispatch(ctx)
		close(release)
		<-done
		Expect(events).To(ContainElement(HaveField("Phase", PhaseInflightDecrement)))
	})
	It("retains only same-request scopes and leaves disabled contexts unchanged", func() {
		r := NewRecorder(func(Event) {})
		ctx, cancel := context.WithCancel(r.Request(context.Background()))
		defer cancel()
		scope, _ := Dispatch(ctx).NewAttempt().Start(PhaseRoute)
		nested := scope.Context(ctx)
		Expect(Dispatch(nested)).To(Equal(scope))
		Expect(RequestID(nested)).To(Equal(RequestID(ctx)))
		cancel()
		Expect(nested.Err()).To(Equal(context.Canceled))
		other := r.Request(context.Background())
		Expect(scope.Context(other)).To(BeIdenticalTo(other))
		disabled := context.Background()
		Expect(scope.Context(disabled)).To(BeIdenticalTo(disabled))
		Expect((DispatchScope{}).Context(ctx)).To(BeIdenticalTo(ctx))
	})
	It("drains a late completion requested while a sink is running", func() {
		var ctx context.Context
		var events []Event
		late := false
		r := NewRecorder(func(e Event) {
			events = append(events, e)
			if !late {
				late = true
				Dispatch(ctx).Record(PhaseInflightDecrement, 0, OutcomeOK, 1)
				FlushDispatch(ctx)
			}
		})
		ctx = r.Request(context.Background())
		Dispatch(ctx).Record(PhaseInflightIncrement, 0, OutcomeOK, 1)
		FlushDispatch(ctx)
		var phases []Phase
		for _, e := range events {
			if e.Phase != PhaseDispatchSummary {
				phases = append(phases, e.Phase)
			}
		}
		Expect(phases).To(Equal([]Phase{PhaseInflightIncrement, PhaseInflightDecrement}))
	})
})
