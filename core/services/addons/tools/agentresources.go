// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"slices"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The modes of an agent's resource list. They match the saved agent.
const (
	ResourceModeRead  = "read"
	ResourceModeWrite = "write"
)

// ResourceLimit is the resources one saved agent may use, by add-on ID and
// resource ID, with the mode of each (read or write). It applies only to an
// add-on whose manifest opts in (contract.ResourceTools.AgentScoped): for any
// other add-on it has no effect. An add-on that opts in and has no entry gives
// the agent no resource.
type ResourceLimit map[string]map[string]string

// ResourceLimits are the limits that bind a call. A call must pass every
// layer, so a sub-agent never reaches more than the agent that started it.
type ResourceLimits struct{ Layers []ResourceLimit }

type resourceLimitsKey struct{}

// WithResourceLimits returns a context that holds the resource limits of the
// agent a chat belongs to. The chats service sets it from the stored agent at
// each call, never from the arguments of the model. Without it a call is not
// limited by an agent.
func WithResourceLimits(ctx context.Context, l ResourceLimits) context.Context {
	return context.WithValue(ctx, resourceLimitsKey{}, &l)
}

// ResourceLimitsFrom returns the limits a context holds, for a consumer that
// stands in for the adapter in a test.
func ResourceLimitsFrom(ctx context.Context) (ResourceLimits, bool) {
	l, ok := resourceLimitsOf(ctx)
	if !ok {
		return ResourceLimits{}, false
	}
	return *l, true
}

func resourceLimitsOf(ctx context.Context) (*ResourceLimits, bool) {
	l, ok := ctx.Value(resourceLimitsKey{}).(*ResourceLimits)
	return l, ok && l != nil
}

// Allows says whether a call may use the resource. A call that changes
// something needs the write mode in every layer, a call that reads needs the
// resource in the list.
func (l ResourceLimits) Allows(addonID, resourceID string, write bool) bool {
	_, why := l.Check(addonID, resourceID, write)
	return why == ""
}

// The reasons of a refusal.
const (
	refusedNotListed = "not_listed"
	refusedReadOnly  = "read_only"
)

// Check returns the mode of the resource in the narrowest layer and "" when
// the call is allowed, else the reason.
func (l ResourceLimits) Check(addonID, resourceID string, write bool) (mode, refused string) {
	mode = ResourceModeWrite
	for _, layer := range l.Layers {
		m, ok := layer[addonID][resourceID]
		if !ok {
			return "", refusedNotListed
		}
		if m != ResourceModeWrite {
			mode = ResourceModeRead
		}
	}
	if write && mode != ResourceModeWrite {
		return mode, refusedReadOnly
	}
	return mode, ""
}

// CodeResourceNotInAgentList is the Code of a ResultError for a call that
// names a resource the agent of the chat may not use. The add-on was not
// called.
const CodeResourceNotInAgentList = "resource_not_in_agent_list"

// ResourceNotInAgentListNote is what the model is told for a resource that is
// not on the list of the agent.
const ResourceNotInAgentListNote = "That resource is not on the list of this agent, so nothing was sent to it. Use one of the resources in allowed, or tell the person to add it to the agent. Do not repeat the call and do not report an outage."

// CodeResourceReadOnlyForAgent is the Code of a ResultError for a call that
// changes a resource the agent may only read.
const CodeResourceReadOnlyForAgent = "resource_read_only_for_agent"

// ResourceReadOnlyForAgentNote is what the model is told for a write on a
// resource that is read only for the agent.
const ResourceReadOnlyForAgentNote = "This agent may only read that resource, so nothing was changed. Tell the person to allow changes for it in the agent. Do not repeat the call and do not report an outage."

// scopedBy reports whether the limits bind this add-on: the manifest opted in.
func scopedBy(def ToolDefinition) bool { return def.ResourceScoped }

// limitedResult is the answer for a call the agent's list refuses, or false
// when the call may go on. A tool that names no resource is not affected.
func limitedResult(ctx context.Context, def ToolDefinition, name, resource string) (ToolResult, bool) {
	l, ok := resourceLimitsOf(ctx)
	if !ok || !scopedBy(def) || resource == "" {
		return ToolResult{}, false
	}
	_, why := l.Check(def.AddonID, resource, def.Kind != contract.ToolKindRead)
	switch why {
	case "":
		return ToolResult{}, false
	case refusedReadOnly:
		return ToolResult{Kind: ResultError, Tool: name, Code: CodeResourceReadOnlyForAgent, Note: ResourceReadOnlyForAgentNote}, true
	}
	return ToolResult{Kind: ResultError, Tool: name, Code: CodeResourceNotInAgentList, Note: ResourceNotInAgentListNote, Allowed: l.listed(def.AddonID)}, true
}

// listed returns the resource IDs every layer lists for the add-on, sorted.
func (l ResourceLimits) listed(addonID string) []string {
	var out []string
	for i, layer := range l.Layers {
		ids := make([]string, 0, len(layer[addonID]))
		for id := range layer[addonID] {
			if i == 0 || slices.Contains(out, id) {
				ids = append(ids, id)
			}
		}
		out = ids
	}
	slices.Sort(out)
	return out
}

// limitRows keeps the rows of a resource listing that the agent may use. A row
// the agent may only read shows the role viewer, so the model does not try to
// change it.
func limitRows[T any](ctx context.Context, scoped bool, addonID string, rows []T, id func(T) string, readOnly func(*T)) []T {
	l, ok := resourceLimitsOf(ctx)
	if !ok || !scoped {
		return rows
	}
	out := rows[:0:0]
	for _, r := range rows {
		mode, why := l.Check(addonID, id(r), false)
		if why != "" {
			continue
		}
		if mode != ResourceModeWrite {
			readOnly(&r)
		}
		out = append(out, r)
	}
	return out
}
