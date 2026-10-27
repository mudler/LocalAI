// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// applyFake adds the owner side of an immediate change to the fake owner.
type applyFake struct {
	*actFake
	confirmed  []string
	digests    []string
	cancelled  []string
	actors     []contract.Actor
	confirmErr error
	state      gateway.ProposalState
}

func (f *applyFake) ConfirmProposal(_ context.Context, a contract.Actor, id, digest string) (gateway.Proposal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confirmed = append(f.confirmed, id)
	f.digests = append(f.digests, digest)
	f.actors = append(f.actors, a)
	if f.confirmErr != nil {
		return gateway.Proposal{}, f.confirmErr
	}
	r := &contract.Receipt{OperationID: id, State: contract.ReceiptCommitted, Result: contract.Result{Payload: json.RawMessage(`{"inserted":2}`)}}
	return gateway.Proposal{ID: id, State: f.state, Receipt: r}, nil
}

func (f *applyFake) CancelProposal(_ context.Context, _ contract.Actor, id string) (gateway.Proposal, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = append(f.cancelled, id)
	return gateway.Proposal{ID: id, State: gateway.ProposalCancelled}, nil
}

var _ = Describe("changes made at once for a chat", func() {
	ctx := context.Background()
	insert := tools.ToolName("tables", "rows-insert")
	rows := args(`{"resourceId":"db-one","table":"t","rows":[{"a":1},{"a":2}]}`)

	build := func() (*applyFake, *tools.ToolAdapter, *tools.ToolAdapter) {
		base := &actFake{fake: &fake{list: newFake().list, proposal: gateway.Proposal{AddonID: "tables", ResourceID: "db-one", Action: "write", Digest: "d1",
			State: gateway.ProposalPending, Effects: gateway.ProposalEffects{Count: 2, Sample: args(`[{"id":1}]`)}}}}
		f := &applyFake{actFake: base, state: gateway.ProposalApplied}
		return f, tools.NewToolAdapter(f), tools.NewChatToolAdapter(f)
	}

	It("applies a row change for the chat adapter, as the actor, with the digest it just stored", func() {
		f, _, chats := build()
		res, err := chats.ExecuteIn(ctx, member, "conv1", insert, rows)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Kind).To(Equal(tools.ResultApplied))
		Expect(res.Proposal).To(BeNil())
		Expect(res.Applied).NotTo(BeNil())
		Expect(res.Applied.ID).To(Equal(f.proposed[0].Request.OperationID))
		Expect(res.Applied.AddonID).To(Equal("tables"))
		Expect(res.Applied.Count).To(Equal(uint64(2)))
		Expect(string(res.Applied.Result)).To(MatchJSON(`{"inserted":2}`))
		Expect(res.Applied.Undoable).To(BeFalse())
		Expect(res.Note).To(ContainSubstring("cannot be undone"))
		Expect(f.confirmed).To(Equal([]string{f.proposed[0].Request.OperationID}))
		Expect(f.digests).To(Equal([]string{"d1"}))
		Expect(f.actors).To(Equal([]contract.Actor{member}))
		Expect(f.cancelled).To(BeEmpty())
	})

	It("keeps the result small enough for the chat to store as valid JSON", func() {
		f, _, chats := build()
		big := make([]byte, 0, 4000)
		big = append(big, `"`...)
		for i := 0; i < 3000; i++ {
			big = append(big, 'x')
		}
		big = append(big, `"`...)
		f.proposal.Effects.Sample = big
		res, err := chats.ExecuteIn(ctx, member, "conv1", insert, rows)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Applied.Summary).To(BeEmpty())
		Expect(len(res.JSON())).To(BeNumerically("<", 1500))
	})

	It("applies a creation and a schema change too", func() {
		f, _, chats := build()
		_, err := chats.ExecuteIn(ctx, member, "conv1", tools.ToolName("tables", "databases-create"), args(`{"name":"Orders"}`))
		Expect(err).NotTo(HaveOccurred())
		_, err = chats.ExecuteIn(ctx, member, "conv1", tools.ToolName("tables", "tables-drop"), args(`{"resourceId":"db-one","table":"t"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(f.confirmed).To(HaveLen(2))
	})

	It("never confirms for an adapter that is not the chats one, even over an owner that could", func() {
		f, plain, _ := build()
		res, err := plain.Execute(ctx, member, insert, rows)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Kind).To(Equal(tools.ResultProposal))
		Expect(f.confirmed).To(BeEmpty())
	})

	It("keeps a proposal when the owner cannot apply, so nothing is lost", func() {
		only := &actFake{fake: &fake{list: newFake().list}}
		res, err := tools.NewChatToolAdapter(only).ExecuteIn(ctx, member, "conv1", insert, rows)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Kind).To(Equal(tools.ResultProposal))
	})

	It("withdraws the proposal and reports the error when the apply is refused as stale", func() {
		f, _, chats := build()
		f.confirmErr = gateway.ErrStale
		_, err := chats.ExecuteIn(ctx, member, "conv1", insert, rows)
		Expect(err).To(MatchError(gateway.ErrStale))
		Expect(f.cancelled).To(Equal([]string{f.proposed[0].Request.OperationID}))
	})

	It("tells the model the call was invalid when the add-on refuses it, and withdraws the proposal", func() {
		f, _, chats := build()
		f.confirmErr = gateway.ErrInvalidRequest
		res, err := chats.ExecuteIn(ctx, member, "conv1", insert, rows)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Kind).To(Equal(tools.ResultError))
		Expect(res.Code).To(Equal(tools.CodeInvalidRequest))
		Expect(f.cancelled).To(HaveLen(1))
	})

	It("does not withdraw, and does not claim success, when the outcome is unknown", func() {
		f, _, chats := build()
		f.confirmErr = &gateway.OutcomeUnknown{OperationID: "x"}
		res, err := chats.ExecuteIn(ctx, member, "conv1", insert, rows)
		var unknown *gateway.OutcomeUnknown
		Expect(errors.As(err, &unknown)).To(BeTrue())
		Expect(res.Applied).To(BeNil())
		Expect(f.cancelled).To(BeEmpty())
	})

	It("does not report a change that the add-on rejected at the apply", func() {
		f, _, chats := build()
		f.state = gateway.ProposalRejected
		res, err := chats.ExecuteIn(ctx, member, "conv1", insert, rows)
		Expect(err).To(HaveOccurred())
		Expect(res.Applied).To(BeNil())
	})
})
