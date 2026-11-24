// SPDX-License-Identifier: MIT
package hostgrants_test

import (
	"testing"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons/hostgrants"
)

// An external test package: core/config imports the gateway, which imports
// hostgrants, so an internal test could not import core/config.
func TestDenyGrammarMatchesThePolicyFile(t *testing.T) {
	for _, entry := range []string{"*", "egress.", "workspace.admin", "connector.file.", "connector.file.put", "X.", "workspace", "a..", ".a", "a.b.", "a.B", "", "a.b.c.", "a-b.c"} {
		_, err := config.ParseAddonHostDeny("a=" + entry)
		if (err == nil) != hostgrants.ValidDenyEntry(entry) {
			t.Errorf("entry %q: config accepts=%v, hostgrants accepts=%v", entry, err == nil, hostgrants.ValidDenyEntry(entry))
		}
	}
}
