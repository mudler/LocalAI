// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

var _ = Describe("optional execution binding backend", func() {
	It("forwards bindings only through an explicit binding backend", func() {
		ctx := context.Background()
		actor := contract.Actor{UserID: "u", CredentialID: "c", Mode: "named-user"}
		claim := gateway.ExecutionBindingClaim{ConversationID: "chat"}
		adapter := tools.NewToolAdapter(&fake{})
		_, err := adapter.BindingForActor(ctx, actor, claim)
		Expect(err).To(HaveOccurred())
		Expect(adapter.ValidateExecutionBinding(ctx, actor, gateway.ExecutionStatusBinding{})).NotTo(Succeed())
		backend := &bindingBackend{binding: gateway.ExecutionStatusBinding{Actor: actor, ConversationID: "chat", CurrentSelection: "owner-epoch"}}
		adapter = tools.NewToolAdapter(backend)
		got, err := adapter.BindingForActor(ctx, actor, claim)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(backend.binding))
		Expect(backend.actor).To(Equal(actor))
		Expect(backend.claim).To(Equal(claim))
		Expect(adapter.ValidateExecutionBinding(ctx, actor, got)).To(Succeed())
		got.CurrentSelection = "forged"
		Expect(adapter.ValidateExecutionBinding(ctx, actor, got)).To(MatchError(gateway.ErrForbidden))
	})
})
