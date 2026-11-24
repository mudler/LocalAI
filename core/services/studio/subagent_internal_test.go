package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

// A long sub-agent run has more entries than one page; its answer is the last
// assistant message, not the first page's.
var _ = Describe("subagent internal", func() {
	// A background sub-agent whose turn is reaped as dead must still report back,
	// or its waiter would block until the server stops.
	It("tells the waiter of a sub-agent whose turn was reaped", func() {
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc, err := New(Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(context.Background()) })
		ctx := context.Background()
		Expect(st.CreateChat(ctx, session.ChatRecord{ID: "kid", Principal: "alice"})).To(Succeed())
		Expect(st.ClaimTurn(ctx, "alice", "kid", "run-1")).To(Succeed())
		done := svc.waitFor(key("alice", "kid"))
		n, err := svc.ReapStale(ctx, -time.Hour)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))
		Eventually(done).Should(Receive(Equal(store.TurnInterrupted)))
	})

	It("cuts a title on a rune boundary", func() {
		got := truncate("héllo wörld", 2)
		Expect(utf8.ValidString(got)).To(BeTrue(), "the title %q is cut inside a rune", got)
		Expect(got).To(Equal("h…"))
		Expect(truncate("  a   b ", 10)).To(Equal("a b"))
	})

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
		x, err := svc.ChatAccess(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.spawn(ctx, x, json.RawMessage(`{"task":"do it"}`))
		Expect(err).To(HaveOccurred(), "spawn should fail when the child cannot start")
		svc.mu.Lock()
		waiters := len(svc.waiters)
		svc.mu.Unlock()
		all, _ := mem.ListChats(ctx, "alice", session.ChatCursor{}, 10)
		Expect(waiters).To(BeNumerically("==", 0), "waiters=%d chats=%d, want none left and only the parent", waiters, len(all))
		Expect(all).To(HaveLen(1), "waiters=%d chats=%d, want none left and only the parent", waiters, len(all))
	})
})
