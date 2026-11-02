// SPDX-License-Identifier: MIT
package addons

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("upgrade observation reducer", func() {
	It("traces completion, retries, detach, interruption and late delivery without replay", func() {
		for _, trace := range []struct {
			events []upgradeEvent
			rules  []string
			end    lifecyclePhase
		}{
			{[]upgradeEvent{upgradeStart, upgradeComplete}, []string{"U2-start", "U3-complete"}, lifecycleCompleted},
			{[]upgradeEvent{upgradeRetry, upgradeStart, upgradeRetry, upgradeComplete}, []string{"U1-observe", "U2-start", "U1-observe", "U3-complete"}, lifecycleCompleted},
			{[]upgradeEvent{upgradeStart, upgradeDetach, upgradeComplete}, []string{"U2-start", "U1-observe", "U3-complete"}, lifecycleCompleted},
			{[]upgradeEvent{upgradeInterrupt, upgradeStart}, []string{"U5-interrupted", "U0-terminal"}, lifecycleFailed},
			{[]upgradeEvent{upgradeRecover, upgradeComplete}, []string{"U6-recovery", "U0-terminal"}, lifecycleRecovery},
		} {
			phase := lifecycleAccepted
			for n, event := range trace.events {
				var rule string
				phase, rule = upgradeTransition(phase, event)
				Expect(rule).To(Equal(trace.rules[n]))
			}
			Expect(phase).To(Equal(trace.end))
		}
	})
	It("keeps terminals immutable for every event and gives each active state a terminal exit", func() {
		events := []upgradeEvent{upgradeStart, upgradeRetry, upgradeDetach, upgradeComplete, upgradeFail, upgradeInterrupt, upgradeRecover}
		for _, phase := range []lifecyclePhase{lifecycleAccepted, lifecycleRunning, lifecycleCompleted, lifecycleFailed, lifecycleRecovery} {
			for _, event := range []upgradeEvent{upgradeStart, upgradeComplete, upgradeFail, upgradeInterrupt, upgradeRecover, upgradeDetach, upgradeRetry} {
				next, rule := upgradeTransition(phase, event)
				Expect(next).NotTo(BeEmpty())
				Expect(rule).NotTo(BeEmpty())
			}
		}
		for _, phase := range []lifecyclePhase{lifecycleCompleted, lifecycleFailed, lifecycleRecovery} {
			for _, event := range events {
				next, _ := upgradeTransition(phase, event)
				Expect(next).To(Equal(phase))
			}
		}
		for _, phase := range []lifecyclePhase{lifecycleAccepted, lifecycleRunning} {
			next, rule := upgradeTransition(phase, upgradeInterrupt)
			Expect(next).To(Equal(lifecycleFailed))
			Expect(rule).To(Equal("U5-interrupted"))
		}
	})
})
