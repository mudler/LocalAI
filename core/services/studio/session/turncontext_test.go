// SPDX-License-Identifier: MIT
package session_test

import (
	"context"
	"encoding/json"
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

	// The hook can be slow (it lists the tools of MCP servers and add-ons), and
	// the turn asks it anyway. A chat that reopens for its next turn keeps the
	// tools of its last turn and does not ask the hook while it opens: only a
	// chat that never had them is primed.
	It("does not ask the hook again when a chat reopens for its next turn", func() {
		h := &sessiontest.FakeHarness{Turns: [][]sessiontest.Step{{}, {}}}
		var seen []string
		m, err := session.New(session.Options{Harness: h, Store: sessiontest.NewMemoryStore(), StateDir: GinkgoT().TempDir(),
			ChatTools: func(ctx context.Context, _ session.Access) (session.TurnTools, error) {
				seen = append(seen, session.TurnMessage(ctx))
				return session.TurnTools{Tools: []session.Tool{{Name: "echo", Description: "echo", Schema: json.RawMessage(`{"type":"object"}`),
					Run: func(context.Context, json.RawMessage) (string, error) { return "", nil }}}}, nil
			}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(m.Close(context.Background())).To(Succeed()) })
		ctx := context.Background()
		a := workspace.Actor{Principal: "alice", ChatID: "c1"}
		for _, text := range []string{"first", "second"} {
			chat, err := openAs(m, ctx, a, "c1")
			Expect(err).NotTo(HaveOccurred())
			Expect(chat.Send(ctx, session.Message{Text: text})).To(Succeed())
			for done := false; !done; {
				select {
				case e := <-chat.Events():
					done = e.Kind == session.KindTurnEnd
				case <-time.After(5 * time.Second):
					Fail("the turn did not end")
				}
			}
			Expect(chat.Close()).To(Succeed())
		}
		Expect(seen).To(Equal([]string{"", "first", "second"}), "the chat is primed once, then each turn asks the hook")
		Expect(h.Starts()[len(h.Starts())-1].Tools).To(ContainElement(HaveField("Name", "echo")), "the reopened harness keeps the tools of the last turn")
	})
})
