// SPDX-License-Identifier: MIT
package hostops

import (
	"slices"
	"sort"
)

// Reason says why Authority.Allows refused. It is for the log, the audit line
// and the administrator pages. It never reaches an add-on: a refused call
// answers the same text whatever the reason.
type Reason string

const (
	// ReasonNone is the reason of an allowed call.
	ReasonNone Reason = ""
	// ReasonNotGranted means no source grants the operation.
	ReasonNotGranted Reason = "not_granted"
	// ReasonDeniedByPolicyFile means a deny rule of the policy file or flags
	// forbids the operation.
	ReasonDeniedByPolicyFile Reason = "denied_by_policy_file"
	// ReasonDeniedByAdmin means a deny rule an administrator set in the UI
	// forbids the operation.
	ReasonDeniedByAdmin Reason = "denied_by_admin"
	// ReasonIdentityMismatch means a grant exists but it belongs to another
	// package than the one installed under this add-on ID.
	ReasonIdentityMismatch Reason = "identity_mismatch"
)

// Denied reports whether the reason is a deny rule, not a missing grant.
func (r Reason) Denied() bool {
	return r == ReasonDeniedByPolicyFile || r == ReasonDeniedByAdmin
}

// EffectiveSet is what one add-on may call at one revision of the grants. It
// is a view for admission and for listings; the check of a call is
// Authority.Allows.
type EffectiveSet struct {
	// Revision is the revision of the grants the set was computed at. A static
	// authority has revision 0.
	Revision uint64
	// Prefixes are the operation prefixes the deployment grants (the policy
	// file floor), sorted.
	Prefixes []string
	// Operations are exact operation names granted through the UI store or a
	// blanket approval, sorted.
	Operations []string
	// Denied are the deny rules in force for the add-on, sorted. Each is a
	// prefix, a literal operation name or "*".
	Denied []string
}

// Authority answers what an add-on may call right now. Safe for concurrent
// use. The broker asks it on every call, so a change applies to the next call
// and never to one that already runs.
type Authority interface {
	// Allows reports whether the add-on may call op, and if not, why.
	Allows(addonID, op string) (ok bool, why Reason)
	// Snapshot returns the effective operations of an add-on and the revision
	// they were computed at. It is for admission and for the Apps listing.
	Snapshot(addonID string) EffectiveSet
	// Entitled reports whether the add-on holds any operation at all, so the
	// supervisor decides whether to open an endpoint.
	Entitled(addonID string) bool
}

// staticAuthority is the Authority of a fixed prefix map: today's behavior.
type staticAuthority struct{ prefixes map[string][]string }

// NewStaticAuthority builds an Authority over a fixed map of add-on ID to
// operation prefixes. It copies the map. An empty prefix is ignored and an
// add-on that is not listed holds nothing.
func NewStaticAuthority(prefixes map[string][]string) Authority {
	out := make(map[string][]string, len(prefixes))
	for id, list := range prefixes {
		clean := make([]string, 0, len(list))
		for _, p := range list {
			if p != "" {
				clean = append(clean, p)
			}
		}
		if len(clean) > 0 {
			sort.Strings(clean)
			out[id] = clean
		}
	}
	return staticAuthority{prefixes: out}
}

func (a staticAuthority) Allows(addonID, op string) (bool, Reason) {
	if hasPrefix(a.prefixes[addonID], op) {
		return true, ReasonNone
	}
	return false, ReasonNotGranted
}

func (a staticAuthority) Snapshot(addonID string) EffectiveSet {
	return EffectiveSet{Prefixes: slices.Clone(a.prefixes[addonID])}
}

func (a staticAuthority) Entitled(addonID string) bool { return len(a.prefixes[addonID]) > 0 }
