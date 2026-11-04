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
	// Content names the file an outbound transfer reads: a blob of the
	// workspace store. The frontend that checked the chat's access sets it;
	// it never reaches the add-on, which gets only the handle.
	Content *TransferContent `json:"content,omitempty"`
}

// TransferContent is the stored content behind an outbound handle.
type TransferContent struct {
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
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
