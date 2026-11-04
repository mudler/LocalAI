// SPDX-License-Identifier: MIT
package addons

import "errors"

// errInvalidUpgradeTransition means the phase or the event is not one the
// reducer knows. The phase can come from a journal on disk, so it is an error
// for the caller to handle, not a reason to crash a worker.
var errInvalidUpgradeTransition = errors.New("invalid upgrade state or event")

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

func upgradeTransition(phase lifecyclePhase, event upgradeEvent) (next lifecyclePhase, rule string, err error) {
	switch phase {
	case lifecycleCompleted, lifecycleFailed, lifecycleRecovery:
		return phase, "U0-terminal", nil
	case lifecycleAccepted, lifecycleRunning:
		switch event {
		case upgradeDetach, upgradeRetry:
			return phase, "U1-observe", nil
		case upgradeStart:
			return lifecycleRunning, "U2-start", nil
		case upgradeComplete:
			return lifecycleCompleted, "U3-complete", nil
		case upgradeFail:
			return lifecycleFailed, "U4-fail", nil
		case upgradeInterrupt:
			return lifecycleFailed, "U5-interrupted", nil
		case upgradeRecover:
			return lifecycleRecovery, "U6-recovery", nil
		}
	}
	return phase, "", errInvalidUpgradeTransition
}

// isActive reports whether an operation in this phase still blocks its add-on:
// anything but completed or failed, including recovery-required.
func isActive(phase string) bool {
	return phase != string(lifecycleCompleted) && phase != string(lifecycleFailed)
}

// sameTransition reports whether another operation asks for the same pinned
// upgrade as cmd: same add-on, same expected and candidate digest. Only a
// failed attempt leaves the transition open for a new operation.
func sameTransition(cmd, other LifecycleCommand, status OperationStatus) bool {
	return other.Action == "upgrade" && other.AddonID == cmd.AddonID &&
		other.ExpectedDigest == cmd.ExpectedDigest && other.CandidateDigest == cmd.CandidateDigest &&
		status.Phase != string(lifecycleFailed)
}
