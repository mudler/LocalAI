// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These specs follow the proposal flow model in pkg/addons/model/proposalflow.
// Rule ids: R29 admission refused returns to Pending, R34 a not-sent dispatch
// is retryable, R38 a definite refusal or conflict is Rejected, R39 only a
// send that may have happened is an unknown outcome.
var _ = Describe("what a failed apply means", Label("proposals"), func() {
	ctx := context.Background()
	var (
		f     *brokerFixture
		owner = named("owner")
		key   ResourceKey
	)
	propose := func(op string) Proposal {
		p, err := f.broker.Propose(ctx, owner, f.request(key, op, "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		return p
	}
	BeforeEach(func() {
		f = newBrokerFixture(ModeNamedUser)
		key = f.create(owner)
	})

	It("R34: returns an admission that was refused before the wire to pending, and a repeat applies", func() {
		propose("op-1")
		f.rt.applyErr = fmt.Errorf("slot: %w", addonruntime.ErrNotSent)
		_, err := f.broker.Confirm(ctx, session("owner"), "op-1")
		Expect(err).To(HaveOccurred())
		var unknown *gateway.OutcomeUnknown
		Expect(errors.As(err, &unknown)).To(BeFalse(), "a request that was never sent is not an unknown outcome")
		Expect(errors.Is(err, addonruntime.ErrNotSent)).To(BeTrue())
		Expect(f.stored("op-1")).To(Equal(gateway.ProposalPending))
		Expect(f.rt.applyCalls()).To(BeEmpty())
		// The resource is not blocked, and the same proposal can be confirmed again.
		propose("op-2")
		receipt, err := f.broker.Confirm(ctx, session("owner"), "op-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal(contract.ReceiptCommitted))
		Expect(f.stored("op-1")).To(Equal(gateway.ProposalApplied))
	})

	It("R35, R52: a hold that cancels the dispatch before the wire ends the proposal as cancelled, not unknown", func() {
		propose("op-1")
		cctx, cancel := context.WithCancel(ctx)
		defer cancel()
		f.rt.applyErr = fmt.Errorf("cancelled before the first byte: %w", addonruntime.ErrNotSent)
		f.broker.hooks.afterApply = func(string) { cancel() }
		_, err := f.broker.Confirm(cctx, session("owner"), "op-1")
		f.broker.hooks.afterApply = nil
		Expect(err).To(MatchError(ErrForbidden))
		Expect(f.stored("op-1")).To(Equal(gateway.ProposalCancelled))
		propose("op-2")
	})

	It("R38: records a conflict of the add-on as rejected, not unknown", func() {
		propose("op-1")
		f.rt.applyErr = addonruntime.ErrConflict
		_, err := f.broker.Confirm(ctx, session("owner"), "op-1")
		Expect(err).To(MatchError(ErrProposalConflict))
		Expect(gateway.StatusFor(err)).To(Equal(http.StatusForbidden))
		Expect(f.stored("op-1")).To(Equal(gateway.ProposalRejected))
		propose("op-2")
	})

	It("R39: still records an unknown outcome when the request may have been sent", func() {
		propose("op-1")
		f.rt.applyErr = addonruntime.ErrRuntime
		_, err := f.broker.Confirm(ctx, session("owner"), "op-1")
		var unknown *gateway.OutcomeUnknown
		Expect(errors.As(err, &unknown)).To(BeTrue())
		Expect(f.stored("op-1")).To(Equal(gateway.ProposalOutcomeUnknown))
		_, err = f.broker.Propose(ctx, owner, f.request(key, "op-2", "write", `{"rows":[2]}`))
		Expect(err).To(MatchError(ErrOutcomeBlocked))
	})

	It("reports a not-sent confirmation through the gateway as an outage that sent nothing, never as unknown", func() {
		p := propose("op-1")
		g := &gateway.Gateway{Proposals: f.broker.Gateway(), Revalidate: func(context.Context, contract.Actor) error { return nil }}
		f.rt.applyErr = fmt.Errorf("slot: %w", addonruntime.ErrNotSent)
		_, err := g.ConfirmProposal(ctx, session("owner"), "op-1", p.Digest)
		Expect(err).To(HaveOccurred())
		var unknown *gateway.OutcomeUnknown
		Expect(errors.As(err, &unknown)).To(BeFalse())
		var unavailable *gateway.ProposalUnavailable
		Expect(errors.As(err, &unavailable)).To(BeTrue())
		Expect(gateway.StatusFor(err)).To(Equal(http.StatusServiceUnavailable))
		// The retry the error asks for is possible.
		got, err := g.ConfirmProposal(ctx, session("owner"), "op-1", p.Digest)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(gateway.ProposalApplied))
		// A possibly-sent failure is still reported as unknown.
		p2 := propose("op-2")
		f.rt.applyErr = addonruntime.ErrRuntime
		_, err = g.ConfirmProposal(ctx, session("owner"), "op-2", p2.Digest)
		Expect(errors.As(err, &unknown)).To(BeTrue())
	})
})
