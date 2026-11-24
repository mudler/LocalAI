// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/addons/apps"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/hostops"
	"github.com/mudler/LocalAI/core/services/addons/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// bindingSpy is an owner that notes, for every call that reaches it, the chat
// binding the call's context carries.
type bindingSpy struct {
	inner *applyFake
	mu    sync.Mutex
	seen  []string
}

func (s *bindingSpy) note(ctx context.Context, what string) {
	b, _ := hostops.BindingOf(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, what+" "+b.Principal+"/"+b.ChatID)
}

func (s *bindingSpy) Apps(ctx context.Context, a contract.Actor) (apps.List, error) {
	return s.inner.Apps(ctx, a)
}

func (s *bindingSpy) Dispatch(ctx context.Context, a contract.Actor, r contract.Request) (contract.Result, error) {
	s.note(ctx, "dispatch")
	return s.inner.Dispatch(ctx, a, r)
}

func (s *bindingSpy) Propose(ctx context.Context, a contract.Actor, r contract.Request) (gateway.Proposal, error) {
	s.note(ctx, "propose")
	return s.inner.Propose(ctx, a, r)
}

func (s *bindingSpy) Act(ctx context.Context, a contract.Actor, r contract.Request) (contract.Result, error) {
	s.note(ctx, "act")
	return s.inner.Act(ctx, a, r)
}

func (s *bindingSpy) ConfirmProposal(ctx context.Context, a contract.Actor, id, digest string) (gateway.Proposal, error) {
	s.note(ctx, "confirm")
	return s.inner.ConfirmProposal(ctx, a, id, digest)
}

func (s *bindingSpy) CancelProposal(ctx context.Context, a contract.Actor, id string) (gateway.Proposal, error) {
	s.note(ctx, "cancel")
	return s.inner.CancelProposal(ctx, a, id)
}

var _ = Describe("the chat binding of a tool call", func() {
	ctx := context.Background()
	build := func() *bindingSpy {
		base := &actFake{fake: &fake{list: apps.List{Apps: []apps.App{tablesApp(tablesManifest()), sandboxApp()}},
			proposal: gateway.Proposal{AddonID: "tables", ResourceID: "db-one", Action: "write", Digest: "d1", State: gateway.ProposalPending}}}
		return &bindingSpy{inner: &applyFake{actFake: base, state: gateway.ProposalApplied}}
	}

	It("binds every tool kind of a chat call to the chat", func() {
		spy := build()
		chats := tools.NewChatToolAdapter(spy)
		call := tools.NewChatCall("bob", "conv1")
		_, err := chats.ExecuteChat(ctx, member, call, tools.ToolName("sandbox", "status"), args(`{}`))
		Expect(err).NotTo(HaveOccurred())
		_, err = chats.ExecuteChat(ctx, member, call, tools.ToolName("sandbox", "bash"), args(`{"command":"ls"}`))
		Expect(err).NotTo(HaveOccurred())
		_, err = chats.ExecuteChat(ctx, member, call, tools.ToolName("tables", "rows-insert"), args(`{"resourceId":"db-one","table":"t","rows":[{"a":1}]}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(spy.seen).To(Equal([]string{"dispatch bob/conv1", "act bob/conv1", "propose bob/conv1", "confirm bob/conv1"}),
			"a read with no resource, an action and a change made at once all carry the chat")
	})

	It("refuses a call of the chats adapter that names a conversation without its binding", func() {
		spy := build()
		chats := tools.NewChatToolAdapter(spy)
		for _, name := range []string{tools.ToolName("sandbox", "status"), tools.ToolName("sandbox", "bash")} {
			_, err := chats.ExecuteIn(ctx, member, "conv1", name, args(`{"command":"ls"}`))
			Expect(err).To(MatchError(tools.ErrInvalidArguments), name)
		}
		Expect(spy.seen).To(BeEmpty())
	})

	It("refuses an incomplete chat call", func() {
		spy := build()
		chats := tools.NewChatToolAdapter(spy)
		for _, call := range []tools.ChatCall{{}, tools.NewChatCall("", "conv1"), tools.NewChatCall("bob", ""), tools.NewChatCall("bob", "bad id/../x")} {
			_, err := chats.ExecuteChat(ctx, member, call, tools.ToolName("sandbox", "status"), args(`{}`))
			Expect(err).To(MatchError(tools.ErrInvalidArguments))
		}
		Expect(spy.seen).To(BeEmpty())
	})

	It("leaves the calls of other consumers without a binding", func() {
		spy := build()
		plain := tools.NewToolAdapter(spy)
		_, err := plain.ExecuteIn(ctx, member, "conv1", tools.ToolName("sandbox", "status"), args(`{}`))
		Expect(err).NotTo(HaveOccurred())
		_, err = tools.NewChatToolAdapter(spy).Execute(ctx, member, tools.ToolName("sandbox", "status"), args(`{}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(spy.seen).To(Equal([]string{"dispatch /", "dispatch /"}))
	})
})
