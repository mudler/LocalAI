// SPDX-License-Identifier: MIT
package diagnostics

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Dispatch contract", func() {
	It("keeps disabled helpers inert and allocation free", func() {
		var r *Recorder
		ctx := context.Background()
		Expect(testing.AllocsPerRun(100, func() {
			attached, ok := r.AttachRequest(ctx, "invalid")
			if ok || attached != ctx {
				panic("disabled attachment")
			}
			s := Dispatch(ctx).NewAttempt().NewConnection()
			_, end := s.Start(PhaseRoute)
			end(OutcomeCanceled, 1)
			s.MarkOnce(PhaseHTTPEntry, OutcomeOK)
			s.Record(PhaseMetadataRPC, 0, OutcomeOK, 1)
			FlushDispatch(ctx)
		})).To(BeZero())
	})
	It("accepts only canonical v4 UUIDs without replacing an existing request", func() {
		r := NewRecorder(func(Event) {})
		ctx := context.Background()
		id := uuid.NewString()
		for _, invalid := range []string{"", "secret", strings.ToUpper(id), strings.ReplaceAll(id, "-", ""), "urn:uuid:" + id, uuid.Nil.String(), "00000000-0000-1000-8000-000000000000"} {
			got, ok := r.AttachRequest(ctx, invalid)
			Expect(ok).To(BeFalse())
			Expect(got).To(BeIdenticalTo(ctx))
		}
		got, ok := r.AttachRequest(ctx, id)
		Expect(ok).To(BeTrue())
		Expect(RequestID(got)).To(Equal(id))
		same, ok := r.AttachRequest(got, id)
		Expect(ok).To(BeTrue())
		Expect(same).To(BeIdenticalTo(got))
		same, ok = r.AttachRequest(got, uuid.NewString())
		Expect(ok).To(BeFalse())
		Expect(same).To(BeIdenticalTo(got))
	})
	It("buffers nested boundaries and accounts for synchronous sink time separately", func() {
		var events []Event
		now := time.Now()
		r := NewRecorder(func(e Event) { events = append(events, e); now = now.Add(time.Second) })
		r.now = func() time.Time { return now }
		ctx := r.Request(context.Background())
		root := Dispatch(ctx).NewAttempt().NewConnection()
		parent, end := root.Start(PhaseRoute)
		now = now.Add(2 * time.Second)
		child, childEnd := parent.Start(PhaseMetadataRPC)
		child.MarkOnce(PhaseRPCFirstMessage, OutcomeOK)
		child.MarkOnce(PhaseRPCFirstMessage, OutcomeOK)
		now = now.Add(3 * time.Second)
		childEnd(OutcomeError, 2)
		childEnd(OutcomeOK, 9)
		end(OutcomeCanceled, 1)
		Expect(events).To(BeEmpty())
		stats := FlushDispatch(ctx)
		Expect(stats.Emitted).To(Equal(uint64(5)))
		Expect(stats.SinkElapsed).To(Equal(5 * time.Second))
		Expect(events).To(HaveLen(6))
		Expect(events[1].Dispatch.Parent).To(Equal(events[0].Dispatch.Span))
		Expect(events[1].Dispatch.Attempt).To(Equal(uint32(1)))
		Expect(events[1].Dispatch.Connection).To(Equal(uint32(1)))
		Expect(events[1].Dispatch.Offset).To(Equal(2 * time.Second))
		Expect(events[3].Elapsed).To(Equal(3 * time.Second))
		Expect(events[4].Elapsed).To(Equal(5 * time.Second))
		Expect(events[4].Outcome).To(Equal(OutcomeCanceled))
		Expect(events[5].Dispatch.SinkElapsed).To(Equal(5 * time.Second))
	})
	It("bounds collection and reports overflow even across drains", func() {
		var events []Event
		ctx := NewRecorder(func(e Event) { events = append(events, e) }).Request(context.Background())
		s := Dispatch(ctx)
		for i := 0; i < MaxDispatchEvents+7; i++ {
			s.Record(PhaseMetadataRPC, 0, OutcomeOK, 1)
		}
		stats := FlushDispatch(ctx)
		Expect(stats.Emitted).To(Equal(uint64(MaxDispatchEvents)))
		Expect(stats.Dropped).To(Equal(uint64(7)))
		s.Record(PhaseMetadataRPC, 0, OutcomeOK, 1)
		stats = FlushDispatch(ctx)
		Expect(stats.Dropped).To(Equal(uint64(8)))
		Expect(stats.Emitted).To(Equal(uint64(MaxDispatchEvents)))
	})
	It("rejects invalid closed schemas before collection and direct logging", func() {
		var events []Event
		ctx := NewRecorder(func(e Event) { events = append(events, e) }).Request(context.Background())
		s := Dispatch(ctx)
		s.Record(Phase("secret"), 0, OutcomeOK, 0)
		s.Record(PhaseRoute, -1, OutcomeOK, 0)
		s.Record(PhaseRoute, 0, Outcome("secret"), 0)
		s.Record(PhaseRoute, 0, OutcomeOK, -1)
		s.MarkOnce(PhaseHTTPEntry, Outcome("secret"))
		s.MarkOnce(PhaseHTTPEntry, OutcomeOK)
		FlushDispatch(ctx)
		Expect(events).To(HaveLen(2))
		e := events[0]
		e.Dispatch = &DispatchFields{Offset: -1}
		Expect(validEvent(e)).To(BeFalse())
		e.Dispatch = &DispatchFields{Span: MaxDispatchEvents + 1}
		Expect(validEvent(e)).To(BeFalse())
	})
	It("bounds each identity allocator and keeps reload observations inert", func() {
		r := NewRecorder(func(Event) {})
		Expect(Dispatch(r.Reload(context.Background()))).To(Equal(DispatchScope{}))
		Expect(Dispatch(nil)).To(Equal(DispatchScope{}))
		for _, allocate := range []func(DispatchScope) DispatchScope{
			func(s DispatchScope) DispatchScope { return s.NewAttempt() },
			func(s DispatchScope) DispatchScope { return s.NewConnection() },
			func(s DispatchScope) DispatchScope { child, _ := s.Start(PhaseRoute); return child },
		} {
			ctx := r.Request(context.Background())
			scope := Dispatch(ctx)
			for i := 0; i < MaxDispatchEvents; i++ {
				Expect(allocate(scope).c).NotTo(BeNil())
			}
			Expect(allocate(scope)).To(Equal(DispatchScope{}))
			Expect(FlushDispatch(ctx).Dropped).To(Equal(uint64(1)))
		}
	})
	It("permits sink reentry without holding collection locks or losing late events", func() {
		var ctx context.Context
		var events []Event
		r := NewRecorder(func(e Event) {
			events = append(events, e)
			if e.Phase == PhaseHTTPEntry {
				Dispatch(ctx).MarkOnce(PhaseTraceStart, OutcomeOK)
				FlushDispatch(ctx)
			}
		})
		ctx = r.Request(context.Background())
		Dispatch(ctx).MarkOnce(PhaseHTTPEntry, OutcomeOK)
		Expect(FlushDispatch(ctx).Emitted).To(Equal(uint64(1)))
		Expect(FlushDispatch(ctx).Emitted).To(Equal(uint64(2)))
		Expect(events).To(HaveLen(4))
		Expect(events[2].Phase).To(Equal(PhaseTraceStart))
	})
	It("isolates concurrent canceled requests and emits each milestone once", func() {
		var mu sync.Mutex
		grouped := map[string][]Event{}
		r := NewRecorder(func(e Event) { mu.Lock(); defer mu.Unlock(); grouped[e.ID] = append(grouped[e.ID], e) })
		var wg sync.WaitGroup
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithCancel(r.Request(context.Background()))
				cancel()
				s := Dispatch(Inherit(context.Background(), ctx)).NewAttempt()
				var marks sync.WaitGroup
				for j := 0; j < 16; j++ {
					marks.Add(1)
					go func() { defer marks.Done(); s.MarkOnce(PhaseHTTPEntry, OutcomeCanceled) }()
				}
				marks.Wait()
				FlushDispatch(ctx)
			}()
		}
		wg.Wait()
		Expect(grouped).To(HaveLen(16))
		for _, events := range grouped {
			Expect(events).To(HaveLen(2))
			Expect(events[0].Dispatch.Attempt).To(Equal(uint32(1)))
			Expect(events[0].Outcome).To(Equal(OutcomeCanceled))
		}
	})
})
