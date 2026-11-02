// SPDX-License-Identifier: MIT
package tools

import (
	"context"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// ExecutionBindingBackend is an optional host-only door. Ordinary Backend
// implementations and model tool execution do not gain binding authority.
type ExecutionBindingBackend interface {
	BindingForActor(context.Context, contract.Actor, gateway.ExecutionBindingClaim) (gateway.ExecutionStatusBinding, error)
	ValidateExecutionBinding(context.Context, contract.Actor, gateway.ExecutionStatusBinding) error
}

// BindingForActor forwards only an authenticated actor and conversation claim.
// Discovery results, model arguments and frontend permissions are not inputs.
func (t *ToolAdapter) BindingForActor(ctx context.Context, a contract.Actor, c gateway.ExecutionBindingClaim) (gateway.ExecutionStatusBinding, error) {
	b, ok := t.backend.(ExecutionBindingBackend)
	if !ok {
		return gateway.ExecutionStatusBinding{}, gateway.ErrForbidden
	}
	return b.BindingForActor(ctx, a, c)
}

func (t *ToolAdapter) ValidateExecutionBinding(ctx context.Context, a contract.Actor, b gateway.ExecutionStatusBinding) error {
	owner, ok := t.backend.(ExecutionBindingBackend)
	if !ok {
		return gateway.ErrForbidden
	}
	return owner.ValidateExecutionBinding(ctx, a, b)
}

// ExecutionStatusRead is optional and never falls back to ordinary tool dispatch.
func (t *ToolAdapter) ExecutionStatusRead(ctx context.Context, a contract.Actor, r gateway.ExecutionStatusRequest) (gateway.ExecutionStatusResponse, error) {
	owner, ok := t.backend.(interface {
		ExecutionStatusRead(context.Context, contract.Actor, gateway.ExecutionStatusRequest) (gateway.ExecutionStatusResponse, error)
	})
	if !ok {
		return gateway.ExecutionStatusResponse{}, gateway.ErrForbidden
	}
	return owner.ExecutionStatusRead(ctx, a, r)
}
