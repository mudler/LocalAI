package session_test

import (
	"context"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/session/sessiontest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var _ = Describe("closing a chat with a turn that runs", func() {
	// A turn leaves the running state before it announces its end. Close waits
	// for the turn to leave it, so it must also wait for the announcement: the
	// event stream drops what comes after it is closed, and whoever follows the
	// turn would never see it end.
	It("still announces the end of the turn", func() {
		h := &sessiontest.FakeHarness{Turns: [][]sessiontest.Step{{{Block: make(chan struct{})}}}}
		m, err := session.New(session.Options{Store: sessiontest.NewMemoryStore(), Harness: h, StateDir: GinkgoT().TempDir(), IdleClose: -1})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = m.Close(context.Background()) })
		entered, gate := make(chan struct{}), make(chan struct{})
		var once sync.Once
		m.HoldTurnEndForTest(func() {
			once.Do(func() { close(entered) })
			<-gate
		})
		DeferCleanup(func() {
			select {
			case <-gate:
			default:
				close(gate)
			}
		})
		ctx := context.Background()
		chat, err := openAs(m, ctx, workspace.Actor{Principal: "alice", ChatID: "c1"}, "c1")
		Expect(err).NotTo(HaveOccurred())
		events := chat.Events()
		Expect(chat.Send(ctx, session.Message{Text: "work"})).To(Succeed())

		closed := make(chan error, 1)
		go func() { closed <- chat.Close() }()
		// The turn is stopped and has left the running state; its end is not
		// announced yet. Close goes on with the harness.
		Eventually(entered, "10s").Should(BeClosed())
		Eventually(h.Closed, "10s", "5ms").Should(Equal(1))
		close(gate)

		sawEnd := false
		for done := false; !done; {
			select {
			case e, ok := <-events:
				if !ok {
					done = true
				} else if e.Kind == session.KindTurnEnd {
					sawEnd = true
				}
			case <-time.After(10 * time.Second):
				Fail("the event stream did not close")
			}
		}
		Expect(sawEnd).To(BeTrue(), "the stream of a closed chat carries the end of the turn that Close stopped")
		Eventually(closed, "10s").Should(Receive(BeNil()))
	})
})
