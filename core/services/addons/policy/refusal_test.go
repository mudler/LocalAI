// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// refusingRuntime answers a preview or an apply as the add-on's own
// invalid-request refusal when switched on, the way the runtime client
// reports an HTTP 400 of the add-on.
type refusingRuntime struct {
	*fixtureRuntime
	preview, apply atomic.Bool
	applies        atomic.Int32
}

func (r *refusingRuntime) Preview(ctx context.Context, e contract.Envelope) (contract.Preview, error) {
	if r.preview.Load() {
		return contract.Preview{}, &addonruntime.Refusal{Status: 400, Reason: "invalid request"}
	}
	return r.fixtureRuntime.Preview(ctx, e)
}

func (r *refusingRuntime) Apply(ctx context.Context, e contract.Envelope, rev contract.Revision) (contract.Receipt, error) {
	r.applies.Add(1)
	if r.apply.Load() {
		return contract.Receipt{}, &addonruntime.Refusal{Status: 400, Reason: "invalid request"}
	}
	return r.fixtureRuntime.Apply(ctx, e, rev)
}

var _ = Describe("proposals the add-on refuses as invalid", Label("proposals"), func() {
	ctx := context.Background()
	setup := func() (*brokerFixture, *refusingRuntime) {
		rt := newFixtureRuntime()
		wrapped := &refusingRuntime{fixtureRuntime: rt}
		f := openBrokerFixture(ModeNamedUser, GinkgoT().TempDir(), rt, &fixtureInstances{gen: 1, rt: wrapped}, &clock{t: time.Now()}, &account{blocked: map[string]bool{}})
		DeferCleanup(func() { f.close() })
		return f, wrapped
	}
	invalid := func(e error) {
		ExpectWithOffset(1, errors.Is(e, gateway.ErrInvalidRequest)).To(BeTrue(), "%v", e)
		var unknown *gateway.OutcomeUnknown
		ExpectWithOffset(1, errors.As(e, &unknown)).To(BeFalse())
		ExpectWithOffset(1, errors.Is(e, gateway.ErrUnavailable)).To(BeFalse())
		ExpectWithOffset(1, gateway.StatusFor(e)).NotTo(Equal(503))
	}

	It("refuses a proposal whose preview the add-on refused, storing nothing", func() {
		f, rt := setup()
		owner := named("owner")
		key := f.create(owner)
		rt.preview.Store(true)
		_, err := f.broker.Propose(ctx, owner, f.request(key, "bad-op", "write", `{"rows":[{"a":1}]}`))
		invalid(err)
		pending, perr := f.broker.Pending(ctx, owner)
		Expect(perr).NotTo(HaveOccurred())
		Expect(pending).To(BeEmpty())
		_, perr = f.broker.Get(ctx, owner, "bad-op")
		Expect(perr).To(HaveOccurred())
		// The operation ID was never consumed: the same ID proposes again
		// once the request is valid.
		rt.preview.Store(false)
		p, err := f.broker.Propose(ctx, owner, f.request(key, "bad-op", "write", `{"rows":[{"a":1}]}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(p.State).To(Equal(gateway.ProposalPending))
	})

	It("settles a confirmation the add-on refused as rejected, never as an unknown outcome", func() {
		f, rt := setup()
		owner := named("owner")
		key := f.create(owner)
		p, err := f.broker.Propose(ctx, owner, f.request(key, "bad-apply", "write", `{"rows":[{"a":1}]}`))
		Expect(err).NotTo(HaveOccurred())
		rt.apply.Store(true)
		_, err = f.broker.Confirm(ctx, session("owner"), p.ID)
		invalid(err)
		Expect(rt.applies.Load()).To(Equal(int32(1)))
		Expect(f.stored("bad-apply")).To(Equal(gateway.ProposalRejected))
		// Nothing is left unresolved, so the resource takes other writes.
		rt.apply.Store(false)
		next, err := f.broker.Propose(ctx, owner, f.request(key, "next-op", "write", `{"rows":[{"a":2}]}`))
		Expect(err).NotTo(HaveOccurred())
		receipt, err := f.broker.Confirm(ctx, session("owner"), next.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.State).To(Equal(contract.ReceiptCommitted))
	})
})
