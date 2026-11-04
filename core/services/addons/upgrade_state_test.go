// SPDX-License-Identifier: MIT
package addons

import (
	"fmt"

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
				phase, rule, _ = upgradeTransition(phase, event)
				Expect(rule).To(Equal(trace.rules[n]))
			}
			Expect(phase).To(Equal(trace.end))
		}
	})
	It("keeps terminals immutable for every event and gives each active state a terminal exit", func() {
		events := []upgradeEvent{upgradeStart, upgradeRetry, upgradeDetach, upgradeComplete, upgradeFail, upgradeInterrupt, upgradeRecover}
		for _, phase := range []lifecyclePhase{lifecycleAccepted, lifecycleRunning, lifecycleCompleted, lifecycleFailed, lifecycleRecovery} {
			for _, event := range []upgradeEvent{upgradeStart, upgradeComplete, upgradeFail, upgradeInterrupt, upgradeRecover, upgradeDetach, upgradeRetry} {
				next, rule, _ := upgradeTransition(phase, event)
				Expect(next).NotTo(BeEmpty())
				Expect(rule).NotTo(BeEmpty())
			}
		}
		for _, phase := range []lifecyclePhase{lifecycleCompleted, lifecycleFailed, lifecycleRecovery} {
			for _, event := range events {
				next, _, _ := upgradeTransition(phase, event)
				Expect(next).To(Equal(phase))
			}
		}
		for _, phase := range []lifecyclePhase{lifecycleAccepted, lifecycleRunning} {
			next, rule, _ := upgradeTransition(phase, upgradeInterrupt)
			Expect(next).To(Equal(lifecycleFailed))
			Expect(rule).To(Equal("U5-interrupted"))
		}
	})
	It("refuses an unknown phase or event with an error instead of panicking", func() {
		for _, phase := range []lifecyclePhase{"", "garbage", lifecycleIntent} {
			next, _, err := upgradeTransition(phase, upgradeStart)
			Expect(err).To(MatchError(errInvalidUpgradeTransition))
			Expect(next).To(Equal(phase))
		}
		_, _, err := upgradeTransition(lifecycleAccepted, upgradeEvent("bogus"))
		Expect(err).To(MatchError(errInvalidUpgradeTransition))
	})
	It("drops the oldest finished views once too many pile up", func() {
		m := &Manager{upgradeViews: map[string]upgradeSnapshot{}, journal: journal{Operations: map[string]journalOperation{}}}
		for i := 0; i < maxFinishedUpgradeViews+10; i++ {
			id := fmt.Sprintf("op-%04d", i)
			m.journal.Operations[id] = journalOperation{Async: true, Status: OperationStatus{OperationID: id, Phase: string(lifecycleAccepted)}}
			m.refreshUpgradeViews()
			op := m.journal.Operations[id]
			op.Status.Phase = string(lifecycleCompleted)
			m.journal.Operations[id] = op
			m.refreshUpgradeViews()
		}
		m.journal.Operations["live"] = journalOperation{Async: true, Status: OperationStatus{OperationID: "live", Phase: string(lifecycleRunning)}}
		m.refreshUpgradeViews()
		Expect(m.upgradeViews).To(HaveLen(maxFinishedUpgradeViews + 1))
		Expect(m.upgradeViews).To(HaveKey("live"))
		Expect(m.upgradeViews).NotTo(HaveKey("op-0000"))
		Expect(m.upgradeViews).To(HaveKey(fmt.Sprintf("op-%04d", maxFinishedUpgradeViews+9)))

	})
})
