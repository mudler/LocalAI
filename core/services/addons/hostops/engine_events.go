// SPDX-License-Identifier: MIT
package hostops

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// MaxEventDedupe bounds the dedupe key an add-on gives.
const MaxEventDedupe = 200

// EmitCall is one events.emit call as the host knows it. Who caused the
// change comes from the host's record of the live call (the actor, the chat
// and the credential), never from the arguments.
type EmitCall struct {
	AddonID string
	// OwnerID is the user whose data changed: the actor of the live call.
	OwnerID string
	// ChatID is the chat the live call was made for, "" for a call of the
	// add-on's page.
	ChatID string
	// CredentialID is the credential of the live call; a paired client's
	// starts with contract.ClientCredentialPrefix.
	CredentialID string
	Type         string
	DedupeKey    string
	Payload      json.RawMessage
}

// EventHost takes the events of add-ons. ErrBadRequest from it is a refusal
// the add-on can fix (an undeclared type, a payload the declaration cannot
// read); any other error is an outage.
type EventHost interface {
	Emit(ctx context.Context, c EmitCall) error
}

// RegisterEvents registers events.emit. It runs under any envelope of a live
// call, because an add-on reports a change from whichever tool made it, a page
// tool of the read class included.
func RegisterEvents(b *Broker, h EventHost) error {
	if h == nil {
		return nil
	}
	return b.Register(Spec{Op: contract.EventsEmitOp, Scope: "read", Feature: featureChats, Handler: func(ctx context.Context, c Call) (json.RawMessage, error) {
		if c.Actor.UserID == "" {
			return nil, fmt.Errorf("%w: no owner", ErrForbidden)
		}
		var a struct {
			Type      string          `json:"type"`
			Payload   json.RawMessage `json:"payload"`
			DedupeKey string          `json:"dedupe_key"`
		}
		dec := json.NewDecoder(bytes.NewReader(c.Arguments))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&a); err != nil {
			return nil, fmt.Errorf("%w: arguments", ErrBadRequest)
		}
		p := bytes.TrimSpace(a.Payload)
		switch {
		case a.Type == "" || len(a.Type) > contract.MaxEventTypeLen:
			return nil, fmt.Errorf("%w: type", ErrBadRequest)
		case len(p) == 0 || p[0] != '{' || !json.Valid(p):
			return nil, fmt.Errorf("%w: payload must be a JSON object", ErrBadRequest)
		case len(p) > contract.MaxEventPayload:
			return nil, fmt.Errorf("%w: payload is larger than %d bytes", ErrBadRequest, contract.MaxEventPayload)
		case len(a.DedupeKey) > MaxEventDedupe || strings.ContainsFunc(a.DedupeKey, func(r rune) bool { return r < 0x20 || r == 0x7f }):
			return nil, fmt.Errorf("%w: dedupe_key", ErrBadRequest)
		}
		err := h.Emit(ctx, EmitCall{AddonID: c.AddonID, OwnerID: c.Actor.UserID, ChatID: c.ChatID, CredentialID: c.Actor.CredentialID,
			Type: a.Type, DedupeKey: a.DedupeKey, Payload: p})
		if err != nil {
			return nil, err
		}
		return json.RawMessage(`{"ok":true}`), nil
	}})
}
