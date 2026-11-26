package studio

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var _ = Describe("the helpers of the reminders", func() {
	It("cuts a long title of a change note on a character boundary", func() {
		t := triggers.Trigger{Kind: triggers.KindReminder, Change: triggers.Change{Op: triggers.ChangeCreate},
			Outcome: triggers.OutcomeAccepted, Reminder: &triggers.Reminder{Title: strings.Repeat("è", 80)}}
		note := reminderChangeNote(t)
		Expect(utf8.ValidString(note)).To(BeTrue())
		Expect(note).To(ContainSubstring(strings.Repeat("è", 60) + "…"))
	})

	It("tells a refusal of a reminder from any other error", func() {
		why, ok := reminderRefusal(fmt.Errorf("wrapped: %w", reminderErr("in must be a delay such as %s", "20m")))
		Expect(ok).To(BeTrue())
		Expect(why).To(Equal("in must be a delay such as 20m"))
		_, ok = reminderRefusal(errors.New("database is locked"))
		Expect(ok).To(BeFalse())
		_, ok = reminderRefusal(nil)
		Expect(ok).To(BeFalse())
	})

	It("sends the buttons of a message only when they are valid, and the hint otherwise", func() {
		good := []contract.MessageAction{{ID: "reminder:r1:skip", Label: "Skip"}}
		text, actions := withActions("Standup", good, "Reply skip.", true)
		Expect(text).To(Equal("Standup"))
		Expect(actions).To(Equal(good))
		text, actions = withActions("Standup", good, "Reply skip.", false)
		Expect(text).To(Equal("Standup\nReply skip."))
		Expect(actions).To(BeNil())
		bad := []contract.MessageAction{{ID: "reminder:" + strings.Repeat("x", contract.MaxActionID), Label: "Skip"}}
		text, actions = withActions("Standup", bad, "Reply skip.", true)
		Expect(text).To(Equal("Standup\nReply skip."))
		Expect(actions).To(BeNil())
	})
})
