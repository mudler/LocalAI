package builtins

import (
	"regexp"
	"slices"
	"strings"
)

// The action policy of an unattended run: a trigger or an agent job, which
// runs with no person at the screen. A run like that starts read only. It may
// do more only for the classes of action that the owner granted (the act.*
// grants), and a run that the owner fixed as read only may do nothing but
// read, whatever it was granted.
//
// The policy is a pure function of the tool being called and the run's
// rights, so every tool of every source goes through the same table and the
// answer does not depend on the prompt.

// The action classes. A tool that only reads has none.
const (
	// ClassFiles is a write inside the fence of the run's folders.
	ClassFiles = GrantActFiles
	// ClassAddons is an action tool of an add-on (any kind but read).
	ClassAddons = GrantActAddons
	// ClassSend is a message beyond the delivery target of the run.
	ClassSend = GrantActSend
	// ClassSpend is a paid or external tool: the network, an MCP server, or an
	// add-on tool that acts with the person's credential outside LocalAI.
	ClassSpend = GrantActSpend
)

// addonGrantRE is the shape of a grant of one add-on: act.addons:<id>.
var addonGrantRE = regexp.MustCompile(`^act\.addons:[a-z0-9][a-z0-9._-]{0,63}$`)

// ActGrants lists the act.* names in declaration order.
func ActGrants() []string {
	return []string{ClassFiles, ClassAddons, ClassSend, ClassSpend}
}

// ValidActGrant says whether name is an act.* grant that a trigger may carry:
// one of the four classes, or act.addons:<id> for one add-on.
func ValidActGrant(name string) bool {
	return slices.Contains(ActGrants(), name) || addonGrantRE.MatchString(name)
}

// AddonActGrant is the grant that allows the action tools of one add-on.
func AddonActGrant(addonID string) string { return ClassAddons + ":" + addonID }

// ActTool is what the policy needs to know of a tool call.
type ActTool struct {
	// Name is the tool as the model sees it.
	Name string
	// Read is true for a tool that only reads. It is the add-on's declared
	// kind (contract.ToolKindRead) for an add-on tool.
	Read bool
	// Class is the class of an acting tool: one of the Class constants.
	Class string
	// AddonID names the add-on of an add-on tool, for act.addons:<id>.
	AddonID string
	// External is true for an acting tool that reaches outside LocalAI with a
	// credential or the network; it needs act.spend as well as its class.
	External bool
}

// ActPolicy is the rights of one unattended run.
type ActPolicy struct {
	// ReadOnly says that the owner fixed the run to read and report: the
	// binding that made it is in the auto_readonly mode, or the run declared
	// that it only reads. No grant overrides it.
	ReadOnly bool
	// Held are the act.* grants the run holds: what the owner gave the
	// trigger, within what the agent's grants allow.
	Held []string
}

// ActVerdict is the answer of the policy.
type ActVerdict struct {
	Allowed bool
	// Missing is the first grant the run lacks. Empty when allowed, and for a
	// read only run.
	Missing string
	// Reason is a short code for the audit line: read, granted, read_only,
	// not_granted.
	Reason string
	// Sentence is what the model is told when the call is refused. It is a
	// plain sentence the model can pass on to the person.
	Sentence string
}

// holds says whether the run holds need. act.addons:<id> is satisfied by
// act.addons too.
func (p ActPolicy) holds(need string) bool {
	if slices.Contains(p.Held, need) {
		return true
	}
	if id, ok := strings.CutPrefix(need, ClassAddons+":"); ok && id != "" {
		return slices.Contains(p.Held, ClassAddons)
	}
	return false
}

// Decide checks one tool call. A tool that reads is always allowed. Any other
// is refused unless the run is not read only and holds every grant the tool
// needs; a tool with no class is refused, never assumed harmless.
func (p ActPolicy) Decide(t ActTool) ActVerdict {
	if t.Read {
		return ActVerdict{Allowed: true, Reason: "read"}
	}
	if p.ReadOnly {
		return ActVerdict{Reason: "read_only", Sentence: "I did not run " + t.Name + ": this run was set up to read and report only, so it cannot " +
			"change anything. Say in your answer what you wanted to do, and leave it to the owner."}
	}
	needs := t.needs()
	for _, n := range needs {
		if !p.holds(n) {
			return ActVerdict{Missing: n, Reason: "not_granted", Sentence: "I did not run " + t.Name + ": nobody is here to approve it and this run " +
				"has no permission for " + n + ". Say in your answer what you wanted to do; the owner can allow " + n + " for this run."}
		}
	}
	return ActVerdict{Allowed: true, Reason: "granted"}
}

// needs lists the grants a tool call requires. A tool with an unknown class
// needs one nobody holds, so it is refused.
func (t ActTool) needs() []string {
	var out []string
	switch t.Class {
	case ClassAddons:
		if t.AddonID != "" {
			out = append(out, AddonActGrant(t.AddonID))
		} else {
			out = append(out, ClassAddons)
		}
	case ClassFiles, ClassSend, ClassSpend:
		out = append(out, t.Class)
	default:
		return []string{"act.unclassified"}
	}
	if t.External && t.Class != ClassSpend {
		out = append(out, ClassSpend)
	}
	return out
}
