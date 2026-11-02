// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// A callback lifetime gets a fresh nonce, so a new owner cannot accept bindings
// from a previous owner even when its configuration and records look identical.
func (a *Application) wireExecutionBinding(g *gateway.Gateway) error {
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	g.ResolveExecutionBinding = func(ctx context.Context, actor contract.Actor, claim gateway.ExecutionBindingClaim) (gateway.ExecutionStatusBinding, error) {
		return a.resolveExecutionBinding(ctx, actor, claim, hex.EncodeToString(nonce[:]))
	}
	g.AuthorizeExecutionStatus = g.ValidateExecutionBinding
	return nil
}

func (a *Application) resolveExecutionBinding(ctx context.Context, actor contract.Actor, claim gateway.ExecutionBindingClaim, nonce string) (gateway.ExecutionStatusBinding, error) {
	empty := gateway.ExecutionStatusBinding{}
	// Named credentials have per-call validation, but no authoritative access ABA
	// revision yet. Never substitute a timestamp, role hash or local counter.
	if a.AddonOwnershipMode() != auth.AddonModeLocalOwner || a.authDB != nil {
		return empty, gateway.ErrUnavailable
	}
	if err := auth.AddonRevalidate(a.authDB, a.AddonOwnershipMode())(ctx, actor); err != nil {
		return empty, err
	}
	if a.addonManager == nil {
		return empty, gateway.ErrUnavailable
	}
	chats, ok := a.studioStore.(store.SelectionRevisionProvider)
	if !ok {
		return empty, gateway.ErrUnavailable
	}
	cfg := a.applicationConfig.ChatSelectionSnapshot()
	epoch := a.addonManager.SelectionEpoch()
	rec, chatToken, err := chats.SelectionSnapshot(ctx, studio.AnonymousPrincipal, claim.ConversationID)
	if err != nil {
		if errors.Is(err, store.ErrSelectionRevisionUnsupported) {
			return empty, gateway.ErrUnavailable
		}
		return empty, gateway.ErrForbidden
	}
	if chatToken == "" || rec.Principal != studio.AnonymousPrincipal || rec.ID != claim.ConversationID {
		return empty, gateway.ErrForbidden
	}
	var grants []string
	var agentToken string
	var agentSource agents.SelectionRevisionProvider
	if rec.AgentID != "" {
		agentSource, ok = a.studioAgents.(agents.SelectionRevisionProvider)
		if !ok {
			return empty, gateway.ErrUnavailable
		}
		ag, token, e := agentSource.SelectionSnapshot(ctx, rec.Principal, rec.AgentID)
		if e != nil {
			if errors.Is(e, agents.ErrSelectionRevisionUnsupported) {
				return empty, gateway.ErrUnavailable
			}
			return empty, gateway.ErrForbidden
		}
		if token == "" || ag.Principal != rec.Principal {
			return empty, gateway.ErrForbidden
		}
		grants, agentToken = ag.Addons, token
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
	// No lock spans IO. Check every authority again, including the live process.
	_, afterChat, err := chats.SelectionSnapshot(ctx, rec.Principal, rec.ID)
	if err != nil || afterChat != chatToken {
		return empty, gateway.ErrUnavailable
	}
	if agentSource != nil {
		_, afterAgent, e := agentSource.SelectionSnapshot(ctx, rec.Principal, rec.AgentID)
		if e != nil || afterAgent != agentToken {
			return empty, gateway.ErrUnavailable
		}
	}
	after, err := a.addonManager.Runtime(ctx, selected.AddonID)
	if err != nil || after.Generation != instance.Generation || after.Incarnation != instance.Incarnation || a.addonManager.SelectionEpoch() != epoch || a.applicationConfig.ChatSelectionSnapshot().Epoch != cfg.Epoch {
		return empty, gateway.ErrUnavailable
	}
	if err = auth.AddonRevalidate(a.authDB, a.AddonOwnershipMode())(ctx, actor); err != nil {
		return empty, err
	}
	revision, _ := json.Marshal([]any{nonce, cfg.Epoch, epoch, chatToken, agentToken, "immutable-local-owner"})
	digest := sha256.Sum256(revision)
	return gateway.ExecutionStatusBinding{ConversationID: rec.ID, Actor: actor, AddonID: selected.AddonID, Generation: instance.Generation, Incarnation: instance.Incarnation, ExecutionTool: tool, StatusTool: provider.StatusTool, SelectionRevision: hex.EncodeToString(digest[:])}, nil
}
