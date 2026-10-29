package studio_test

import (
	"context"

	"github.com/mudler/LocalAI/core/services/studio"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// An administrator who switches the chats feature off for a user stops what
// that user's bots do by themselves, not only their page and API.
var _ = Describe("hardening", func() {
	It("a bot stops answering when its owner loses the chats feature", func() {
		sh, bot := newShared(), newScriptedBot()
		sh.users = func(string) error { return studio.ErrChatsOff }
		r := newConnRig(sh, connStore(), bot, connSay("the agent ran"), nil)
		b := r.bind(studio.ConnectorInput{AcceptAny: true})
		bot.say(1, 1, "One", "", "hello")
		waitFor("an answer", func() bool { return len(bot.answers()) == 1 })

		Expect(bot.answers()[0]).To(Equal("This bot is not available right now."))
		chats, _ := r.svc.List(context.Background(), alice, 10)
		Expect(chats).To(BeEmpty(), "the sender got %d chats of a user who lost the feature", len(chats))
		got, _ := r.conns.GetConnector(context.Background(), alice, b.ID)
		Expect(got.Enabled).To(BeTrue(), "the binding was switched off; an administrator can give the feature back")
	})
})
