// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Status is advisory: each admission reads current authorization and selection.
// It does not promise to detect changes that were restored between reads.
func (a *Application) wireExecutionBinding(g *gateway.Gateway) error {
	g.ResolveExecutionBinding = a.resolveExecutionBinding
	g.AuthorizeExecutionStatus = g.ValidateExecutionBinding
	return nil
}

func (a *Application) resolveExecutionBinding(ctx context.Context, actor contract.Actor, claim gateway.ExecutionBindingClaim) (gateway.ExecutionStatusBinding, error) {
	empty := gateway.ExecutionStatusBinding{}
	if err := auth.AddonRevalidate(a.authDB, a.AddonOwnershipMode())(ctx, actor); err != nil {
		return empty, err
	}
	if a.addonManager == nil {
		return empty, gateway.ErrUnavailable
	}
	if a.studioStore == nil {
		return empty, gateway.ErrUnavailable
	}
	principal := actor.UserID
	if a.AddonOwnershipMode() == auth.AddonModeLocalOwner {
		principal = studio.AnonymousPrincipal
	}
	cfg := a.applicationConfig.ChatSelectionSnapshot()
	rec, err := a.studioStore.GetChat(ctx, principal, claim.ConversationID)
	if err != nil || rec.Principal != principal || rec.ID != claim.ConversationID {
		return empty, gateway.ErrForbidden
	}
	var grants []string
	if rec.AgentID != "" {
		if a.studioAgents == nil {
			return empty, gateway.ErrUnavailable
		}
		ag, err := a.studioAgents.Get(ctx, principal, rec.AgentID)
		if err != nil || ag.Principal != principal {
			return empty, gateway.ErrForbidden
		}
		grants = ag.Addons
	}
	// Discover through the actual Studio adapter: only currently running,
	// actor-visible native replacements are candidates. No readiness read occurs.
	broker := a.StudioAddonTools()
	if broker == nil {
		return empty, gateway.ErrUnavailable
	}
	defs, err := broker.Tools(ctx, rec.Principal)
	if err != nil {
		return empty, err
	}
	lister, ok := broker.(studio.ShadowLister)
	if !ok {
		return empty, gateway.ErrUnavailable
	}
	installed, err := lister.AddonShadowers(ctx, rec.Principal)
	if err != nil {
		return empty, err
	}
	// Workspace-dependent tools cannot be selected without the actual workspace.
	eligible := defs[:0:0]
	for _, d := range defs {
		needsWorkspace := false
		for _, p := range a.applicationConfig.AddonHostOps[d.AddonID] {
			if strings.HasPrefix(p, "workspace.") {
				needsWorkspace = true
			}
		}
		if needsWorkspace && !a.StudioFiles().Available() {
			continue
		}
		eligible = append(eligible, d)
	}
	selected, ok := studio.SelectExecutionProvider(eligible, installed, rec, grants, cfg.ToolProviders["bash"])
	if !ok {
		return empty, gateway.ErrForbidden
	} // Native bash is not an addon binding; no host fallback.
	instance, err := a.addonManager.Runtime(ctx, selected.AddonID)
	if err != nil {
		return empty, gateway.ErrUnavailable
	}
	caps, err := a.addonManager.DeclaredCapabilities(ctx, selected.AddonID, instance.Generation)
	if err != nil {
		return empty, err
	}
	provider := caps.ExecutionProvider
	tool := strings.TrimPrefix(selected.Name, "addon__"+selected.AddonID+"__")
	if provider == nil || !slices.Contains(provider.Tools, tool) {
		return empty, gateway.ErrForbidden
	}
	executionOK, statusOK := false, false
	for _, d := range caps.ToolDefinitions {
		if d.Name == tool && d.Kind == contract.ToolKindAction && d.Shadows == "bash" && !d.UIOnly && slices.Contains(caps.Tools, d.Name) {
			executionOK = true
		}
		if d.Name == provider.StatusTool && d.Kind == contract.ToolKindRead && d.UIOnly && contract.IsResourceless(d) && slices.Contains(caps.Tools, d.Name) && !slices.Contains(caps.AdminTools, d.Name) {
			statusOK = true
		}
	}
	if !executionOK || !statusOK || tool == provider.StatusTool {
		return empty, gateway.ErrForbidden
	}
	// Exact runtime identity is checked again; the gateway re-resolves current
	// authorization and selection before dispatch and positive publication.
	after, err := a.addonManager.Runtime(ctx, selected.AddonID)
	if err != nil || after.Generation != instance.Generation || after.Incarnation != instance.Incarnation {
		return empty, gateway.ErrUnavailable
	}
	if err = auth.AddonRevalidate(a.authDB, a.AddonOwnershipMode())(ctx, actor); err != nil {
		return empty, err
	}
	// Compare current selection inputs even when they resolve to the same provider.
	selection, _ := json.Marshal([]any{rec.Principal, rec.AgentID, rec.Origin, rec.AddonsOff, grants, cfg.HostExec, cfg.ToolProviders})
	digest := sha256.Sum256(selection)
	return gateway.ExecutionStatusBinding{ConversationID: rec.ID, Actor: actor, AddonID: selected.AddonID, Generation: instance.Generation, Incarnation: instance.Incarnation, ExecutionTool: tool, StatusTool: provider.StatusTool, CurrentSelection: hex.EncodeToString(digest[:])}, nil
}
