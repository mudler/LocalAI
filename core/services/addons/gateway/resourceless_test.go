// SPDX-License-Identifier: MIT
package gateway

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// capSet adds the manifest definitions to a tool source.
type capSet struct {
	toolSet
	defs []contract.ToolDefinition
}

func (c capSet) DeclaredCapabilities(_ context.Context, id string, gen uint64) (contract.Capabilities, error) {
	if _, err := c.DeclaredTools(context.Background(), id, gen); err != nil {
		return contract.Capabilities{}, err
	}
	return contract.Capabilities{ToolDefinitions: c.defs}, nil
}

// strictResolver is the real rule: no resource, no key.
type strictResolver struct{ resolved }

func (strictResolver) Resolve(ctx context.Context, a contract.Actor, r contract.Request) (ResourceKey, error) {
	if r.ResourceID == "" {
		return ResourceKey{}, ErrForbidden
	}
	return resolved{}.Resolve(ctx, a, r)
}

// addonAdmitted admits a read of an add-on and records who was admitted.
type addonAdmitted struct {
	admitted
	mu     sync.Mutex
	actors []string
}

func (a *addonAdmitted) AdmitAddon(ctx context.Context, actor contract.Actor, _ string, run func(context.Context) error) error {
	a.mu.Lock()
	a.actors = append(a.actors, actor.UserID)
	a.mu.Unlock()
	return run(ctx)
}

// actorClient answers a read with the actor of its envelope, as a list tool
// that scopes its answer to the caller does.
type actorClient struct {
	countingClient
}

func (c *actorClient) Read(_ context.Context, e contract.Envelope) (contract.Result, error) {
	c.reads.Add(1)
	b, _ := json.Marshal(map[string]string{"user": e.Authority.Actor.UserID, "resource": e.Request.ResourceID})
	return contract.Result{Payload: b}, nil
}

var _ = Describe("a read that names no resource", Label("tools"), func() {
	ctx := context.Background()
	schema := func(s string) json.RawMessage { return json.RawMessage(s) }
	defs := []contract.ToolDefinition{
		{Name: "list", Kind: "read", Description: "x", InputSchema: schema(`{"type":"object","properties":{},"additionalProperties":false}`)},
		{Name: "one", Kind: "read", Description: "x", InputSchema: schema(`{"type":"object","properties":{"resourceId":{"type":"string"}},"required":["resourceId"],"additionalProperties":false}`)},
		{Name: "change", Kind: "write", Description: "x", InputSchema: schema(`{"type":"object","properties":{"resourceId":{"type":"string"}},"required":["resourceId"],"additionalProperties":false}`)},
	}
	source := capSet{toolSet: toolSet{gen: 1, tools: map[string][]string{"sample": {"list", "one", "change"}}}, defs: defs}
	build := func(adm Admission) (*Gateway, *actorClient) {
		client := &actorClient{}
		return &Gateway{Admission: adm, Resolver: strictResolver{}, Runtime: currentFixture{client}, Tools: source, Revalidate: func(context.Context, contract.Actor) error { return nil }}, client
	}
	read := func(g *Gateway, user, args string) (contract.Result, error) {
		return g.Dispatch(ctx, contract.Actor{UserID: user, CredentialID: "c", Mode: "named-user"}, contract.Request{AddonID: "sample", Generation: 1, OperationID: "op", Action: "read", Arguments: json.RawMessage(args)})
	}

	It("runs a resource-less read tool for the calling user only", func() {
		adm := &addonAdmitted{}
		g, client := build(adm)
		a, err := read(g, "alice", `{"tool":"list"}`)
		Expect(err).NotTo(HaveOccurred())
		b, err := read(g, "bob", `{"tool":"list"}`)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(a.Payload)).To(Equal(`{"resource":"","user":"alice"}`))
		Expect(string(b.Payload)).To(Equal(`{"resource":"","user":"bob"}`))
		Expect(adm.actors).To(Equal([]string{"alice", "bob"}))
		Expect(client.reads.Load()).To(Equal(int32(2)))
	})

	It("denies a tool that declares a resource when none is given, and never reaches the add-on", func() {
		g, client := build(&addonAdmitted{})
		for _, tool := range []string{"one", "change", "missing"} {
			_, err := read(g, "alice", `{"tool":"`+tool+`"}`)
			Expect(err).To(MatchError(ErrForbidden), tool)
		}
		_, err := read(g, "alice", `{}`)
		Expect(err).To(MatchError(ErrForbidden), "no tool named")
		Expect(client.reads.Load()).To(Equal(int32(0)))
	})

	It("denies a resource-less read when the admission cannot admit an add-on, or the tool is undeclared", func() {
		g, client := build(&admitted{})
		_, err := read(g, "alice", `{"tool":"list"}`)
		Expect(err).To(MatchError(ErrForbidden))
		g2, _ := build(&addonAdmitted{})
		g2.Tools = capSet{toolSet: toolSet{gen: 1, tools: map[string][]string{"sample": {"one"}}}, defs: defs}
		_, err = read(g2, "alice", `{"tool":"list"}`)
		Expect(err).To(MatchError(ErrForbidden))
		Expect(client.reads.Load()).To(Equal(int32(0)))
	})
})
