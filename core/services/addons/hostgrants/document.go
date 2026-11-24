// SPDX-License-Identifier: MIT

// Package hostgrants owns the owner document of host operation grants that an
// administrator makes in the UI, the deny rules and the blanket approval
// switch. It also holds the pure policy logic that combines that document
// with the startup policy file and flags into the operations an add-on may
// call. The document lives in the owner policy folder beside, and separate
// from, the host settings document, so it is never projected to a frontend.
//
// The package is not an authorization boundary by itself: only the
// authenticated host owner may call Store.Update.
package hostgrants

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// SchemaVersion is the version of the stored document.
const SchemaVersion = 1

// Limits of the document. The add-on and deny limits match the policy file.
const (
	MaxAddons         = 128
	MaxOpsPerAddon    = 32
	MaxDenyKeys       = 128
	MaxDenyEntries    = 32
	MaxHintsPerAddon  = 32
	MaxReceipts       = 256
	maxDocumentBytes  = 4 << 20
	maxTextLen        = 256
	maxOperationIDLen = 128
)

var (
	ErrInvalid           = errors.New("invalid addon host grants")
	ErrConflict          = errors.New("addon host grants revision conflict")
	ErrOperationMismatch = errors.New("addon host grants operation body mismatch")
	ErrUnavailable       = errors.New("addon host grants unavailable")
	// ErrAuditFailed is a write that was refused because its audit line could
	// not be written. It counts as unavailable: the caller may retry later.
	ErrAuditFailed = fmt.Errorf("%w: the audit line cannot be written", ErrUnavailable)
	ErrUncertain   = errors.New("addon host grants durability uncertain; reopen and reconcile operation")
)

var (
	addonIDPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	opNamePattern   = regexp.MustCompile(`^[a-z]+(\.[a-z]+)+$`)
	prefixPattern   = regexp.MustCompile(`^[a-z]+\.([a-z]+\.)*$`)
	hexDigestFormat = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Source says why an operation is granted. The first three are an explicit
// administrator choice; Blanket was made by the blanket switch.
type Source string

const (
	SourceInstall Source = "install"
	SourceUpgrade Source = "upgrade"
	SourceEdit    Source = "edit"
	SourceBlanket Source = "blanket"
)

func (s Source) valid() bool {
	switch s {
	case SourceInstall, SourceUpgrade, SourceEdit, SourceBlanket:
		return true
	}
	return false
}

// Blanket modes.
const (
	BlanketOff      = "off"
	BlanketOfficial = "official"
	BlanketAny      = "any"
)

// OpGrant is one exact operation granted to an add-on. Grants are never
// prefixes: the UI can grant only what the manifest names.
type OpGrant struct {
	Name    string `json:"name"`
	Source  Source `json:"source"`
	At      string `json:"at,omitempty"`
	By      string `json:"by,omitempty"`
	Version string `json:"version,omitempty"`
	// BlanketRevision names the blanket switch revision that produced a
	// grant with Source blanket, so the group can be told apart and revoked.
	BlanketRevision uint64 `json:"blanketRevision,omitempty"`
	// OperationID is set when the operation was added to an entry that already
	// existed, by the lifecycle operation of an upgrade. It takes effect only
	// once that operation completed, while the other operations of the entry
	// keep working. Empty means the operation of the entry counts.
	OperationID string `json:"operationId,omitempty"`
}

// AddonGrants is the UI-made grant entry of one add-on.
type AddonGrants struct {
	// Identity is the trusted identity of the installed package. An entry
	// whose identity differs from the current receipt is ineffective.
	Identity   string `json:"identity"`
	Generation uint64 `json:"generation"`
	// OperationID is the lifecycle operation that created the entry. The
	// entry takes effect only once that operation completed.
	OperationID string    `json:"operationId"`
	Ops         []OpGrant `json:"ops"`
}

// Blanket is the blanket approval switch. Revision counts changes of Mode or
// IncludeOptional and is assigned by the store.
type Blanket struct {
	Mode            string `json:"mode"`
	IncludeOptional bool   `json:"includeOptional"`
	Revision        uint64 `json:"revision"`
	ChangedAt       string `json:"changedAt,omitempty"`
	ChangedBy       string `json:"changedBy,omitempty"`
}

// Snapshot is the visible content of the document at one revision.
type Snapshot struct {
	SchemaVersion int                    `json:"schemaVersion"`
	Revision      uint64                 `json:"revision"`
	Blanket       Blanket                `json:"blanket"`
	Grants        map[string]AddonGrants `json:"grants"`
	// Deny holds the UI deny rules: add-on ID or "*" to prefixes, exact
	// operation names, or the single token "*".
	Deny map[string][]string `json:"deny"`
	// Hints are non-authoritative: operations granted before an uninstall,
	// used only to pre-tick the dialog on reinstall.
	Hints map[string][]string `json:"hints"`
}

type receipt struct {
	Body     string `json:"body"`
	Revision uint64 `json:"revision"`
}

type document struct {
	Snapshot
	Receipts map[string]receipt `json:"receipts"`
}

func emptySnapshot() Snapshot {
	return Snapshot{
		SchemaVersion: SchemaVersion,
		Blanket:       Blanket{Mode: BlanketOff},
		Grants:        map[string]AddonGrants{},
		Deny:          map[string][]string{},
		Hints:         map[string][]string{},
	}
}

// Clone returns a deep copy that the caller may modify.
func (s Snapshot) Clone() Snapshot {
	out := s
	out.Grants = make(map[string]AddonGrants, len(s.Grants))
	for id, g := range s.Grants {
		g.Ops = slices.Clone(g.Ops)
		out.Grants[id] = g
	}
	out.Deny = cloneLists(s.Deny)
	out.Hints = cloneLists(s.Hints)
	return out
}

func cloneLists(in map[string][]string) map[string][]string {
	out := make(map[string][]string, len(in))
	for k, v := range in {
		out[k] = slices.Clone(v)
	}
	return out
}

// Patch is one atomic change. Parts apply in this order: RevokeBlanket,
// Addons, Blanket, Deny, Hints. A nil pointer or map leaves its part alone.
type Patch struct {
	// RevokeBlanket drops every op with source blanket from every add-on,
	// removing entries left without ops.
	RevokeBlanket bool `json:"revokeBlanket,omitempty"`
	// Addons replaces the whole entry of an add-on; a nil value deletes it.
	// Callers read the snapshot, edit one entry and send it back; the
	// expected revision makes that safe.
	Addons map[string]*AddonGrants `json:"addons,omitempty"`
	// Blanket sets the switch. The store assigns Revision: it grows by one
	// when Mode or IncludeOptional change.
	Blanket *Blanket `json:"blanket,omitempty"`
	// Deny replaces the whole UI deny rule set.
	Deny *map[string][]string `json:"deny,omitempty"`
	// Hints replaces the hint of an add-on; an empty list deletes it.
	Hints map[string][]string `json:"hints,omitempty"`
}

func apply(base Snapshot, p Patch) (Snapshot, error) {
	next := base.Clone()
	if p.RevokeBlanket {
		for id, g := range next.Grants {
			kept := g.Ops[:0]
			for _, op := range g.Ops {
				if op.Source != SourceBlanket {
					kept = append(kept, op)
				}
			}
			if len(kept) == 0 {
				delete(next.Grants, id)
				continue
			}
			g.Ops = kept
			next.Grants[id] = g
		}
	}
	for id, g := range p.Addons {
		if g == nil {
			delete(next.Grants, id)
			continue
		}
		entry := *g
		entry.Ops = slices.Clone(g.Ops)
		next.Grants[id] = entry
	}
	if p.Blanket != nil {
		b := *p.Blanket
		if b.Mode != next.Blanket.Mode || b.IncludeOptional != next.Blanket.IncludeOptional {
			b.Revision = next.Blanket.Revision + 1
		} else {
			b.Revision = next.Blanket.Revision
		}
		next.Blanket = b
	}
	if p.Deny != nil {
		next.Deny = cloneLists(*p.Deny)
	}
	for id, ops := range p.Hints {
		if len(ops) == 0 {
			delete(next.Hints, id)
			continue
		}
		next.Hints[id] = slices.Clone(ops)
	}
	normalize(&next)
	if err := validate(next); err != nil {
		return Snapshot{}, err
	}
	return next, nil
}

// normalize sorts what has no meaningful order, so equal content has one form.
func normalize(s *Snapshot) {
	for id, g := range s.Grants {
		sort.Slice(g.Ops, func(i, j int) bool { return g.Ops[i].Name < g.Ops[j].Name })
		s.Grants[id] = g
	}
	for id, ops := range s.Hints {
		sort.Strings(ops)
		s.Hints[id] = ops
	}
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func validText(s string) bool {
	if len(s) > maxTextLen {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func validTime(s string) bool {
	if s == "" {
		return true
	}
	_, err := time.Parse(time.RFC3339, s)
	return err == nil && validText(s)
}

func validOperationID(id string) bool {
	return len(id) > 0 && len(id) <= maxOperationIDLen && strings.TrimSpace(id) == id && validText(id)
}

// ValidDenyEntry reports whether s may appear in a deny list: a prefix such
// as "egress.", an exact operation name such as "workspace.admin", or "*".
func ValidDenyEntry(s string) bool {
	return s == "*" || prefixPattern.MatchString(s) || opNamePattern.MatchString(s)
}

// ValidateDeny checks a deny rule set against the limits of the policy file.
func ValidateDeny(deny map[string][]string) error {
	if len(deny) > MaxDenyKeys {
		return invalid("deny exceeds %d add-ons", MaxDenyKeys)
	}
	for id, entries := range deny {
		if id != "*" && !addonIDPattern.MatchString(id) {
			return invalid("deny key %q", id)
		}
		if len(entries) > MaxDenyEntries {
			return invalid("deny for %q exceeds %d entries", id, MaxDenyEntries)
		}
		seen := map[string]bool{}
		for _, e := range entries {
			if !ValidDenyEntry(e) || seen[e] {
				return invalid("deny entry %q for %q", e, id)
			}
			seen[e] = true
		}
		if seen["*"] && len(entries) != 1 {
			return invalid(`deny "*" must be the only entry of %q`, id)
		}
	}
	return nil
}

func validate(s Snapshot) error {
	if s.SchemaVersion != SchemaVersion {
		return invalid("schema version")
	}
	switch s.Blanket.Mode {
	case BlanketOff, BlanketOfficial, BlanketAny:
	default:
		return invalid("blanket mode")
	}
	if !validText(s.Blanket.ChangedBy) || !validTime(s.Blanket.ChangedAt) {
		return invalid("blanket metadata")
	}
	if len(s.Grants) > MaxAddons {
		return invalid("more than %d add-ons", MaxAddons)
	}
	for id, g := range s.Grants {
		if !addonIDPattern.MatchString(id) {
			return invalid("add-on ID %q", id)
		}
		if g.Identity == "" || !validText(g.Identity) || !validOperationID(g.OperationID) {
			return invalid("entry of %q", id)
		}
		if len(g.Ops) == 0 || len(g.Ops) > MaxOpsPerAddon {
			return invalid("%q must hold between 1 and %d operations", id, MaxOpsPerAddon)
		}
		seen := map[string]bool{}
		for _, op := range g.Ops {
			if !opNamePattern.MatchString(op.Name) || seen[op.Name] || !op.Source.valid() {
				return invalid("operation %q of %q", op.Name, id)
			}
			seen[op.Name] = true
			if op.OperationID != "" && !validOperationID(op.OperationID) {
				return invalid("operation ID of %q for %q", op.Name, id)
			}
			if !validText(op.By) || !validText(op.Version) || !validTime(op.At) {
				return invalid("metadata of %q for %q", op.Name, id)
			}
			if (op.Source == SourceBlanket) != (op.BlanketRevision != 0) || op.BlanketRevision > s.Blanket.Revision {
				return invalid("blanket revision of %q for %q", op.Name, id)
			}
		}
	}
	if err := ValidateDeny(s.Deny); err != nil {
		return err
	}
	if len(s.Hints) > MaxAddons {
		return invalid("more than %d hints", MaxAddons)
	}
	for id, ops := range s.Hints {
		if !addonIDPattern.MatchString(id) || len(ops) > MaxHintsPerAddon {
			return invalid("hint of %q", id)
		}
		seen := map[string]bool{}
		for _, op := range ops {
			if !opNamePattern.MatchString(op) || seen[op] {
				return invalid("hint operation %q of %q", op, id)
			}
			seen[op] = true
		}
	}
	return nil
}

func validateDocument(d document) error {
	if d.Revision == 0 {
		return invalid("revision")
	}
	if err := validate(d.Snapshot); err != nil {
		return err
	}
	if len(d.Receipts) > MaxReceipts {
		return invalid("too many receipts")
	}
	for id, r := range d.Receipts {
		if !validOperationID(id) || !hexDigestFormat.MatchString(r.Body) || r.Revision < 1 || r.Revision > d.Revision {
			return invalid("receipt %q", id)
		}
	}
	return nil
}
