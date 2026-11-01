package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// A long sub-agent run has more entries than one page; its answer is the last
// assistant message, not the first page's.
var _ = Describe("subagent internal", func() {
	It("final text of a long child is its last answer", func() {
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc, err := New(Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(context.Background()) })
		ctx := context.Background()
		Expect(st.CreateChat(ctx, session.ChatRecord{ID: "kid", Principal: "alice"})).To(Succeed())
		_, _ = st.AppendEntry(ctx, "kid", session.Entry{Role: session.RoleAssistant, Text: "early"})
		for i := range 2500 {
			_, _ = st.AppendEntry(ctx, "kid", session.Entry{Role: session.RoleTool, Text: fmt.Sprint("t", i)})
		}
		_, _ = st.AppendEntry(ctx, "kid", session.Entry{Role: session.RoleAssistant, Text: "final"})
		Expect(svc.finalText(ctx, "kid")).To(Equal("final"))
	})
})

type noClaimForKids struct{ store.Store }

func (n noClaimForKids) ClaimTurn(ctx context.Context, p, id, run string) error {
	if rec, err := n.Store.GetChat(ctx, p, id); err == nil && rec.Kind == session.KindSubAgent {
		return errors.New("claim refused")
	}
	return n.Store.ClaimTurn(ctx, p, id, run)
}

// A sub-agent that could not start leaves neither a waiter nor a conversation.
var _ = Describe("subagent internal", func() {
	It("a child that cannot start leaves nothing behind", func() {
		mem, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc, err := New(Deps{Store: noClaimForKids{mem}, Provider: provider.New(), StateDir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(context.Background()) })
		ctx := context.Background()
		rec, err := svc.Create(ctx, "alice", CreateInput{Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.spawn(ctx, workspace.Actor{Principal: "alice", ChatID: rec.ID}, json.RawMessage(`{"task":"do it"}`))
		Expect(err).To(HaveOccurred(), "spawn should fail when the child cannot start")
		svc.mu.Lock()
		waiters := len(svc.waiters)
		svc.mu.Unlock()
		all, _ := mem.ListChats(ctx, "alice", session.ChatCursor{}, 10)
		Expect(waiters).To(BeNumerically("==", 0), "waiters=%d chats=%d, want none left and only the parent", waiters, len(all))
		Expect(all).To(HaveLen(1), "waiters=%d chats=%d, want none left and only the parent", waiters, len(all))
	})
})
