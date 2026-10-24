// SPDX-License-Identifier: MIT

// Package migration classifies legacy data offline. Imported identities are
// metadata, not authorization. No bundle can construct or execute a runtime.
//
// PrivateBackupSink is a trusted host boundary: successful Put must mean durable,
// private, create-only storage. This package does not implement encryption, ACLs,
// retention or filesystem security. EncodePrivateBundle intentionally reveals
// credentials and must only target that private boundary, never a public report.
package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

type SourceFormat string

const (
	LocalAIDefinition     SourceFormat = "localai-definition"
	LocalAGIDefinition    SourceFormat = "localagi-definition"
	LocalAGIPool          SourceFormat = "localagi-pool"
	LocalAIRecordSnapshot SourceFormat = "localai-record-snapshot"
	InstallationOwner                  = "installation-owner"
	Version                            = "localai-agent-migration/v1"
	LocalAGIPin                        = "v0.0.0-20261016084905-7aa0c8fea076"
)

type Source struct {
	Format    SourceFormat      `json:"format"`
	Data      []byte            `json:"data"`
	Principal string            `json:"principal"`
	Sidecars  map[string][]byte `json:"sidecars"`
}
type Finding struct {
	Definition  string `json:"definition"`
	Path        string `json:"path"`
	Code        string `json:"code"`
	Disposition string `json:"disposition"`
	Remediation string `json:"remediation"`
}
type Report struct {
	Version     string    `json:"version"`
	Definitions int       `json:"definitions"`
	Mapped      int       `json:"mapped"`
	Unsupported int       `json:"unsupported"`
	Invalid     int       `json:"invalid"`
	Findings    []Finding `json:"findings"`
}
type Plan struct {
	wire    wireBundle
	invalid bool
	backup  *backupState
}
type backupState struct {
	sync.Mutex
	owner    *Plan
	digest   string
	complete bool
}
type definition struct {
	Executable  bool                       `json:"executable"`
	Principal   string                     `json:"principal"`
	Name        string                     `json:"name"`
	Normalized  map[string]string          `json:"normalized"`
	Presence    map[string]json.RawMessage `json:"presence"`
	Unsupported []string                   `json:"unsupported"`
	Metadata    map[string]json.RawMessage `json:"metadata"`
}
type wireBundle struct {
	Version     string       `json:"version"`
	Digest      string       `json:"digest"`
	LocalAIPin  string       `json:"localai_pin"`
	LocalAGIPin string       `json:"localagi_pin"`
	NibPin      string       `json:"nib_pin"`
	Source      Source       `json:"source"`
	Definitions []definition `json:"definitions"`
}
type PrivateBundle struct{ wire wireBundle }
type backupArchive struct {
	Version string `json:"version"`
	Digest  string `json:"digest"`
	Source  Source `json:"source"`
}

// BackupReceipt holds only an opaque reference, not imported identity or data.
type BackupReceipt struct{ reference string }

func (r BackupReceipt) Reference() string { return r.reference }

// Put must be create-only: identical bytes are idempotent; different bytes at
// the same opaque key must fail. The implementation must not mutate bytes.
type PrivateBackupSink interface {
	Put(context.Context, string, []byte) error
}

var (
	errSource   = errors.New("invalid migration source")
	errPlan     = errors.New("invalid migration plan")
	errBackup   = errors.New("private migration backup failed")
	errRequired = errors.New("private migration backup required")
	errBundle   = errors.New("invalid private migration bundle")
)

func (Plan) String() string               { return "migration.Plan(private)" }
func (p Plan) GoString() string           { return p.String() }
func (Plan) MarshalJSON() ([]byte, error) { return []byte(`{"private_migration_plan":true}`), nil }
func (PrivateBundle) String() string      { return "migration.PrivateBundle(private, non-executable)" }
func (b PrivateBundle) GoString() string  { return b.String() }
func (PrivateBundle) MarshalJSON() ([]byte, error) {
	return []byte(`{"private_migration_bundle":true,"executable":false}`), nil
}

func cloneWire(w wireBundle) wireBundle {
	data, _ := json.Marshal(w)
	var out wireBundle
	_ = json.Unmarshal(data, &out)
	return out
}
func sourceDigest(s Source) string {
	data, _ := json.Marshal(struct {
		Version string
		Source  Source
	}{Version, s})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func newPlan(s Source) *Plan {
	w := cloneWire(wireBundle{Version: Version, Source: s, LocalAIPin: "789ab14a4955eba98efcd681a217a69c16a17379", LocalAGIPin: LocalAGIPin, NibPin: "v0.12.1"})
	w.Digest = sourceDigest(w.Source)
	p := &Plan{wire: w, backup: &backupState{}}
	p.backup.owner = p
	return p
}

// Analyze never guesses a source format and never executes legacy decoders.
func Analyze(s Source) (*Plan, Report, error) {
	r := Report{Version: Version, Findings: []Finding{}}
	switch s.Format {
	case LocalAIDefinition, LocalAGIDefinition, LocalAGIPool, LocalAIRecordSnapshot:
	default:
		return nil, r, errSource
	}
	value, e := strictJSON(s.Data)
	if e != nil {
		return nil, r, errSource
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, r, errSource
	}
	p := newPlan(s)
	c := classifier{report: &r, legacy: s.Format == LocalAGIDefinition || s.Format == LocalAGIPool}
	identities := map[string]bool{}
	add := func(config any, principal, key string, metadata map[string]any) {
		ref := fmt.Sprintf("definition-%06d", len(p.wire.Definitions)+1)
		d := definition{Principal: principal, Normalized: map[string]string{}, Presence: map[string]json.RawMessage{}, Metadata: map[string]json.RawMessage{}, Unsupported: []string{}}
		c.ref = ref
		c.def = &d
		m, valid := config.(map[string]any)
		if !valid {
			c.find("config", "config", "invalid")
			m = map[string]any{}
		}
		name, nameOK := m["name"].(string)
		if !nameOK || strings.TrimSpace(name) == "" {
			c.find("name", "name", "invalid")
		}
		d.Name = name
		if strings.TrimSpace(principal) == "" || strings.Contains(principal, ":") {
			c.find("identity", "identity", "invalid")
		}
		if key != "" && name != key {
			c.find("identity", "identity", "invalid")
		}
		// Pool names retain their original namespaced spelling in Normalized and
		// Presence, but identity metadata splits the logical principal and name.
		if s.Format == LocalAGIPool {
			parts := strings.Split(name, ":")
			if len(parts) == 2 {
				d.Name = parts[1]
			}
		}
		identity := principal + "\x00" + d.Name
		if identities[identity] {
			c.find("identity", "identity", "invalid")
		}
		identities[identity] = true
		for _, k := range keys(m) {
			c.field(k, m[k])
		}
		for i, k := range keys(metadata) {
			v := metadata[k]
			d.Metadata[k] = raw(v)
			path := k
			if !recordFields[k] {
				path = fmt.Sprintf("unknown[%d]", i+1)
			}
			disposition := "unsupported"
			if recordFields[k] && k != "last_run_at" {
				if _, ok := v.(string); !ok {
					disposition = "invalid"
				}
			}
			if k == "last_run_at" && v != nil {
				if _, ok := v.(string); !ok {
					disposition = "invalid"
				}
			}
			if k == "status" && v != "active" && v != "paused" && v != "deleted" {
				disposition = "invalid"
			}
			if k == "created_at" || k == "updated_at" || (k == "last_run_at" && v != nil) {
				str, _ := v.(string)
				if _, e := time.Parse(time.RFC3339Nano, str); e != nil {
					disposition = "invalid"
				}
			}
			c.find("record."+path, "record."+k, disposition)
			c.unknownChildren("record."+path, "record."+k, v)
		}
		sort.Strings(d.Unsupported)
		p.wire.Definitions = append(p.wire.Definitions, d)
	}
	switch s.Format {
	case LocalAGIPool:
		for _, key := range keys(obj) {
			parts := strings.Split(key, ":")
			principal := InstallationOwner
			good := true
			if len(parts) == 2 {
				principal = parts[0]
			} else if len(parts) != 1 {
				good = false
			}
			for _, part := range parts {
				if strings.TrimSpace(part) == "" {
					good = false
				}
			}
			add(obj[key], principal, key, nil)
			if !good {
				c.find("identity", "identity", "invalid")
			}
		}
	case LocalAIRecordSnapshot:
		config, exists := obj["config"]
		if !exists {
			return nil, r, errSource
		}
		if _, ok := config.(map[string]any); !ok {
			return nil, r, errSource
		}
		metadata := map[string]any{}
		for k, v := range obj {
			if k != "config" {
				metadata[k] = v
			}
		}
		add(config, s.Principal, "", metadata)
		owner, ownerOK := obj["user_id"].(string)
		name, nameOK := obj["name"].(string)
		if !ownerOK || owner != s.Principal || !nameOK || name != p.wire.Definitions[0].Name {
			c.find("identity", "identity", "invalid")
		}
	default:
		add(obj, s.Principal, "", nil)
	}
	c.ref = ""
	c.def = nil
	for i, k := range keys(s.Sidecars) {
		path := fmt.Sprintf("sidecars.unknown[%d]", i+1)
		if k == "state" || k == "character" || k == "scheduler" || k == "history" {
			path = "sidecars." + k
		}
		c.find(path, "sidecars."+k, "unsupported")
	}
	sort.SliceStable(r.Findings, func(i, j int) bool {
		a, b := r.Findings[i], r.Findings[j]
		if a.Definition != b.Definition {
			return a.Definition < b.Definition
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Disposition < b.Disposition
	})
	r.Definitions = len(p.wire.Definitions)
	p.invalid = r.Invalid != 0
	return p, r, nil
}

var recordFields = map[string]bool{"user_id": true, "name": true, "status": true, "id": true, "created_at": true, "updated_at": true, "last_run_at": true}

// Backup passes a complete versioned source archive to the trusted private
// sink. Only a successful write unlocks this particular Plan instance.
func Backup(ctx context.Context, p *Plan, sink PrivateBackupSink) (BackupReceipt, error) {
	if p == nil || p.backup == nil || p.backup.owner != p || p.wire.Digest != sourceDigest(p.wire.Source) {
		return BackupReceipt{}, errPlan
	}
	if sink == nil || ctx == nil {
		return BackupReceipt{}, errBackup
	}
	data, e := json.Marshal(backupArchive{Version: Version, Digest: p.wire.Digest, Source: p.wire.Source})
	if e != nil {
		return BackupReceipt{}, errBackup
	}
	key := "migration-" + p.wire.Digest
	if e = ctx.Err(); e != nil {
		return BackupReceipt{}, errBackup
	}
	if e = sink.Put(ctx, key, data); e != nil {
		return BackupReceipt{}, errBackup
	}
	p.backup.Lock()
	p.backup.complete = true
	p.backup.digest = p.wire.Digest
	p.backup.Unlock()
	return BackupReceipt{reference: key}, nil
}
func Convert(p *Plan) (PrivateBundle, error) {
	if p == nil || p.backup == nil || p.backup.owner != p || p.invalid {
		return PrivateBundle{}, errPlan
	}
	p.backup.Lock()
	complete := p.backup.complete && p.backup.digest == p.wire.Digest
	p.backup.Unlock()
	if !complete {
		return PrivateBundle{}, errRequired
	}
	b := PrivateBundle{wire: cloneWire(p.wire)}
	if _, e := validateBundle(b); e != nil {
		return PrivateBundle{}, errPlan
	}
	return b, nil
}
func validateBundle(b PrivateBundle) ([]byte, error) {
	w := b.wire
	if w.Version != Version || w.Digest != sourceDigest(w.Source) {
		return nil, errBundle
	}
	p, _, e := Analyze(w.Source)
	if e != nil || p.invalid {
		return nil, errBundle
	}
	expected, _ := json.Marshal(p.wire)
	actual, _ := json.Marshal(w)
	if !bytes.Equal(expected, actual) {
		return nil, errBundle
	}
	return actual, nil
}

// EncodePrivateBundle is the sole intentional secret-bearing bundle encoder.
func EncodePrivateBundle(b PrivateBundle) ([]byte, error) { return validateBundle(b) }
func DecodePrivateBundle(data []byte) (PrivateBundle, error) {
	if _, e := strictJSON(data); e != nil {
		return PrivateBundle{}, errBundle
	}
	var w wireBundle
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if e := dec.Decode(&w); e != nil {
		return PrivateBundle{}, errBundle
	}
	b := PrivateBundle{wire: w}
	if _, e := validateBundle(b); e != nil {
		return PrivateBundle{}, errBundle
	}
	return b, nil
}
func raw(v any) json.RawMessage { data, _ := json.Marshal(v); return data }
func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Token-level validation rejects duplicate keys at every depth, including
// wrappers parsed by the classifier. UseNumber avoids float64 rounding.
func strictJSON(data []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	v, e := jsonValue(d)
	if e != nil {
		return nil, errSource
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, errSource
	}
	return v, nil
}
func jsonValue(d *json.Decoder) (any, error) {
	t, e := d.Token()
	if e != nil {
		return nil, errSource
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return t, nil
	}
	switch delim {
	case '{':
		m := map[string]any{}
		for d.More() {
			token, e := d.Token()
			if e != nil {
				return nil, errSource
			}
			k, ok := token.(string)
			if !ok {
				return nil, errSource
			}
			if _, ok = m[k]; ok {
				return nil, errSource
			}
			v, e := jsonValue(d)
			if e != nil {
				return nil, e
			}
			m[k] = v
		}
		end, e := d.Token()
		if e != nil || end != json.Delim('}') {
			return nil, errSource
		}
		return m, nil
	case '[':
		a := []any{}
		for d.More() {
			v, e := jsonValue(d)
			if e != nil {
				return nil, e
			}
			a = append(a, v)
		}
		end, e := d.Token()
		if e != nil || end != json.Delim(']') {
			return nil, errSource
		}
		return a, nil
	default:
		return nil, errSource
	}
}
