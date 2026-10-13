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
	Generations                                  []Generation
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
}
type OperationStatus struct {
	OperationID, AddonID, Phase string
	Generation                  uint64
	// RuntimeState is an observation, never a durable execution receipt.
	RuntimeState string `json:"runtimeState,omitempty"`
}
