package studio

import (
	"context"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/hostmsg"
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
func actWords(v hostmsg.Voice, name string) string {
	switch name {
	case builtins.ClassFiles:
		return v.T("act.word.files")
	case builtins.ClassAddons:
		return v.T("act.word.addons")
	case builtins.ClassSend:
		return v.T("act.word.send")
	case builtins.ClassSpend:
		return v.T("act.word.spend")
	}
	if id, ok := strings.CutPrefix(name, builtins.ClassAddons+":"); ok {
		return v.T("act.addon", "id", id)
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

// actLabel names an act in a few plain words, for the text of a proposal.
func actLabel(v hostmsg.Voice, name string) string {
	switch name {
	case builtins.ClassFiles:
		return v.T("act.label.files")
	case builtins.ClassAddons:
		return v.T("act.label.addons")
	case builtins.ClassSend:
		return v.T("act.label.send")
	case builtins.ClassSpend:
		return v.T("act.label.spend")
	}
	if id, ok := strings.CutPrefix(name, builtins.ClassAddons+":"); ok {
		return v.T("act.addon", "id", id)
	}
	return name
}

// DroppedActs are the acts a model asked for in a proposal that the host left
// out, and why. The proposal text names them, so the person is not misled
// into thinking the run can do what the model said it could.
type DroppedActs struct {
	// Binding are the acts the owner has not allowed for this connection.
	Binding []string
	// Agent are the acts the agent of the connection does not hold.
	Agent []string
}

// Empty says whether nothing was dropped.
func (d DroppedActs) Empty() bool { return len(d.Binding) == 0 && len(d.Agent) == 0 }

// Text is one sentence that names the dropped acts, or "" when none.
func (d DroppedActs) Text() string { return d.TextIn(hostmsg.Voice{}) }

// TextIn is Text in the language of the voice.
func (d DroppedActs) TextIn(v hostmsg.Voice) string {
	if d.Empty() {
		return ""
	}
	var parts []string
	words := func(names []string) string {
		w := make([]string, len(names))
		for i, n := range names {
			w[i] = actLabel(v, n)
		}
		return strings.Join(w, ", ")
	}
	if len(d.Binding) > 0 {
		parts = append(parts, v.T("dropped.binding", "words", words(d.Binding)))
	}
	if len(d.Agent) > 0 {
		parts = append(parts, v.T("dropped.agent", "words", words(d.Agent)))
	}
	return v.T("dropped.text", "parts", strings.Join(parts, "; "))
}

// limitConnectorActs reduces what a model asked for in a chat of a messaging
// app to the acts that the binding allows (its Acts) and the agent holds. The
// rest is dropped and named, never an error: the proposal still stands, as
// the read only or narrower job the person can judge. A chat without an agent
// cannot propose from a connector, so the agent ceiling is always applied.
func limitConnectorActs(asked []string, ap connectors.Approval, grants []string) (kept []string, dropped DroppedActs) {
	ceiling := actNames(grants)
	for _, n := range asked {
		switch {
		case !ap.AllowsAct(n):
			dropped.Binding = append(dropped.Binding, n)
		case !withinCeiling(n, ceiling):
			dropped.Agent = append(dropped.Agent, n)
		default:
			kept = append(kept, n)
		}
	}
	return kept, dropped
}

// requestableActs are the acts a model may ask for in a proposal from this
// binding: what the owner allows for it, within what the agent holds.
func requestableActs(ap connectors.Approval, grants []string) []string {
	kept, _ := limitConnectorActs(ap.Acts, ap, grants)
	return kept
}

// effectiveActs are the acts a trigger holds: its own list, plus act.files
// when its plan names a file it writes. The card and the run read the same.
func effectiveActs(t triggers.Trigger) []string {
	held := slices.Clone(t.Acts)
	if t.Writes != "" && !slices.Contains(held, builtins.ClassFiles) {
		held = append(held, builtins.ClassFiles)
	}
	return held
}

// actsText is one sentence about what the runs of a proposed trigger may do.
// It is on the text of a proposal in a chat of a messaging app, so the person
// reads it before they answer yes. canAsk says that the binding lets runs act,
// so the person can ask the agent for it; a read only text then says so.
func actsText(v hostmsg.Voice, t triggers.Trigger, canAsk bool) string {
	held := effectiveActs(t)
	if t.Route.ReadOnly || len(held) == 0 {
		next := v.T("acts.next_page")
		if canAsk {
			next = v.T("acts.next_ask")
		}
		return v.T("acts.read_only", "next", next)
	}
	words := make([]string, len(held))
	for i, n := range held {
		words[i] = actWords(v, n)
		if actSensitive(n) {
			words[i] = v.T("acts.sensitive", "words", words[i])
		}
	}
	return v.T("acts.with", "list", strings.Join(words, "; "))
}
