// SPDX-License-Identifier: MIT

// Package hostsettings owns the bounded add-on host policy. It is not an
// authorization boundary: only the authenticated host owner may call Update.
package hostsettings

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

const SchemaVersion = 1
const MaxReceipts = 1024
const maxDocumentBytes = 64 << 20

var (
	ErrInvalid           = errors.New("invalid addon host policy")
	ErrConflict          = errors.New("addon host policy revision conflict")
	ErrOperationMismatch = errors.New("addon host policy operation body mismatch")
	ErrUnavailable       = errors.New("addon host policy unavailable")
	ErrUncertain         = errors.New("addon host policy durability uncertain; reopen and reconcile operation")
	ErrReceiptCapacity   = errors.New("addon host policy receipt capacity reached")
	ErrMigrationRequired = errors.New("explicit owner policy migration required")
)

type Policy struct {
	Ceiling           string            `json:"chats_egress_ceiling"`
	Default           string            `json:"chats_egress_default"`
	AllowPrivate      bool              `json:"chats_egress_allow_private"`
	Ports             string            `json:"chats_egress_ports"`
	ExtraRegistries   string            `json:"chats_egress_extra_registries"`
	AllowedHosts      string            `json:"chats_egress_allowed_hosts"`
	ToolProviders     map[string]string `json:"chats_tool_providers"`
	AutoUpgradeAddons bool              `json:"auto_upgrade_addons"`
}

// Patch changes only present keys. An empty string clears a string field;
// false clears a boolean; a null provider value deletes that individual pin.
// Null is invalid for every other field, including the provider map itself.
type Patch struct {
	Ceiling           *string            `json:"chats_egress_ceiling,omitempty"`
	Default           *string            `json:"chats_egress_default,omitempty"`
	AllowPrivate      *bool              `json:"chats_egress_allow_private,omitempty"`
	Ports             *string            `json:"chats_egress_ports,omitempty"`
	ExtraRegistries   *string            `json:"chats_egress_extra_registries,omitempty"`
	AllowedHosts      *string            `json:"chats_egress_allowed_hosts,omitempty"`
	ToolProviders     map[string]*string `json:"chats_tool_providers,omitempty"`
	AutoUpgradeAddons *bool              `json:"auto_upgrade_addons,omitempty"`
}

func (p *Patch) UnmarshalJSON(b []byte) error {
	type plain Patch
	var raw map[string]json.RawMessage
	if err := strictJSON(b, &raw); err != nil || raw == nil {
		return ErrInvalid
	}
	for _, v := range raw {
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return ErrInvalid
		}
	}
	var out plain
	if err := strictJSON(b, &out); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	*p = Patch(out)
	return nil
}

func strictJSON(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return ErrInvalid
	}
	return nil
}

type Provenance struct {
	Mode    string `json:"mode"`
	Source  string `json:"source"`
	Version string `json:"version"`
}

type Snapshot struct {
	SchemaVersion int        `json:"schema_version"`
	Revision      uint64     `json:"revision"`
	Policy        Policy     `json:"policy"`
	Migration     Provenance `json:"migration"`
}

func clone(s Snapshot) Snapshot {
	s.Policy.ToolProviders = maps.Clone(s.Policy.ToolProviders)
	return s
}

func validPin(k, v string) bool {
	return (contract.Replaceable(k) || contract.ReplaceableSkill(k)) && contract.ValidAddonID(v)
}

func validate(p Policy) error {
	for _, s := range []string{p.Ceiling, p.Default, p.Ports, p.ExtraRegistries, p.AllowedHosts} {
		if len(s) > 8192 {
			return ErrInvalid
		}
	}
	if !config.ValidEgressPreset(p.Ceiling) || !config.ValidEgressPreset(p.Default) {
		return ErrInvalid
	}
	if _, err := config.ParseEgressPorts(p.Ports); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if _, err := config.ParseEgressHosts(p.ExtraRegistries); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if _, err := config.ParseEgressAllowedHosts(p.AllowedHosts, p.AllowPrivate); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if len(p.ToolProviders) > 128 {
		return ErrInvalid
	}
	for k, v := range p.ToolProviders {
		if len(k) > 128 || len(v) > 128 || !validPin(k, v) {
			return fmt.Errorf("%w: provider pin %q", ErrInvalid, k)
		}
	}
	return nil
}

func apply(s Snapshot, p Patch) (Snapshot, error) {
	s = clone(s)
	for _, f := range []struct {
		to   *string
		from *string
	}{{&s.Policy.Ceiling, p.Ceiling}, {&s.Policy.Default, p.Default}, {&s.Policy.Ports, p.Ports}, {&s.Policy.ExtraRegistries, p.ExtraRegistries}, {&s.Policy.AllowedHosts, p.AllowedHosts}} {
		if f.from != nil {
			*f.to = *f.from
		}
	}
	if p.AllowPrivate != nil {
		s.Policy.AllowPrivate = *p.AllowPrivate
	}
	if p.AutoUpgradeAddons != nil {
		s.Policy.AutoUpgradeAddons = *p.AutoUpgradeAddons
	}
	if s.Policy.ToolProviders == nil {
		s.Policy.ToolProviders = map[string]string{}
	}
	for k, v := range p.ToolProviders {
		if v == nil {
			delete(s.Policy.ToolProviders, k)
		} else {
			s.Policy.ToolProviders[k] = *v
		}
	}
	return s, validate(s.Policy)
}

type receipt struct {
	Body     string   `json:"body"`
	Snapshot Snapshot `json:"snapshot"`
}
type document struct {
	Snapshot Snapshot           `json:"snapshot"`
	Receipts map[string]receipt `json:"receipts"`
}

// The pure reducer is the transaction contract. The shell executes Persist
// and feeds its result back before exposing a Reply. Rule IDs are stable.
type state interface{ policyState() }
type ready struct{ doc document }
type pending struct{ before, next document }
type unavailable struct{}

func (ready) policyState()       {}
func (pending) policyState()     {}
func (unavailable) policyState() {}

type event interface{ policyEvent() }
type update struct {
	Patch    Patch
	Revision uint64
	ID       string
}
type persisted struct{}
type writeFailed struct{}
type durabilityUnknown struct{}

func (update) policyEvent()            {}
func (persisted) policyEvent()         {}
func (writeFailed) policyEvent()       {}
func (durabilityUnknown) policyEvent() {}

type transition struct {
	State   state
	Persist *document
	Reply   Snapshot
	Err     error
	Rule    string
}

func reduce(s state, e event) transition {
	r := transition{State: s, Rule: "R0-ignored"}
	switch v := s.(type) {
	case unavailable:
		r.Err = ErrUnavailable
		r.Rule = "R9"
	case ready:
		switch u := e.(type) {
		case update:
			b, _ := json.Marshal(struct {
				Patch    Patch
				Revision uint64
			}{u.Patch, u.Revision})
			h := sha256.Sum256(b)
			body := hex.EncodeToString(h[:])
			if old, ok := v.doc.Receipts[u.ID]; ok {
				if old.Body != body {
					r.Err = ErrOperationMismatch
					r.Rule = "R3"
				} else {
					r.Reply = clone(old.Snapshot)
					r.Rule = "R2"
				}
				return r
			}
			r.Rule = "R5"
			if !validOperationID(u.ID) {
				r.Err = ErrInvalid
				return r
			}
			if u.Revision != v.doc.Snapshot.Revision {
				r.Err = ErrConflict
				r.Rule = "R4"
				return r
			}
			next, err := apply(v.doc.Snapshot, u.Patch)
			if err != nil {
				r.Err = err
				return r
			}
			if next.Revision == ^uint64(0) {
				r.Err = ErrInvalid
				return r
			}
			next.Revision++
			d := document{Snapshot: next, Receipts: maps.Clone(v.doc.Receipts)}
			if d.Receipts == nil {
				d.Receipts = map[string]receipt{}
			}
			d.Receipts[u.ID] = receipt{Body: body, Snapshot: clone(next)}
			for len(d.Receipts) > MaxReceipts {
				oldestID := u.ID
				oldestRevision := next.Revision
				for id, receipt := range d.Receipts {
					if receipt.Snapshot.Revision < oldestRevision {
						oldestID = id
						oldestRevision = receipt.Snapshot.Revision
					}
				}
				delete(d.Receipts, oldestID)
			}
			r.State = pending{before: v.doc, next: d}
			r.Persist = &d
			r.Rule = "R1"
		case persisted, writeFailed, durabilityUnknown: // No outstanding effect.
		}
	case pending:
		switch e.(type) {
		case persisted:
			r.State = ready{v.next}
			r.Reply = clone(v.next.Snapshot)
			r.Rule = "R6"
		case writeFailed:
			r.State = ready{v.before}
			r.Err = ErrUnavailable
			r.Rule = "R7"
		case durabilityUnknown:
			r.State = unavailable{}
			r.Err = ErrUncertain
			r.Rule = "R8"
		case update:
			r.Err = ErrUnavailable
			r.Rule = "R0-serialized"
		}
	}
	return r
}
