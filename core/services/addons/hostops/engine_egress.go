// SPDX-License-Identifier: MIT
package hostops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The network operations of the Sandbox. The add-on owns the proxy of its
// sandboxes and dials; the host decides. egress.check asks whether a command
// of the live call may reach one host; egress.audit reports what the allowed
// connections moved. The chat is the resource of the live envelope and the
// user is its actor: neither is an argument.

// EgressCheckTimeout bounds egress.check, which may wait for a person's answer
// to a card (60 seconds) before it answers.
const EgressCheckTimeout = 75 * time.Second

// MaxEgressEvents bounds one egress.audit call.
const MaxEgressEvents = 500

// EgressCall says whose call asks: the actor and the resource of the live
// envelope, and its operation.
type EgressCall struct {
	Actor                   contract.Actor
	ResourceID, OperationID string
}

// EgressCheck are the arguments of egress.check.
type EgressCheck struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Kind    string `json:"kind"`
	TLS     bool   `json:"tls"`
	Private bool   `json:"private,omitempty"`
}

// EgressVerdict is the answer of egress.check.
type EgressVerdict struct {
	Action       string `json:"action"`
	Reason       string `json:"reason"`
	AllowPrivate bool   `json:"allowPrivate,omitempty"`
	Card         string `json:"card,omitempty"`
	// Self lists the addresses of the server as CIDR strings (see
	// safehttp.FormatDenyList): its interfaces, loopback and the addresses of
	// the external base URL host. The add-on refuses them on every dial, as
	// web_fetch does, since it cannot read the server's public address itself.
	// It is set on an allow.
	Self []string `json:"self,omitempty"`
}

// EgressEvent is one line of egress.audit.
type EgressEvent struct {
	Host       string    `json:"host"`
	Port       int       `json:"port"`
	Kind       string    `json:"kind"`
	Verdict    string    `json:"verdict"`
	Reason     string    `json:"reason"`
	Addr       string    `json:"addr,omitempty"`
	BytesUp    int64     `json:"bytesUp,omitempty"`
	BytesDown  int64     `json:"bytesDown,omitempty"`
	DurationMS int64     `json:"durationMs,omitempty"`
	At         time.Time `json:"at"`
}

// EgressHost is the host side of the network operations: the chats service.
type EgressHost interface {
	// Check decides one connection and audits the verdict.
	Check(ctx context.Context, c EgressCall, a EgressCheck) (EgressVerdict, error)
	// Audit records the lines of the call.
	Audit(ctx context.Context, c EgressCall, evs []EgressEvent) error
}

// egressRefused are argument names that would let the add-on pick the chat,
// the user or the policy. The broker refuses identity names already; these
// are refused here too.
var egressRefused = []string{"chat", "user", "preset", "allow"}

func egressArgs(raw json.RawMessage, out any) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return fmt.Errorf("%w: arguments must be an object", ErrBadRequest)
	}
	for k := range obj {
		for _, r := range egressRefused {
			if strings.EqualFold(k, r) {
				return fmt.Errorf("%w: argument %s is not accepted", ErrBadRequest, r)
			}
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%w: arguments", ErrBadRequest)
	}
	return nil
}

func egressCall(c Call) (EgressCall, error) {
	if c.ResourceID == "" {
		return EgressCall{}, fmt.Errorf("%w: the live request has no chat", ErrBadRequest)
	}
	return EgressCall{Actor: c.Actor, ResourceID: c.ResourceID, OperationID: c.OperationID}, nil
}

func validKind(k string) bool { return k == "connect" || k == "http" }

// RegisterEgress registers egress.check and egress.audit.
func RegisterEgress(b *Broker, h EgressHost) error {
	if h == nil {
		return nil
	}
	if err := b.Register(Spec{Op: "egress.check", Scope: "act", Feature: featureChats, Timeout: EgressCheckTimeout, Handler: func(ctx context.Context, c Call) (json.RawMessage, error) {
		ec, err := egressCall(c)
		if err != nil {
			return nil, err
		}
		var a EgressCheck
		if err := egressArgs(c.Arguments, &a); err != nil {
			return nil, err
		}
		if a.Host == "" || len(a.Host) > 253 || a.Port < 1 || a.Port > 65535 || !validKind(a.Kind) {
			return nil, fmt.Errorf("%w: host, port and kind", ErrBadRequest)
		}
		v, err := h.Check(ctx, ec, a)
		if err != nil {
			return nil, err
		}
		return json.Marshal(v)
	}}); err != nil {
		return err
	}
	return b.Register(Spec{Op: "egress.audit", Scope: "act", Feature: featureChats, Handler: func(ctx context.Context, c Call) (json.RawMessage, error) {
		ec, err := egressCall(c)
		if err != nil {
			return nil, err
		}
		var a struct {
			Events []EgressEvent `json:"events"`
		}
		if err := egressArgs(c.Arguments, &a); err != nil {
			return nil, err
		}
		if len(a.Events) > MaxEgressEvents {
			return nil, fmt.Errorf("%w: at most %d events", ErrBadRequest, MaxEgressEvents)
		}
		for _, e := range a.Events {
			if len(e.Host) > 253 || e.Port < 0 || e.Port > 65535 || (e.Verdict != "done" && e.Verdict != "block") || len(e.Reason) > 32 || len(e.Addr) > 64 {
				return nil, fmt.Errorf("%w: an event", ErrBadRequest)
			}
		}
		if err := h.Audit(ctx, ec, a.Events); err != nil {
			return nil, err
		}
		return json.RawMessage(`{}`), nil
	}})
}
