// SPDX-License-Identifier: MIT
package cli

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/xlog"
)

// localAddonOwnerID names the one instance of a single-node run. It is also the
// owner, so the node supervises the add-ons itself.
const localAddonOwnerID = "local"

// The roles of --addons-role.
const (
	addonRoleCombined = "combined"
	addonRoleOwner    = "owner"
	addonRoleFrontend = "frontend"
)

// addonRoleFlags holds the flags that choose how this process takes part in the
// add-on owner. The zero value is the combined role of a single node.
type addonRoleFlags struct {
	Role         string
	OwnerAddress string
	OwnerListen  string
	CarrierDir   string
}

// addonConfigFor derives the add-on owner configuration from the run flags. In
// the combined role (the default) the node is its own owner and only the
// on/off switch and the ownership mode matter. In the owner and frontend roles
// the owner is its own workload: the carrier keys come from a key directory
// and the frontend never touches the owner's files.
//
// An empty mode takes the documented default: local-owner when authentication
// is off, named-user when it is on. An explicit mode always wins. Named-user
// needs the account database, so an explicit named-user without
// authentication leaves the owner off with a warning instead of stopping the
// server.
func addonConfigFor(enabled bool, mode, dataPath string, authEnabled bool, roleFlags addonRoleFlags) (config.AddonConfig, error) {
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
	role := roleFlags.Role
	if role == "" {
		role = addonRoleCombined
	}
	switch role {
	case addonRoleCombined:
		if roleFlags.OwnerAddress != "" {
			return config.AddonConfig{}, fmt.Errorf("--addons-owner-address: only for --addons-role=%s or --addons-role=%s, not %s", addonRoleOwner, addonRoleFrontend, addonRoleCombined)
		}
		if roleFlags.CarrierDir != "" {
			return config.AddonConfig{}, fmt.Errorf("--addons-carrier-dir: only for --addons-role=%s or --addons-role=%s, not %s", addonRoleOwner, addonRoleFrontend, addonRoleCombined)
		}
	case addonRoleOwner, addonRoleFrontend:
		return splitAddonConfig(role, mode, dataPath, roleFlags)
	default:
		return config.AddonConfig{}, fmt.Errorf("--addons-role: %q is not valid; use %q, %q or %q", role, addonRoleCombined, addonRoleOwner, addonRoleFrontend)
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

// splitAddonConfig builds the configuration of the owner role or the frontend
// role. Both use the fixed carrier identities, so a root made by a single node
// keeps passing the owner identity check. The frontend has no root: it never
// creates or opens the owner's files.
func splitAddonConfig(role, mode, dataPath string, f addonRoleFlags) (config.AddonConfig, error) {
	if f.OwnerAddress == "" {
		return config.AddonConfig{}, fmt.Errorf("--addons-owner-address is required with --addons-role=%s", role)
	}
	if f.CarrierDir == "" {
		return config.AddonConfig{}, fmt.Errorf("--addons-carrier-dir is required with --addons-role=%s", role)
	}
	port, err := ownerAddressPort(f.OwnerAddress)
	if err != nil {
		return config.AddonConfig{}, err
	}
	transport, err := gateway.LoadCarrierConfig(f.CarrierDir, role, f.OwnerAddress)
	if err != nil {
		return config.AddonConfig{}, fmt.Errorf("--addons-carrier-dir: %w", err)
	}
	c := config.AddonConfig{
		Enabled:    true,
		InstanceID: transport.InstanceID, OwnerID: gateway.OwnerIdentity, OwnerAddress: f.OwnerAddress,
		StorageID: "local", Transport: transport, OwnershipMode: mode,
	}
	if role == addonRoleFrontend {
		return c, nil
	}
	c.Root, err = filepath.Abs(filepath.Join(dataPath, "addons"))
	if err != nil {
		return config.AddonConfig{}, err
	}
	c.ListenAddress = f.OwnerListen
	if c.ListenAddress == "" {
		c.ListenAddress = ":" + port
	}
	return c, nil
}

// ownerAddressPort checks that the owner address is https://host:port with no
// path, and returns the port.
func ownerAddressPort(address string) (string, error) {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("--addons-owner-address: %q must look like https://host:port, with no path", address)
	}
	_, port, err := net.SplitHostPort(u.Host)
	if err != nil || port == "" {
		return "", fmt.Errorf("--addons-owner-address: %q must name a port, like https://host:port", address)
	}
	return port, nil
}
