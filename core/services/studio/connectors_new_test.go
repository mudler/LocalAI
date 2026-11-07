package studio_test

import (
	"context"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The new chat command: the host reads it before any model turn, and it only
// drops the mapping from a conversation to its chat.
var _ = Describe("the new chat command of a connector", func() {
	ctx := context.Background()
	var (
		sh  *shared
		bot *scriptedBot
		st  connectors.Store
		r   *connRig
		b   connectors.Binding
	)
	const (
		done     = "Started a new chat. I will not see our earlier messages."
		each     = "Every message here already starts a new chat."
		sharedNo = "Only a verified account can start a new chat here, because everyone in this conversation shares it."
		busy     = "I am still answering. Send the command again when I am done."
	)
	setup := func(in studio.ConnectorInput) {
		sh, bot, st = newShared(), newScriptedBot(), connStore()
		r = newConnRig(sh, st, bot, connSay("ok"), func(context.Context) bool { return false })
		b = r.bind(in)
	}
	dm := func(sender, text string) contract.ConnectorMessage {
		return contract.ConnectorMessage{Chat: sender, Direct: true, SenderID: sender, SenderName: "S" + sender, Text: text}
	}
	group := func(sender, text string) contract.ConnectorMessage {
		return contract.ConnectorMessage{Chat: "-600", Mention: true, SenderID: sender, SenderName: "S" + sender, Text: text}
	}
	topic := func(sender, thread, text string) contract.ConnectorMessage {
		m := group(sender, text)
		m.Thread = thread
		return m
	}
	// ingest sends a message on a frontend and waits for the next answer of
	// the bot.
	ingest := func(via *connRig, m contract.ConnectorMessage) {
		n := len(bot.answers())
		Expect(via.conns.Ingest(ctx, "telegram", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: m})).To(Succeed())
		Eventually(func() int { return len(bot.answers()) }, "10s").Should(Equal(n + 1))
		sh.clock.Advance(20 * time.Second) // stays inside the rate limit of the sender
	}
	// send is ingest, then waits for the frontend to let go of the reply. The
	// answer is out before the reply loop has seen the turn end, and a command
	// in that gap is refused as busy.
	send := func(via *connRig, m contract.ConnectorMessage) {
		ingest(via, m)
		Eventually(func() bool { return via.conns.RepliesActiveForTest() }, "10s", "5ms").Should(BeFalse())
	}
	last := func() string { a := bot.answers(); return a[len(a)-1] }
	chats := func() []session.ChatRecord {
		l, err := r.svc.List(ctx, alice, 50)
		Expect(err).NotTo(HaveOccurred())
		return l
	}
	// sentTo lists what each chat received, oldest chat first.
	sentTo := func() [][]string {
		l := chats()
		var out [][]string
		for i := len(l) - 1; i >= 0; i-- {
			out = append(out, r.h.Sent(l[i].ID))
		}
		return out
	}

	It("starts a new chat for the sender mode and keeps the old one", func() {
		setup(studio.ConnectorInput{Allow: []string{"11"}})
		send(r, dm("11", "one"))
		Expect(chats()).To(HaveLen(1))
		old := chats()[0].ID
		send(r, dm("11", "/new"))
		Expect(last()).To(Equal(done))
		Expect(chats()).To(HaveLen(1), "the command makes no empty chat")
		send(r, dm("11", "two"))
		Expect(chats()).To(HaveLen(2))
		Expect(sentTo()).To(Equal([][]string{{"one"}, {"two"}}), "the command never reaches a chat")
		rec, err := r.svc.Get(ctx, alice, old)
		Expect(err).NotTo(HaveOccurred(), "the old chat stays")
		Expect(rec.ID).To(Equal(old))
		send(r, dm("11", "three"))
		Expect(sentTo()).To(Equal([][]string{{"one"}, {"two", "three"}}))
	})

	It("takes the bang form and the command of the bot in any case", func() {
		setup(studio.ConnectorInput{Allow: []string{"11"}})
		send(r, dm("11", "one"))
		send(r, dm("11", "!NEW"))
		Expect(last()).To(Equal(done))
		send(r, dm("11", "two"))
		send(r, dm("11", " /New@helper_bot \n"))
		Expect(last()).To(Equal(done))
		send(r, dm("11", "three"))
		Expect(sentTo()).To(Equal([][]string{{"one"}, {"two"}, {"three"}}))
	})

	It("treats a command with other text as a plain message", func() {
		setup(studio.ConnectorInput{Allow: []string{"11"}})
		send(r, dm("11", "one"))
		send(r, dm("11", "/new please summarise"))
		Expect(last()).To(Equal("ok"), "the model answered")
		Expect(sentTo()).To(Equal([][]string{{"one", "/new please summarise"}}))
	})

	It("starts a new chat after a command for the window mode", func() {
		setup(studio.ConnectorInput{Allow: []string{"11"}, Conversation: connectors.ConversationWindow, WindowMinutes: 30})
		send(r, dm("11", "one"))
		send(r, dm("11", "/new"))
		Expect(last()).To(Equal(done))
		send(r, dm("11", "two"))
		Expect(sentTo()).To(Equal([][]string{{"one"}, {"two"}}))
	})

	It("says that the thread mode needs no command, and changes nothing", func() {
		setup(studio.ConnectorInput{Allow: []string{"11"}, Conversation: connectors.ConversationThread})
		send(r, dm("11", "/new"))
		Expect(last()).To(Equal(each))
		Expect(chats()).To(BeEmpty())
	})

	It("lets a paired sender reset the shared chat of a group, for everyone", func() {
		setup(studio.ConnectorInput{Allow: []string{"-600"}, Conversation: connectors.ConversationChat})
		send(r, group("21", "first"))
		send(r, group("11", "/new")) // 11 is paired by the rig
		Expect(last()).To(Equal(done))
		send(r, group("21", "second"))
		Expect(sentTo()).To(Equal([][]string{{"first"}, {"second"}}))
		send(r, group("22", "third"))
		Expect(sentTo()).To(Equal([][]string{{"first"}, {"second", "third"}}), "the group shares the new chat")
	})

	It("refuses an unpaired member of a group to reset the shared chat", func() {
		setup(studio.ConnectorInput{Allow: []string{"-600"}, Conversation: connectors.ConversationChat})
		send(r, group("21", "first"))
		send(r, group("21", "/new"))
		Expect(last()).To(Equal(sharedNo))
		send(r, group("22", "second"))
		Expect(sentTo()).To(Equal([][]string{{"first", "second"}}), "the chat is the same")
	})

	It("resets only the chat of a thread, and only for a paired sender", func() {
		setup(studio.ConnectorInput{Allow: []string{"-600"}})
		send(r, topic("21", "77", "in one"))
		send(r, topic("21", "78", "in two"))
		send(r, topic("21", "77", "/new"))
		Expect(last()).To(Equal(sharedNo), "the thread is shared by the people in it")
		send(r, topic("11", "77", "/new"))
		Expect(last()).To(Equal(done))
		send(r, topic("21", "77", "again in one"))
		send(r, topic("21", "78", "again in two"))
		got := map[string]bool{}
		for _, s := range sentTo() {
			got[strings.Join(s, "|")] = true
		}
		Expect(got).To(Equal(map[string]bool{"in one": true, "in two|again in two": true, "again in one": true}))
	})

	It("lets a sender of a group reset their own chat in the sender mode", func() {
		setup(studio.ConnectorInput{Allow: []string{"-600"}})
		send(r, group("21", "mine"))
		send(r, group("22", "his"))
		send(r, group("21", "/new"))
		Expect(last()).To(Equal(done))
		send(r, group("21", "mine again"))
		send(r, group("22", "his again"))
		got := map[string]bool{}
		for _, s := range sentTo() {
			got[strings.Join(s, "|")] = true
		}
		Expect(got).To(Equal(map[string]bool{"mine": true, "his|his again": true, "mine again": true}))
	})

	It("gives a sender who is not listed nothing, and changes nothing", func() {
		setup(studio.ConnectorInput{Allow: []string{"11"}})
		send(r, dm("11", "one"))
		n := len(bot.answers())
		Expect(r.conns.Ingest(ctx, "telegram", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: dm("99", "/new")})).To(Succeed())
		Expect(r.conns.Ingest(ctx, "telegram", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{Chat: "-600", SenderID: "11", Text: "/new"}})).To(Succeed()) // a group message that names no bot
		Consistently(func() int { return len(bot.answers()) }, "300ms").Should(Equal(n))
		send(r, dm("11", "two"))
		Expect(sentTo()).To(Equal([][]string{{"one", "two"}}))
	})

	It("keeps the mapping while a turn of the chat runs", func() {
		sh, bot, st = newShared(), newScriptedBot(), connStore()
		release := make(chan struct{})
		script := func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Say: "Half"}, {Block: release}, {Say: " done."}}
		}
		r = newConnRig(sh, st, bot, script, func(context.Context) bool { return false })
		b = r.bind(studio.ConnectorInput{Allow: []string{"11"}})
		Expect(r.conns.Ingest(ctx, "telegram", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: dm("11", "go")})).To(Succeed())
		Eventually(func() int { return len(chats()) }, "10s").Should(Equal(1))
		sh.clock.Advance(20 * time.Second)
		ingest(r, dm("11", "/new")) // the turn runs, so the reply is held on purpose
		Expect(last()).To(Equal(busy))
		close(release)
		Eventually(func() string { a := bot.answers(); return a[len(a)-1] }, "10s").Should(Equal("Half done."))
		sh.clock.Advance(20 * time.Second)
		Eventually(func() bool {
			n := len(bot.answers())
			Expect(r.conns.Ingest(ctx, "telegram", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: dm("11", "/new")})).To(Succeed())
			Eventually(func() int { return len(bot.answers()) }, "10s").Should(Equal(n + 1))
			sh.clock.Advance(20 * time.Second)
			return last() == done
		}, "10s", "50ms").Should(BeTrue())
		Expect(chats()).To(HaveLen(1), "the chat of the turn is kept")
	})

	It("shares the reset between frontends through the store", func() {
		setup(studio.ConnectorInput{Allow: []string{"11"}})
		second := newConnRig(sh, st, bot, connSay("ok"), func(context.Context) bool { return false })
		send(r, dm("11", "one"))
		send(second, dm("11", "/new"))
		Expect(last()).To(Equal(done))
		send(r, dm("11", "two"))
		send(second, dm("11", "three"))
		l := chats()
		Expect(l).To(HaveLen(2))
		// Each frontend has its own model harness: add what both received.
		got := map[string]bool{}
		for _, c := range l {
			got[strings.Join(append(r.h.Sent(c.ID), second.h.Sent(c.ID)...), "|")] = true
		}
		Expect(got).To(Equal(map[string]bool{"one": true, "two|three": true}))
	})

	It("writes no text of the message to the log", func() {
		setup(studio.ConnectorInput{Allow: []string{"11"}})
		send(r, dm("11", "one"))
		send(r, dm("11", "/new"))
		send(r, dm("11", "!new"))
		logs := r.logs.String()
		Expect(logs).To(ContainSubstring("chat.new"))
		Expect(logs).To(ContainSubstring("sender=11"))
		Expect(strings.ToLower(logs)).NotTo(ContainSubstring("/new"))
		Expect(strings.ToLower(logs)).NotTo(ContainSubstring("!new"))
	})
})
