package studio

import (
	"context"
	"slices"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/resourcesets"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The builtin grants of the agent that a binding serves also govern what a
// connector chat may do. The rule for one capability of the list below:
//
//	a tool exists  =  the sender's right on the binding (Authorize)
//	              AND the grant of the agent (this file)
//	              AND the chat switch of the tool's group
//
// This file is the middle term. It is a CapabilitySwitch, so every question
// that goes through the connector gate asks it too: the capabilities a turn
// gets (grantFor), the check of a feature that has a sender (Authorize), the
// question of a run without one (CeilingAllows: a trigger that fires from a
// message pauses when the grant goes) and the gate that the trigger tools use.
//
// What an empty list means. An agent saved before grants existed has none, and
// a connection of it keeps working: an agent with no grant list at all (the
// Builtins list is empty) holds every non-sensitive capability below, as it
// did. A sensitive one is never implied; it needs the explicit grant. As soon
// as the owner grants any builtin, the list is the whole answer: a capability
// that is not on it is off. This is the one place where adding a grant (say
// memory.write) narrows an agent that had no list, and the editor shows the
// checklist so the owner sees it.

// grantGated are the capabilities that an agent grant governs at the gate.
// The grants of the proposal kinds (memory.write and the others) have their
// own enforcement point in the proposal tools and are not repeated here.
// act.* are not listed on purpose: they are the action policy of unattended
// runs, which has no enforcement point yet (see the customization spec).
var grantGated = []string{
	contract.CapTriggersPropose,
	contract.CapTriggersRun,
	contract.CapTalkbackChannels,
	contract.CapTalkbackNewChan,
	contract.CapFilesUpload,
}

// GrantGatedCapability reports whether the agent's grants govern the
// capability at the connector gate.
func GrantGatedCapability(capability string) bool { return slices.Contains(grantGated, capability) }

// grantVerdict is the pure rule of the switch. listed is the grants the owner
// wrote on the agent (empty: the agent has no list), held is that list after
// the intersection with what the user may use. It returns the reason the
// capability is off, or "".
func grantVerdict(capability string, listed, held []string, hasAgent bool) string {
	if !GrantGatedCapability(capability) {
		return ""
	}
	switch {
	case slices.Contains(held, capability):
		return ""
	case builtins.SensitiveGrant(capability):
		return "the agent of this bot has not been granted " + capability
	case !hasAgent || len(listed) == 0:
		return "" // no grant list: the capabilities that are not sensitive keep working
	}
	return "the agent of this bot has not been granted " + capability
}

// agentGrantSwitch is the CapabilitySwitch that reads the grants of the agent
// of a binding. It fails closed: a binding whose agent cannot be read gets
// nothing.
func agentGrantSwitch(s *Service) CapabilitySwitch {
	return func(ctx context.Context, b connectors.Binding, capability string) Block {
		if !GrantGatedCapability(capability) {
			return Block{}
		}
		if b.AgentID == "" {
			return Block{Code: CodeNoAgent, Reason: "this connection has no agent"}
		}
		unreadable := Block{Code: CodeAgentUnavailable, Reason: "the agent of this bot cannot be read",
			Fix: &Fix{Kind: FixOpenAgent, Target: b.AgentID}}
		if s.d.Agents == nil {
			return unreadable
		}
		ag, err := s.d.Agents.Get(ctx, principal(b.Owner), b.AgentID)
		if err != nil {
			return unreadable
		}
		if why := grantVerdict(capability, ag.Builtins, s.usableGrants(ctx, principal(b.Owner), ag.Builtins), true); why != "" {
			return Block{Code: CodeAgentPermissionOff, Reason: why,
				Fix: &Fix{Kind: FixOpenAgent, Target: b.AgentID, Field: capability}}
		}
		return Block{}
	}
}

// toolGroupOf names the Tools switch group that serves a capability in a
// connector chat: the tool that uses the capability is not built while its
// group is off, so the capability is not usable either.
var toolGroupOf = map[string]string{
	contract.CapTriggersPropose: builtins.GroupTriggers,
	contract.CapTriggersChange:  builtins.GroupTriggers,
	contract.CapMemoryWrite:     builtins.GroupChanges,
	contract.CapProfileWrite:    builtins.GroupChanges,
	contract.CapSkillsWrite:     builtins.GroupChanges,
	contract.CapPagesWrite:      builtins.GroupChanges,
	contract.CapDashboardWrite:  builtins.GroupChanges,
	contract.CapKnowledgeWrite:  builtins.GroupChanges,
}

// extraToolGroupOf names a second group that a capability also needs: a
// knowledge text is proposed with the change tools but searched and indexed
// through the Knowledge group, and the tool picker nests the grant under it.
var extraToolGroupOf = map[string]string{
	contract.CapKnowledgeWrite: builtins.GroupCollections,
}

// toolsSwitch is the CapabilitySwitch of the Tools switch: a capability whose
// tool group the agent of the binding turns off is off. The grant of the agent
// is a separate link (agentGrantSwitch).
func toolsSwitch(s *Service) CapabilitySwitch {
	return func(ctx context.Context, b connectors.Binding, capability string) Block {
		group := toolGroupOf[capability]
		if group == "" || b.AgentID == "" || s.d.Agents == nil {
			return Block{}
		}
		ag, err := s.d.Agents.Get(ctx, principal(b.Owner), b.AgentID)
		if err != nil {
			return Block{} // the grants switch reports an agent it cannot read
		}
		for _, g := range []string{group, extraToolGroupOf[capability]} {
			if g == "" {
				continue
			}
			if slices.Contains(ag.BuiltinsOff, g) || (s.builtins.IsOptIn(g) && !slices.Contains(ag.BuiltinsOn, g)) {
				return Block{Code: CodeToolsSwitchOff, Reason: "the " + g + " tools are switched off for the agent of this bot",
					Fix: &Fix{Kind: FixOpenAgent, Target: b.AgentID, Field: "tools:" + g}}
			}
		}
		return Block{}
	}
}

// knowledgeSwitch is the CapabilitySwitch of adding to Knowledge: the server
// must be able to index a text, and the agent of the binding needs a
// collection on its list. Other capabilities pass.
func knowledgeSwitch(s *Service) CapabilitySwitch {
	return func(ctx context.Context, b connectors.Binding, capability string) Block {
		if capability != contract.CapKnowledgeWrite {
			return Block{}
		}
		code, reason := s.knowledgeBlock(ctx, b.Owner, b.AgentID)
		switch code {
		case "":
			return Block{}
		case CodeKnowledgeNone:
			return Block{Code: code, Reason: reason, Fix: &Fix{Kind: FixOpenAgent, Target: b.AgentID, Field: "collections"}}
		}
		return Block{Code: code, Reason: reason}
	}
}

// resourcesSwitch is the CapabilitySwitch of the changes that add-on tools
// make: when every add-on of the agent that can change things works on
// resources the agent lists, and the list holds none to change, the capability
// is off for the binding. An agent that also names an add-on without such a
// list keeps the capability, because that add-on's tools can still change
// things. Other capabilities pass.
func resourcesSwitch(s *Service) CapabilitySwitch {
	return func(ctx context.Context, b connectors.Binding, capability string) Block {
		if capability != contract.CapToolsWrite || b.AgentID == "" || s.d.Agents == nil || s.d.AddonTools == nil {
			return Block{}
		}
		ag, err := s.d.Agents.Get(ctx, principal(b.Owner), b.AgentID)
		if err != nil {
			return Block{} // the grants switch reports an agent it cannot read
		}
		defs, err := s.d.AddonTools.Tools(ctx, principal(b.Owner))
		if err != nil {
			return Block{}
		}
		scopedWriters := map[string]bool{}
		for _, d := range defs {
			if !d.Action || !slices.Contains(ag.Addons, d.AddonID) {
				continue
			}
			if !d.ResourceScoped {
				return Block{} // an add-on without a resource list can change things
			}
			scopedWriters[d.AddonID] = true
		}
		if len(scopedWriters) == 0 {
			return Block{}
		}
		listed := false
		for addon := range scopedWriters {
			for _, e := range ag.Resources[addon] {
				listed = true
				if e.Mode == resourcesets.ModeWrite {
					return Block{}
				}
			}
		}
		fix := &Fix{Kind: FixOpenAgent, Target: b.AgentID, Field: "resources"}
		if listed {
			return Block{Code: CodeAgentResourcesReadOnly, Reason: "every resource on the list of the agent is read only", Fix: fix}
		}
		return Block{Code: CodeAgentResourcesNone, Reason: "no resource is on the list of the agent", Fix: fix}
	}
}
