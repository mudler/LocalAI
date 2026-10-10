// SPDX-License-Identifier: MIT
package diagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/xlog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Diagnostics timing", func() {
	It("keeps disabled calls inert and allocation free", func() {
		ctx := context.Background()
		var r *Recorder
		Expect(r.Request(ctx)).To(BeIdenticalTo(ctx))
		Expect(r.Reload(ctx)).To(BeIdenticalTo(ctx))
		Expect(Inherit(ctx, ctx)).To(BeIdenticalTo(ctx))
		Expect(Enabled(ctx)).To(BeFalse())
		Expect(reflect.ValueOf(Begin(ctx, PhaseExtraction)).Pointer()).To(Equal(reflect.ValueOf(Begin(ctx, PhaseBodyLookup)).Pointer()))
		Expect(testing.AllocsPerRun(100, func() {
			r.Request(ctx)
			r.Reload(ctx)
			Begin(ctx, PhaseExtraction)(OutcomeOK, 0)
			Mark(ctx, PhaseModelInit)
			Record(ctx, PhaseConfigFilter, 0, OutcomeOK, 0)
		})).To(BeZero())
	})
	Describe("nil contexts", func() {
		It("keeps Mark inert", func() {
			Expect(func() { Mark(nil, PhaseModelInit) }).NotTo(Panic())
		})
		It("returns an inert Begin closure", func() {
			Expect(func() { Begin(nil, PhaseExtraction)(OutcomeOK, 0) }).NotTo(Panic())
		})
		It("keeps Record inert", func() {
			Expect(func() { Record(nil, PhaseConfigFilter, time.Second, OutcomeOK, 1) }).NotTo(Panic())
		})
		It("reports timing disabled", func() {
			Expect(Enabled(nil)).To(BeFalse())
		})
		It("leaves the inheritance destination unchanged", func() {
			type key struct{}
			dst, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "destination"))
			cancel()
			Expect(Inherit(dst, nil)).To(BeIdenticalTo(dst))
			Expect(Inherit(dst, nil).Err()).To(Equal(context.Canceled))
			Expect(Inherit(dst, nil).Value(key{})).To(Equal("destination"))
			Expect(Enabled(Inherit(dst, nil))).To(BeFalse())
		})
		It("emits no events and allocates nothing", func() {
			var events []Event
			dst := NewRecorder(func(e Event) { events = append(events, e) }).Request(context.Background())
			Expect(testing.AllocsPerRun(100, func() {
				Mark(nil, PhaseModelInit)
				Begin(nil, PhaseExtraction)(OutcomeOK, 0)
				Record(nil, PhaseConfigFilter, time.Second, OutcomeOK, 1)
				Enabled(nil)
				Inherit(dst, nil)
			})).To(BeZero())
			Expect(Inherit(dst, nil)).To(BeIdenticalTo(dst))
			Expect(events).To(BeEmpty())
		})
	})
	It("records exact nested durations and aggregate outcomes without summing", func() {
		var events []Event
		r := NewRecorder(func(e Event) { events = append(events, e) })
		now := time.Unix(100, 0)
		r.now = func() time.Time { return now }
		ctx := r.Request(context.Background())
		outer := Begin(ctx, PhaseExtraction)
		now = now.Add(2 * time.Second)
		inner := Begin(ctx, PhaseBodyLookup)
		now = now.Add(3 * time.Second)
		inner(OutcomeError, 1)
		now = now.Add(time.Second)
		outer(OutcomeCanceled, 2)
		Mark(ctx, PhaseModelInit)
		Record(ctx, PhaseReloadMetadata, 9*time.Second, OutcomeSkipped, 4)
		Expect(events).To(HaveLen(6))
		Expect(events[0].State).To(Equal(StateStart))
		Expect(events[2].Elapsed).To(Equal(3 * time.Second))
		Expect(events[2].Outcome).To(Equal(OutcomeError))
		Expect(events[3].Elapsed).To(Equal(6 * time.Second))
		Expect(events[3].Outcome).To(Equal(OutcomeCanceled))
		Expect(events[4].State).To(Equal(StateMark))
		Expect(events[4].Elapsed).To(BeZero())
		Expect(events[5].Count).To(Equal(4))
		Expect(events[5].Elapsed).To(Equal(9 * time.Second))
		for _, e := range events {
			Expect(e.ID).To(Equal(events[0].ID))
			Expect(e.Kind).To(Equal(KindRequest))
		}
	})
	It("inherits only private observation and preserves destination cancellation and values", func() {
		type key struct{}
		var events []Event
		r := NewRecorder(func(e Event) { events = append(events, e) })
		src, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "secret-header"))
		src = r.Request(src)
		cancel()
		dst := context.WithValue(context.Background(), key{}, "destination")
		inherited := Inherit(dst, src)
		Expect(inherited.Err()).To(BeNil())
		_, deadline := inherited.Deadline()
		Expect(deadline).To(BeFalse())
		Expect(inherited.Value(key{})).To(Equal("destination"))
		Mark(src, PhaseExtraction)
		Mark(inherited, PhaseExtraction)
		Expect(events[0].ID).To(Equal(events[1].ID))
		Expect(r.Request(inherited)).To(BeIdenticalTo(inherited))
		dstCanceled, cancelDst := context.WithCancel(dst)
		cancelDst()
		Expect(Inherit(dstCanceled, src).Err()).To(Equal(context.Canceled))
	})
	It("reuses another recorder's request identity and original observer", func() {
		var originalEvents, otherEvents []Event
		original := NewRecorder(func(e Event) { originalEvents = append(originalEvents, e) })
		other := NewRecorder(func(e Event) { otherEvents = append(otherEvents, e) })
		req := original.Request(context.Background())
		Mark(req, PhaseExtraction)

		reused := other.Request(req)
		Expect(reused).To(BeIdenticalTo(req))
		Expect(observed(reused)).To(BeIdenticalTo(observed(req)))
		Expect(observed(reused).recorder).To(BeIdenticalTo(original))
		Mark(reused, PhaseModelInit)
		Expect(originalEvents).To(HaveLen(2))
		Expect(originalEvents[1].ID).To(Equal(originalEvents[0].ID))
		Expect(otherEvents).To(BeEmpty())
		Expect(testing.AllocsPerRun(100, func() { other.Request(req) })).To(BeZero())

		var disabled *Recorder
		Expect(disabled.Request(req)).To(BeIdenticalTo(req))
		Expect(disabled.Reload(req)).To(BeIdenticalTo(req))
	})
	It("generates fresh reload identities and a fresh request after reload", func() {
		var events []Event
		sink := func(e Event) { events = append(events, e) }
		r := NewRecorder(sink)
		req := r.Request(context.Background())
		reload := r.Reload(req)
		for _, ctx := range []context.Context{req, reload, r.Reload(reload), r.Request(reload)} {
			Mark(ctx, PhaseExtraction)
		}
		ids := map[string]bool{}
		for _, e := range events {
			_, err := uuid.Parse(e.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(ids[e.ID]).To(BeFalse())
			ids[e.ID] = true
		}
		Expect(events[1].Kind).To(Equal(KindConfigReload))
		Expect(events[2].Kind).To(Equal(KindConfigReload))
		Expect(events[3].Kind).To(Equal(KindRequest))
	})
	It("isolates concurrent requests while permitting concurrent phases", func() {
		var mu sync.Mutex
		var events []Event
		r := NewRecorder(func(e Event) { mu.Lock(); defer mu.Unlock(); events = append(events, e) })
		var wg sync.WaitGroup
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx := r.Request(context.Background())
				end := Begin(ctx, PhaseExtraction)
				Mark(ctx, PhaseModelRouterCallback)
				end(OutcomeOK, 0)
			}()
		}
		wg.Wait()
		grouped := map[string][]Event{}
		for _, e := range events {
			grouped[e.ID] = append(grouped[e.ID], e)
		}
		Expect(grouped).To(HaveLen(32))
		for _, group := range grouped {
			Expect(group).To(HaveLen(3))
			Expect(group[0].State).To(Equal(StateStart))
			Expect(group[2].State).To(Equal(StateEnd))
		}
	})
	It("accepts exactly the documented phase vocabulary", func() {
		phases := []Phase{PhaseExtraction, PhaseBearerLookup, PhaseDefaultListing, PhaseBodyLookup, PhaseConfigLoadDefaults, PhaseAliasResolution, PhaseConfigLockWait, PhaseConfigLockHold, PhaseConfigFilter, PhaseFSEnumeration, PhaseLooseFilter, PhaseExistenceFallback, PhaseModelInit, PhaseModelRouterCallback, PhaseReloadLockWait, PhaseReloadLockHold, PhaseReloadEnumeration, PhaseReloadMetadata, PhaseReloadYAMLRead, PhaseReloadParseDefaults}
		expected := []string{"extraction", "bearer_lookup", "default_listing", "body_lookup", "config_load_defaults", "alias_resolution", "config_lock_wait", "config_lock_hold", "config_filter", "fs_enumeration", "loose_filter", "existence_fallback", "model_init", "model_router_callback", "reload_lock_wait", "reload_lock_hold", "reload_enumeration", "reload_metadata", "reload_yaml_read", "reload_parse_defaults"}
		var events []Event
		ctx := NewRecorder(func(e Event) { events = append(events, e) }).Request(context.Background())
		for i, phase := range phases {
			Expect(string(phase)).To(Equal(expected[i]))
			Mark(ctx, phase)
		}
		Expect(events).To(HaveLen(len(phases)))
	})
	It("rejects arbitrary enums before sink emission", func() {
		var events []Event
		r := NewRecorder(func(e Event) { events = append(events, e) })
		ctx := r.Request(context.Background())
		Begin(ctx, Phase("secret"))(OutcomeOK, 0)
		Mark(ctx, Phase("secret"))
		Record(ctx, PhaseExtraction, 0, Outcome("secret"), 0)
		Expect(events).To(BeEmpty())
	})
	It("logs only sanitized fixed fields at Info and rejects forged payloads", func() {
		var buf bytes.Buffer
		xlog.SetLogger(xlog.NewLoggerWithHandler(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}), xlog.LogLevel("info")))
		defer xlog.SetLogger(xlog.NewLogger(xlog.LogLevel(os.Getenv(xlog.EnvLogLevel)), os.Getenv(xlog.EnvLogFormat)))
		ctx := NewRecorder(nil).Request(context.WithValue(context.Background(), "X-Correlation-ID", "secret"))
		Mark(ctx, PhaseModelInit)
		var logged map[string]any
		Expect(json.Unmarshal(buf.Bytes(), &logged)).To(Succeed())
		Expect(logged["msg"]).To(Equal("diagnostic_phase"))
		Expect(logged["level"]).To(Equal("INFO"))
		Expect(logged).To(HaveLen(10))
		Expect(buf.String()).NotTo(ContainSubstring("secret"))
		valid := Event{ID: uuid.NewString(), Kind: KindRequest, Phase: PhaseExtraction, State: StateEnd, Outcome: OutcomeOK}
		for _, mutate := range []func(*Event){func(e *Event) { e.ID = "secret" }, func(e *Event) { e.Kind = "secret" }, func(e *Event) { e.Phase = "secret" }, func(e *Event) { e.State = "secret" }, func(e *Event) { e.Outcome = "secret" }} {
			e := valid
			mutate(&e)
			buf.Reset()
			LogEvent(e)
			Expect(buf.Len()).To(BeZero())
		}
	})
})
