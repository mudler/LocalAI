// SPDX-License-Identifier: MIT
package studio

import (
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
}

func (p *observationProvider) NewExecutionObservation(ctx context.Context, user, conv string) (*executionrequest.Request, string, func() string) {
	b := model.Binding{Actor: user, Addon: "renamed-provider", Generation: "1", Incarnation: "one", ExecutionTool: "run", StatusTool: "inspect"}
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
	return r, b.Addon, func() string { return "" }
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
