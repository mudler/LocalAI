package triggers_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

var _ = Describe("watches and reminders", func() {
	watch := func() triggers.Trigger {
		return triggers.Trigger{ID: "w", Owner: "u", Name: "Calendar", Prompt: "Watch my calendar.", Enabled: true, Kind: triggers.KindEvent,
			Source: "notes", Watch: &triggers.WatchRule{Offset: -15 * time.Minute, QuietFrom: "22:00", QuietTo: "07:00"}}
	}
	reminder := func() triggers.Trigger {
		return triggers.Trigger{ID: "r", Owner: "u", Name: "Standup", Prompt: "Remind me.", Enabled: true, Kind: triggers.KindReminder,
			Reminder: &triggers.Reminder{ParentID: "w", Key: "notes:1", Anchor: time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC), Offset: -15 * time.Minute}}
	}

	It("accepts a watch and a reminder", func() {
		Expect(watch().Validate()).To(Succeed())
		Expect(watch().IsWatch()).To(BeTrue())
		Expect(reminder().Validate()).To(Succeed())
		Expect(reminder().IsReminder()).To(BeTrue())
	})

	DescribeTable("refuses a bad watch",
		func(change func(*triggers.Trigger), want string) {
			t := watch()
			change(&t)
			Expect(t.Validate()).To(MatchError(ContainSubstring(want)))
		},
		Entry("quiet hours with one end", func(t *triggers.Trigger) { t.Watch.QuietTo = "" }, "quiet hours"),
		Entry("quiet hours that are no time", func(t *triggers.Trigger) { t.Watch.QuietFrom, t.Watch.QuietTo = "25:00", "07:00" }, "quiet hours"),
		Entry("an unknown all-day rule", func(t *triggers.Trigger) { t.Watch.AllDay = "noon" }, "all-day"),
		Entry("an offset beyond the bound", func(t *triggers.Trigger) { t.Watch.Offset = -31 * 24 * time.Hour }, "reminder time"),
		Entry("a rule on a schedule trigger", func(t *triggers.Trigger) {
			t.Kind, t.Schedule, t.Source = triggers.KindSchedule, "daily 08:00", ""
		}, "belongs to a watch"),
	)

	DescribeTable("refuses a bad reminder",
		func(change func(*triggers.Trigger), want string) {
			t := reminder()
			change(&t)
			Expect(t.Validate()).To(MatchError(ContainSubstring(want)))
		},
		Entry("no watch", func(t *triggers.Trigger) { t.Reminder.ParentID = "" }, "belongs to a watch"),
		Entry("no key", func(t *triggers.Trigger) { t.Reminder.Key = "" }, "names the thing"),
		Entry("a key with no source", func(t *triggers.Trigger) { t.Reminder.Key = "1" }, "names the thing"),
		Entry("no time at all", func(t *triggers.Trigger) { t.Reminder.Anchor = time.Time{} }, "needs a time"),
		Entry("a negative lead", func(t *triggers.Trigger) { t.Reminder.Lead = -time.Minute }, "lead time"),
		Entry("an unknown zone", func(t *triggers.Trigger) { t.Reminder.Zone = "Mars/Base" }, "not known"),
		Entry("no reminder fields", func(t *triggers.Trigger) { t.Reminder = nil }, "needs its watch"),
		Entry("event fields", func(t *triggers.Trigger) { t.Source = "notes" }, "no event fields"),
	)
})
