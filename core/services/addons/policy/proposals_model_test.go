// SPDX-License-Identifier: MIT
package policy

import (
	"context"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/model/proposalflow"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// modelStep finds the step of a model trace that fired the given rule and
// returns the durable state the model says follows it. The trace is the
// oracle: the broker must store what the model says.
func modelStep(trace, rule string) string {
	for _, t := range proposalflow.Traces() {
		if t.Name != trace {
			continue
		}
		Expect(t.Verify()).To(BeEmpty(), "the model trace must hold before it is used as an oracle")
		for _, r := range proposalflow.Run(proposalflow.Unproposed{}, t.Events()) {
			if r.Rule == rule {
				return r.After.Durable()
			}
		}
	}
	Fail(fmt.Sprintf("model trace %q has no step for rule %s", trace, rule))
	return ""
}

// TestBrokerFollowsModel replays the apply outcomes of the model against a real
// broker: the stored state after each outcome equals the durable state the
// model gives for that rule.
var _ = Describe("TestBrokerFollowsModel", Label("proposals"), func() {
	ctx := context.Background()
	It("retry-after-not-sent: R34 then R37", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		_, err := f.broker.Propose(ctx, owner, f.request(key, "op", "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		f.rt.applyErr = fmt.Errorf("slot: %w", addonruntime.ErrNotSent)
		_, _ = f.broker.Confirm(ctx, session("owner"), "op")
		Expect(string(f.stored("op"))).To(Equal(modelStep("retry-after-not-sent", "R34")))
		_, err = f.broker.Confirm(ctx, session("owner"), "op")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(f.stored("op"))).To(Equal(modelStep("retry-after-not-sent", "R37")))
	})
	It("addon-conflict-is-rejected: R38", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		_, err := f.broker.Propose(ctx, owner, f.request(key, "op", "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		f.rt.applyErr = addonruntime.ErrConflict
		_, _ = f.broker.Confirm(ctx, session("owner"), "op")
		Expect(string(f.stored("op"))).To(Equal(modelStep("addon-conflict-is-rejected", "R38")))
	})
	It("timeout-unknown-then-receipt-committed: R39 then R40", func() {
		f := newBrokerFixture(ModeNamedUser)
		owner := named("owner")
		key := f.create(owner)
		_, err := f.broker.Propose(ctx, owner, f.request(key, "op", "write", `{"rows":[1]}`))
		Expect(err).NotTo(HaveOccurred())
		f.rt.mu.Lock()
		f.rt.commitFirst = true
		f.rt.mu.Unlock()
		var unknown *gateway.OutcomeUnknown
		_, err = f.broker.Confirm(ctx, session("owner"), "op")
		Expect(err).To(HaveOccurred())
		Expect(errors.As(err, &unknown)).To(BeTrue())
		Expect(string(f.stored("op"))).To(Equal(modelStep("timeout-unknown-then-receipt-committed", "R39")))
		_, err = f.broker.Reconcile(ctx, "op")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(f.stored("op"))).To(Equal(modelStep("timeout-unknown-then-receipt-committed", "R40")))
	})
})
