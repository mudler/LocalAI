// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/toolargs"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ResourceLister is the optional host-owned read boundary. It does not expose
// resource management or let the add-on executable enumerate caller grants.
type ResourceLister interface {
	ListResources(context.Context, contract.Actor, string) (gateway.ResourceList, error)
}

var _ ResourceLister = (*gateway.Gateway)(nil)

func (t *ToolAdapter) hasResourceLister() bool {
	_, ok := t.backend.(ResourceLister)
	return ok
}

func databaseListingDefinition() ToolDefinition {
	return ToolDefinition{Name: ToolName("tables", "databases-list"), AddonID: "tables", Tool: "databases-list", Kind: contract.ToolKindRead,
		Description: "List databases you may read. At most 100, ordered by resource ID; truncated means incomplete. No pagination. Inspect a chosen database before querying; never create one as a discovery fallback.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}}
}

func validateTablesListing(a apps.App) error {
	if a.ID == "tables" {
		for _, d := range a.ToolDefinitions {
			if d.Name == "databases-list" {
				return gateway.ErrInvalidRequest
			}
		}
	}
	return nil
}

func (t *ToolAdapter) listDatabases(ctx context.Context, actor contract.Actor, name string, arguments json.RawMessage) (ToolResult, error) {
	def := databaseListingDefinition()
	if _, _, err := toolargs.Bind(def.Tool, def.Parameters, arguments, false); err != nil {
		return ToolResult{}, err
	}
	list, err := t.backend.(ResourceLister).ListResources(ctx, actor, "tables")
	if err != nil {
		return ToolResult{}, err
	}
	if list.AddonID != "" && list.AddonID != "tables" {
		return ToolResult{}, gateway.ErrInvalidRequest
	}
	type database struct {
		ResourceID string `json:"resourceId"`
		Name       string `json:"name,omitempty"`
		Role       string `json:"role"`
	}
	rows := make([]database, 0, len(list.Resources))
	seen := make(map[string]bool, len(list.Resources))
	for _, r := range list.Resources {
		if !toolargs.ValidResourceID(r.ID) || seen[r.ID] || (r.AddonID != "" && r.AddonID != "tables") || (r.Role != "viewer" && r.Role != "editor" && r.Role != "owner") {
			return ToolResult{}, gateway.ErrInvalidRequest
		}
		seen[r.ID] = true
		rows = append(rows, database{r.ID, r.Name, r.Role})
	}
	slices.SortFunc(rows, func(a, b database) int { return strings.Compare(a.ResourceID, b.ResourceID) })
	truncated := list.Truncated || len(rows) > 100
	if len(rows) > 100 {
		rows = rows[:100]
	}
	payload, err := json.Marshal(struct {
		Databases []database `json:"databases"`
		Truncated bool       `json:"truncated"`
	}{rows, truncated})
	if err != nil {
		return ToolResult{}, gateway.ErrInvalidRequest
	}
	return readResult(name, contract.Result{Payload: payload})
}
