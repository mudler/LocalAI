package studio

import (
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the note about tools held back from a sender", func() {
	conn := session.ChatRecord{Origin: session.OriginConnector}
	It("is empty for a chat that no connector started", func() {
		Expect(deniedToolsNote(session.ChatRecord{}, []builtins.Built{{ID: "propose_trigger", Reason: "not permitted: x"}})).To(BeEmpty())
	})
	It("is empty when no tool was held back for a missing right", func() {
		Expect(deniedToolsNote(conn, []builtins.Built{{ID: "bash", Reason: builtins.ReasonOff}, {ID: "web_search", OK: true}})).To(BeEmpty())
	})
	It("names the reasons in plain words, and tells the model to relay them", func() {
		note := deniedToolsNote(conn, []builtins.Built{
			{ID: "propose_trigger", Reason: "not permitted: the owner has not enabled triggers.propose for this bot"},
			{ID: "list_triggers", Reason: "not permitted: the owner has not enabled triggers.propose for this bot"},
			{ID: "send_message", Reason: "not permitted: talkback.new_channel needs a sender that the owner paired with this bot"},
			{ID: "bash", Reason: builtins.ReasonOff},
		})
		Expect(note).To(HavePrefix("Held back from this sender: "))
		Expect(note).To(ContainSubstring("The owner has not enabled reminders and scheduled tasks for this bot."), "said once for both tools")
		Expect(strings.Count(note, "reminders")).To(Equal(1))
		Expect(note).To(ContainSubstring("Starting conversations in new channels needs a sender that the owner paired with this bot."))
		Expect(note).To(ContainSubstring("never say it does not exist"))
		Expect(note).NotTo(ContainSubstring("triggers.propose"))
		Expect(note).NotTo(ContainSubstring("bash"))
	})
	It("fits what the session keeps of a note, whatever is held back", func() {
		var built []builtins.Built
		for name := range capabilityWords {
			built = append(built, builtins.Built{ID: name, Reason: "not permitted: " + name + " needs a sender that the owner paired with this bot"})
		}
		Expect(len(deniedToolsNote(conn, built))).To(BeNumerically("<=", 400))
	})
	It("keeps the reason of an add-on tool when the budget cuts the rest, and names its tools", func() {
		var held heldTools
		for _, n := range []string{"create-issue", "comment-issue", "edit-issue", "add-labels", "review-comment-pr"} {
			held.add(AddonTool{AddonID: "tracker", Tool: n}, "The owner has not allowed this bot to change things in other services with add-on tools")
		}
		built := held.built()
		for name := range capabilityWords {
			built = append(built, builtins.Built{ID: name, Reason: "not permitted: " + name + " needs a sender that the owner paired with this bot"})
		}
		note := deniedToolsNote(conn, built)
		Expect(len(note)).To(BeNumerically("<=", 400))
		Expect(note).To(ContainSubstring("tracker add-on: create-issue, comment-issue, edit-issue, and 2 more"))
		Expect(note).To(ContainSubstring("change things in other services"))
	})
})
