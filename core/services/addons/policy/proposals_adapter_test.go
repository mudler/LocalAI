// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// brokerBackend lets the real tool adapter run over the real broker. It records
// each confirmation the adapter asks for.
type brokerBackend struct {
	broker *Broker
	list   apps.List
	mu     sync.Mutex
	asked  []string
}

func (b *brokerBackend) Apps(context.Context, contract.Actor) (apps.List, error) { return b.list, nil }

func (b *brokerBackend) Dispatch(context.Context, contract.Actor, contract.Request) (contract.Result, error) {
	return contract.Result{}, nil
}

func (b *brokerBackend) Propose(ctx context.Context, a contract.Actor, r contract.Request) (gateway.Proposal, error) {
	return b.broker.Propose(ctx, a, r)
}

func (b *brokerBackend) ConfirmProposal(ctx context.Context, a contract.Actor, id, digest string) (gateway.Proposal, error) {
	b.mu.Lock()
	b.asked = append(b.asked, id)
	b.mu.Unlock()
	if _, err := b.broker.ConfirmBound(ctx, a, id, digest); err != nil {
		return gateway.Proposal{}, err
	}
	return b.broker.Get(ctx, a, id)
}

func (b *brokerBackend) CancelProposal(ctx context.Context, a contract.Actor, id string) (gateway.Proposal, error) {
	if err := b.broker.Cancel(ctx, a, id); err != nil {
		return gateway.Proposal{}, err
	}
	return b.broker.Get(ctx, a, id)
}

var _ = Describe("a chat call of a write tool in an add-on without a credential", Label("proposals", "approval"), func() {
	ctx := context.Background()
	person := contract.Actor{UserID: "bob", CredentialID: "session:s1", Mode: "named-user"}
	schema := json.RawMessage(`{"type":"object","properties":{"resourceId":{"type":"string"},"rows":{"type":"array","items":{"type":"integer"}}},"required":["resourceId"],"additionalProperties":false}`)

	build := func() (*brokerFixture, *brokerBackend, ResourceKey) {
		defs := []contract.ToolDefinition{
			{Name: "hard", Description: "Changes data.", Kind: contract.ToolKindWrite, Approval: contract.ApprovalAlways, InputSchema: schema},
			{Name: "plain", Description: "Changes data.", Kind: contract.ToolKindWrite, InputSchema: schema},
		}
		caps := contract.Capabilities{Tools: []string{"hard", "plain"}, ToolDefinitions: defs}
		f := newBrokerFixture(ModeNamedUser, func(o *BrokerOptions) {
			o.Tools = manifestTools{caps: caps}
			o.ConfirmPerson = func(a contract.Actor) bool { return strings.HasPrefix(a.CredentialID, "session:") }
		})
		key := f.create(person)
		be := &brokerBackend{broker: f.broker, list: apps.List{Apps: []apps.App{{
			ID: key.AddonID, Name: key.AddonID, Generation: 1, Enabled: true, State: "running", Tools: caps.Tools, ToolDefinitions: defs}}}}
		return f, be, key
	}
	call := func(be *brokerBackend, key ResourceKey, tool string) tools.ToolResult {
		res, err := tools.NewChatToolAdapter(be).ExecuteChat(ctx, person, tools.NewChatCall("bob", "conv1"),
			tools.ToolName(key.AddonID, tool), json.RawMessage(`{"resourceId":"`+key.ResourceID+`","rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		return res
	}

	It("returns a proposal that waits and runs nothing until a person confirms", func() {
		f, be, key := build()
		res := call(be, key, "hard")
		Expect(res.Kind).To(Equal(tools.ResultProposal))
		Expect(res.Proposal).NotTo(BeNil())
		Expect(res.Proposal.Hold).To(Equal("approval_always"))
		Expect(be.asked).To(BeEmpty(), "the adapter did not try to apply it")
		Expect(f.rt.applyCalls()).To(BeEmpty())
		stored, err := f.broker.Get(ctx, person, res.Proposal.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.AwaitsPerson).To(BeTrue())
		Expect(stored.State).To(Equal(gateway.ProposalPending))

		_, err = f.broker.ConfirmBound(ctx, person, res.Proposal.ID, res.Proposal.Digest)
		Expect(err).NotTo(HaveOccurred())
		Expect(f.rt.applyCalls()).To(HaveLen(1))
	})

	It("still applies a tool without approval always at once", func() {
		f, be, key := build()
		res := call(be, key, "plain")
		Expect(res.Kind).To(Equal(tools.ResultApplied))
		Expect(be.asked).To(HaveLen(1))
		Expect(f.rt.applyCalls()).To(HaveLen(1))
	})
})
