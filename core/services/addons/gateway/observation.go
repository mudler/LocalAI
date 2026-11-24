// SPDX-License-Identifier: MIT
package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"slices"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ExecutionStatusBinding contains opaque authenticated identities, not credentials.
// Every component is a precondition; readiness never authorizes execution.
type ExecutionStatusBinding struct {
	ConversationID string         `json:"conversationId,omitempty"`
	Actor          contract.Actor `json:"actor"`
	AddonID        string         `json:"addonId"`
	Generation     uint64         `json:"generation"`
	Incarnation    string         `json:"incarnation"`
	ExecutionTool  string         `json:"executionTool"`
	StatusTool     string         `json:"statusTool"`
	// CurrentSelection is a digest of current selection inputs, not mutation history.
	// It conveys no authority and contains no raw store or configuration data.
	CurrentSelection string `json:"currentSelection"`
}

// Version belongs to this host-to-host DTO, not the addon protocol (still v1).
type ExecutionStatusRequest struct {
	Version       int                    `json:"version"`
	Binding       ExecutionStatusBinding `json:"binding"`
	ObservationID string                 `json:"observationId"`
}
type ExecutionStatusResponse struct {
	Version       int                      `json:"version"`
	Binding       ExecutionStatusBinding   `json:"binding"`
	ObservationID string                   `json:"observationId"`
	Status        contract.ExecutionStatus `json:"status"`
}

const executionStatusAction = "execution-status-read-v1"

type observationRuntime interface {
	AdmitObservation(uint64, string) (func(), error)
	ReadObservation(context.Context, contract.Envelope, string) (contract.ExecutionStatus, error)
}

func observationUnknown(r ExecutionStatusRequest) ExecutionStatusResponse {
	return ExecutionStatusResponse{Version: 1, Binding: r.Binding, ObservationID: r.ObservationID, Status: contract.ExecutionStatus{Readiness: "unknown", Isolation: "unknown"}}
}
func (r ExecutionStatusRequest) valid(a contract.Actor) bool {
	b := r.Binding
	return (b.ConversationID == "" || validExecutionConversation(b.ConversationID)) && r.Version == 1 && b.Actor == a && a.UserID != "" && a.CredentialID != "" && len(a.UserID) <= 256 && len(a.CredentialID) <= 256 && (a.Mode == "named-user" || a.Mode == "local-owner") && contract.ValidName(b.AddonID) && b.Generation != 0 && b.Incarnation != "" && len(b.Incarnation) <= 256 && contract.ValidName(b.ExecutionTool) && contract.ValidName(b.StatusTool) && b.CurrentSelection != "" && len(b.CurrentSelection) <= 256 && r.ObservationID != "" && len(r.ObservationID) <= 256
}

// ExecutionStatusRead is a host-only observation door. It cannot name arguments,
// resources, operation IDs or grants. Ordinary model dispatch remains unchanged.
func (g *Gateway) ExecutionStatusRead(ctx context.Context, a contract.Actor, r ExecutionStatusRequest) (ExecutionStatusResponse, error) {
	out := observationUnknown(r)
	if ctx.Err() != nil {
		return out, ErrUnavailable
	}
	if !r.valid(a) {
		return out, ErrForbidden
	}
	if e := g.revalidate(ctx, a); e != nil {
		return out, e
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if g.Forward != nil {
		return g.Forward.executionStatus(ctx, a, r)
	}
	return g.executionStatusLocal(ctx, a, r)
}
func (g *Gateway) executionStatusLocal(ctx context.Context, a contract.Actor, r ExecutionStatusRequest) (ExecutionStatusResponse, error) {
	out := observationUnknown(r)
	if !r.valid(a) || g.AuthorizeExecutionStatus == nil {
		return out, ErrForbidden
	}
	aa, ok := g.Admission.(AddonAdmission)
	if !ok {
		return out, ErrForbidden
	}
	b := r.Binding
	check := func(ctx context.Context) error {
		if e := g.revalidate(ctx, a); e != nil {
			return e
		}
		// Host selection/feature/builtin authority is independent of reported facts.
		if e := ClassifyCheck(g.AuthorizeExecutionStatus(ctx, a, b)); e != nil {
			return e
		}
		src, ok := g.toolSource().(CapabilitySource)
		if !ok {
			return ErrForbidden
		}
		caps, e := src.DeclaredCapabilities(ctx, b.AddonID, b.Generation)
		if e != nil {
			return unreachableOrDenied(e)
		}
		p := caps.ExecutionProvider
		if p == nil || p.StatusTool != b.StatusTool || !slices.Contains(p.Tools, b.ExecutionTool) || !slices.Contains(caps.Tools, b.ExecutionTool) || !slices.Contains(caps.Tools, b.StatusTool) || slices.Contains(caps.AdminTools, b.StatusTool) {
			return ErrForbidden
		}
		action, status := false, false
		for _, d := range caps.ToolDefinitions {
			if d.Name == b.ExecutionTool {
				action = contract.IsExecutionAction(caps, d)
			}
			if d.Name == b.StatusTool {
				status = contract.IsExecutionStatusTool(caps, d) && contract.ExecutionStatusAcceptsEmptyArguments(d)
			}
		}
		if !action || !status {
			return ErrForbidden
		}
		return nil
	}
	err := aa.AdmitAddon(ctx, a, b.AddonID, func(ctx context.Context) error {
		if e := check(ctx); e != nil {
			return e
		}
		instance, e := g.current(ctx, b.AddonID)
		if e != nil {
			return e
		}
		if instance.Generation != b.Generation || instance.Incarnation != b.Incarnation {
			return ErrForbidden
		}
		client, ok := instance.Client.(observationRuntime)
		if !ok {
			return ErrUnavailable
		}
		// Identity and accepting state are tested together under the actual client's
		// withdrawal lock. Never look up another client to dispatch after this point.
		release, e := client.AdmitObservation(b.Generation, b.Incarnation)
		if e != nil {
			return e
		}
		defer release()
		var raw [24]byte
		if _, e = rand.Read(raw[:]); e != nil {
			return ErrUnavailable
		}
		args, _ := json.Marshal(map[string]string{"tool": b.StatusTool})
		env := contract.Envelope{Request: contract.Request{AddonID: b.AddonID, Generation: b.Generation, Action: "read", OperationID: "obs-" + hex.EncodeToString(raw[:]), Arguments: args}, Authority: contract.Authority{Actor: a, Scope: "read", ExpiresAt: time.Now().Add(5 * time.Second)}}
		value, e := client.ReadObservation(ctx, env, r.ObservationID)
		if e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		if e = check(ctx); e != nil {
			return e
		}
		now, e := g.current(ctx, b.AddonID)
		if e != nil {
			return e
		}
		if now.Generation != b.Generation || now.Incarnation != b.Incarnation || now.Client != instance.Client {
			return ErrUnavailable
		}
		done, e := client.AdmitObservation(b.Generation, b.Incarnation)
		if e != nil {
			return e
		}
		done()
		rawStatus, e := json.Marshal(value)
		if e != nil {
			return ErrUnavailable
		}
		value, e = contract.DecodeExecutionStatus(rawStatus)
		if e != nil {
			return ErrUnavailable
		}
		out.Status = value
		return nil
	})
	if err != nil {
		return observationUnknown(r), ClassifyCheck(err)
	}
	return out, nil
}
func (c *Carrier) executionStatus(ctx context.Context, a contract.Actor, r ExecutionStatusRequest) (ExecutionStatusResponse, error) {
	out := observationUnknown(r)
	caps, err := c.Capabilities(ctx)
	if err != nil || !caps.Supports(executionStatusAction) {
		return out, ErrUnavailable
	}
	var id [24]byte
	if _, e := rand.Read(id[:]); e != nil {
		return out, ErrUnavailable
	}
	req := contract.Request{OperationID: "status-" + hex.EncodeToString(id[:]), Action: executionStatusAction}
	resp, e := c.send(ctx, a, wireRequest{Request: req, Observation: &r})
	if e != nil {
		return out, e
	}
	// Read-only cleanup: close errors cannot change the decoded result.
	defer func() { _ = resp.Body.Close() }()
	var got ExecutionStatusResponse
	if decode(io.LimitReader(resp.Body, 32768), &got) != nil || got.Version != 1 || got.Binding != r.Binding || got.ObservationID != r.ObservationID || ctx.Err() != nil {
		return out, ErrUnavailable
	}
	raw, e := json.Marshal(got.Status)
	if e != nil {
		return out, ErrUnavailable
	}
	got.Status, e = contract.DecodeExecutionStatus(raw)
	if e != nil {
		return out, ErrUnavailable
	}
	return got, nil
}

// Observation DTOs use exact keys even though older carrier messages permit
// encoding/json's case-insensitive aliases. Reject ambiguous nested identities.
func observationObject(raw []byte, keys ...string) error {
	if checkObject(raw) != nil {
		return ErrForbidden
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return ErrForbidden
	}
	for k := range fields {
		if !slices.Contains(keys, k) {
			return ErrForbidden
		}
	}
	return nil
}
func (b *ExecutionStatusBinding) UnmarshalJSON(raw []byte) error {
	if observationObject(raw, "conversationId", "actor", "addonId", "generation", "incarnation", "executionTool", "statusTool", "currentSelection") != nil {
		return ErrForbidden
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if observationObject(fields["actor"], "userId", "credentialId", "mode") != nil {
		return ErrForbidden
	}
	type plain ExecutionStatusBinding
	var value plain
	if decode(bytes.NewReader(raw), &value) != nil {
		return ErrForbidden
	}
	*b = ExecutionStatusBinding(value)
	return nil
}
func (r *ExecutionStatusRequest) UnmarshalJSON(raw []byte) error {
	if observationObject(raw, "version", "binding", "observationId") != nil {
		return ErrForbidden
	}
	type plain ExecutionStatusRequest
	var value plain
	if decode(bytes.NewReader(raw), &value) != nil {
		return ErrForbidden
	}
	*r = ExecutionStatusRequest(value)
	return nil
}
func (r *ExecutionStatusResponse) UnmarshalJSON(raw []byte) error {
	if observationObject(raw, "version", "binding", "observationId", "status") != nil {
		return ErrForbidden
	}
	type plain ExecutionStatusResponse
	var value plain
	if decode(bytes.NewReader(raw), &value) != nil {
		return ErrForbidden
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	status, e := contract.DecodeExecutionStatus(fields["status"])
	if e != nil {
		return e
	}
	*r = ExecutionStatusResponse(value)
	r.Status = status
	return nil
}
