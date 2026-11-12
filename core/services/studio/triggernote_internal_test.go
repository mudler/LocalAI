package studio

import (
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the note for a turn without trigger tools", func() {
	refusedAll := func(reason string) []builtins.Built {
		var out []builtins.Built
		for _, id := range triggerToolNames {
			out = append(out, builtins.Built{ID: id, Reason: reason})
		}
		return out
	}

	It("names every reason that Build gives, and forbids the claim", func() {
		for reason, words := range map[string]string{
			builtins.ReasonOff:                                                 "switched off on this server",
			"triggers are off on this server":                                  "switched off on this server",
			builtins.ReasonChatOff:                                             "switched off for this chat",
			builtins.ReasonNotOn:                                               "not switched on for this chat",
			"a sub-agent cannot set triggers":                                  "a sub-agent cannot set triggers",
			"a run that started by itself cannot set triggers":                 "started by itself",
			ErrTriggersForbidden.Error():                                       "does not have the triggers feature",
			"a trigger from a messaging app needs the agent of the connection": "needs the agent",
		} {
			note := triggersAbsentNote(refusedAll(reason))
			Expect(note).To(ContainSubstring(words), "reason %q gave %q", reason, note)
			Expect(note).To(ContainSubstring("Do not say one was set"), "reason %q", reason)
			Expect(len(note)).To(BeNumerically("<", 200), "the note is short: %q", note)
		}
	})

	It("leaves a sender who lacks the right to the note about held back tools", func() {
		Expect(triggersAbsentNote(refusedAll(builtins.ReasonNotPermitted + ": the owner has not enabled triggers.propose for this bot"))).To(BeEmpty())
	})

	It("is empty when any trigger tool is offered, and when the registry has none", func() {
		built := refusedAll(builtins.ReasonChatOff)
		built[1].OK, built[1].Reason = true, ""
		Expect(triggersAbsentNote(built)).To(BeEmpty())
		Expect(triggersAbsentNote(nil)).To(BeEmpty())
		Expect(triggersAbsentNote([]builtins.Built{{ID: "bash", Reason: builtins.ReasonOff}})).To(BeEmpty())
	})

	It("leaves room in the denied note, so the two together fit the 400 characters of the session", func() {
		conn := session.ChatRecord{Origin: session.OriginConnector}
		built := refusedAll(builtins.ReasonChatOff)
		for name := range capabilityWords {
			built = append(built, builtins.Built{ID: name, Reason: "not permitted: " + name + " needs a sender that the owner paired with this bot"})
		}
		absent := triggersAbsentNote(built)
		denied := deniedToolsNoteReserve(conn, built, len(absent)+1)
		Expect(len(absent) + 1 + len(denied)).To(BeNumerically("<=", 400))
	})

	It("traces every refusal reason of Build with the tool name", func() {
		rec := session.ChatRecord{ID: "c1", Origin: session.OriginConnector, OriginID: "b1", AgentID: "a1"}
		attrs := triggerToolsAttrs(rec, builtins.Chat{}, refusedAll("a sub-agent cannot set triggers"))
		Expect(attrs).To(ContainElements("chat", "c1", "origin", "connector", "agent", "a1"))
		Expect(attrs).To(ContainElement(ContainSubstring("update_trigger=a sub-agent cannot set triggers")))
	})
})
