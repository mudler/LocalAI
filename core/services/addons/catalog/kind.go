// SPDX-License-Identifier: MIT
package catalog

import (
	"regexp"
	"slices"
	"sort"
)

// Kind says what an index entry installs. One entry schema serves every kind;
// the kind decides which host component owns the install.
type Kind string

const (
	// KindAddon is a supervised add-on package. It is the default: an entry
	// without a kind is an add-on, so every index written before kinds existed
	// stays valid.
	KindAddon Kind = "addon"
	// KindBackend is a gRPC backend image installed into the backends directory.
	KindBackend Kind = "backend"
	// KindModel is reserved by the schema. No reader installs it yet, so a
	// reader skips it like any other kind it does not know.
	KindModel Kind = "model"
)

// kindPattern is the grammar of a kind name. A future kind must fit it to be
// skipped; anything else is a malformed entry and the index is refused.
var kindPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// ReadableKinds are the kinds this host installs.
var ReadableKinds = []Kind{KindAddon, KindBackend}

// EntryKind is the kind of the release, with the default applied.
func (r Release) EntryKind() Kind {
	if r.Kind == "" {
		return KindAddon
	}
	return r.Kind
}

// SkippedEntry is an index entry a reader left out because it does not know
// its kind. The entry is not validated further: a newer kind may carry fields
// this reader has never seen.
type SkippedEntry struct {
	// Position is the zero-based place of the entry in the index list.
	Position int    `json:"position"`
	Kind     string `json:"kind"`
	// ID is the entry's addonId when it is plain text, to help the report.
	ID string `json:"id,omitempty"`
}

// IndexResult is what a reader made of one index: the entries it understands
// and a record of those it skipped.
type IndexResult struct {
	Releases []Release
	Skipped  []SkippedEntry
}

// SkippedKinds lists the distinct kinds that were skipped, sorted.
func (r IndexResult) SkippedKinds() []string {
	out := []string{}
	for _, s := range r.Skipped {
		if !slices.Contains(out, s.Kind) {
			out = append(out, s.Kind)
		}
	}
	sort.Strings(out)
	return out
}

// Query kinds. The empty value lists add-ons only, so every caller written
// before kinds existed keeps seeing what it saw; QueryAllKinds lists every
// kind this host reads.
const QueryAllKinds Kind = "all"

func (q Query) matchesKind(r Release) bool {
	switch q.Kind {
	case "", KindAddon:
		return r.EntryKind() == KindAddon
	case QueryAllKinds:
		return true
	default:
		return r.EntryKind() == q.Kind
	}
}

// validQueryKind accepts the kinds a caller may ask for.
func validQueryKind(k Kind) bool {
	return k == "" || k == QueryAllKinds || slices.Contains(ReadableKinds, k)
}
