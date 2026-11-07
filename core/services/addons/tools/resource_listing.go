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
	return ToolDefinition{Name: ToolName(a.ID, l.Tool), AddonID: a.ID, Tool: l.Tool, Kind: contract.ToolKindRead, ResourceScoped: a.Resources.AgentScoped,
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

type listedResource struct {
	ResourceID string `json:"resourceId"`
	Name       string `json:"name,omitempty"`
	Role       string `json:"role"`
}

// listRows asks the owner for the resources the actor may use and validates
// the answer, ordered by resource ID. truncated says the answer is cut short.
func (t *ToolAdapter) listRows(ctx context.Context, actor contract.Actor, a apps.App) ([]listedResource, bool, error) {
	list, err := t.backend.(ResourceLister).ListResources(ctx, actor, a.ID)
	if err != nil {
		return nil, false, err
	}
	if list.AddonID != "" && list.AddonID != a.ID {
		return nil, false, gateway.ErrInvalidRequest
	}
	rows := make([]listedResource, 0, len(list.Resources))
	seen := make(map[string]bool, len(list.Resources))
	for _, r := range list.Resources {
		if !toolargs.ValidResourceID(r.ID) || seen[r.ID] || (r.AddonID != "" && r.AddonID != a.ID) || (r.Role != "viewer" && r.Role != "editor" && r.Role != "owner") {
			return nil, false, gateway.ErrInvalidRequest
		}
		seen[r.ID] = true
		rows = append(rows, listedResource{r.ID, r.Name, r.Role})
	}
	slices.SortFunc(rows, func(a, b listedResource) int { return strings.Compare(a.ResourceID, b.ResourceID) })
	return rows, list.Truncated, nil
}

// ResourceChoice is one resource of an add-on that a person may give to a
// saved agent.
type ResourceChoice struct {
	ID   string
	Name string
	// Role is the person's own role: viewer, editor or owner.
	Role string
}

// ResourceChoices lists, for an add-on whose manifest scopes its resources per
// agent, the resources the actor may use now: what the declared listing
// returns, before any agent limit. The second result says the list is cut
// short. An add-on that does not opt in, or declares no listing, is unknown.
func (t *ToolAdapter) ResourceChoices(ctx context.Context, actor contract.Actor, addonID string) ([]ResourceChoice, bool, error) {
	list, err := t.visible(ctx, actor)
	if err != nil {
		return nil, false, err
	}
	for _, a := range list {
		if a.ID != addonID {
			continue
		}
		if _, ok := t.listingOf(a); !ok || a.Resources == nil || !a.Resources.AgentScoped {
			break
		}
		rows, truncated, err := t.listRows(ctx, actor, a)
		if err != nil {
			return nil, false, err
		}
		if len(rows) > contract.MaxListedResources {
			rows, truncated = rows[:contract.MaxListedResources], true
		}
		out := make([]ResourceChoice, len(rows))
		for i, r := range rows {
			out[i] = ResourceChoice{ID: r.ResourceID, Name: r.Name, Role: r.Role}
		}
		return out, truncated, nil
	}
	return nil, false, ErrUnknownTool
}

func (t *ToolAdapter) listResources(ctx context.Context, actor contract.Actor, a apps.App, l contract.ResourceList, name string, arguments json.RawMessage) (ToolResult, error) {
	def := resourceListingDefinition(a, l)
	if _, _, err := toolargs.Bind(def.Tool, def.Parameters, arguments, false); err != nil {
		return ToolResult{}, err
	}
	rows, truncated, err := t.listRows(ctx, actor, a)
	if err != nil {
		return ToolResult{}, err
	}
	rows = limitRows(ctx, a.Resources != nil && a.Resources.AgentScoped, a.ID, rows, func(r listedResource) string { return r.ResourceID }, func(r *listedResource) { r.Role = "viewer" })
	truncated = truncated || len(rows) > contract.MaxListedResources
	if len(rows) > contract.MaxListedResources {
		rows = rows[:contract.MaxListedResources]
	}
	payload, err := json.Marshal(map[string]any{l.ItemsKey(): rows, "truncated": truncated})
	if err != nil {
		return ToolResult{}, gateway.ErrInvalidRequest
	}
	return readResult(name, contract.Result{Payload: payload})
}
