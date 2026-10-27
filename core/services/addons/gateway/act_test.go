// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// actingClient answers an act call with the actor and scope of its envelope.
type actingClient struct {
	countingClient
	acts atomic.Int32
}

func (c *actingClient) Act(_ context.Context, e contract.Envelope) (contract.Result, error) {
	c.acts.Add(1)
	b, _ := json.Marshal(map[string]string{"user": e.Authority.Actor.UserID, "scope": e.Authority.Scope, "action": e.Request.Action})
	return contract.Result{Payload: b}, nil
}

// slowActClient answers an action call after a delay.
type slowActClient struct {
	actingClient
	delay time.Duration
}

func (c *slowActClient) Act(ctx context.Context, e contract.Envelope) (contract.Result, error) {
	select {
	case <-time.After(c.delay):
	case <-ctx.Done():
		return contract.Result{}, ctx.Err()
	}
	return c.actingClient.Act(ctx, e)
}

// readOnlyClient is a runtime that does not serve action tools.
type readOnlyClient struct{ countingClient }

var _ = Describe("action tools through the gateway", Label("tools"), func() {
	ctx := context.Background()
	schema := func(s string) json.RawMessage { return json.RawMessage(s) }
	defs := []contract.ToolDefinition{
		{Name: "run", Kind: contract.ToolKindAction, Description: "x", InputSchema: schema(`{"type":"object","properties":{"command":{"type":"string"}},"additionalProperties":false}`)},
		{Name: "list", Kind: contract.ToolKindRead, Description: "x", InputSchema: schema(`{"type":"object","properties":{},"additionalProperties":false}`)},
	}
	source := func(actions bool) capSet {
		return capSet{toolSet: toolSet{gen: 1, tools: map[string][]string{"sample": {"run", "list"}}}, defs: defs, actions: actions}
	}
	actor := func(user string) contract.Actor {
		return contract.Actor{UserID: user, CredentialID: "c", Mode: "named-user"}
	}
	request := func(args string) contract.Request {
		return contract.Request{AddonID: "sample", Generation: 1, OperationID: "op1", Action: "act", Arguments: json.RawMessage(args)}
	}
	build := func(rt contract.Runtime, src capSet) (*Gateway, *addonAdmitted) {
		adm := &addonAdmitted{}
		return &Gateway{Admission: adm, Resolver: strictResolver{}, Runtime: currentFixture{rt}, Tools: src, Revalidate: func(context.Context, contract.Actor) error { return nil }}, adm
	}

	It("runs a declared action tool for the calling user, under the act scope, admitted as an add-on call", func() {
		client := &actingClient{}
		g, adm := build(client, source(true))
		out, err := g.Act(ctx, actor("alice"), request(`{"tool":"run","command":"echo"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(out.Payload)).To(And(ContainSubstring(`"user":"alice"`), ContainSubstring(`"scope":"act"`), ContainSubstring(`"action":"act"`)))
		Expect(adm.actors).To(ConsistOf("alice"))
	})

	It("is not reachable through the typed dispatch of the direct API", func() {
		client := &actingClient{}
		g, _ := build(client, source(true))
		_, err := g.Dispatch(ctx, actor("alice"), request(`{"tool":"run","command":"echo"}`))
		Expect(err).To(HaveOccurred())
		Expect(client.acts.Load()).To(BeZero())
	})

	It("refuses what is not a declared action tool", func() {
		client := &actingClient{}
		g, _ := build(client, source(true))
		for name, args := range map[string]string{
			"a read tool":       `{"tool":"list"}`,
			"an undeclared one": `{"tool":"nope"}`,
			"no tool":           `{"command":"echo"}`,
		} {
			_, err := g.Act(ctx, actor("alice"), request(args))
			Expect(err).To(MatchError(ErrForbidden), name)
		}
		bad := request(`{"tool":"run"}`)
		bad.Action = "read"
		_, err := g.Act(ctx, actor("alice"), bad)
		Expect(err).To(MatchError(ErrForbidden))
		Expect(client.acts.Load()).To(BeZero())
	})

	It("refuses an add-on that did not declare actions", func() {
		client := &actingClient{}
		g, _ := build(client, source(false))
		_, err := g.Act(ctx, actor("alice"), request(`{"tool":"run"}`))
		Expect(err).To(MatchError(ErrForbidden))
		Expect(client.acts.Load()).To(BeZero())
	})

	It("is unavailable when the runtime does not serve action tools, and never runs for an unvalidated user", func() {
		g, _ := build(&readOnlyClient{}, source(true))
		_, err := g.Act(ctx, actor("alice"), request(`{"tool":"run"}`))
		Expect(err).To(MatchError(ErrUnavailable))

		client := &actingClient{}
		g2, _ := build(client, source(true))
		g2.Revalidate = func(context.Context, contract.Actor) error { return ErrForbidden }
		_, err = g2.Act(ctx, actor("mallory"), request(`{"tool":"run"}`))
		Expect(err).To(MatchError(ErrForbidden))
		Expect(client.acts.Load()).To(BeZero())
	})

	It("reports an unknown outcome, not a refusal, when the runtime fails after the call began", func() {
		fail := &failingActClient{}
		g, _ := build(fail, source(true))
		_, err := g.Act(ctx, actor("alice"), request(`{"tool":"run"}`))
		var unknown *OutcomeUnknown
		Expect(errors.As(err, &unknown)).To(BeTrue(), "got %v", err)
		Expect(unknown.OperationID).To(Equal("op1"))
	})
})

type failingActClient struct{ countingClient }

func (*failingActClient) Act(context.Context, contract.Envelope) (contract.Result, error) {
	return contract.Result{}, errors.New("connection reset")
}

var _ = Describe("an action tool forwarded to the owner", Label("tools"), func() {
	actor := contract.Actor{UserID: "alice", CredentialID: "c", Mode: "named-user"}
	ok := func(context.Context, contract.Actor) error { return nil }
	defs := []contract.ToolDefinition{{Name: "run", Kind: contract.ToolKindAction, Description: "x",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"additionalProperties":false}`)}}
	req := contract.Request{AddonID: "sample", Generation: 1, OperationID: "fwd-op", Action: "act", Arguments: json.RawMessage(`{"tool":"run","command":"echo"}`)}

	It("runs on the owner for the actor the signature binds, and the typed direct dispatch still refuses it", func() {
		client := &actingClient{}
		owner := &Gateway{Admission: &addonAdmitted{}, Resolver: strictResolver{}, Runtime: currentFixture{client}, Revalidate: ok,
			Tools: capSet{toolSet: toolSet{gen: 1, tools: map[string][]string{"sample": {"run"}}}, defs: defs, actions: true}}
		env := carrierFixtureWith(RelayLimits{}, func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(owner)
			Expect(e).NotTo(HaveOccurred())
			return h
		})
		frontend := &Gateway{Forward: env.carriers["front-a"], Revalidate: ok}
		out, err := frontend.Act(context.Background(), actor, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(out.Payload)).To(ContainSubstring(`"user":"alice"`))
		Expect(client.acts.Load()).To(Equal(int32(1)))

		// A dispatch of the same request, forwarded the same way, is refused by the owner.
		_, err = frontend.Dispatch(context.Background(), actor, req)
		Expect(err).To(HaveOccurred())
		Expect(client.acts.Load()).To(Equal(int32(1)))
	})

	It("keeps the answer of a call that outlasts the write timeout of the owner server", func() {
		client := &slowActClient{delay: 700 * time.Millisecond}
		owner := &Gateway{Admission: &addonAdmitted{}, Resolver: strictResolver{}, Runtime: currentFixture{client}, Revalidate: ok,
			Tools: capSet{toolSet: toolSet{gen: 1, tools: map[string][]string{"sample": {"run"}}}, defs: defs, actions: true}}
		env := carrierFixtureWith(RelayLimits{}, func(c *Carrier) http.Handler {
			h, e := c.OwnerHandler(owner)
			Expect(e).NotTo(HaveOccurred())
			// What http.Server does for its WriteTimeout, with a short one.
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
				h.ServeHTTP(w, r)
			})
		})
		frontend := &Gateway{Forward: env.carriers["front-a"], Revalidate: ok}
		out, err := frontend.Act(context.Background(), actor, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(out.Payload)).To(ContainSubstring(`"user":"alice"`))
	})

	It("is an unknown outcome when the answer is lost", func() {
		lost := func(*Carrier) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/addons/audience" {
					_, _ = w.Write([]byte(`{"audience":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}`))
					return
				}
				conn, _, e := w.(http.Hijacker).Hijack()
				Expect(e).NotTo(HaveOccurred())
				_ = conn.Close()
			})
		}
		env := carrierFixtureWith(RelayLimits{}, lost)
		frontend := &Gateway{Forward: env.carriers["front-a"], Revalidate: ok}
		_, err := frontend.Act(context.Background(), actor, req)
		var unknown *OutcomeUnknown
		Expect(errors.As(err, &unknown)).To(BeTrue(), "got %v", err)
		Expect(unknown.OperationID).To(Equal("fwd-op"))
	})
})
