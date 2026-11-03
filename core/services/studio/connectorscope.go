// SPDX-License-Identifier: MIT
package studio

import (
	"context"
	"reflect"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ConnectorCallScope is host-only authority shared with the private carrier.
type ConnectorCallScope = connectors.ConnectorCallScope

const (
	ConnectorPurposeBinding     = connectors.ConnectorPurposeBinding
	ConnectorPurposeVerifyToken = connectors.ConnectorPurposeVerifyToken
)

// BindingConnectorScope also covers background calls with fresh contexts.
func BindingConnectorScope(b connectors.Binding) ConnectorCallScope {
	return ConnectorCallScope{Purpose: ConnectorPurposeBinding, Principal: b.Owner, BindingID: b.ID, AgentID: b.AgentID, AddonID: b.AddonID, Kind: b.Kind}
}
func verificationScope(b connectors.Binding) ConnectorCallScope {
	s := BindingConnectorScope(b)
	s.Purpose = ConnectorPurposeVerifyToken
	// A new binding has an ID already, but is not stored until verification ends.
	if len(b.Token) == 0 {
		s.BindingID = ""
	}
	return s
}

// AuthorizeConnector derives binding credentials exclusively from owner storage.
// All missing or mismatched authority receives the same refusal.
func (c *Connectors) AuthorizeConnector(ctx context.Context, s ConnectorCallScope, req contract.ConnectorRequest) (contract.ConnectorRequest, error) {
	deny := func() (contract.ConnectorRequest, error) { return contract.ConnectorRequest{}, connectors.ErrForbidden }
	if s.Principal == "" || s.Principal != principal(s.Principal) || s.AddonID == "" || s.Kind == "" || req.Kind != s.Kind {
		return deny()
	}
	if s.Purpose != ConnectorPurposeBinding && s.Purpose != ConnectorPurposeVerifyToken {
		return deny()
	}
	var b connectors.Binding
	if s.BindingID != "" {
		var err error
		b, err = c.o.Store.GetBinding(ctx, s.BindingID)
		if err != nil || b.Owner != s.Principal || b.AddonID != s.AddonID || b.Kind != s.Kind || b.AgentID != s.AgentID {
			return deny()
		}
	} else if s.Purpose != ConnectorPurposeVerifyToken {
		return deny()
	}
	if s.Purpose == ConnectorPurposeVerifyToken {
		only := contract.ConnectorRequest{Op: contract.ConnectorOpMe, Kind: s.Kind, Token: req.Token}
		if req.Token == "" || !reflect.DeepEqual(req, only) {
			return deny()
		}
		// Mirror studio's user mapping: anonymous bypasses lookup; legacy and
		// named users resolve through the application-provided lookup.
		if c.svc.d.Users != nil && s.Principal != AnonymousPrincipal {
			u, err := c.svc.d.Users(ctx, s.Principal)
			if err != nil || u == nil || u.ID != s.Principal {
				return deny()
			}
		}
		if s.BindingID == "" {
			if err := c.checkAgent(ctx, s.Principal, s.AgentID); err != nil {
				return deny()
			}
		}
		return req, nil
	}
	switch req.Op {
	case contract.ConnectorOpPoll:
		if !b.Enabled {
			return deny()
		}
	case contract.ConnectorOpMe, contract.ConnectorOpSend, contract.ConnectorOpEdit, contract.ConnectorOpTyping, contract.ConnectorOpDraft, contract.ConnectorOpMedia, contract.ConnectorOpSendFile:
	default:
		return deny()
	}
	token, err := c.openToken(ctx, b)
	if err != nil {
		return contract.ConnectorRequest{}, err
	}
	req.Token = token
	return req, nil
}
