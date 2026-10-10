// SPDX-License-Identifier: MIT
package model

import (
	"context"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/pkg/diagnostics"
	"github.com/mudler/LocalAI/pkg/grpc"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Embedding leaves unexpected backend work as a panic rather than a network call.
type diagnosticsHealthyBackend struct{ grpc.Backend }

func (*diagnosticsHealthyBackend) HealthCheck(context.Context) (bool, error) { return true, nil }

var _ = Describe("Diagnostics model initialization", func() {
	for _, enabled := range []bool{false, true} {
		for _, outcome := range []string{"success", "error", "cancel"} {
			It(fmt.Sprintf("times only the callback: enabled=%t outcome=%s", enabled, outcome), func() {
				var events []diagnostics.Event
				source := diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) }).Request(context.Background())
				diagnostics.Mark(source, diagnostics.PhaseExtraction)
				id := events[0].ID
				events = nil
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if enabled {
					ctx = diagnostics.Inherit(ctx, source)
				}
				ml := NewModelLoader(&system.SystemState{})
				want := NewModel("secret-id", "unused", nil)
				cause := errors.New("secret-error")
				if outcome == "success" {
					cause = nil
				}
				if outcome == "cancel" {
					cancel()
					cause = context.Canceled
				}
				opts := NewOptions(WithContext(ctx), WithConfigRevision("secret-revision"), EnableParallelRequests)
				calls := 0
				ml.SetModelRouter(func(got context.Context, backend, id, name, file, revision string, options *pb.ModelOptions, parallel bool) (*Model, error) {
					calls++
					Expect(got).To(BeIdenticalTo(ctx))
					Expect([]string{backend, id, name, file, revision}).To(Equal([]string{"secret-backend", "secret-id", "secret-name", "secret-file", "secret-revision"}))
					Expect(options).To(BeIdenticalTo(opts.gRPCOptions))
					Expect(parallel).To(BeTrue())
					if enabled {
						Expect(events).To(HaveLen(1))
						Expect(events[0].State).To(Equal(diagnostics.StateStart))
					}
					return want, cause
				})
				got, err := ml.grpcModel("secret-backend", opts)("secret-id", "secret-name", "secret-file")
				Expect(got).To(BeIdenticalTo(want))
				if cause == nil {
					Expect(err).To(BeNil())
				} else {
					Expect(err).To(Equal(cause))
				}
				Expect(calls).To(Equal(1))
				if !enabled {
					Expect(events).To(BeEmpty())
					return
				}
				Expect(events).To(HaveLen(2))
				expected := diagnostics.OutcomeOK
				if outcome == "error" {
					expected = diagnostics.OutcomeError
				}
				if outcome == "cancel" {
					expected = diagnostics.OutcomeCanceled
				}
				Expect(events[1].State).To(Equal(diagnostics.StateEnd))
				Expect(events[1].Outcome).To(Equal(expected))
				for _, e := range events {
					Expect(e.ID).To(Equal(id))
					Expect(e.Phase).To(Equal(diagnostics.PhaseModelRouterCallback))
				}
				Expect(fmt.Sprint(events)).NotTo(ContainSubstring("secret"))
			})
		}
	}
	It("carries one inherited identity from Load into the router", func() {
		var events []diagnostics.Event
		source := diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) }).Request(context.Background())
		diagnostics.Mark(source, diagnostics.PhaseExtraction)
		id := events[0].ID
		events = nil
		ctx := diagnostics.Inherit(context.Background(), source)
		ml := NewModelLoader(&system.SystemState{})
		ml.SetModelRouter(func(got context.Context, _, _, _, _, _ string, _ *pb.ModelOptions, _ bool) (*Model, error) {
			Expect(got).To(BeIdenticalTo(ctx))
			Expect(events).To(HaveLen(2))
			Expect(events[0].Phase).To(Equal(diagnostics.PhaseModelInit))
			Expect(events[1].Phase).To(Equal(diagnostics.PhaseModelRouterCallback))
			return nil, errors.New("secret-router-error")
		})
		got, err := ml.Load(WithContext(ctx), WithModelID("secret-id"), WithBackendString("secret-backend"))
		Expect(got).To(BeNil())
		Expect(err).To(MatchError("failed to route model with internal loader: secret-router-error"))
		Expect(events).To(HaveLen(3))
		Expect(events[2].Outcome).To(Equal(diagnostics.OutcomeError))
		for _, e := range events {
			Expect(e.ID).To(Equal(id))
		}
		Expect(fmt.Sprint(events)).NotTo(ContainSubstring("secret"))
	})
	It("marks Load before admission and does not time the local branch", func() {
		var events []diagnostics.Event
		ctx := diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) }).Request(context.Background())
		ml := NewModelLoader(&system.SystemState{})
		cause := errors.New("admission rejected without spawning")
		ml.SetLoadLifecycleObserver(func(BackendLoadEvent) (func(BackendLoadEvent), error) {
			Expect(events).To(HaveLen(1))
			Expect(events[0].Phase).To(Equal(diagnostics.PhaseModelInit))
			Expect(events[0].State).To(Equal(diagnostics.StateMark))
			return nil, cause
		})
		got, err := ml.Load(WithContext(ctx), WithModelID("secret-id"), WithBackendString("secret-backend"))
		Expect(got).To(BeNil())
		Expect(err).To(MatchError("failed to load model with internal loader: " + cause.Error()))
		Expect(events).To(HaveLen(1))
	})
	It("marks cache hits and leaves unobserved loads inert", func() {
		var events []diagnostics.Event
		ctx := diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) }).Request(context.Background())
		ml := NewModelLoader(&system.SystemState{})
		cached := NewModelWithClient("cached", "unused", &diagnosticsHealthyBackend{})
		cached.MarkHealthy()
		ml.store.Set("cached", cached)
		for _, c := range []context.Context{context.Background(), ctx} {
			got, err := ml.Load(WithContext(c), WithModelID("cached"))
			Expect(err).NotTo(HaveOccurred())
			Expect(got).NotTo(BeNil())
		}
		Expect(events).To(HaveLen(1))
		Expect(events[0].Phase).To(Equal(diagnostics.PhaseModelInit))
	})
})
