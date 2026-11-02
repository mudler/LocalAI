// SPDX-License-Identifier: MIT
package addons

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("upgrade observation reducer", func() {
	It("traces completion, retries, detach, interruption and late delivery without replay", func() {
		for _, trace := range []struct {
			events, rules []string
			end           string
		}{
			{[]string{"start", "complete"}, []string{"U2-start", "U3-complete"}, "completed"},
			{[]string{"retry", "start", "retry", "complete"}, []string{"U1-observe", "U2-start", "U1-observe", "U3-complete"}, "completed"},
			{[]string{"start", "detach", "complete"}, []string{"U2-start", "U1-observe", "U3-complete"}, "completed"},
			{[]string{"interrupt", "start"}, []string{"U5-interrupted", "U0-terminal"}, "failed"},
			{[]string{"recover", "complete"}, []string{"U6-recovery", "U0-terminal"}, "recovery-required"},
		} {
			phase := upgradePhase("accepted")
			for n, event := range trace.events {
				var rule string
				phase, rule = upgradeTransition(upgradePhase(phase), upgradeEvent(event))
				Expect(rule).To(Equal(trace.rules[n]))
			}
			Expect(string(phase)).To(Equal(trace.end))
		}
	})
	It("keeps terminals immutable for every event and gives each active state a terminal exit", func() {
		events := []string{"start", "retry", "detach", "complete", "fail", "interrupt", "recover"}
		for _, phase := range []upgradePhase{upgradeAccepted, upgradeRunning, upgradeCompleted, upgradeFailed, upgradeRecovery} {
			for _, event := range []upgradeEvent{upgradeStart, upgradeComplete, upgradeFail, upgradeInterrupt, upgradeRecover, upgradeDetach, upgradeRetry} {
				next, rule := upgradeTransition(phase, event)
				Expect(next).NotTo(BeEmpty())
				Expect(rule).NotTo(BeEmpty())
			}
		}
		for _, phase := range []string{"completed", "failed", "recovery-required"} {
			for _, event := range events {
				next, _ := upgradeTransition(upgradePhase(phase), upgradeEvent(event))
				Expect(string(next)).To(Equal(phase))
			}
		}
		for _, phase := range []string{"accepted", "running"} {
			next, rule := upgradeTransition(upgradePhase(phase), "interrupt")
			Expect(string(next)).To(Equal("failed"))
			Expect(rule).To(Equal("U5-interrupted"))
		}
	})
})
