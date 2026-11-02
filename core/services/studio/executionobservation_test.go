// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
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
	b := model.Binding{Actor: user, Addon: "renamed-provider", Generation: "1", Incarnation: "one", ExecutionTool: "run", StatusTool: "inspect", SelectionRevision: conv}
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
func TestExecutionProductScopeActualSessionHook(t *testing.T) {
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
	m, err := session.New(session.Options{Store: sessiontest.NewMemoryStore(), StateDir: t.TempDir(), Harness: h, ProductScope: s.productScope,
		ChatTools: func(ctx context.Context, _ session.Access) (session.TurnTools, error) {
			observe(ctx)
			tools := []session.Tool{}
			for _, name := range []string{"status", "egress"} {
				tools = append(tools, session.Tool{Name: name, Description: name, Schema: json.RawMessage(`{"type":"object"}`), Run: func(ctx context.Context, _ json.RawMessage) (string, error) { observe(ctx); return "ok", nil }})
			}
			return session.TurnTools{Tools: tools}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
	x, err := session.NewAccess(workspace.Actor{Principal: "alice", ChatID: "c1"}, workspace.View{})
	if err != nil {
		t.Fatal(err)
	}
	chat, err := m.Open(context.Background(), x, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if err = chat.Send(context.Background(), session.Message{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case e := <-chat.Events():
			if e.Kind == session.KindTurnEnd {
				goto finished
			}
		case <-timer.C:
			t.Fatal("turn timed out")
		}
	}
finished:
	if len(failures) != 0 {
		t.Fatal(<-failures)
	}
	if p.reads.Load() != 2 {
		t.Fatalf("priming + one turn: reads=%d", p.reads.Load())
	}
	for range 2 {
		r := <-p.requests
		if r.Observe(context.Background()) != model.Unknown() {
			t.Fatal("scope not closed")
		}
	}
}
func TestExecutionProductScopeCancellationAndFailure(t *testing.T) {
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
			t.Fatal("no read")
		}
		cancel()
		if v := <-done; v != model.Unknown() {
			t.Fatal(v)
		}
		if fail {
			close(p.release)
		} else {
			closeScope()
		}
		v, _ := ExecutionObservation(ctx, "alice", "renamed-provider")
		if v != model.Unknown() {
			t.Fatal(v)
		}
		closeScope()
		if p.reads.Load() != 1 {
			t.Fatal("retried read")
		}
		if v, _ := ExecutionObservation(ctx, "alice", "legacy-provider"); v != model.Unknown() {
			t.Fatal("legacy isolation inferred")
		}
	}
}
