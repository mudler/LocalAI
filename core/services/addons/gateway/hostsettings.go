// SPDX-License-Identifier: MIT
package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"

	"github.com/mudler/LocalAI/core/services/addons/hostsettings"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

const (
	ActionHostSettingsRead   = "host-settings-read"
	ActionHostSettingsUpdate = "host-settings-update"
)

// HostSettingsService is supplied only on the owner. A missing service never
// means that a frontend's local configuration is authoritative.
type HostSettingsService interface {
	Snapshot(context.Context) (hostsettings.Snapshot, error)
	Update(context.Context, hostsettings.Patch, uint64, string) (hostsettings.Snapshot, error)
}

// HostSettingsPolicy is the bounded policy projection shared by callers and
// trusted host consumers. Migration provenance belongs only to owner storage.
type HostSettingsPolicy struct {
	SchemaVersion int                 `json:"schema_version"`
	Revision      uint64              `json:"revision"`
	Policy        hostsettings.Policy `json:"policy"`
}

func projectHostSettings(s hostsettings.Snapshot) HostSettingsPolicy {
	return HostSettingsPolicy{SchemaVersion: s.SchemaVersion, Revision: s.Revision, Policy: s.Policy}
}

type hostSettingsEnvelope struct {
	Data  *HostSettingsPolicy `json:"data,omitempty"`
	Error string              `json:"error,omitempty"`
}

var hostSettingsFailures = map[string]error{
	"invalid":            hostsettings.ErrInvalid,
	"revision_conflict":  hostsettings.ErrConflict,
	"operation_mismatch": hostsettings.ErrOperationMismatch,
}

// DecodeHostSettingsResult preserves definite store refusals across the carrier.
// Unavailable or uncertain writes instead use the carrier's unknown-outcome path.
func DecodeHostSettingsResult(r contract.Result) (HostSettingsPolicy, error) {
	var env hostSettingsEnvelope
	if decode(bytes.NewReader(r.Payload), &env) != nil {
		return HostSettingsPolicy{}, ErrUnavailable
	}
	if env.Error != "" {
		if e, ok := hostSettingsFailures[env.Error]; ok && env.Data == nil {
			return HostSettingsPolicy{}, e
		}
		return HostSettingsPolicy{}, ErrUnavailable
	}
	if env.Data == nil || env.Data.SchemaVersion != hostsettings.SchemaVersion || env.Data.Revision == 0 {
		return HostSettingsPolicy{}, ErrUnavailable
	}
	return *env.Data, nil
}

func (g *Gateway) ReadHostSettings(ctx context.Context, actor contract.Actor) (HostSettingsPolicy, error) {
	var identity [16]byte
	if _, err := rand.Read(identity[:]); err != nil {
		return HostSettingsPolicy{}, ErrUnavailable
	}
	r, err := g.Dispatch(ctx, actor, contract.Request{OperationID: "s" + hex.EncodeToString(identity[:]), Action: ActionHostSettingsRead, Arguments: json.RawMessage(`{}`)})
	if err != nil {
		return HostSettingsPolicy{}, err
	}
	return DecodeHostSettingsResult(r)
}

// HostSettingsSnapshot is the authenticated read entry point.
func (g *Gateway) HostSettingsSnapshot(ctx context.Context, actor contract.Actor) (HostSettingsPolicy, error) {
	return g.ReadHostSettings(ctx, actor)
}

func (g *Gateway) UpdateHostSettings(ctx context.Context, actor contract.Actor, patch hostsettings.Patch, revision uint64, operationID string) (HostSettingsPolicy, error) {
	raw, err := json.Marshal(struct {
		Revision uint64             `json:"expectedRevision"`
		Patch    hostsettings.Patch `json:"patch"`
	}{revision, patch})
	if err != nil {
		return HostSettingsPolicy{}, hostsettings.ErrInvalid
	}
	r, err := g.Dispatch(ctx, actor, contract.Request{Action: ActionHostSettingsUpdate, OperationID: operationID, Arguments: raw})
	if err != nil {
		return HostSettingsPolicy{}, err
	}
	return DecodeHostSettingsResult(r)
}

// HostSettingsInternalSnapshot is for trusted in-process host consumers, not
// add-ons or public handlers. It cannot mutate or silently use a replica's store.
func (g *Gateway) HostSettingsInternalSnapshot(ctx context.Context) (HostSettingsPolicy, error) {
	if g.Forward != nil {
		return g.Forward.HostSettingsSnapshot(ctx)
	}
	if g.HostSettings == nil {
		return HostSettingsPolicy{}, ErrUnavailable
	}
	s, err := g.HostSettings.Snapshot(ctx)
	if err != nil {
		return HostSettingsPolicy{}, ErrUnavailable
	}
	return projectHostSettings(s), nil
}

// hostSettingsArguments rejects duplicate keys, aliases, missing fields, nulls,
// and trailing JSON before encoding/json can silently normalize them.
func hostSettingsArguments(raw []byte, update bool) (uint64, hostsettings.Patch, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return 0, hostsettings.Patch{}, hostsettings.ErrInvalid
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		tok, err = d.Token()
		if err != nil {
			return 0, hostsettings.Patch{}, hostsettings.ErrInvalid
		}
		key, ok := tok.(string)
		if !ok || fields[key] != nil || !update || (key != "expectedRevision" && key != "patch") {
			return 0, hostsettings.Patch{}, hostsettings.ErrInvalid
		}
		var value json.RawMessage
		if d.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return 0, hostsettings.Patch{}, hostsettings.ErrInvalid
		}
		fields[key] = value
	}
	if tok, err = d.Token(); err != nil || tok != json.Delim('}') {
		return 0, hostsettings.Patch{}, hostsettings.ErrInvalid
	}
	if _, err = d.Token(); err != io.EOF {
		return 0, hostsettings.Patch{}, hostsettings.ErrInvalid
	}
	var rev uint64
	var patch hostsettings.Patch
	if update && (len(fields) != 2 || json.Unmarshal(fields["expectedRevision"], &rev) != nil || rev == 0 || json.Unmarshal(fields["patch"], &patch) != nil) {
		return 0, patch, hostsettings.ErrInvalid
	}
	return rev, patch, nil
}

func (g *Gateway) hostSettingsAction(ctx context.Context, actor contract.Actor, r contract.Request) (contract.Result, error) {
	if r.Action == ActionHostSettingsUpdate {
		if g.HostSettingsAdmin == nil {
			return contract.Result{}, ErrForbidden
		}
		if err := ClassifyCheck(g.HostSettingsAdmin(ctx, actor)); err != nil {
			return contract.Result{}, err
		}
	}
	rev, patch, err := hostSettingsArguments(r.Arguments, r.Action == ActionHostSettingsUpdate)
	var s hostsettings.Snapshot
	if err == nil {
		if g.HostSettings == nil {
			return contract.Result{}, ErrUnavailable
		}
		if r.Action == ActionHostSettingsRead {
			s, err = g.HostSettings.Snapshot(ctx)
		} else {
			s, err = g.HostSettings.Update(ctx, patch, rev, r.OperationID)
		}
	}
	env := hostSettingsEnvelope{}
	if err != nil {
		for code, failure := range hostSettingsFailures {
			if errors.Is(err, failure) {
				env.Error = code
				break
			}
		}
		if env.Error == "" {
			return contract.Result{}, ErrUnavailable
		}
	} else {
		policy := projectHostSettings(s)
		env.Data = &policy
	}
	raw, err := json.Marshal(env)
	return contract.Result{Payload: raw}, err
}

// ReadHostSettingsInternal reads the bounded policy for trusted host consumers.
func (g *Gateway) ReadHostSettingsInternal(ctx context.Context) (HostSettingsPolicy, error) {
	return g.HostSettingsInternalSnapshot(ctx)
}
