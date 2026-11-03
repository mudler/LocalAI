// SPDX-License-Identifier: MIT
package studio

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/executionrequest"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/session/sessiontest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	model "github.com/mudler/LocalAI/pkg/addons/model/executionstatus"
)

type observationProvider struct {
	AddonTools
	reads    atomic.Int32
	requests chan *executionrequest.Request
	started  chan struct{}
	release  chan struct{}
	fail     bool
	// addon is the add-on the observation is bound to; empty is renamed-provider.
	addon string
}

func (p *observationProvider) NewExecutionObservation(ctx context.Context, user, conv string) (*executionrequest.Request, string, func() ExecutionDetail) {
	b := model.Binding{Actor: user, Addon: cmp.Or(p.addon, "renamed-provider"), Generation: "1", Incarnation: "one", ExecutionTool: "run", StatusTool: "inspect"}
	r := executionrequest.New(ctx, b, "host-q", model.Eligible, executionrequest.IO{
		Validate: func(context.Context, model.Binding) error { return nil },
		Read: func(ctx context.Context, _ model.Binding, _ model.ObservationID, _ time.Time) (model.Payload, error) {
			p.reads.Add(1)
			if p.started != nil {
				close(p.started)
			}
			if p.release != nil {
				select {
				case <-p.release:
				case <-ctx.Done():
					return model.InvalidPayload(), ctx.Err()
				}
			}
			if p.fail {
				return model.InvalidPayload(), errors.New("failed")
			}
			return model.ValidPayload(model.Value{Readiness: model.Available, Isolation: model.Isolated}), nil
		}}, nil)
	if p.requests != nil {
		p.requests <- r
	}
	return r, b.Addon, func() ExecutionDetail { return ExecutionDetail{} }
}

var _ = Describe("execution status product scope", func() {
	It("shares one status observation across the actual session hook", func() {
		p := &observationProvider{requests: make(chan *executionrequest.Request, 3)}
		s := &Service{d: Deps{AddonTools: p}}
		h := &sessiontest.FakeHarness{Turns: [][]sessiontest.Step{{{Tool: "status", Args: "{}"}, {Tool: "egress", Args: "{}"}}}}
		failures := make(chan string, 8)
		observe := func(ctx context.Context) {
			v, _ := ExecutionObservation(ctx, "alice", "renamed-provider")
			if v.Isolation != model.Isolated {
				failures <- "not isolated"
			}
		}
		m, err := session.New(session.Options{Store: sessiontest.NewMemoryStore(), StateDir: GinkgoT().TempDir(), Harness: h, ProductScope: s.productScope,
			ChatTools: func(ctx context.Context, _ session.Access) (session.TurnTools, error) {
				observe(ctx)
				tools := []session.Tool{}
				for _, name := range []string{"status", "egress"} {
					tools = append(tools, session.Tool{Name: name, Description: name, Schema: json.RawMessage(`{"type":"object"}`), Run: func(ctx context.Context, _ json.RawMessage) (string, error) { observe(ctx); return "ok", nil }})
				}
				return session.TurnTools{Tools: tools}, nil
			}})
		Expect(err).NotTo(HaveOccurred())
		defer func() { Expect(m.Close(context.Background())).To(Succeed()) }()
		x, err := session.NewAccess(workspace.Actor{Principal: "alice", ChatID: "c1"}, workspace.View{})
		Expect(err).NotTo(HaveOccurred())
		chat, err := m.Open(context.Background(), x, "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(chat.Send(context.Background(), session.Message{Text: "hello"})).To(Succeed())
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case e := <-chat.Events():
				if e.Kind == session.KindTurnEnd {
					goto finished
				}
			case <-timer.C:
				Fail("turn timed out")
			}
		}
	finished:
		Expect(failures).To(BeEmpty())
		Expect(p.reads.Load()).To(Equal(int32(2)), "priming + one turn")
		for range 2 {
			r := <-p.requests
			Expect(r.Observe(context.Background()) != model.Unknown()).To(BeFalse(), "scope not closed")
		}
	})
})
var _ = Describe("execution status product scope", func() {
	It("closes status observations on cancellation and failure", func() {
		for _, fail := range []bool{false, true} {
			p := &observationProvider{started: make(chan struct{}), release: make(chan struct{}), fail: fail}
			s := &Service{d: Deps{AddonTools: p}}
			ctx, closeScope := s.executionScope(context.Background(), "alice", "c1")
			consumer, cancel := context.WithCancel(ctx)
			done := make(chan model.Value, 1)
			go func() { v, _ := ExecutionObservation(consumer, "alice", "renamed-provider"); done <- v }()
			select {
			case <-p.started:
			case <-time.After(5 * time.Second):
				Fail("no read")
			}
			cancel()
			Expect(<-done).To(Equal(model.Unknown()))
			if fail {
				close(p.release)
			} else {
				closeScope()
			}
			v, _ := ExecutionObservation(ctx, "alice", "renamed-provider")
			Expect(v != model.Unknown()).To(BeFalse(), v)
			closeScope()
			Expect(p.reads.Load() != 1).To(BeFalse(), "retried read")
			legacy, _ := ExecutionObservation(ctx, "alice", "legacy-provider")
			Expect(legacy).To(Equal(model.Unknown()))
		}
	})
})

var _ = Describe("execution status product scope", func() {
	It("retains the admitted status presentation binding per request", func() {
		p := &observationProvider{}
		s := &Service{d: Deps{AddonTools: p}}
		ctx, closeScope := s.executionScope(context.Background(), "alice", "chat-one")
		defer closeScope()
		v := executionStatusView(ctx, "alice", "renamed-provider")
		Expect(v.Readiness != "available" || v.Isolation != "isolated" || v.Generation != "1" || v.Incarnation != "one").To(BeFalse(), "missing or unredacted admitted presentation binding", v)
		Expect(*executionStatusView(ctx, "alice", "renamed-provider")).To(Equal(*v))
		Expect(executionStatusView(ctx, "bob", "renamed-provider").Readiness).To(Equal("unknown"))
		next, closeNext := s.executionScope(context.Background(), "alice", "chat-two")
		defer closeNext()
		Expect(executionStatusView(next, "alice", "renamed-provider").Readiness != "available").To(BeFalse(), "fresh request lost status")
		Expect(p.reads.Load() != 2).To(BeFalse(), "DTO re-probed within request")
	})
})

var _ = Describe("execution status product scope", func() {
	It("leaves unbound status presentation unknown", func() {
		s := &Service{}
		ctx, done := s.executionScope(context.Background(), "alice", "chat")
		defer done()
		v := executionStatusView(ctx, "alice", "")
		Expect(v.Readiness != "unknown" || v.Isolation != "unknown" || v.Generation != "" || v.Incarnation != "").To(BeFalse(), "unbound status manufactured identity", v)
	})
})

// reasonProvider returns a fixed observation, or none with a failure.
type reasonProvider struct {
	AddonTools
	request *executionrequest.Request
	addon   string
	detail  func() ExecutionDetail
}

func (p reasonProvider) NewExecutionObservation(context.Context, string, string) (*executionrequest.Request, string, func() ExecutionDetail) {
	return p.request, p.addon, p.detail
}

var _ = Describe("why an execution status is unknown", func() {
	view := func(p reasonProvider, addon string) *ExecutionStatusView {
		s := &Service{d: Deps{AddonTools: p}}
		ctx, done := s.executionScope(context.Background(), "alice", "chat")
		DeferCleanup(done)
		return executionStatusView(ctx, "alice", addon)
	}
	request := func(payload model.Payload, err error) *executionrequest.Request {
		b := model.Binding{Actor: "alice", Addon: "runner", Generation: "1", Incarnation: "one", ExecutionTool: "run", StatusTool: "inspect"}
		r := executionrequest.New(context.Background(), b, "q", model.Eligible, executionrequest.IO{
			Validate: func(context.Context, model.Binding) error { return nil },
			Read: func(context.Context, model.Binding, model.ObservationID, time.Time) (model.Payload, error) {
				return payload, err
			}}, nil)
		DeferCleanup(r.Close)
		return r
	}

	It("carries the failure the provider recorded when it could not admit a request", func() {
		for _, reason := range []UnknownReason{UnknownOwnerUnreachable, UnknownNotReported, UnknownStale} {
			v := view(reasonProvider{detail: func() ExecutionDetail { return ExecutionDetail{Failure: reason} }}, "runner")
			Expect(v.Readiness).To(Equal("unknown"))
			Expect(v.UnknownReason).To(Equal(string(reason)))
		}
	})
	It("says the status was not reported when the provider recorded nothing", func() {
		Expect(view(reasonProvider{}, "runner").UnknownReason).To(Equal(string(UnknownNotReported)))
	})
	It("says the add-on does not run the chat's commands when another add-on was admitted", func() {
		p := reasonProvider{request: request(model.ValidPayload(model.Value{Readiness: model.Available, Isolation: model.Isolated}), nil), addon: "runner", detail: func() ExecutionDetail { return ExecutionDetail{} }}
		Expect(view(p, "other").UnknownReason).To(Equal(string(UnknownNotSelected)))
		Expect(view(p, "").UnknownReason).To(Equal(string(UnknownNotSelected)))
	})
	It("carries the failure of a read the add-on did not answer", func() {
		p := reasonProvider{request: request(model.InvalidPayload(), errors.New("boom")), addon: "runner",
			detail: func() ExecutionDetail { return ExecutionDetail{Failure: UnknownInvalid} }}
		Expect(view(p, "runner").UnknownReason).To(Equal(string(UnknownInvalid)))
	})
	It("says the add-on reported unknown when it answered unknown", func() {
		p := reasonProvider{request: request(model.ValidPayload(model.Unknown()), nil), addon: "runner", detail: func() ExecutionDetail { return ExecutionDetail{} }}
		Expect(view(p, "runner").UnknownReason).To(Equal(string(UnknownReportedUnknown)))
	})
	It("gives no reason once the status is known, and trusts enforcement only for available isolated", func() {
		for value, want := range map[model.Value]bool{
			{Readiness: model.Available, Isolation: model.Isolated}: true,
			{Readiness: model.Available, Isolation: model.Host}:     false,
		} {
			p := reasonProvider{request: request(model.ValidPayload(value), nil), addon: "runner",
				detail: func() ExecutionDetail { return ExecutionDetail{NetworkPolicyEnforced: true, Failure: UnknownStale} }}
			v := view(p, "runner")
			Expect(v.UnknownReason).To(BeEmpty())
			Expect(v.NetworkPolicyEnforced).To(Equal(want))
		}
	})
})

// flakyProvider fails to admit its first observations, then succeeds.
type flakyProvider struct {
	AddonTools
	failures int
	failure  UnknownReason
	calls    atomic.Int32
}

func (p *flakyProvider) NewExecutionObservation(ctx context.Context, user, _ string) (*executionrequest.Request, string, func() ExecutionDetail) {
	if int(p.calls.Add(1)) <= p.failures {
		return nil, "", func() ExecutionDetail { return ExecutionDetail{Failure: p.failure} }
	}
	b := model.Binding{Actor: user, Addon: "runner", Generation: "2", Incarnation: "two", ExecutionTool: "run", StatusTool: "inspect"}
	r := executionrequest.New(ctx, b, "q", model.Eligible, executionrequest.IO{
		Validate: func(context.Context, model.Binding) error { return nil },
		Read: func(context.Context, model.Binding, model.ObservationID, time.Time) (model.Payload, error) {
			return model.ValidPayload(model.Value{Readiness: model.Available, Isolation: model.Isolated}), nil
		}}, nil)
	return r, b.Addon, func() ExecutionDetail { return ExecutionDetail{} }
}

var _ = Describe("observing again after the add-on owner restarted", func() {
	scopeOf := func(p ExecutionObserver) context.Context {
		s := &Service{d: Deps{AddonTools: p.(AddonTools)}}
		ctx, done := s.executionScope(context.Background(), "alice", "chat")
		DeferCleanup(done)
		return ctx
	}
	BeforeEach(func() {
		old := observationRetryAfter
		observationRetryAfter = 20 * time.Millisecond
		DeferCleanup(func() { observationRetryAfter = old })
	})

	It("keeps the first answer inside the interval, then binds and reads again", func() {
		p := &flakyProvider{failures: 1, failure: UnknownOwnerUnreachable}
		ctx := scopeOf(p)
		v, d := ObserveExecution(ctx, "alice", "runner")
		Expect(v).To(Equal(model.Unknown()))
		Expect(d.Failure).To(Equal(UnknownOwnerUnreachable))
		_, _ = ObserveExecution(ctx, "alice", "runner")
		Expect(p.calls.Load()).To(BeEquivalentTo(1), "no second admission within the interval")
		time.Sleep(2 * observationRetryAfter)
		v, d = ObserveExecution(ctx, "alice", "runner")
		Expect(v.Isolation).To(Equal(model.Isolated))
		Expect(d.Failure).To(BeEmpty())
		Expect(p.calls.Load()).To(BeEquivalentTo(2))
		_, _ = ObserveExecution(ctx, "alice", "runner")
		Expect(p.calls.Load()).To(BeEquivalentTo(2), "a good status is not read again")
	})
	It("does not retry a refusal that waiting will not fix", func() {
		p := &flakyProvider{failures: 1, failure: UnknownNotReported}
		ctx := scopeOf(p)
		_, _ = ObserveExecution(ctx, "alice", "runner")
		time.Sleep(2 * observationRetryAfter)
		v, d := ObserveExecution(ctx, "alice", "runner")
		Expect(v).To(Equal(model.Unknown()))
		Expect(d.Failure).To(Equal(UnknownNotReported))
		Expect(p.calls.Load()).To(BeEquivalentTo(1))
	})
	It("does not start again once the scope is closed", func() {
		p := &flakyProvider{failures: 1, failure: UnknownStale}
		s := &Service{d: Deps{AddonTools: p}}
		ctx, done := s.executionScope(context.Background(), "alice", "chat")
		_, _ = ObserveExecution(ctx, "alice", "runner")
		done()
		time.Sleep(2 * observationRetryAfter)
		v, _ := ObserveExecution(ctx, "alice", "runner")
		Expect(v).To(Equal(model.Unknown()))
		Expect(p.calls.Load()).To(BeEquivalentTo(1))
	})
})
