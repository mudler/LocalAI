// SPDX-License-Identifier: MIT
package hostgrants

import (
	"slices"
	"strings"
)

// Reason says why an operation is not allowed. The strings are the ones of
// hostops.Reason in the design; the caller converts. An add-on must never see
// them: only logs, audit lines and the administrator page do.
type Reason string

const (
	ReasonNone             Reason = ""
	ReasonNotGranted       Reason = "not_granted"
	ReasonDeniedByFile     Reason = "denied_by_policy_file"
	ReasonDeniedByAdmin    Reason = "denied_by_admin"
	ReasonIdentityMismatch Reason = "identity_mismatch"
)

// SourceDeployment labels a grant that comes from the policy file or flags.
const SourceDeployment Source = "deployment"

// Decision is the answer for one add-on and operation.
type Decision struct {
	Allowed bool
	Reason  Reason
	// Source is set when Allowed: deployment, install, upgrade, edit or
	// blanket. When several sources grant the same operation the display
	// precedence applies: deployment, then explicit UI, then blanket.
	Source Source
}

// Context carries what the evaluator cannot know from the document.
type Context struct {
	// Identity is the trusted identity of the installed package of the add-on
	// being evaluated. Empty means unknown: UI entries then do not count.
	Identity string
	// Completed reports whether a lifecycle operation completed in the
	// journal. A UI entry counts only once its operation completed. Nil
	// treats every entry as completed.
	Completed func(operationID string) bool
	// Registered reports whether the host registers an operation. Nil
	// registers every name.
	Registered func(op string) bool
}

// Floor is the startup policy of the file and flags. It is read-only after
// construction.
type Floor struct {
	// Grants maps an add-on ID to operation prefixes (file and flat flag).
	Grants map[string][]string
	// Deny maps an add-on ID, or "*", to deny entries (file and flag).
	Deny map[string][]string
}

// matchDeny reports whether any rule of rules names op for addonID.
func matchDeny(rules map[string][]string, addonID, op string) bool {
	for _, key := range [...]string{addonID, "*"} {
		for _, e := range rules[key] {
			switch {
			case e == "*":
				return true
			case strings.HasSuffix(e, "."):
				if strings.HasPrefix(op, e) {
					return true
				}
			case e == op:
				return true
			}
		}
	}
	return false
}

// Denied reports whether op is denied for the add-on and by which layer. File
// and flag deny come before the deny set in the UI document.
func (f Floor) Denied(doc Snapshot, addonID, op string) (bool, Reason) {
	if matchDeny(f.Deny, addonID, op) {
		return true, ReasonDeniedByFile
	}
	if matchDeny(doc.Deny, addonID, op) {
		return true, ReasonDeniedByAdmin
	}
	return false, ReasonNone
}

func (f Floor) floorGrants(addonID, op string) bool {
	for _, p := range f.Grants[addonID] {
		if strings.HasPrefix(op, p) {
			return true
		}
	}
	return false
}

// Decide computes: denied by file or flag, denied in the UI, granted by the
// floor (prefix), granted by a UI entry (equality, identity checked), else not
// granted. Deny always wins, whatever else grants the operation.
func (f Floor) Decide(doc Snapshot, c Context, addonID, op string) Decision {
	if denied, why := f.Denied(doc, addonID, op); denied {
		return Decision{Reason: why}
	}
	if c.Registered != nil && !c.Registered(op) {
		return Decision{Reason: ReasonNotGranted}
	}
	if f.floorGrants(addonID, op) {
		return Decision{Allowed: true, Source: SourceDeployment}
	}
	entry, ok := doc.Grants[addonID]
	if !ok {
		return Decision{Reason: ReasonNotGranted}
	}
	i := slices.IndexFunc(entry.Ops, func(g OpGrant) bool { return g.Name == op })
	if i < 0 {
		return Decision{Reason: ReasonNotGranted}
	}
	if c.Identity == "" || entry.Identity != c.Identity {
		return Decision{Reason: ReasonIdentityMismatch}
	}
	if c.Completed != nil && !c.Completed(EffectiveOperationID(entry, entry.Ops[i])) {
		return Decision{Reason: ReasonNotGranted}
	}
	return Decision{Allowed: true, Source: entry.Ops[i].Source}
}

// EffectiveOperationID is the lifecycle operation whose completion makes one
// operation of an entry effective: its own, when an upgrade added it to an
// existing entry, else the one that created the entry.
func EffectiveOperationID(entry AddonGrants, op OpGrant) string {
	if op.OperationID != "" {
		return op.OperationID
	}
	return entry.OperationID
}

// OpStatus is a Decision for one named operation.
type OpStatus struct {
	Op string
	Decision
}

// Effective evaluates each candidate (usually the operations the manifest
// declares) for the add-on, in the order given.
func (f Floor) Effective(doc Snapshot, c Context, addonID string, candidates []string) []OpStatus {
	out := make([]OpStatus, 0, len(candidates))
	for _, op := range candidates {
		out = append(out, OpStatus{Op: op, Decision: f.Decide(doc, c, addonID, op)})
	}
	return out
}

// Entitled reports whether the add-on may call anything at all: it has a file
// or flag grant that no add-on-wide deny ("*") removes, or an effective UI
// entry. A narrower deny cannot make this false for a prefix grant, because
// part of the prefix may stay callable; the per-call check is Decide.
func (f Floor) Entitled(doc Snapshot, c Context, addonID string) bool {
	if len(f.Grants[addonID]) > 0 && !denyAll(f.Deny, addonID) && !denyAll(doc.Deny, addonID) {
		return true
	}
	for _, op := range doc.Grants[addonID].Ops {
		if f.Decide(doc, c, addonID, op.Name).Allowed {
			return true
		}
	}
	return false
}

func denyAll(rules map[string][]string, addonID string) bool {
	return slices.Contains(rules[addonID], "*") || slices.Contains(rules["*"], "*")
}

// PlanInput asks which of the wanted operations may be granted now.
type PlanInput struct {
	AddonID string
	// Required and Optional are the operations of the verified manifest.
	Required, Optional []string
	// Wanted is what the administrator approved, or what the blanket switch
	// picked. Only declared, registered, not denied, not yet held names are
	// granted; anything else in Wanted is ignored.
	Wanted []string
}

// Plan is the outcome of PlanInput. Every list keeps the order of its input
// and holds each name once.
type Plan struct {
	// Grant is the set to write: wanted, declared, registered, not denied
	// and not yet held.
	Grant []string
	// Held are declared operations already allowed by any source.
	Held []string
	// Denied are declared operations that a deny rule blocks.
	Denied []string
	// Unsupported are declared operations the host does not register.
	Unsupported []string
	// BlockedRequired are required operations that are denied; the install
	// is refused as blocked_by_policy.
	BlockedRequired []string
	// MissingRequired are required operations that are neither granted nor
	// held nor denied, including unsupported ones; the install is refused
	// as not approved.
	MissingRequired []string
}

// Plan applies the grant rule of the design: approved ∩ declared ∩ registered,
// minus deny, minus what is already held. It never reads Wanted outside the
// declared names, so a lying approval cannot widen anything.
func (f Floor) Plan(doc Snapshot, c Context, in PlanInput) Plan {
	var p Plan
	wanted := map[string]bool{}
	for _, w := range in.Wanted {
		wanted[w] = true
	}
	required := map[string]bool{}
	for _, r := range in.Required {
		required[r] = true
	}
	seen := map[string]bool{}
	declared := append(slices.Clone(in.Required), in.Optional...)
	for _, op := range declared {
		if seen[op] {
			continue
		}
		seen[op] = true
		if denied, _ := f.Denied(doc, in.AddonID, op); denied {
			p.Denied = append(p.Denied, op)
			if required[op] {
				p.BlockedRequired = append(p.BlockedRequired, op)
			}
			continue
		}
		if c.Registered != nil && !c.Registered(op) {
			p.Unsupported = append(p.Unsupported, op)
			if required[op] {
				p.MissingRequired = append(p.MissingRequired, op)
			}
			continue
		}
		if f.Decide(doc, c, in.AddonID, op).Allowed {
			p.Held = append(p.Held, op)
			continue
		}
		if wanted[op] {
			p.Grant = append(p.Grant, op)
			continue
		}
		if required[op] {
			p.MissingRequired = append(p.MissingRequired, op)
		}
	}
	return p
}
