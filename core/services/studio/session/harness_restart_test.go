package session_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/session/sessiontest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var _ = Describe("harness restarts and chat opening", func() {
	// When the host tools change and the harness cannot restart, the turn has no
	// model to talk to. The user must get an error event, not silence.
	It("tells the user when a turn has no harness after a restart failure", func() {
		h := &sessiontest.FakeHarness{}
		var calls atomic.Int32
		o := session.Options{Store: sessiontest.NewMemoryStore(), Harness: h, StateDir: GinkgoT().TempDir()}
		o.ChatTools = func(context.Context, workspace.Actor) (session.TurnTools, error) {
			if calls.Add(1) == 1 {
				return session.TurnTools{}, nil
			}
			// A new tool changes the host tools, so the harness is restarted.
			return session.TurnTools{Tools: []session.Tool{{Name: "added_tool", Description: "x", Schema: []byte(`{"type":"object"}`),
				Run: func(context.Context, json.RawMessage) (string, error) { return "ok", nil }}}}, nil
		}
		m, err := session.New(o)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = m.Close(context.Background()) })
		chat, err := m.Open(context.Background(), workspace.Actor{Principal: "alice", ChatID: "c1"}, "c1")
		Expect(err).NotTo(HaveOccurred())
		// Take the channel once, before the turn. After the restart fails the chat
		// closes and drops its bus, so Events() then returns a new, closed channel
		// and the events that were not read yet are only in the old one.
		events := chat.Events()
		h.FailNextStarts = 8 // every start fails: the swap, its fresh retry and the plain restart
		Expect(chat.Send(context.Background(), session.Message{Text: "hello"})).To(Succeed())
		var sawError bool
		timeout := time.After(10 * time.Second)
		for done := false; !done; {
			select {
			case e, open := <-events:
				sawError = sawError || e.Kind == session.KindError
				done = !open || e.Kind == session.KindTurnEnd
			case <-timeout:
				Fail("the turn did not end")
			}
		}
		Expect(h.Sent()).To(BeEmpty(), "the message reached a harness")
		Expect(sawError).To(BeTrue(), "the turn ended with no error event: the user's message was swallowed")
	})

	// The workspace administrator is a principal of its own. A chat actor that
	// could carry that name would write anywhere in the workspace.
	It("refuses to open a chat as the workspace administrator", func() {
		m, err := session.New(session.Options{Store: sessiontest.NewMemoryStore(), Harness: &sessiontest.FakeHarness{}, StateDir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = m.Close(context.Background()) })
		a := workspace.Actor{Principal: workspace.StudioAdmin, ChatID: "c1"}
		_, err = m.Open(context.Background(), a, "c1")
		Expect(err).To(MatchError(session.ErrInvalid), "Open as the administrator")
	})

	// When the saved state of the harness cannot be read, the new harness gets the
	// transcript so far in the system prompt, oldest line first.
	It("keeps the order of the conversation in the transcript summary", func() {
		h := &sessiontest.FakeHarness{}
		m, err := session.New(session.Options{Store: sessiontest.NewMemoryStore(), Harness: h, StateDir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = m.Close(context.Background()) })
		ctx := context.Background()
		a := workspace.Actor{Principal: "alice", ChatID: "c1"}
		chat, err := m.Open(ctx, a, "c1")
		Expect(err).NotTo(HaveOccurred())
		for _, text := range []string{"first question", "second question"} {
			Expect(chat.Send(ctx, session.Message{Text: text})).To(Succeed())
			for done := false; !done; {
				select {
				case e := <-chat.Events():
					done = e.Kind == session.KindTurnEnd
				case <-time.After(5 * time.Second):
					Fail("the turn did not end")
				}
			}
		}
		Expect(chat.Close()).To(Succeed())
		h.FailResume = true
		_, err = m.Open(ctx, a, "c1")
		Expect(err).NotTo(HaveOccurred())
		starts := h.Starts()
		prompt := starts[len(starts)-1].SystemPrompt
		i, j := strings.Index(prompt, "first question"), strings.Index(prompt, "second question")
		Expect(i).To(BeNumerically(">=", 0), "the summary is missing the first question: %q", prompt)
		Expect(j).To(BeNumerically(">=", 0), "the summary is missing the second question: %q", prompt)
		Expect(i).To(BeNumerically("<=", j), "the summary is out of order (first at %d, second at %d): %q", i, j, prompt)
	})
})
