// SPDX-License-Identifier: MIT
package connectors

import "github.com/mudler/LocalAI/pkg/addons/contract"

// ConnectorCallScope is host authority, never part of the add-on protocol.
// A pinned frontend asserts the canonical studio principal on behalf of its user.
type ConnectorCallScope struct {
	Purpose   string `json:"purpose"`
	Principal string `json:"principal"`
	BindingID string `json:"bindingId,omitempty"`
	AgentID   string `json:"agentId,omitempty"`
	AddonID   string `json:"addonId"`
	Kind      string `json:"kind"`
}

const (
	ConnectorPurposeBinding     = "binding"
	ConnectorPurposeVerifyToken = "verify-token"
)

// Kind describes a connector registered by an add-on runtime.
type Kind struct {
	AddonID string
	contract.ConnectorKind
}
