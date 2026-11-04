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
	"reflect"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/egresspolicy"
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
	// ConnectorFilesCeiling is the largest file, in bytes, that a connector
	// moves in either direction. Zero means contract.DefaultFileCeilingBytes.
	// A document written before this key existed has none; MarshalJSON always
	// writes it, so a reader never has to guess whether it is absent.
	ConnectorFilesCeiling int64 `json:"connector_files_ceiling_bytes,omitempty"`
}

// MinFilesCeiling is the smallest host ceiling an owner can set.
const MinFilesCeiling int64 = 1024

// FilesCeiling is the host ceiling of a connector file in effect.
func (p Policy) FilesCeiling() int64 {
	if p.ConnectorFilesCeiling <= 0 {
		return contract.DefaultFileCeilingBytes
	}
	return p.ConnectorFilesCeiling
}

// MarshalJSON keeps an empty provider set canonical rather than emitting null,
// including when a legacy owner has no pins.
func (p Policy) MarshalJSON() ([]byte, error) {
	type plain Policy
	if p.ToolProviders == nil {
		p.ToolProviders = map[string]string{}
	}
	return json.Marshal(struct {
		plain
		// Shadows the omitempty field of plain: the key is always written.
		ConnectorFilesCeiling int64 `json:"connector_files_ceiling_bytes"`
	}{plain(p), p.ConnectorFilesCeiling})
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
	// ConnectorFilesCeiling changes the host ceiling of connector files; zero
	// clears it to the default.
	ConnectorFilesCeiling *int64 `json:"connector_files_ceiling_bytes,omitempty"`
	// EgressPrivateConfirm carries the typed phrase that turning on
	// chats_egress_allow_private requires. The owner checks it, so a direct
	// call cannot skip what the page asks for. It is never stored.
	EgressPrivateConfirm *string `json:"chats_egress_private_confirm,omitempty"`
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

// Check the wire shape before encoding/json can discard duplicates, accept
// case-insensitive field aliases, or turn null/missing fields into zero values.
func strictJSON(b []byte, v any) error {
	shape := json.NewDecoder(bytes.NewReader(b))
	shape.UseNumber()
	if err := jsonShape(shape, reflect.TypeOf(v).Elem()); err != nil {
		return err
	}
	if _, err := shape.Token(); err != io.EOF {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	return d.Decode(v)
}

func jsonShape(d *json.Decoder, t reflect.Type) error {
	nullable := t.Kind() == reflect.Pointer
	if nullable {
		t = t.Elem()
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	if token == nil {
		// Only pointer map values (provider deletions) may be null. Patch
		// UnmarshalJSON separately rejects null at the top level.
		if nullable || t == reflect.TypeOf(json.RawMessage{}) {
			return nil
		}
		return ErrInvalid
	}
	// RawMessage is used only to inspect a patch before decoding its fields.
	raw := t == reflect.TypeOf(json.RawMessage{})
	if delim, ok := token.(json.Delim); ok {
		switch delim {
		case '{':
			if !raw && t.Kind() != reflect.Struct && t.Kind() != reflect.Map {
				return ErrInvalid
			}
			fields := map[string]reflect.StructField{}
			if t.Kind() == reflect.Struct && !raw {
				for i := 0; i < t.NumField(); i++ {
					f := t.Field(i)
					fields[strings.Split(f.Tag.Get("json"), ",")[0]] = f
				}
			}
			seen := map[string]bool{}
			for d.More() {
				keyToken, err := d.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok || seen[key] {
					return ErrInvalid
				}
				seen[key] = true
				var child reflect.Type
				switch {
				case raw:
					child = t
				case t.Kind() == reflect.Map:
					child = t.Elem()
				default:
					f, ok := fields[key]
					if !ok {
						return ErrInvalid
					}
					child = f.Type
				}
				if err := jsonShape(d, child); err != nil {
					return err
				}
			}
			if _, err := d.Token(); err != nil {
				return err
			}
			for name, f := range fields {
				if !strings.Contains(f.Tag.Get("json"), ",omitempty") && !seen[name] {
					return ErrInvalid
				}
			}
			return nil
		case '[':
			if !raw {
				return ErrInvalid
			}
			for d.More() {
				if err := jsonShape(d, t); err != nil {
					return err
				}
			}
			_, err := d.Token()
			return err
		}
		return ErrInvalid
	}
	if raw {
		return nil
	}
	switch t.Kind() {
	case reflect.String:
		if _, ok := token.(string); ok {
			return nil
		}
	case reflect.Bool:
		if _, ok := token.(bool); ok {
			return nil
		}
	case reflect.Int, reflect.Int64, reflect.Uint64:
		if _, ok := token.(json.Number); ok {
			return nil
		}
	}
	return ErrInvalid
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
	if !egresspolicy.ValidEgressPreset(p.Ceiling) || !egresspolicy.ValidEgressPreset(p.Default) {
		return ErrInvalid
	}
	if _, err := egresspolicy.ParseEgressPorts(p.Ports); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if _, err := egresspolicy.ParseEgressHosts(p.ExtraRegistries); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if _, err := egresspolicy.ParseEgressAllowedHosts(p.AllowedHosts, p.AllowPrivate); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if c := p.ConnectorFilesCeiling; c != 0 && (c < MinFilesCeiling || c > contract.MaxDeclaredFileBytes) {
		return fmt.Errorf("%w: connector_files_ceiling_bytes must be 0 or %d to %d", ErrInvalid, MinFilesCeiling, contract.MaxDeclaredFileBytes)
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

func confirmedPrivate(phrase *string) bool {
	return phrase != nil && strings.TrimSpace(*phrase) == egresspolicy.EgressPrivatePhrase
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
		if *p.AllowPrivate && !s.Policy.AllowPrivate && !confirmedPrivate(p.EgressPrivateConfirm) {
			return Snapshot{}, fmt.Errorf("%w: chats_egress_allow_private needs chats_egress_private_confirm", ErrInvalid)
		}
		s.Policy.AllowPrivate = *p.AllowPrivate
	}
	if p.AutoUpgradeAddons != nil {
		s.Policy.AutoUpgradeAddons = *p.AutoUpgradeAddons
	}
	if p.ConnectorFilesCeiling != nil {
		s.Policy.ConnectorFilesCeiling = *p.ConnectorFilesCeiling
	}
	if s.Policy.ToolProviders == nil {
		s.Policy.ToolProviders = map[string]string{}
	}
	for k, v := range p.ToolProviders {
		if len(k) > 128 || !(contract.Replaceable(k) || contract.ReplaceableSkill(k)) {
			return Snapshot{}, fmt.Errorf("%w: provider pin %q", ErrInvalid, k)
		}
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
