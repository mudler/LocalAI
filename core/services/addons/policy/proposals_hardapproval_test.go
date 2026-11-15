// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"strings"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A write tool that declares approval "always" waits for a person whether or
// not the add-on has a credential. Without a credential nothing can relax it.
var _ = Describe("a tool that always asks, in an add-on without a credential", Label("proposals", "approval"), func() {
	ctx := context.Background()
	owner := named("owner")
	const (
		hardArgs  = `{"tool":"hard","rows":[1]}`
		plainArgs = `{"tool":"plain","rows":[1]}`
	)

	open := func(overridable bool) *brokerFixture {
		caps := contract.Capabilities{
			Tools: []string{"hard", "plain"},
			ToolDefinitions: []contract.ToolDefinition{
				{Name: "hard", Kind: contract.ToolKindWrite, Approval: contract.ApprovalAlways, ApprovalOverridable: overridable},
				{Name: "plain", Kind: contract.ToolKindWrite},
			},
		}
		return newBrokerFixture(ModeNamedUser, func(o *BrokerOptions) {
			o.Tools = manifestTools{caps: caps}
			o.ConfirmPerson = func(a contract.Actor) bool { return strings.HasPrefix(a.CredentialID, "session:") }
			// A setting that relaxes must never reach an add-on without a credential.
			o.RelaxApproval = func(context.Context, contract.Actor, string, string) bool { return true }
		})
	}
	propose := func(f *brokerFixture, key ResourceKey, who contract.Actor, args string) Proposal {
		p, err := f.broker.Propose(ctx, who, f.request(key, "op-1", "write", args))
		Expect(err).NotTo(HaveOccurred())
		return p
	}

	It("holds the proposal for a person, with the hold and the expiry on it", func() {
		f := open(false)
		key := f.create(owner)
		p := propose(f, key, owner, hardArgs)
		Expect(p.AwaitsPerson).To(BeTrue())
		Expect(p.Hold).To(Equal("approval_always"))
		Expect(p.ExpiresAt).To(BeTemporally(">", f.clock.now()))
		Expect(f.rt.applyCalls()).To(BeEmpty())
	})

	It("holds an overridable tool too, since only a credential can relax it", func() {
		f := open(true)
		key := f.create(owner)
		p := propose(f, key, owner, hardArgs)
		Expect(p.AwaitsPerson).To(BeTrue())
		Expect(p.Hold).To(Equal("approval_always"))
	})

	It("applies once when a person confirms, and a repeat sends nothing", func() {
		f := open(false)
		key := f.create(owner)
		p := propose(f, key, owner, hardArgs)
		receipt, err := f.broker.ConfirmBound(ctx, session("owner"), "op-1", p.Digest)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal(contract.ReceiptCommitted))
		_, err = f.broker.ConfirmBound(ctx, session("owner"), "op-1", p.Digest)
		Expect(err).NotTo(HaveOccurred())
		Expect(f.rt.applyCalls()).To(HaveLen(1))
		Expect(f.stored("op-1")).To(Equal(gateway.ProposalApplied))
	})

	It("never runs for a caller that is not a person, with or without the chat mark", func() {
		f := open(false)
		key := f.create(owner)
		p := propose(f, key, owner, hardArgs)
		callers := []contract.Actor{
			chat("owner"),  // model in a chat
			named("owner"), // API key
			{UserID: "owner", CredentialID: "trigger:1", Mode: "named-user"}, // trigger
			{UserID: "owner", CredentialID: "job:1", Mode: "named-user"},     // job
			{UserID: "owner", CredentialID: "agent:1", Mode: "named-user"},   // sub-agent
			{UserID: "owner", Mode: "named-user"},                            // no chat, no credential
		}
		for _, a := range callers {
			_, err := f.broker.ConfirmBound(ctx, a, "op-1", p.Digest)
			Expect(err).To(MatchError(ErrForbidden), a.CredentialID)
			_, err = f.broker.ConfirmBound(gateway.WithChatConfirmed(ctx), a, "op-1", p.Digest)
			Expect(err).To(MatchError(ErrForbidden), a.CredentialID)
		}
		Expect(f.rt.applyCalls()).To(BeEmpty())
		Expect(f.stored("op-1")).To(Equal(gateway.ProposalPending))
	})

	It("does not let a person's browser confirm through a chat turn mark", func() {
		f := open(false)
		key := f.create(owner)
		p := propose(f, key, owner, hardArgs)
		_, err := f.broker.ConfirmBound(gateway.WithChatTurn(ctx), session("owner"), "op-1", p.Digest)
		Expect(err).To(MatchError(ErrForbidden))
		Expect(f.rt.applyCalls()).To(BeEmpty())
	})

	It("does not let the messaging-chat mark confirm it, even for a credential that stands for a person", func() {
		f := open(false)
		key := f.create(owner)
		p := propose(f, key, owner, hardArgs)
		_, err := f.broker.ConfirmBound(gateway.WithChatConfirmed(ctx), session("owner"), "op-1", p.Digest)
		Expect(err).To(MatchError(ErrForbidden))
		Expect(f.rt.applyCalls()).To(BeEmpty())
		_, err = f.broker.ConfirmBound(ctx, session("owner"), "op-1", p.Digest)
		Expect(err).NotTo(HaveOccurred())
		Expect(f.rt.applyCalls()).To(HaveLen(1))
	})

	It("never runs a cancelled or an expired proposal", func() {
		f := open(false)
		key := f.create(owner)
		propose(f, key, owner, hardArgs)
		Expect(f.broker.Cancel(ctx, session("owner"), "op-1")).To(Succeed())
		_, err := f.broker.Confirm(ctx, session("owner"), "op-1")
		Expect(err).To(HaveOccurred())

		p2, err := f.broker.Propose(ctx, owner, f.request(key, "op-2", "write", hardArgs))
		Expect(err).NotTo(HaveOccurred())
		f.clock.advance(contract.DefaultLimits().ProposalExpiry + 1)
		_, err = f.broker.ConfirmBound(ctx, session("owner"), "op-2", p2.Digest)
		Expect(err).To(MatchError(ErrProposalExpired))
		Expect(f.rt.applyCalls()).To(BeEmpty())
	})

	It("keeps today's behavior for a tool without approval always", func() {
		f := open(false)
		key := f.create(owner)
		p := propose(f, key, owner, plainArgs)
		Expect(p.AwaitsPerson).To(BeFalse())
		Expect(p.Hold).To(BeEmpty())
		_, err := f.broker.Confirm(ctx, session("owner"), "op-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(f.rt.applyCalls()).To(HaveLen(1))
	})
})
