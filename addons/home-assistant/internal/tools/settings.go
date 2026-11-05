// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/addons/home-assistant/internal/policy"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func (r *Runtime) isAdmin(ctx context.Context, env contract.Envelope) bool {
	return r.Admin != nil && r.Admin.IsAdmin(ctx, env.Request.OperationID)
}

// pinLines renders the pins as host:port=fingerprint, sorted.
func pinLines(p map[string]string) []string {
	out := make([]string, 0, len(p))
	for k, v := range p {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

func policyView(p policy.Policy) map[string]any {
	return map[string]any{
		"allow": p.Allow, "excludeDomains": p.ExcludeDomains, "excludeAreas": p.ExcludeAreas, "excludePatterns": p.ExcludePatterns,
		"excludeLabel": p.ExcludeLabel, "maxTargets": p.MaxTargets, "pins": pinLines(p.Pins),
	}
}

// getPolicy is the page's read of the policy. Anyone who may open the page
// sees it; only an administrator sees it as editable.
func (r *Runtime) getPolicy(ctx context.Context, env contract.Envelope) (contract.Result, error) {
	p, err := r.Policy.Load()
	broken := err != nil
	if broken {
		p = policy.Defaults()
	}
	return finish(map[string]any{
		"policy":   policyView(p),
		"defaults": policyView(policy.Defaults()),
		"admin":    r.isAdmin(ctx, env),
		"broken":   broken,
		"hard": map[string]any{
			"domains": policy.HardDomains, "serviceNames": policy.HardServiceNames, "servicePrefixes": []string{"reload"},
			"coverClasses": policy.SafeCoverClasses,
		},
		"physical": physicalDomains(p),
	}, nil)
}

// physicalDomains lists the allowed domains whose services wait for a confirmation.
func physicalDomains(p policy.Policy) []string {
	seen := map[string]bool{}
	for _, e := range p.Allow {
		d, s, err := policy.ParseAllow(e)
		if err == nil && policy.Physical(d, s) {
			seen[d] = true
		}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

type setPolicyArgs struct {
	Allow           *[]string `json:"allow"`
	ExcludeDomains  *[]string `json:"excludeDomains"`
	ExcludeAreas    *[]string `json:"excludeAreas"`
	ExcludePatterns *[]string `json:"excludePatterns"`
	ExcludeLabel    *string   `json:"excludeLabel"`
	MaxTargets      *int      `json:"maxTargets"`
	Pins            *[]string `json:"pins"`
}

// setPolicy replaces the fields that the page names. Only an administrator may
// call it, and nothing is saved when any entry is refused.
func (r *Runtime) setPolicy(ctx context.Context, env contract.Envelope, args map[string]json.RawMessage) (contract.Result, error) {
	if !r.isAdmin(ctx, env) {
		return contract.Result{}, bad("only an administrator can change the policy")
	}
	var a setPolicyArgs
	if err := decodeArgs(args, &a); err != nil {
		return contract.Result{}, err
	}
	p, err := r.Policy.Load()
	if err != nil {
		// A damaged file is replaced only by a complete policy from the page.
		p = policy.Defaults()
	}
	if a.Allow != nil {
		p.Allow = *a.Allow
	}
	if a.ExcludeDomains != nil {
		p.ExcludeDomains = *a.ExcludeDomains
	}
	if a.ExcludeAreas != nil {
		p.ExcludeAreas = *a.ExcludeAreas
	}
	if a.ExcludePatterns != nil {
		p.ExcludePatterns = *a.ExcludePatterns
	}
	if a.ExcludeLabel != nil {
		p.ExcludeLabel = strings.TrimSpace(*a.ExcludeLabel)
	}
	if a.MaxTargets != nil {
		p.MaxTargets = *a.MaxTargets
	}
	if a.Pins != nil {
		pins := map[string]string{}
		for _, line := range *a.Pins {
			hp, fp, ok := strings.Cut(strings.TrimSpace(line), "=")
			if !ok {
				return contract.Result{}, bad("a pin is written host:port=fingerprint")
			}
			// Colons between the digit pairs, as openssl prints them, are accepted.
			pins[strings.ToLower(strings.TrimSpace(hp))] = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(fp)), ":", "")
		}
		p.Pins = pins
	}
	saved, err := r.Policy.Save(p)
	if err != nil {
		return contract.Result{}, bad("%s", err.Error())
	}
	return finish(map[string]any{"saved": true, "policy": policyView(saved), "physical": physicalDomains(saved)}, nil)
}
