package studio

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
)

var _ = Describe("waits and sub-agents, internal", func() {
	ctx := context.Background()

	newSvc := func() (*Service, store.Store, loops.Store) {
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc, err := New(Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(context.Background()) })
		ls, err := loops.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		host, err := NewLoops(svc, LoopOptions{Store: ls})
		Expect(err).NotTo(HaveOccurred())
		svc.SetLoops(host)
		DeferCleanup(host.Close)
		Expect(st.CreateChat(ctx, session.ChatRecord{ID: "kid", Principal: "alice", Kind: session.KindSubAgent, ParentID: "mom"})).To(Succeed())
		Expect(st.ClaimTurn(ctx, "alice", "kid", "run-1")).To(Succeed())
		Expect(st.ReleaseTurn(ctx, "alice", "kid", "run-1", store.TurnDone)).To(Succeed())
		return svc, st, ls
	}

	Describe("the childPoll safeguard", func() {
		BeforeEach(func() {
			old := childPoll.get()
			childPoll.set(10 * time.Millisecond)
			DeferCleanup(func() { childPoll.set(old) })
		})

		It("counts an idle sub-agent with no active loop as done after two checks in a row", func() {
			svc, _, _ := newSvc()
			done := svc.waitFor(key("alice", "kid")) // the word that never comes
			start := time.Now()
			got, err := svc.waitChild(ctx, "alice", "kid", done, 0)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(store.TurnDone))
			Expect(time.Since(start)).To(BeNumerically(">=", 20*time.Millisecond), "one check was enough")
		})

		It("does not count a sub-agent that has an active loop", func() {
			svc, _, ls := newSvc()
			now := time.Now().UTC()
			Expect(ls.Add(ctx, loops.Loop{ID: "l1", Owner: "alice", ChatID: "kid", Prompt: "check", State: loops.StateActive,
				Next: now.Add(time.Hour), Scheduled: true, Created: now, Updated: now}, loops.Limits{})).To(Succeed())
			done := svc.waitFor(key("alice", "kid"))
			c, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			defer cancel()
			_, err := svc.waitChild(c, "alice", "kid", done, 0)
			Expect(err).To(MatchError(context.DeadlineExceeded))
		})

		It("does not count a sub-agent that never ran", func() {
			svc, st, _ := newSvc()
			Expect(st.CreateChat(ctx, session.ChatRecord{ID: "fresh", Principal: "alice", Kind: session.KindSubAgent, ParentID: "mom"})).To(Succeed())
			done := svc.waitFor(key("alice", "fresh"))
			c, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			defer cancel()
			_, err := svc.waitChild(c, "alice", "fresh", done, 0)
			Expect(err).To(MatchError(context.DeadlineExceeded))
		})

		It("gives up on a sub-agent that holds for a loop for longer than the bound", func() {
			svc, _, ls := newSvc()
			now := time.Now().UTC()
			Expect(ls.Add(ctx, loops.Loop{ID: "l1", Owner: "alice", ChatID: "kid", Prompt: "check", State: loops.StateActive,
				Next: now.Add(time.Hour), Scheduled: true, Created: now, Updated: now}, loops.Limits{})).To(Succeed())
			done := svc.waitFor(key("alice", "kid"))
			_, err := svc.waitChild(ctx, "alice", "kid", done, 60*time.Millisecond)
			Expect(err).To(MatchError(errChildHeld))
			svc.mu.Lock()
			Expect(svc.waiters[key("alice", "kid")]).To(HaveLen(1), "the waiter was dropped, so the report would be lost")
			svc.mu.Unlock()
		})
	})

	Describe("the note of the promise guard", func() {
		It("only tells a connector chat to say plainly that it cannot watch", func() {
			svc, _, _ := newSvc()
			rec := session.ChatRecord{ID: "c", Principal: "alice", Origin: session.OriginConnector}
			Expect(svc.guardTools(ctx, rec)).To(BeEmpty())
			note := guardNote("I'll check in 5 minutes.", nil)
			Expect(note).To(ContainSubstring("Tell the person plainly"))
			Expect(note).NotTo(ContainSubstring("start_loop"))
			Expect(note).NotTo(ContainSubstring("wait_for"))
			Expect(note).NotTo(ContainSubstring("spawn_agent"))
		})

		It("names the tools a person's chat really has", func() {
			svc, _, _ := newSvc()
			Expect(svc.guardTools(ctx, session.ChatRecord{ID: "c", Principal: "alice"})).To(HaveLen(3))
			svc.loops = nil
			tools := svc.guardTools(ctx, session.ChatRecord{ID: "c", Principal: "alice"})
			Expect(tools).To(HaveLen(1), "without a loop host only spawn_agent remains")
			Expect(tools[0]).To(HavePrefix("spawn_agent"))
		})
	})
})
