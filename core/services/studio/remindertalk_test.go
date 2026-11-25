package studio_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

// The buttons of a reminder in a messaging app, and the words that stand for
// them, are answered by the host with no model.
var _ = Describe("the buttons of a reminder in a messaging app", func() {
	ctx := context.Background()

	setup := func() (*connRig, *scriptedBot, triggers.Trigger) {
		sh, bot := newShared(), newScriptedBot()
		r := newConnRig(sh, connStore(), bot, connSay("Hello from the agent."), nil)
		b := r.bind(studio.ConnectorInput{Allow: []string{"11"}})
		route := triggers.Route{Connection: b.ID, Target: triggers.TargetChannel, Channel: "11"}
		w := triggers.Trigger{ID: "w1", Owner: alice, Name: "My notes", Prompt: "Watch.", Enabled: true, Kind: triggers.KindEvent, Source: "notes", TZ: "UTC",
			Model: "m", Watch: &triggers.WatchRule{Offset: -15 * time.Minute}, Route: route}
		Expect(sh.trg.PutTrigger(ctx, w)).To(Succeed())
		due := sh.clock.Now().Add(3 * time.Hour)
		rem := triggers.Trigger{ID: "rem1", Owner: alice, Name: "Standup", Prompt: "remind me", Enabled: true, Kind: triggers.KindReminder, TZ: "UTC",
			Model: "m", Route: route, Since: sh.clock.Now(), Reminder: &triggers.Reminder{ParentID: "w1", Key: "notes:1", Title: "Standup", Anchor: due, Offset: -15 * time.Minute, Task: "remind me"}}
		Expect(sh.trg.PutTrigger(ctx, rem)).To(Succeed())
		return r, bot, rem
	}
	tap := func(bot *scriptedBot, id int, data string) {
		bot.mu.Lock()
		defer bot.mu.Unlock()
		bot.updates = append(bot.updates, map[string]any{"update_id": id, "callback_query": map[string]any{
			"id": "cb", "from": map[string]any{"id": 11, "first_name": "Ada"}, "data": data,
			"message": map[string]any{"message_id": 5, "chat": map[string]any{"id": 11, "type": "private"}}}})
	}

	It("snoozes on a tap and says when, with no model turn", func() {
		r, bot, rem := setup()
		tap(bot, 1, "reminder:rem1:snooze5")
		waitFor("the answer", func() bool { return len(bot.answers()) == 1 })
		Expect(bot.answers()[0]).To(HavePrefix("Snoozed. I'll remind you at"))
		got, _ := r.sh.trg.GetTrigger(ctx, rem.ID)
		Expect(got.Reminder.SnoozedTo).To(Equal(r.sh.clock.Now().Add(5 * time.Minute).UTC()))
		chats, _ := r.svc.List(ctx, alice, 10)
		Expect(chats).To(BeEmpty(), "no chat with the agent")
	})

	It("skips on a tap, and refuses a button of a reminder that is gone", func() {
		r, bot, rem := setup()
		tap(bot, 1, "reminder:rem1:skip")
		waitFor("the answer", func() bool { return len(bot.answers()) == 1 })
		Expect(bot.answers()[0]).To(Equal("Skipped. I won't remind you of this one."))
		got, _ := r.sh.trg.GetTrigger(ctx, rem.ID)
		Expect(got.Reminder.Skipped.IsZero()).To(BeFalse())
		tap(bot, 2, "reminder:nope:skip")
		waitFor("the second answer", func() bool { return len(bot.answers()) == 2 })
		Expect(bot.answers()[1]).To(Equal("This reminder is no longer there."))
	})

	It("takes the words of a button in the chat that just got a reminder, and leaves other text to the agent", func() {
		r, bot, rem := setup()
		_, err := r.sh.trg.Claim(ctx, triggers.Run{ID: "x1", TriggerID: rem.ID, DueKey: "reminder@x", Due: r.sh.clock.Now(), Status: triggers.StatusDone,
			Started: r.sh.clock.Now(), Ended: r.sh.clock.Now()})
		Expect(err).NotTo(HaveOccurred())
		bot.say(1, 11, "Ada", "ada", "30 min")
		waitFor("the answer", func() bool { return len(bot.answers()) == 1 })
		Expect(bot.answers()[0]).To(HavePrefix("Snoozed. I'll remind you at"))
		got, _ := r.sh.trg.GetTrigger(ctx, rem.ID)
		Expect(got.Reminder.SnoozedTo).To(Equal(r.sh.clock.Now().Add(30 * time.Minute).UTC()))
		bot.say(2, 11, "Ada", "ada", "hi there")
		waitFor("the agent's answer", func() bool { return len(bot.answers()) == 2 })
		Expect(bot.answers()[1]).To(Equal("Hello from the agent."))
	})
})
