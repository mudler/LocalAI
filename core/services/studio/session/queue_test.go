package session_test

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/session/sessiontest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// A harness whose turn is ending refuses Inject, so the message waits in the
// queue of the chat. These specs drive that path.
var _ = Describe("messages that wait for the next turn", func() {
	var (
		h     *sessiontest.FakeHarness
		store session.Store
		chat  *session.Chat
		block chan struct{}
		once  sync.Once
	)

	userTexts := func() []string {
		entries, err := store.Entries(context.Background(), "c1", 0, 1000)
		Expect(err).NotTo(HaveOccurred())
		var out []string
		for _, e := range entries {
			if e.Role == session.RoleUser {
				out = append(out, e.Text)
			}
		}
		return out
	}

	release := func() { once.Do(func() { close(block) }) }

	BeforeEach(func() {
		once = sync.Once{}
		block = make(chan struct{})
		h = &sessiontest.FakeHarness{InjectErr: session.ErrNoTurn}
		h.Turns = [][]sessiontest.Step{{{Hold: block}}}
		store = sessiontest.NewMemoryStore()
		m, err := session.New(session.Options{Store: store, Harness: h, StateDir: GinkgoT().TempDir()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = m.Close(context.Background()) })
		// The held turn ignores a stop, so a failing spec must let it go.
		DeferCleanup(release)
		chat, err = openAs(m, context.Background(), workspace.Actor{Principal: "alice", ChatID: "c1"}, "c1")
		Expect(err).NotTo(HaveOccurred())
		Expect(chat.Send(context.Background(), session.Message{Text: "first"})).To(Succeed())
		Eventually(func() int { return len(h.Sent()) }).Should(Equal(1))
	})

	It("does not refuse a message that reached the transcript when the turn is stopped meanwhile", func() {
		stopped := make(chan struct{})
		var first atomic.Bool
		h.InjectHook = func(string) error {
			if first.CompareAndSwap(false, true) {
				go func() {
					_ = chat.Stop(context.Background())
					close(stopped)
				}()
				// Wait until Stop has marked the turn. The turn winds down slowly,
				// so it still runs when Send looks at it again.
				Eventually(func() error {
					return chat.Send(context.Background(), session.Message{Text: "probe"})
				}).Should(MatchError(ContainSubstring("stopping")))
				time.AfterFunc(100*time.Millisecond, release)
			}
			return session.ErrNoTurn
		}
		err := chat.Send(context.Background(), session.Message{Text: "second"})
		Expect(err).NotTo(HaveOccurred(), "a message that is in the transcript was refused")
		Eventually(stopped).Should(BeClosed())
		Expect(userTexts()).To(ContainElement("second"), "the message is not in the transcript")
		Eventually(func() []string { return h.Sent() }).Should(HaveLen(2), "the message never reached a turn")
	})
})
