package studio_test

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A message that arrives while a reply runs joins that reply. The reply must
// not give the chat up in the moment between the message being told to join
// and the message reaching the chat: the turn it starts would have no reply
// to carry its answer.
var _ = Describe("a message that joins a running reply", func() {
	It("is answered even when the first turn ends before the message reaches the chat", func() {
		ctx := context.Background()
		sh, bot := newShared(), newScriptedBot()
		release := make(chan struct{})
		script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "second" {
				return []studiotest.ToolStep{{Say: "Second."}}
			}
			return []studiotest.ToolStep{{Say: "First"}, {Block: release}, {Say: " done."}}
		}
		r := newConnRig(sh, connStore(), bot, script, func(context.Context) bool { return false })
		b := r.bind(studio.ConnectorInput{Allow: []string{"11"}})
		msg := func(text string) contract.ConnectorInbound {
			return contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{Chat: "11", Direct: true, SenderID: "11", SenderName: "S11", Text: text}}
		}
		held, hold := make(chan struct{}), make(chan struct{})
		r.conns.HoldJoiningMessagesForTest(func() {
			close(held)
			<-hold
		})
		DeferCleanup(func() {
			select {
			case <-hold:
			default:
				close(hold)
			}
		})

		Expect(r.conns.Ingest(ctx, "telegram", msg("first"))).To(Succeed())
		Eventually(func() bool { return r.conns.RepliesActiveForTest() }, "10s", "5ms").Should(BeTrue())
		sh.clock.Advance(20 * time.Second)
		// Ingest returns when the message has reached the chat, so it is held too.
		ingested := make(chan error, 1)
		go func() { ingested <- r.conns.Ingest(ctx, "telegram", msg("second")) }()
		Eventually(held, "10s").Should(BeClosed(), "the second message joined the running reply")

		// The first turn ends while the second message is still on its way.
		close(release)
		Eventually(func() []string { return bot.answers() }, "10s").Should(ContainElement("First done."))
		Consistently(func() bool { return r.conns.RepliesActiveForTest() }, "300ms", "10ms").Should(BeTrue(),
			"the reply keeps the chat while a joined message is on its way")

		close(hold)
		Eventually(ingested, "10s").Should(Receive(BeNil()))
		Eventually(func() []string { return bot.answers() }, "10s").Should(ContainElement("Second."))
		Eventually(func() bool { return r.conns.RepliesActiveForTest() }, "10s", "10ms").Should(BeFalse())
	})

	// A turn announces its end and only then starts the next one from the
	// messages that wait. In between the chat is idle with a message waiting,
	// and the reply must not give the chat up: the turn that follows would have
	// no reply to carry its answer.
	It("is answered when the first turn ends before the next one starts", func() {
		ctx := context.Background()
		sh, bot := newShared(), newScriptedBot()
		release := make(chan struct{})
		script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "second" {
				return []studiotest.ToolStep{{Say: "Second."}}
			}
			return []studiotest.ToolStep{{Say: "First"}, {Block: release}, {Say: " done."}}
		}
		r := newConnRig(sh, connStore(), bot, script, func(context.Context) bool { return false })
		b := r.bind(studio.ConnectorInput{Allow: []string{"11"}})
		msg := func(text string) contract.ConnectorInbound {
			return contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{Chat: "11", Direct: true, SenderID: "11", SenderName: "S11", Text: text}}
		}
		gate := make(chan struct{})
		settles := make(chan bool, 8)
		r.svc.HoldNextTurnForTest(func() { <-gate })
		r.conns.OnSettleForTest(func(settled bool) { settles <- settled })
		DeferCleanup(func() {
			select {
			case <-gate:
			default:
				close(gate)
			}
		})

		Expect(r.conns.Ingest(ctx, "telegram", msg("first"))).To(Succeed())
		Eventually(func() bool { return r.conns.RepliesActiveForTest() }, "10s", "5ms").Should(BeTrue())
		sh.clock.Advance(20 * time.Second)
		Expect(r.conns.Ingest(ctx, "telegram", msg("second"))).To(Succeed())

		// The first turn ends and its answer is sent. The next turn waits at
		// the gate, so the reply decides about the chat while the message waits.
		close(release)
		Eventually(settles, "10s").Should(Receive(BeFalse()),
			"the reply keeps the chat while a waiting message is about to start a turn")

		close(gate)
		Eventually(func() []string { return bot.answers() }, "10s").Should(ContainElement("Second."))
		Eventually(func() bool { return r.conns.RepliesActiveForTest() }, "10s", "10ms").Should(BeFalse())
	})
})
