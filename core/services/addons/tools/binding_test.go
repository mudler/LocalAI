// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"testing"
)

type bindingBackend struct {
	fake
	actor   contract.Actor
	claim   gateway.ExecutionBindingClaim
	binding gateway.ExecutionStatusBinding
}

func (b *bindingBackend) BindingForActor(_ context.Context, a contract.Actor, c gateway.ExecutionBindingClaim) (gateway.ExecutionStatusBinding, error) {
	b.actor = a
	b.claim = c
	return b.binding, nil
}
func (b *bindingBackend) ValidateExecutionBinding(_ context.Context, a contract.Actor, v gateway.ExecutionStatusBinding) error {
	b.actor = a
	if v != b.binding {
		return gateway.ErrForbidden
	}
	return nil
}
func TestBindingOptionalBackend(t *testing.T) {
	ctx := context.Background()
	a := contract.Actor{UserID: "u", CredentialID: "c", Mode: "named-user"}
	claim := gateway.ExecutionBindingClaim{ConversationID: "chat"}
	adapter := tools.NewToolAdapter(&fake{})
	if _, e := adapter.BindingForActor(ctx, a, claim); e == nil {
		t.Fatal("ordinary backend granted binding")
	}
	if e := adapter.ValidateExecutionBinding(ctx, a, gateway.ExecutionStatusBinding{}); e == nil {
		t.Fatal("ordinary backend validated binding")
	}
	b := &bindingBackend{binding: gateway.ExecutionStatusBinding{Actor: a, ConversationID: "chat", SelectionRevision: "owner-epoch"}}
	adapter = tools.NewToolAdapter(b)
	got, e := adapter.BindingForActor(ctx, a, claim)
	if e != nil || got != b.binding || b.actor != a || b.claim != claim {
		t.Fatal("binding forwarding changed input or output")
	}
	if e = adapter.ValidateExecutionBinding(ctx, a, got); e != nil {
		t.Fatal(e)
	}
	got.SelectionRevision = "forged"
	if e = adapter.ValidateExecutionBinding(ctx, a, got); e == nil {
		t.Fatal("validation refusal lost")
	}
}
