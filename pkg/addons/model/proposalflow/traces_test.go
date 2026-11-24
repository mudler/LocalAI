// SPDX-License-Identifier: MIT

package proposalflow

import (
	"fmt"
	"sort"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("the scenario traces", func() {
	for _, tr := range Traces() {
		ginkgo.It("holds trace "+tr.Name, func() {
			Expect(tr.Verify()).To(BeEmpty())
		})
	}

	ginkgo.It("has unique trace names", func() {
		seen := map[string]bool{}
		for _, tr := range Traces() {
			Expect(seen[tr.Name]).To(BeFalse(), "duplicate trace %s", tr.Name)
			seen[tr.Name] = true
		}
	})

	// This names the scenarios the model must keep.
	ginkgo.It("keeps the required scenarios", func() {
		want := []string{
			"happy-path", "retry-after-not-sent", "retry-after-admission-refused", "timeout-unknown-then-receipt-committed",
			"unknown-bounded-then-manual-resolution", "two-actors-racing-confirm", "hold-during-prepared-dispatch",
			"hold-during-sent-request", "generation-change-while-pending", "duplicate-confirm-after-applied",
			"editor-schema-propose-refused", "out-of-order-receipt",
		}
		have := map[string]bool{}
		for _, tr := range Traces() {
			have[tr.Name] = true
		}
		for _, n := range want {
			Expect(have[n]).To(BeTrue(), "missing trace %s", n)
		}
	})

	// The traces and the search together fire every rule id, so no rule is dead.
	ginkgo.It("fires every rule", func() {
		fired := map[string]bool{}
		for _, tr := range Traces() {
			for _, r := range Run(Unproposed{}, tr.Events()) {
				fired[r.Rule] = true
			}
		}
		for id := range Explore().RuleHits {
			fired[id] = true
		}
		var dead []string
		for _, r := range Rules {
			if !fired[r.ID] {
				dead = append(dead, r.ID)
			}
		}
		sort.Strings(dead)
		Expect(dead).To(BeEmpty(), "rules that never fire")
	})

	// The hand-written traces, not only the search, exercise each transition
	// rule. A rule with no named trace is reported, not failed.
	ginkgo.It("reports transition rules that only the search fires", func() {
		fired := map[string]bool{}
		for _, tr := range Traces() {
			for _, r := range Run(Unproposed{}, tr.Events()) {
				fired[r.Rule] = true
			}
		}
		var missing []string
		for _, r := range Rules {
			if r.Kind == KindTransition && !fired[r.ID] {
				missing = append(missing, r.ID)
			}
		}
		if len(missing) > 0 {
			_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "transition rules with no named trace (covered by the search only): %v\n", missing)
		}
	})
})
