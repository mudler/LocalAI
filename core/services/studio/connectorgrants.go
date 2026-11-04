package studio

import (
	"context"
	"slices"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
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
	return func(ctx context.Context, b connectors.Binding, capability string) string {
		if !GrantGatedCapability(capability) {
			return ""
		}
		if b.AgentID == "" {
			return "this connection has no agent"
		}
		if s.d.Agents == nil {
			return "the agent of this bot cannot be read"
		}
		ag, err := s.d.Agents.Get(ctx, principal(b.Owner), b.AgentID)
		if err != nil {
			return "the agent of this bot cannot be read"
		}
		return grantVerdict(capability, ag.Builtins, s.usableGrants(ctx, principal(b.Owner), ag.Builtins), true)
	}
}
