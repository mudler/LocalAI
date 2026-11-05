// SPDX-License-Identifier: MIT
package session_test

import (
	"context"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/session/sessiontest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the context of a turn", func() {
	It("gives the hook the message and puts the block in front of it for this turn only", func() {
		h := &sessiontest.FakeHarness{Turns: [][]sessiontest.Step{{}, {}}}
		var seen []string
		m, err := session.New(session.Options{Harness: h, Store: sessiontest.NewMemoryStore(), StateDir: GinkgoT().TempDir(),
			ChatTools: func(ctx context.Context, _ session.Access) (session.TurnTools, error) {
				seen = append(seen, session.TurnMessage(ctx))
				return session.TurnTools{Context: "NOTES for " + session.TurnMessage(ctx) + "\n" + strings.Repeat("x", session.MaxTurnContext)}, nil
			}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(m.Close(context.Background())).To(Succeed()) })
		chat, err := openAs(m, context.Background(), workspace.Actor{Principal: "alice", ChatID: "c1"}, "c1")
		Expect(err).NotTo(HaveOccurred())
		wait := func() {
			timer := time.After(5 * time.Second)
			for {
				select {
				case e := <-chat.Events():
					if e.Kind == session.KindTurnEnd {
						return
					}
				case <-timer:
					Fail("turn did not end")
				}
			}
		}
		Expect(chat.Send(context.Background(), session.Message{Text: "first"})).To(Succeed())
		wait()
		Expect(chat.Send(context.Background(), session.Message{Text: "second"})).To(Succeed())
		wait()
		Expect(seen[len(seen)-2:]).To(Equal([]string{"first", "second"}), "the hook that primes a chat has no message")
		sent := h.Sent()
		Expect(sent).To(HaveLen(2))
		Expect(sent[0]).To(HavePrefix("NOTES for first\n"))
		Expect(sent[0]).To(HaveSuffix("first"))
		Expect(len(sent[0])).To(BeNumerically("<", session.MaxTurnContext+64), "the block is cut")
		Expect(sent[1]).To(HavePrefix("NOTES for second\n"))
		Expect(sent[1]).NotTo(ContainSubstring("NOTES for first"), "an earlier block does not stay")
	})
})
