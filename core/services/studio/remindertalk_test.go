package studio_test

import (
	"context"
	"fmt"
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

	It("says that nothing changed, not that the reminder is gone, when a change is refused", func() {
		r, bot, rem := setup()
		got, err := r.sh.trg.GetTrigger(ctx, rem.ID)
		Expect(err).NotTo(HaveOccurred())
		got.Reminder.Anchor = r.sh.clock.Now().Add(-time.Minute) // the item has begun
		Expect(r.sh.trg.PutTrigger(ctx, got)).To(Succeed())
		tap(bot, 1, "reminder:rem1:early60")
		waitFor("the answer", func() bool { return len(bot.answers()) == 1 })
		Expect(bot.answers()[0]).To(Equal("I could not change this reminder. Change it on the watch's page."))
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

var _ = Describe("the buttons of a heads-up in a messaging app", func() {
	ctx := context.Background()

	It("moves the reminder the watch's run set to an hour before, or removes it", func() {
		sh, bot := newShared(), newScriptedBot()
		r := newConnRig(sh, connStore(), bot, connSay("Hello from the agent."), nil)
		b := r.bind(studio.ConnectorInput{Allow: []string{"11"}})
		route := triggers.Route{Connection: b.ID, Target: triggers.TargetChannel, Channel: "11"}
		w := triggers.Trigger{ID: "w1", Owner: alice, Name: "My notes", Prompt: "Watch.", Enabled: true, Kind: triggers.KindEvent, Source: "notes", TZ: "UTC",
			Model: "m", Watch: &triggers.WatchRule{Offset: -15 * time.Minute}, Route: route}
		Expect(sh.trg.PutTrigger(ctx, w)).To(Succeed())
		now := sh.clock.Now()
		due := now.Add(5 * time.Hour)
		// rem1 was set by the run "wr" and changed before it, as far as its
		// last-change time says; rem2 changed after the run but was not set
		// by it. The reply finds rem1 by the run it records.
		rem := triggers.Trigger{ID: "rem1", Owner: alice, Name: "Call", Prompt: "remind me", Enabled: true, Kind: triggers.KindReminder, TZ: "UTC",
			Model: "m", Route: route, Since: now.Add(-time.Hour), Reminder: &triggers.Reminder{ParentID: "w1", Key: "notes:1", Title: "Call", Anchor: due,
				Offset: -15 * time.Minute, Task: "remind me", SetBy: "wr"}}
		Expect(sh.trg.PutTrigger(ctx, rem)).To(Succeed())
		other := triggers.Trigger{ID: "rem2", Owner: alice, Name: "Other", Prompt: "remind me", Enabled: true, Kind: triggers.KindReminder, TZ: "UTC",
			Model: "m", Route: route, Since: now.Add(time.Minute), Reminder: &triggers.Reminder{ParentID: "w1", Key: "notes:2", Title: "Other", Anchor: due,
				Offset: -15 * time.Minute, Task: "remind me"}}
		Expect(sh.trg.PutTrigger(ctx, other)).To(Succeed())
		_, err := sh.trg.Claim(ctx, triggers.Run{ID: "wr", TriggerID: "w1", DueKey: "event@x", Due: now, Status: triggers.StatusDone, ChatID: "c1", Started: now, Ended: now})
		Expect(err).NotTo(HaveOccurred())

		bot.say(1, 11, "Ada", "ada", "earlier")
		waitFor("the answer", func() bool { return len(bot.answers()) == 1 })
		Expect(bot.answers()[0]).To(HavePrefix("Moved. I'll remind you at"))
		got, _ := sh.trg.GetTrigger(ctx, "rem1")
		Expect(got.Reminder.Offset).To(Equal(-time.Hour))
		untouched, _ := sh.trg.GetTrigger(ctx, "rem2")
		Expect(untouched.Reminder.Offset).To(Equal(-15*time.Minute), "a reminder the run did not set is not the one the reply answers")

		bot.mu.Lock()
		bot.updates = append(bot.updates, map[string]any{"update_id": 2, "callback_query": map[string]any{
			"id": "cb", "from": map[string]any{"id": 11, "first_name": "Ada"}, "data": "reminder:rem1:none",
			"message": map[string]any{"message_id": 5, "chat": map[string]any{"id": 11, "type": "private"}}}})
		bot.mu.Unlock()
		waitFor("the second answer", func() bool { return len(bot.answers()) == 2 })
		Expect(bot.answers()[1]).To(Equal("Cancelled. There is no reminder for this one."))
		_, err = sh.trg.GetTrigger(ctx, "rem1")
		Expect(err).To(MatchError(triggers.ErrNotFound))
	})

	It("answers the heads-up the owner got last when two watches share the chat", func() {
		sh, bot := newShared(), newScriptedBot()
		r := newConnRig(sh, connStore(), bot, connSay("Hello from the agent."), nil)
		b := r.bind(studio.ConnectorInput{Allow: []string{"11"}})
		route := triggers.Route{Connection: b.ID, Target: triggers.TargetChannel, Channel: "11"}
		now := sh.clock.Now()
		due := now.Add(5 * time.Hour)
		for i, name := range []string{"A first", "B second"} {
			wid, rid, run := fmt.Sprintf("w%d", i), fmt.Sprintf("rem%d", i), fmt.Sprintf("run%d", i)
			Expect(sh.trg.PutTrigger(ctx, triggers.Trigger{ID: wid, Owner: alice, Name: name, Prompt: "Watch.", Enabled: true, Kind: triggers.KindEvent,
				Source: "notes", TZ: "UTC", Model: "m", Watch: &triggers.WatchRule{Offset: -15 * time.Minute}, Route: route})).To(Succeed())
			Expect(sh.trg.PutTrigger(ctx, triggers.Trigger{ID: rid, Owner: alice, Name: name, Prompt: "remind me", Enabled: true, Kind: triggers.KindReminder,
				TZ: "UTC", Model: "m", Route: route, Since: now, Reminder: &triggers.Reminder{ParentID: wid, Key: "notes:" + rid, Title: name, Anchor: due,
					Offset: -15 * time.Minute, Task: "remind me", SetBy: run}})).To(Succeed())
			ended := now.Add(time.Duration(i) * time.Minute) // B's heads-up came last
			_, err := sh.trg.Claim(ctx, triggers.Run{ID: run, TriggerID: wid, DueKey: "event@" + run, Due: ended, Status: triggers.StatusDone,
				ChatID: "c" + run, Started: ended, Ended: ended})
			Expect(err).NotTo(HaveOccurred())
		}
		// A run of the first watch that is still going does not hide its
		// older heads-up, and does not win over the newer one.
		_, err := sh.trg.Claim(ctx, triggers.Run{ID: "busy", TriggerID: "w0", DueKey: "event@busy", Due: now.Add(2 * time.Minute), Status: triggers.StatusRunning,
			ChatID: "cbusy", Started: now.Add(2 * time.Minute)})
		Expect(err).NotTo(HaveOccurred())
		sh.clock.Advance(3 * time.Minute)

		bot.say(1, 11, "Ada", "ada", "no reminder")
		waitFor("the answer", func() bool { return len(bot.answers()) == 1 })
		_, err = sh.trg.GetTrigger(ctx, "rem1")
		Expect(err).To(MatchError(triggers.ErrNotFound), "the reply answers the newest heads-up")
		_, err = sh.trg.GetTrigger(ctx, "rem0")
		Expect(err).NotTo(HaveOccurred(), "the other watch's reminder stays")
	})
})
