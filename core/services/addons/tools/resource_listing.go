// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"encoding/json"
	"fmt"
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

// listingOf returns the listing the add-on declares and this adapter can
// serve.
func (t *ToolAdapter) listingOf(a apps.App) (contract.ResourceList, bool) {
	if a.Resources == nil || a.Resources.List == nil || !t.hasResourceLister() {
		return contract.ResourceList{}, false
	}
	return *a.Resources.List, true
}

func (t *ToolAdapter) hasListing(a apps.App) bool {
	_, ok := t.listingOf(a)
	return ok
}

// resourceListingDefinition is the strict, argument-less read tool the host
// builds from the declaration of an add-on.
func resourceListingDefinition(a apps.App, l contract.ResourceList) ToolDefinition {
	noun := a.Resources.Noun
	if noun == "" {
		noun = "resource"
	}
	return ToolDefinition{Name: ToolName(a.ID, l.Tool), AddonID: a.ID, Tool: l.Tool, Kind: contract.ToolKindRead,
		Description: fmt.Sprintf("List the %s resources you may read. At most %d, ordered by resource ID; truncated means incomplete. No pagination. Inspect a chosen %s before using it; never create one as a discovery fallback.", noun, contract.MaxListedResources, noun),
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}}
}

// validateListing refuses an add-on whose manifest ships a tool with the name
// of the listing the host builds for it.
func validateListing(a apps.App) error {
	if a.Resources == nil || a.Resources.List == nil {
		return nil
	}
	for _, d := range a.ToolDefinitions {
		if d.Name == a.Resources.List.Tool {
			return gateway.ErrInvalidRequest
		}
	}
	return nil
}

func (t *ToolAdapter) listResources(ctx context.Context, actor contract.Actor, a apps.App, l contract.ResourceList, name string, arguments json.RawMessage) (ToolResult, error) {
	def := resourceListingDefinition(a, l)
	if _, _, err := toolargs.Bind(def.Tool, def.Parameters, arguments, false); err != nil {
		return ToolResult{}, err
	}
	list, err := t.backend.(ResourceLister).ListResources(ctx, actor, a.ID)
	if err != nil {
		return ToolResult{}, err
	}
	if list.AddonID != "" && list.AddonID != a.ID {
		return ToolResult{}, gateway.ErrInvalidRequest
	}
	type row struct {
		ResourceID string `json:"resourceId"`
		Name       string `json:"name,omitempty"`
		Role       string `json:"role"`
	}
	rows := make([]row, 0, len(list.Resources))
	seen := make(map[string]bool, len(list.Resources))
	for _, r := range list.Resources {
		if !toolargs.ValidResourceID(r.ID) || seen[r.ID] || (r.AddonID != "" && r.AddonID != a.ID) || (r.Role != "viewer" && r.Role != "editor" && r.Role != "owner") {
			return ToolResult{}, gateway.ErrInvalidRequest
		}
		seen[r.ID] = true
		rows = append(rows, row{r.ID, r.Name, r.Role})
	}
	slices.SortFunc(rows, func(a, b row) int { return strings.Compare(a.ResourceID, b.ResourceID) })
	truncated := list.Truncated || len(rows) > contract.MaxListedResources
	if len(rows) > contract.MaxListedResources {
		rows = rows[:contract.MaxListedResources]
	}
	payload, err := json.Marshal(map[string]any{l.ItemsKey(): rows, "truncated": truncated})
	if err != nil {
		return ToolResult{}, gateway.ErrInvalidRequest
	}
	return readResult(name, contract.Result{Payload: payload})
}
