// SPDX-License-Identifier: MIT
package addons

// upgradeTransition is the owner's observation reducer. Effects remain in the
// existing lifecycle and recovery journals; observer detach never emits work.
// Every terminal result is sticky, including after late progress or a retry.
type upgradePhase string
type upgradeEvent string

const (
	upgradeAccepted  upgradePhase = "accepted"
	upgradeRunning   upgradePhase = "running"
	upgradeCompleted upgradePhase = "completed"
	upgradeFailed    upgradePhase = "failed"
	upgradeRecovery  upgradePhase = "recovery-required"
	upgradeStart     upgradeEvent = "start"
	upgradeComplete  upgradeEvent = "complete"
	upgradeFail      upgradeEvent = "fail"
	upgradeInterrupt upgradeEvent = "interrupt"
	upgradeRecover   upgradeEvent = "recover"
	upgradeDetach    upgradeEvent = "detach"
	upgradeRetry     upgradeEvent = "retry"
)

func upgradeTransition(phase upgradePhase, event upgradeEvent) (next upgradePhase, rule string) {
	switch phase {
	case "completed", "failed", "recovery-required":
		return phase, "U0-terminal"
	case "accepted", "running":
		switch event {
		case "detach", "retry":
			return phase, "U1-observe"
		case "start":
			return "running", "U2-start"
		case "complete":
			return "completed", "U3-complete"
		case "fail":
			return "failed", "U4-fail"
		case "interrupt":
			return "failed", "U5-interrupted"
		case "recover":
			return "recovery-required", "U6-recovery"
		}
	}
	panic("invalid upgrade state or event")
}
