// SPDX-License-Identifier: MIT
package addons

// upgradeTransition is the owner's observation reducer. Effects remain in the
// existing lifecycle and recovery journals; observer detach never emits work.
// Every terminal result is sticky, including after late progress or a retry.
type upgradeEvent string

const (
	upgradeStart     upgradeEvent = "start"
	upgradeComplete  upgradeEvent = "complete"
	upgradeFail      upgradeEvent = "fail"
	upgradeInterrupt upgradeEvent = "interrupt"
	upgradeRecover   upgradeEvent = "recover"
	upgradeDetach    upgradeEvent = "detach"
	upgradeRetry     upgradeEvent = "retry"
)

func upgradeTransition(phase lifecyclePhase, event upgradeEvent) (next lifecyclePhase, rule string) {
	switch phase {
	case lifecycleCompleted, lifecycleFailed, lifecycleRecovery:
		return phase, "U0-terminal"
	case lifecycleAccepted, lifecycleRunning:
		switch event {
		case upgradeDetach, upgradeRetry:
			return phase, "U1-observe"
		case upgradeStart:
			return lifecycleRunning, "U2-start"
		case upgradeComplete:
			return lifecycleCompleted, "U3-complete"
		case upgradeFail:
			return lifecycleFailed, "U4-fail"
		case upgradeInterrupt:
			return lifecycleFailed, "U5-interrupted"
		case upgradeRecover:
			return lifecycleRecovery, "U6-recovery"
		}
	}
	panic("invalid upgrade state or event")
}
