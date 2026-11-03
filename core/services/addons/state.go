// SPDX-License-Identifier: MIT
package addons

import (
	"slices"

	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/addons/packages"
)

// lifecyclePhase is shared by the owner journal and upgrade reducer. The
// operation DTO retains string fields for compatibility with existing callers.
type lifecyclePhase string

const (
	lifecycleAccepted  lifecyclePhase = "accepted"
	lifecycleRunning   lifecyclePhase = "running"
	lifecycleCompleted lifecyclePhase = "completed"
	lifecycleFailed    lifecyclePhase = "failed"
	lifecycleRecovery  lifecyclePhase = "recovery-required"
	lifecycleIntent    lifecyclePhase = "intent"
	lifecyclePrepared  lifecyclePhase = "prepared"
	lifecycleApplied   lifecyclePhase = "applied"
)

type Generation struct {
	Number  uint64
	Receipt packages.PackageReceipt
}
type Status struct {
	AddonID, Identity, DataDirectory, GrantsPath string
	Installed, DesiredEnabled                    bool
	Generation                                   uint64
	// InstalledAt is when the add-on was first installed, RFC 3339 in UTC. An
	// upgrade keeps it; an uninstall followed by an install sets it again. A
	// record from before the field has none.
	InstalledAt string `json:",omitempty"`
	Generations []Generation
	// Observation is deliberately not part of durable intent.
	ObservedHealth string `json:"-"`
	StartEligible  bool   `json:"-"`
}
type LifecycleCommand struct {
	OperationID, AddonID, Action, SourceID, Version string
	// Generation, when not zero, is the generation the caller saw. enable and
	// disable are denied as stale when the installed generation differs, and
	// nothing is recorded. Zero means the caller made no claim.
	Generation uint64
	// ExpectedDigest and CandidateDigest pin an explicit upgrade: the digest
	// of the package the caller saw installed and the digest of the candidate
	// it checked. Both are set or neither is. With them the owner upgrades
	// only the same library and channel the add-on came from, re-checks the
	// candidate and its compatibility at execution, and refuses a change of
	// either digest before it records anything. Without them the command is
	// the plain typed upgrade of an earlier release.
	ExpectedDigest, CandidateDigest string
	// Approval is the administrator's choice of host operations for an install
	// or an upgrade. The owner checks it against the package it actually
	// stages and grants only what the verified manifest declares and the host
	// registers. It carries no authority by itself: the caller must be a human
	// surface, which the gateway checks before it sets this field.
	Approval *HostApproval `json:",omitempty"`
	// Downgrade confirms that a local file may install an older version than
	// the one installed. Without it such a file is refused.
	Downgrade bool `json:",omitempty"`
}

// HostApproval binds an approval to one staged package and one revision of the
// grants. Digest is the digest of the package the administrator saw. Ops are
// exact operation names. Revision is the revision of the grants document the
// administrator saw. Nonce proves the preview was shown to this session; the
// owner does not store it.
type HostApproval struct {
	Digest   string
	Ops      []string
	Revision uint64
	Nonce    string `json:"-"`
}

// same reports whether two commands are the same request, for the replay of an
// operation ID. A nonce is a one-time proof and does not count.
func (c LifecycleCommand) same(o LifecycleCommand) bool {
	a, b := c.Approval, o.Approval
	c.Approval, o.Approval = nil, nil
	if c != o {
		return false
	}
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Digest == b.Digest && a.Revision == b.Revision && slices.Equal(a.Ops, b.Ops)
}

type OperationStatus struct {
	OperationID, AddonID, Phase string
	Generation                  uint64
	// RuntimeState is an observation, never a durable execution receipt.
	Stage          string                  `json:"stage,omitempty"`
	FailureCode    string                  `json:"failureCode,omitempty"`
	RuntimeState   string                  `json:"runtimeState,omitempty"`
	HostOperations *hostops.RequestSummary `json:"hostOperations,omitempty"`
}
