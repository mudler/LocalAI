// SPDX-License-Identifier: MIT
package tools

import (
	"encoding/json"
	"fmt"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/toolargs"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Give old Office callers a migration hint without accepting their old schema.
// Structural and reserved-field refusals keep their existing semantics.
func officeCreationDiagnostic(addonID, tool string, raw json.RawMessage) string {
	if addonID != "office" {
		return ""
	}
	var legacy, replacement string
	switch tool {
	case "create-docx", "create-pdf":
		legacy, replacement = "blocks", "markdown"
	case "create-xlsx":
		legacy, replacement = "sheets", "csv"
	default:
		return ""
	}
	if len(raw) == 0 || len(raw) > toolargs.MaxArgumentBytes || gateway.CheckToolArguments(raw) != nil {
		return ""
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return ""
	}
	for key := range fields {
		if contract.ForbiddenToolProperty(key) {
			return ""
		}
	}
	if _, ok := fields[legacy]; !ok {
		return ""
	}
	return fmt.Sprintf("%s is no longer supported; use the %s string", legacy, replacement)
}
