// SPDX-License-Identifier: MIT
package apps

import (
	"slices"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// This file is a compatibility shim and the only place in core that names an
// add-on for resource listing.
//
// Before the manifest could declare a listing (capabilities.resources.list),
// the host offered one add-on a listing tool that the manifest did not
// mention. That add-on now declares it. An installed copy that still has the
// older manifest (API 1) keeps the tool through this shim until it updates.
//
// Removal: delete this file, resources_test.go's "legacy resource listing"
// block and the call in build once contract.HostAPIMin reaches
// contract.MinResourceListAPI. The last spec of that block fails then.

// legacyListingAddon is the add-on whose older manifests the shim covers.
const legacyListingAddon = "tables"

// legacyResourceList returns the declaration an older manifest of the legacy
// add-on would have made, or nil when the shim does not apply.
func legacyResourceList(m contract.Manifest) *contract.ResourceList {
	r := m.Capabilities.Resources
	if m.ID != legacyListingAddon || r == nil || r.List != nil || m.API.Max >= contract.MinResourceListAPI {
		return nil
	}
	const tool = "databases-list"
	if slices.Contains(m.Capabilities.Tools, tool) {
		return nil
	}
	return &contract.ResourceList{Tool: tool, Items: "databases"}
}
