// SPDX-License-Identifier: MIT
package cli

import (
	"os"

	"github.com/alecthomas/kong"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/xlog"
)

// AfterApply records actual command-line presence, not default or environment
// values. Kong's Path trace distinguishes an explicit empty flag from omission.
func (r *RunCMD) AfterApply(ctx *kong.Context) error {
	r.addonHostOpsFlagPresent = false
	r.addonHostPolicyFlagPresent = false
	for _, path := range ctx.Path {
		if path.Flag == nil || path.Resolved {
			continue
		}
		switch path.Flag.Name {
		case "addon-host-ops":
			r.addonHostOpsFlagPresent = true
		case "addon-host-policy":
			r.addonHostPolicyFlagPresent = true
		}
	}
	return nil
}

func (r *RunCMD) resolveAddonHostOps() (config.AddonHostOpsResolution, error) {
	var flag, environment, policy *string
	if r.addonHostOpsFlagPresent {
		flag = &r.AddonHostOps
	}
	if value, present := os.LookupEnv("LOCALAI_ADDON_HOST_OPS"); present {
		environment = &value
	}
	if r.addonHostPolicyFlagPresent {
		policy = &r.AddonHostPolicy
	} else if value, present := os.LookupEnv("LOCALAI_ADDON_HOST_POLICY"); present {
		policy = &value
	}
	// T5 sequencing bridge: keep the old CLI default until T6 ships and selects
	// the official deployment policy. The generic resolver itself grants nothing
	// when unconfigured. Explicit selectors (even empty) never use this bridge.
	if flag == nil && environment == nil && policy == nil {
		grants, err := config.ParseAddonHostOps(r.AddonHostOps)
		return config.AddonHostOpsResolution{Grants: grants, Source: "legacy-default"}, err
	}
	return config.ResolveAddonHostOps(flag, environment, policy)
}

func logAddonHostOpsResolution(resolved config.AddonHostOpsResolution) {
	prefixes := 0
	for _, grants := range resolved.Grants {
		prefixes += len(grants)
	}
	xlog.Info("Addon host operation policy resolved", "source", resolved.Source, "addons", len(resolved.Grants), "prefixes", prefixes)
	if resolved.PolicyIgnored {
		xlog.Warn("Addon host policy ignored: explicit host operations override takes precedence")
	}
	xlog.Info("Addon manifests and libraries cannot grant host operations; policy changes require restart")
}
