// SPDX-License-Identifier: MIT
package studio

import (
	"slices"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

// AddonToolEligible is the common chat/owner eligibility policy. Agent grants
// only narrow access. Unattended chats without an agent never get action tools.
func AddonToolEligible(d AddonTool, rec session.ChatRecord, grants []string) bool {
	return !slices.Contains(rec.AddonsOff, d.AddonID) && !reservedToolNames[d.Name] &&
		(rec.AgentID == "" || slices.Contains(grants, d.AddonID)) &&
		!(d.Action && rec.Origin != "" && rec.AgentID == "")
}

// SelectExecutionProvider uses the same install ordering and pinned-idle rule
// as chat shadow resolution. Installed but ineligible pins never fall through.
func SelectExecutionProvider(defs []AddonTool, installed []AddonShadower, rec session.ChatRecord, grants []string, pin string) (AddonTool, bool) {
	var ids []string
	when := map[string]string{}
	sh := shadowers{byName: map[string][]string{}}
	for _, d := range installed {
		if slices.Contains(d.Tools, "bash") {
			sh.byName["bash"] = append(sh.byName["bash"], d.AddonID)
		}
	}
	for _, d := range defs {
		if d.Shadows == "bash" && AddonToolEligible(d, rec, grants) {
			if !slices.Contains(ids, d.AddonID) {
				ids = append(ids, d.AddonID)
			}
			when[d.AddonID] = d.InstalledAt
		}
	}
	if sh.pinnedButIdle("bash", pin, ids) {
		return AddonTool{}, false
	}
	chosen := ChooseProvider(ids, when, pin)
	for _, d := range defs {
		if chosen != "" && d.AddonID == chosen && d.Shadows == "bash" && AddonToolEligible(d, rec, grants) {
			return d, true
		}
	}
	return AddonTool{}, false
}
