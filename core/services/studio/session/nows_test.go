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

var _ = Describe("a manager without a workspace", func() {
	// The studio loop runs before the workspace exists (phase 1), so a manager
	// with no workspace and no sandbox must open a chat and finish a turn.
	It("opens a chat and finishes a turn", func() {
		h := &sessiontest.FakeHarness{Turns: [][]sessiontest.Step{{{Say: "hello back"}}}}
		m, err := session.New(session.Options{
			Store:    sessiontest.NewMemoryStore(),
			Harness:  h,
			StateDir: GinkgoT().TempDir(),
		})
		Expect(err).NotTo(HaveOccurred(), "session.New without a workspace")
		defer func() { _ = m.Close(context.Background()) }()

		ctx := context.Background()
		chat, err := m.Open(ctx, workspace.Actor{Principal: "alice", ChatID: "c1"}, "c1")
		Expect(err).NotTo(HaveOccurred(), "Open without a workspace")
		Expect(chat.Send(ctx, session.Message{Text: "hi"})).To(Succeed())
		var sawText, sawEnd bool
		timeout := time.After(20 * time.Second)
		for !sawEnd {
			select {
			case e, ok := <-chat.Events():
				Expect(ok).To(BeTrue(), "events closed before turn_end")
				if e.Kind == session.KindText && e.Text == "hello back" {
					sawText = true
				}
				if e.Kind == session.KindTurnEnd {
					sawEnd = true
				}
			case <-timeout:
				Fail("timed out waiting for turn_end")
			}
		}
		Expect(sawText).To(BeTrue(), "the scripted reply never arrived as a text event")
	})
})
