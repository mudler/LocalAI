package studio

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

// Why the runs of a trigger only read.
const (
	// ReadOnlyDeclared: the proposal said that its runs only read and report.
	ReadOnlyDeclared = "declared"
	// ReadOnlyBinding: a connection in the auto_readonly mode made it.
	ReadOnlyBinding = "binding"
	// ReadOnlyNoActs: the trigger holds no act.* permission.
	ReadOnlyNoActs = "no_acts"
	// ReadOnlyAgent: the trigger holds permissions, but its agent holds none
	// of them, so none applies.
	ReadOnlyAgent = "agent"
)

// ActsView is what the runs of a trigger may do, as the interface shows it.
// It is computed with the same rules as the run (triggerPolicy).
type ActsView struct {
	// Held are the act.* names that apply to a run: the trigger's own, within
	// the grants of its agent, plus act.files when its plan names a file it
	// writes. Empty for a read only run.
	Held []string
	// ReadOnly is true when a run may only read.
	ReadOnly bool
	// Why is a ReadOnly* code when ReadOnly is true.
	Why string
}

// ActsView gives the rights of the runs of t.
func (h *Triggers) ActsView(ctx context.Context, t triggers.Trigger) ActsView {
	ceiling := actNames(h.svc.AgentGrants(ctx, t.Owner, t.AgentID))
	pol := h.svc.triggerPolicy(ctx, t, ceiling, t.AgentID != "")
	v := ActsView{Held: pol.Held}
	switch {
	case t.Route.ReadOnly:
		v.Why = ReadOnlyDeclared
	case pol.ReadOnly:
		v.Why = ReadOnlyBinding
	case len(pol.Held) == 0 && len(t.Acts) == 0 && t.Writes == "":
		v.Why = ReadOnlyNoActs
	case len(pol.Held) == 0:
		v.Why = ReadOnlyAgent
	}
	if v.Why != "" {
		v.ReadOnly, v.Held = true, nil
	}
	return v
}

// checkProposedActs checks the acts a model asks for in a proposal. They must
// be valid names and lie within the act.* grants of the agent of the chat; a
// chat without an agent is the person's own and has no ceiling but the list.
// The model cannot name more than the agent holds, and nothing after the
// person confirms: no tool changes the acts of a trigger.
func checkProposedActs(names []string, agentID string, grants []string, readOnly bool) ([]string, error) {
	acts, err := cleanActs(names)
	if err != nil {
		return nil, err
	}
	if len(acts) == 0 {
		return nil, nil
	}
	if readOnly {
		return nil, bad("access read_only cannot come with acts; leave acts out, or use read_write")
	}
	if agentID == "" {
		return acts, nil
	}
	ceiling := actNames(grants)
	for _, n := range acts {
		if !withinCeiling(n, ceiling) {
			return nil, bad("your agent does not hold %s, so a trigger cannot get it. Leave it out; the owner can add it to the agent first", n)
		}
	}
	return acts, nil
}

// sameActs says whether two lists name the same acts, in any order.
func sameActs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, n := range a {
		if !slices.Contains(b, n) {
			return false
		}
	}
	return true
}

// actWords says in plain words what an act lets an unattended run do.
func actWords(name string) string {
	switch name {
	case builtins.ClassFiles:
		return "write files in the agent's folders"
	case builtins.ClassAddons:
		return "use the actions of the agent's add-ons"
	case builtins.ClassSend:
		return "send messages beyond where it reports"
	case builtins.ClassSpend:
		return "use paid or external tools and the network"
	}
	if id, ok := strings.CutPrefix(name, builtins.ClassAddons+":"); ok {
		return "use the actions of the add-on " + id
	}
	return name
}

// actSensitive says whether the act is marked sensitive in the grants list.
// An act of one add-on is sensitive like act.addons.
func actSensitive(name string) bool {
	if strings.HasPrefix(name, builtins.ClassAddons+":") {
		name = builtins.ClassAddons
	}
	g, ok := builtins.LookupGrant(name)
	return ok && g.Sensitive
}

// actsText is one sentence about what the runs of a proposed trigger may do.
// It is on the text of a proposal in a chat of a messaging app, so the person
// reads it before they answer yes.
func actsText(t triggers.Trigger) string {
	held := slices.Clone(t.Acts)
	if t.Writes != "" && !slices.Contains(held, builtins.ClassFiles) {
		held = append(held, builtins.ClassFiles)
	}
	if t.Route.ReadOnly || len(held) == 0 {
		return "Permissions: read only. A run can read and report, and cannot change anything."
	}
	words := make([]string, len(held))
	for i, n := range held {
		words[i] = actWords(n)
		if actSensitive(n) {
			words[i] += " (sensitive)"
		}
	}
	return fmt.Sprintf("Permissions: with nobody present, a run may %s. Say no if that is more than you want.", strings.Join(words, "; "))
}
