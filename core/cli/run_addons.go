// SPDX-License-Identifier: MIT
package cli

import (
	"fmt"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/xlog"
)

// localAddonOwnerID names the one instance of a single-node run. It is also the
// owner, so the node supervises the add-ons itself.
const localAddonOwnerID = "local"

// addonConfigFor derives the add-on owner configuration of a single node from
// the run flags. Only the on/off switch and the ownership mode are flags; the
// rest follows from the node being its own owner.
//
// The default mode, named-user, needs the account database. Without
// authentication there is no such database, so the owner stays off with a
// warning instead of stopping the server: local-owner mode is never inferred
// and an operator must select it.
func addonConfigFor(enabled bool, mode, dataPath string, authEnabled bool) (config.AddonConfig, error) {
	if !enabled {
		return config.AddonConfig{}, nil
	}
	switch mode {
	case auth.AddonModeNamedUser, auth.AddonModeLocalOwner:
	default:
		return config.AddonConfig{}, fmt.Errorf("--addons-ownership-mode: %q is not valid; use %q or %q", mode, auth.AddonModeNamedUser, auth.AddonModeLocalOwner)
	}
	if mode == auth.AddonModeNamedUser && !authEnabled {
		xlog.Warn("Add-ons are off: named-user ownership needs authentication. Enable authentication, or set --addons-ownership-mode=local-owner to let every caller share the owner's authority")
		return config.AddonConfig{}, nil
	}
	root, err := filepath.Abs(filepath.Join(dataPath, "addons"))
	if err != nil {
		return config.AddonConfig{}, err
	}
	transport, address, err := gateway.NewLoopbackCarrierConfig(localAddonOwnerID)
	if err != nil {
		return config.AddonConfig{}, fmt.Errorf("add-on owner transport: %w", err)
	}
	return config.AddonConfig{
		Enabled:    true,
		InstanceID: localAddonOwnerID, OwnerID: localAddonOwnerID, OwnerAddress: transport.OwnerAddress,
		StorageID: "local", Root: root, ListenAddress: address, Transport: transport,
		OwnershipMode: mode,
	}, nil
}
