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
// An empty mode takes the documented default: local-owner when authentication
// is off, named-user when it is on. An explicit mode always wins. Named-user
// needs the account database, so an explicit named-user without
// authentication leaves the owner off with a warning instead of stopping the
// server.
func addonConfigFor(enabled bool, mode, dataPath string, authEnabled bool) (config.AddonConfig, error) {
	if !enabled {
		return config.AddonConfig{}, nil
	}
	if mode == "" {
		// The documented default for an install that did not choose a mode.
		// With authentication off there are no accounts, so the one shared
		// local owner is what makes add-ons usable; with authentication on,
		// every account keeps its own access. This is the one place that
		// resolves the default. It is an explicit, documented default for
		// auth-off installs, not an inference from the request or the network,
		// and a flag or environment value always wins over it.
		mode = auth.AddonModeNamedUser
		if !authEnabled {
			mode = auth.AddonModeLocalOwner
		}
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
	transport, address, err := gateway.NewLoopbackCarrierConfig(localAddonOwnerID, root)
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
