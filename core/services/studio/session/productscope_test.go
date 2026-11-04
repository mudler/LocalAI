// SPDX-License-Identifier: MIT
package session_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	shell "github.com/mudler/LocalAI/core/services/studio/executionrequest"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/session/sessiontest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	model "github.com/mudler/LocalAI/pkg/addons/model/executionstatus"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type productScopeKey struct{}
type productScope struct {
	request *shell.Request
	q       model.ObservationID
	closed  atomic.Bool
}

var _ = Describe("product request scopes", func() {
	newChat := func(o session.Options) *session.Chat {
		o.Store, o.StateDir = sessiontest.NewMemoryStore(), GinkgoT().TempDir()
		m, err := session.New(o)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(m.Close(context.Background())).To(Succeed()) })
		chat, err := openAs(m, context.Background(), workspace.Actor{Principal: "alice", ChatID: "c1"}, "c1")
		Expect(err).NotTo(HaveOccurred())
		return chat
	}
	waitTurn := func(chat *session.Chat) {
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for {
			select {
			case e, open := <-chat.Events():
				Expect(open).To(BeTrue(), "event stream closed before turn end")
				if e.Kind == session.KindTurnEnd {
					return
				}
			case <-timer.C:
				Fail("turn did not end")
			}
		}
	}

	It("keeps one scope through prompt/status/egress and isolates priming and subsequent turns", func() {
		errors := make(chan error, 16)
		report := func(err error) error { errors <- err; return err }
		var serial, reads atomic.Int32
		scopes := make(chan *productScope, 3)
		consumed := make(chan model.ObservationID, 7)
		b := model.Binding{Actor: "alice", Addon: "renamed-provider", Generation: "1", Incarnation: "one", ExecutionTool: "run", StatusTool: "health"}
		expected := model.Value{Readiness: model.Available, Isolation: model.Isolated}
		o := session.Options{Harness: &sessiontest.FakeHarness{Turns: [][]sessiontest.Step{
			{{Tool: "status", Args: "{}"}, {Tool: "egress", Args: "{}"}},
			{{Tool: "status", Args: "{}"}, {Tool: "egress", Args: "{}"}},
		}}}
		o.ProductScope = func(ctx context.Context, x session.Access) (context.Context, func()) {
			if x.Actor().ChatID != "c1" {
				_ = report(fmt.Errorf("wrong chat ID")) // report queues the failure for the spec goroutine.
			}
			s := &productScope{q: model.ObservationID(fmt.Sprint(serial.Add(1)))}
			s.request = shell.New(ctx, b, s.q, model.Eligible, shell.IO{
				Read: func(ctx context.Context, got model.Binding, q model.ObservationID, deadline time.Time) (model.Payload, error) {
					reads.Add(1)
					if got != b || q != s.q || time.Until(deadline) > 5*time.Second {
						return model.InvalidPayload(), fmt.Errorf("wrong admission")
					}
					return model.ValidPayload(expected), nil
				},
				Validate: func(context.Context, model.Binding) error { return nil },
			}, nil)
			scopes <- s
			return context.WithValue(ctx, productScopeKey{}, s), func() { s.request.Close(); s.closed.Store(true) }
		}
		o.ChatTools = func(ctx context.Context, _ session.Access) (session.TurnTools, error) {
			s, ok := ctx.Value(productScopeKey{}).(*productScope)
			if !ok {
				return session.TurnTools{}, report(fmt.Errorf("missing scope"))
			}
			if s.closed.Load() || s.request.Observe(ctx) != expected {
				return session.TurnTools{}, report(fmt.Errorf("prompt scope not live"))
			}
			consumed <- s.q
			var tools []session.Tool
			for _, name := range []string{"status", "egress"} {
				tools = append(tools, session.Tool{Name: name, Description: name, Schema: json.RawMessage(`{"type":"object"}`), Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
					current, ok := ctx.Value(productScopeKey{}).(*productScope)
					if !ok || current != s || s.closed.Load() || s.request.Observe(ctx) != expected {
						return "", report(fmt.Errorf("scope not live or shared"))
					}
					consumed <- s.q
					return "ok", nil
				}})
			}
			return session.TurnTools{Tools: tools, Prompt: "stable prompt"}, nil
		}
		chat := newChat(o)
		var prime *productScope
		Eventually(scopes, 5*time.Second).Should(Receive(&prime))
		Expect(prime.closed.Load()).To(BeTrue())
		for range 2 {
			Expect(chat.Send(context.Background(), session.Message{Text: "hello"})).To(Succeed())
			waitTurn(chat)
			var s *productScope
			Eventually(scopes, 5*time.Second).Should(Receive(&s))
			Expect(s.closed.Load()).To(BeTrue())
		}
		Expect(errors).To(BeEmpty())
		Expect(serial.Load()).To(Equal(int32(3)))
		Expect(reads.Load()).To(Equal(int32(3)))
		var got []model.ObservationID
		for len(consumed) > 0 {
			got = append(got, <-consumed)
		}
		Expect(got).To(Equal([]model.ObservationID{"1", "2", "2", "2", "3", "3", "3"}))
	})

	It("gives a tool the product scope of the turn when the harness calls it with its own context", func() {
		seen := make(chan bool, 1)
		o := session.Options{Harness: &sessiontest.FakeHarness{OwnToolContext: true, Turns: [][]sessiontest.Step{{{Tool: "status", Args: "{}"}}}}}
		o.ProductScope = func(ctx context.Context, _ session.Access) (context.Context, func()) {
			return context.WithValue(ctx, productScopeKey{}, &productScope{}), func() {}
		}
		o.ChatTools = func(context.Context, session.Access) (session.TurnTools, error) {
			return session.TurnTools{Tools: []session.Tool{{Name: "status", Description: "status", Schema: json.RawMessage(`{"type":"object"}`),
				Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
					_, ok := ctx.Value(productScopeKey{}).(*productScope)
					seen <- ok
					return "ok", nil
				}}}}, nil
		}
		chat := newChat(o)
		Expect(chat.Send(context.Background(), session.Message{Text: "hello"})).To(Succeed())
		waitTurn(chat)
		Expect(<-seen).To(BeTrue(), "the tool sees the scope of its turn")
	})

	for _, stop := range []bool{false, true} {
		It(fmt.Sprintf("closes pending observations at the real turn boundary (stop=%t)", stop), func() {
			errors := make(chan error, 8)
			var serial atomic.Int32
			scopes := make(chan *productScope, 2)
			entered, canceled := make(chan struct{}, 2), make(chan struct{}, 2)
			results := make(chan model.Value, 2)
			block := make(chan struct{})
			h := &sessiontest.FakeHarness{}
			if stop {
				h.Turns = [][]sessiontest.Step{{{Block: block}}}
			}
			o := session.Options{Harness: h}
			o.ProductScope = func(ctx context.Context, _ session.Access) (context.Context, func()) {
				s := &productScope{q: model.ObservationID(fmt.Sprint(serial.Add(1)))}
				s.request = shell.New(ctx, model.Binding{Actor: "alice", Addon: "renamed", ExecutionTool: "run", StatusTool: "health"}, s.q, model.Eligible, shell.IO{
					Read: func(ctx context.Context, _ model.Binding, _ model.ObservationID, _ time.Time) (model.Payload, error) {
						entered <- struct{}{}
						<-ctx.Done()
						canceled <- struct{}{}
						return model.InvalidPayload(), ctx.Err()
					}, Validate: func(context.Context, model.Binding) error { return nil },
				}, nil)
				scopes <- s
				return context.WithValue(ctx, productScopeKey{}, s), func() { s.request.Close(); s.closed.Store(true) }
			}
			o.ChatTools = func(ctx context.Context, _ session.Access) (session.TurnTools, error) {
				s, ok := ctx.Value(productScopeKey{}).(*productScope)
				if !ok {
					err := fmt.Errorf("missing scope")
					errors <- err
					return session.TurnTools{}, err
				}
				go func() { results <- s.request.Observe(context.Background()) }()
				select {
				case <-entered:
				case <-time.After(2 * time.Second):
					err := fmt.Errorf("observation did not start")
					errors <- err
					return session.TurnTools{}, err
				}
				return session.TurnTools{}, nil
			}
			chat := newChat(o)
			var prime *productScope
			Eventually(scopes, 5*time.Second).Should(Receive(&prime))
			Expect(prime.closed.Load()).To(BeTrue())
			Eventually(canceled).Should(Receive())
			Eventually(results).Should(Receive(Equal(model.Unknown())))
			Expect(chat.Send(context.Background(), session.Message{Text: "hello"})).To(Succeed())
			var turn *productScope
			Eventually(scopes).Should(Receive(&turn))
			if stop {
				Eventually(h.Sent).Should(HaveLen(1))
				Expect(turn.closed.Load()).To(BeFalse(), "scope must outlive ChatTools")
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				Expect(chat.Stop(ctx)).To(Succeed())
			}
			waitTurn(chat)
			Expect(turn.closed.Load()).To(BeTrue())
			Eventually(canceled).Should(Receive())
			Eventually(results).Should(Receive(Equal(model.Unknown())))
			Expect(errors).To(BeEmpty())
		})
	}
})
