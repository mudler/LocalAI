// SPDX-License-Identifier: MIT
package memory

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Replay traces", func() {
	It("pins every rule and effect in order, not only final status", func() {
		Expect(ExampleTraces()).To(HaveLen(16))
		for _, trace := range ExampleTraces() {
			transitions := RunTrace(trace.Initial, trace.Events)
			Expect(transitions).To(HaveLen(len(trace.Rules)), trace.Name)
			for i, tr := range transitions {
				Expect(tr.RuleID).To(Equal(trace.Rules[i]), "%s step %d", trace.Name, i)
				Expect(effectNames(tr.Effects)).To(Equal(trace.Effects[i]), "%s step %d", trace.Name, i)
				Expect(CheckInvariants(tr.State)).To(Succeed(), "%s step %d", trace.Name, i)
			}
			Expect(stateName(transitions[len(transitions)-1].State)).To(Equal(trace.Final), trace.Name)
		}
	})
})
