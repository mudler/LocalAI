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
	var deny *string
	if r.AddonHostDeny != "" {
		deny = &r.AddonHostDeny
	}
	// Kong has already merged the deny environment variable into the field,
	// so there is no separate environment value to union.
	resolved, err := config.ResolveAddonHostOps(flag, environment, policy, deny, nil)
	if err == nil && resolved.Source == "policy" {
		resolved.Source = "policy:" + *policy
	}
	return resolved, err
}

func logAddonHostOpsResolution(resolved config.AddonHostOpsResolution) {
	if resolved.Source == "empty" {
		xlog.Warn("Addons receive no host operations from the policy file or flags: an administrator can grant them in the UI at install, or select --addon-host-policy or --addon-host-ops")
	}
	prefixes := 0
	for _, grants := range resolved.Grants {
		prefixes += len(grants)
	}
	denied := 0
	for _, entries := range resolved.Deny {
		denied += len(entries)
	}
	xlog.Info("Addon host operation policy resolved", "source", resolved.Source, "addons", len(resolved.Grants), "prefixes", prefixes, "denyRules", denied)
	if resolved.GrantsIgnored {
		xlog.Warn("Addon host policy grants ignored, deny applied: an explicit host operations list replaces the file grants")
	}
	xlog.Info("Addon manifests and libraries cannot grant host operations; file and flag policy changes require restart")
}
