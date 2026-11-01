package session_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/session/sessiontest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var _ = Describe("closing idle chats", func() {
	// A chat that nobody uses must not keep its event goroutine and channel for
	// the life of the process, and a later Open must bring it back.
	It("closes an idle chat and opens it again", func() {
		h := &sessiontest.FakeHarness{}
		m, err := session.New(session.Options{Store: sessiontest.NewMemoryStore(), Harness: h, StateDir: GinkgoT().TempDir(), IdleClose: 40 * time.Millisecond})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = m.Close(context.Background()) })
		ctx := context.Background()
		a := workspace.Actor{Principal: "alice", ChatID: "c1"}
		chat, err := m.Open(ctx, a, "c1")
		Expect(err).NotTo(HaveOccurred())
		events := chat.Events()
		deadline := time.After(5 * time.Second)
		for closed := false; !closed; {
			select {
			case _, ok := <-events:
				closed = !ok
			case <-deadline:
				Fail("the idle chat was not closed")
			}
		}
		Expect(h.Closed()).To(Equal(1), "the harness of the idle chat must be closed once")
		again, err := m.Open(ctx, a, "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(again.Send(ctx, session.Message{Text: "hello"})).To(Succeed())
		for done := false; !done; {
			select {
			case e, ok := <-again.Events():
				done = !ok || e.Kind == session.KindTurnEnd
			case <-time.After(5 * time.Second):
				Fail("the reopened chat did not answer")
			}
		}
		Expect(h.Sent()).To(Equal([]string{"hello"}))
	})

	It("does not close a chat in use by the janitor", func() {
		h := &sessiontest.FakeHarness{}
		m, err := session.New(session.Options{Store: sessiontest.NewMemoryStore(), Harness: h, StateDir: GinkgoT().TempDir(), IdleClose: -1})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = m.Close(context.Background()) })
		_, err = m.Open(context.Background(), workspace.Actor{Principal: "alice", ChatID: "c1"}, "c1")
		Expect(err).NotTo(HaveOccurred())
		time.Sleep(60 * time.Millisecond)
		Expect(h.Closed()).To(BeZero(), "a negative IdleClose closed a chat")
	})
})
