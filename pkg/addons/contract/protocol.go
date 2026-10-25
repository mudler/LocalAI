// SPDX-License-Identifier: MIT
package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"strings"
	"time"
)

// ErrRefused is the add-on's own refusal of a request it answered as
// invalid: nothing was changed and repeating the same call cannot succeed.
// It is not an outage and not an unknown outcome.
var ErrRefused = errors.New("addon refused the request as invalid")

type Actor struct {
	UserID       string `json:"userId"`
	CredentialID string `json:"credentialId"`
	Mode         string `json:"mode"`
}
type Request struct {
	AddonID     string          `json:"addonId"`
	ResourceID  string          `json:"resourceId,omitempty"`
	OperationID string          `json:"operationId"`
	Generation  uint64          `json:"generation"`
	Action      string          `json:"action"`
	Arguments   json.RawMessage `json:"arguments" swaggertype:"object"`
}
type Authority struct {
	Actor     Actor     `json:"actor"`
	Scope     string    `json:"scope"`
	ExpiresAt time.Time `json:"expiresAt"`
}
type Envelope struct {
	Request   Request   `json:"request"`
	Authority Authority `json:"authority"`
}
type OutputDescription struct {
	Handle    string `json:"handle"`
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	Size      int64  `json:"size"`
}
type Result struct {
	Payload json.RawMessage     `json:"payload,omitempty" swaggertype:"object"`
	Outputs []OutputDescription `json:"outputs,omitempty"`
}
type Revision struct {
	Schema uint64 `json:"schema"`
	Data   uint64 `json:"data"`
}
type Preview struct {
	OperationID string          `json:"operationId"`
	Revision    Revision        `json:"revision"`
	ExpiresAt   time.Time       `json:"expiresAt"`
	Summary     json.RawMessage `json:"summary" swaggertype:"object"`
}
type ReceiptState string

const (
	ReceiptPending   ReceiptState = "pending"
	ReceiptCommitted ReceiptState = "committed"
	ReceiptRejected  ReceiptState = "rejected"
	ReceiptUnknown   ReceiptState = "unknown"
)

type Receipt struct {
	OperationID string       `json:"operationId"`
	State       ReceiptState `json:"state"`
	Revision    Revision     `json:"revision"`
	Result      Result       `json:"result"`
}
type Runtime interface {
	Read(context.Context, Envelope) (Result, error)
	Preview(context.Context, Envelope) (Preview, error)
	Apply(context.Context, Envelope, Revision) (Receipt, error)
	Receipt(context.Context, string) (Receipt, error)
	OpenOutput(context.Context, Envelope, string) (io.ReadCloser, error)
}

// UnmarshalJSON applies fixed default ingress bounds and structural validation.
// Hosts with overrides decode using their transport's bounded decoder and call
// Validate with their configured limits before admission.
func (v *Envelope) UnmarshalJSON(b []byte) error {
	type wire Envelope
	var w wire
	if err := strictJSON(b, &w, DefaultLimits().EnvelopeBytes); err != nil {
		return err
	}
	candidate := Envelope(w)
	if err := candidate.Validate(time.Time{}, DefaultLimits()); err != nil {
		return err
	}
	*v = candidate
	return nil
}
func (v Envelope) Validate(now time.Time, l Limits) error {
	if err := l.Validate(); err != nil {
		return err
	}
	r, a := v.Request, v.Authority
	if !namespace.MatchString(r.AddonID) || !namespace.MatchString(r.OperationID) || !namespace.MatchString(r.Action) || (r.ResourceID != "" && !namespace.MatchString(r.ResourceID)) || r.Generation == 0 {
		return fmt.Errorf("invalid request binding")
	}
	// Principal identifiers are opaque authentication-system IDs, not filesystem paths.
	if strings.TrimSpace(a.Actor.UserID) == "" || strings.TrimSpace(a.Actor.CredentialID) == "" || len(a.Actor.UserID) > 256 || len(a.Actor.CredentialID) > 256 || (a.Actor.Mode != "named-user" && a.Actor.Mode != "local-owner") || !a.ExpiresAt.After(now) {
		return fmt.Errorf("incomplete or expired authority")
	}
	switch a.Scope {
	case "read", "preview", "apply", "output":
	default:
		return fmt.Errorf("invalid authority scope")
	}
	var args map[string]json.RawMessage
	if err := strictJSON(r.Arguments, &args, l.PayloadBytes); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if int64(len(b)) > l.EnvelopeBytes {
		return fmt.Errorf("oversized envelope")
	}
	return nil
}
func (r Result) Validate(l Limits) error {
	if err := l.Validate(); err != nil {
		return err
	}
	if len(r.Payload) > 0 && (!json.Valid(r.Payload) || int64(len(r.Payload)) > l.PayloadBytes) {
		return fmt.Errorf("invalid result payload")
	}
	if int64(len(r.Outputs)) > l.ArtifactCount {
		return fmt.Errorf("too many outputs")
	}
	seen := map[string]bool{}
	var total int64
	for _, o := range r.Outputs {
		if !namespace.MatchString(o.Handle) || seen[o.Handle] || !relativePath(o.Name) || strings.Contains(o.Name, "/") || o.Size < 0 || o.Size > l.ArtifactBytes {
			return fmt.Errorf("invalid output")
		}
		seen[o.Handle] = true
		if _, _, err := mime.ParseMediaType(o.MediaType); err != nil || !strings.Contains(o.MediaType, "/") {
			return fmt.Errorf("invalid media type")
		}
		if o.Size > l.ArtifactQuotaBytes-total {
			return fmt.Errorf("output quota exceeded")
		}
		total += o.Size
	}
	return nil
}
func (p Preview) Validate(now time.Time, l Limits) error {
	if err := l.Validate(); err != nil {
		return err
	}
	if !namespace.MatchString(p.OperationID) || p.Revision.Schema == 0 || !p.ExpiresAt.After(now) {
		return fmt.Errorf("invalid preview")
	}
	if !json.Valid(p.Summary) || int64(len(p.Summary)) > l.PayloadBytes {
		return fmt.Errorf("invalid preview summary")
	}
	return nil
}
func (r Receipt) Validate(l Limits) error {
	if !namespace.MatchString(r.OperationID) {
		return fmt.Errorf("invalid receipt operation ID")
	}
	switch r.State {
	case ReceiptPending, ReceiptCommitted, ReceiptRejected, ReceiptUnknown:
	default:
		return fmt.Errorf("invalid receipt state")
	}
	if r.State == ReceiptCommitted && r.Revision.Schema == 0 {
		return fmt.Errorf("committed receipt requires revision")
	}
	return r.Result.Validate(l)
}

func (v *Result) UnmarshalJSON(b []byte) error {
	type wire Result
	var w wire
	if err := strictJSON(b, &w, DefaultLimits().EnvelopeBytes); err != nil {
		return err
	}
	candidate := Result(w)
	if err := candidate.Validate(DefaultLimits()); err != nil {
		return err
	}
	*v = candidate
	return nil
}
func (v *Preview) UnmarshalJSON(b []byte) error {
	type wire Preview
	var w wire
	if err := strictJSON(b, &w, DefaultLimits().EnvelopeBytes); err != nil {
		return err
	}
	candidate := Preview(w)
	if err := candidate.Validate(time.Time{}, DefaultLimits()); err != nil {
		return err
	}
	*v = candidate
	return nil
}
func (v *Receipt) UnmarshalJSON(b []byte) error {
	type wire Receipt
	var w wire
	if err := strictJSON(b, &w, DefaultLimits().EnvelopeBytes); err != nil {
		return err
	}
	candidate := Receipt(w)
	if err := candidate.Validate(DefaultLimits()); err != nil {
		return err
	}
	*v = candidate
	return nil
}
