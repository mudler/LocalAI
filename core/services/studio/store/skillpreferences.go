// SPDX-License-Identifier: MIT
package store

import (
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// normalizeDocumentSkillsOff consumes old core OFF markers so a later explicit
// re-enable cannot be undone by another read. Only document add-on collisions
// are deduplicated; plain names and unrelated preferences retain their order.
func normalizeDocumentSkillsOff(off []string) ([]string, bool) {
	changed := false
	for _, name := range off {
		if strings.HasPrefix(name, "builtin:") && contract.DocumentSkill(strings.TrimPrefix(name, "builtin:")) {
			changed = true
			break
		}
	}
	if !changed {
		return off, false
	}
	clean := make([]string, 0, len(off))
	seen := make(map[string]bool)
	for _, name := range off {
		if strings.HasPrefix(name, "builtin:") && contract.DocumentSkill(strings.TrimPrefix(name, "builtin:")) {
			name = "addon:" + strings.TrimPrefix(name, "builtin:")
		}
		if strings.HasPrefix(name, "addon:") && contract.DocumentSkill(strings.TrimPrefix(name, "addon:")) {
			if seen[name] {
				continue
			}
			seen[name] = true
		}
		clean = append(clean, name)
	}
	return clean, true
}
