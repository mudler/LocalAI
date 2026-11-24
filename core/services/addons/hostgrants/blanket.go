// SPDX-License-Identifier: MIT
package hostgrants

// BlanketInput describes one install or upgrade to the blanket evaluator.
type BlanketInput struct {
	AddonID string
	// Official is true only when the package comes from the built-in
	// official library AND its receipt shows the official signer. The
	// source ID alone is not enough, since an administrator names sources
	// freely. The caller computes it from the verified receipt.
	Official           bool
	Required, Optional []string
}

// BlanketResult says what the switch does for one install or upgrade.
type BlanketResult struct {
	// Applies is false when the switch is off or the package is outside its
	// scope. Plan is then empty and no grant is written.
	Applies bool
	// Revision is the switch revision that produces the grants; it goes into
	// each OpGrant.BlanketRevision and the audit entry.
	Revision uint64
	// Plan holds the grants (required operations, plus optional ones when
	// IncludeOptional is set) and the refusals. BlockedRequired non-empty
	// means the install is refused as blocked_by_policy: blanket never
	// writes a grant for a denied operation.
	Plan Plan
}

// InScope reports whether the switch covers a package.
func (b Blanket) InScope(official bool) bool {
	switch b.Mode {
	case BlanketAny:
		return true
	case BlanketOfficial:
		return official
	}
	return false
}

// EvaluateBlanket decides what the blanket switch grants. It grants only
// declared, registered names, required ones unless IncludeOptional is set,
// and never an operation a deny rule blocks. The effective switch is the one
// in doc, unless the deployment fixes the mode, which the caller passes as
// override ("" keeps the document's mode).
func (f Floor) EvaluateBlanket(doc Snapshot, c Context, override string, in BlanketInput) BlanketResult {
	b := doc.Blanket
	if override != "" {
		b.Mode = override
	}
	if !b.InScope(in.Official) {
		return BlanketResult{Revision: b.Revision}
	}
	wanted := append([]string(nil), in.Required...)
	if b.IncludeOptional {
		wanted = append(wanted, in.Optional...)
	}
	return BlanketResult{
		Applies:  true,
		Revision: b.Revision,
		Plan: f.Plan(doc, c, PlanInput{
			AddonID: in.AddonID, Required: in.Required, Optional: in.Optional, Wanted: wanted,
		}),
	}
}

// MergeGrants returns entry with the named operations added, each stamped with
// the given metadata. Existing operations keep their own source: an explicit
// grant is never relabelled blanket. The identity, generation and operation ID
// of the entry become those of the new write.
func MergeGrants(entry AddonGrants, identity string, generation uint64, operationID string, add []OpGrant) AddonGrants {
	out := AddonGrants{Identity: identity, Generation: generation, OperationID: operationID}
	have := map[string]bool{}
	// An entry for another identity is ineffective, so it is not carried over.
	if entry.Identity == identity {
		for _, op := range entry.Ops {
			out.Ops = append(out.Ops, op)
			have[op.Name] = true
		}
	}
	for _, op := range add {
		if !have[op.Name] {
			out.Ops = append(out.Ops, op)
			have[op.Name] = true
		}
	}
	return out
}

// KeepAsOwn relabels the blanket grants of an entry as the administrator's
// own, so a later "revoke blanket grants" no longer removes them.
func KeepAsOwn(entry AddonGrants, at, by string) AddonGrants {
	out := entry
	out.Ops = make([]OpGrant, len(entry.Ops))
	for i, op := range entry.Ops {
		if op.Source == SourceBlanket {
			op.Source, op.BlanketRevision, op.At, op.By = SourceEdit, 0, at, by
		}
		out.Ops[i] = op
	}
	return out
}
