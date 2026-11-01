// SPDX-License-Identifier: MIT
package addons

import "github.com/mudler/LocalAI/core/services/addons/packages"

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
}
type OperationStatus struct {
	OperationID, AddonID, Phase string
	Generation                  uint64
	// RuntimeState is an observation, never a durable execution receipt.
	RuntimeState string `json:"runtimeState,omitempty"`
}
