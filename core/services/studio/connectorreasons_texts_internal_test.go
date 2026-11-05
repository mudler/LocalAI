package studio

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The sentences of the table are read by people: the owner on the connection
// page, the sender in the chat, the model in its note. These specs render every
// code for every capability and hold the grammar of each result, so a new
// capability phrase cannot slip in a noun where a verb belongs.
var _ = Describe("the texts of a refusal", func() {
	// verbs a capability action may start with: a new action needs a new entry.
	actionVerbs := []string{"confirm", "save", "receive", "set", "run", "write", "start", "change", "publish"}
	names := append(contract.ConnectorCapabilityNames(), "")
	internal := regexp.MustCompile(`[a-z]+\.[a-z_]+|_|https?:|\d+\.\d+\.\d+|[{}%]|(?i:triggers?\.|localai_|bash)`)
	const (
		messageMax = 200
		sayMax     = 150
	)

	It("has an action phrase, starting with a verb, for every capability of the closed list", func() {
		Expect(capabilityWords).To(HaveLen(len(contract.ConnectorCapabilityNames())))
		for _, name := range contract.ConnectorCapabilityNames() {
			w, ok := capabilityWords[name]
			Expect(ok).To(BeTrue(), "no words for %s", name)
			first, _, _ := strings.Cut(w.Action, " ")
			Expect(actionVerbs).To(ContainElement(first), "%s: %q must start with a verb of the list", name, w.Action)
			Expect(w.Action).To(Equal(strings.ToLower(w.Action)), name)
			Expect(w.Noun).NotTo(BeEmpty(), name)
			Expect(w.Noun).NotTo(HavePrefix(first+" "), "%s: the noun phrase must not be the action", name)
			for _, text := range []string{w.Action, w.Noun} {
				Expect(text).NotTo(ContainSubstring("  "), name)
				Expect(text).NotTo(MatchRegexp(`[.,;:]`), "%s: a phrase has no punctuation: %q", name, text)
				Expect(internal.MatchString(text)).To(BeFalse(), "%s: %q", name, text)
			}
		}
	})

	It("renders a whole, plain sentence for every code and capability", func() {
		for _, code := range BlockCodes() {
			for _, name := range names {
				for kind, text := range map[string]string{"message": sampleOther(blockMessage(code, name)), "say": BlockSay(code, name)} {
					where := code + "/" + name + "/" + kind + ": " + text
					Expect(text).NotTo(BeEmpty(), where)
					Expect(text).To(HaveSuffix("."), where)
					Expect([]rune(text)[0]).To(Satisfy(unicode.IsUpper), "starts with a capital: %s", where)
					Expect(text).NotTo(ContainSubstring("  "), where)
					Expect(text).NotTo(ContainSubstring("localai"), "write LocalAI: %s", where)
					Expect(text).NotTo(ContainSubstring(" ."), where)
					Expect(text).NotTo(ContainSubstring(".."), where)
					Expect(strings.TrimSpace(text)).To(Equal(text), where)
					Expect(internal.MatchString(text)).To(BeFalse(), "no key, setting, host or code: %s", where)
					Expect(text).NotTo(MatchRegexp(`(?i)permission for|allowed for [a-z]+ing|given [a-z]+ing`), "a noun slot filled with a verb: %s", where)
					Expect(strings.Count(text, ".")).To(BeNumerically("<=", 2), "one or two sentences: %s", where)
					for _, key := range contract.ConnectorCapabilityNames() {
						Expect(text).NotTo(ContainSubstring(key), where)
					}
					limit := messageMax
					if kind == "say" {
						limit = sayMax
					}
					Expect(len(text)).To(BeNumerically("<=", limit), where)
				}
			}
		}
	})

	It("says the action inside the sentence for every capability that has one", func() {
		for _, code := range BlockCodes() {
			if !strings.Contains(blockTable[code].Message, "{action}") {
				continue
			}
			for _, name := range contract.ConnectorCapabilityNames() {
				Expect(blockMessage(code, name)).To(ContainSubstring(capabilityWords[name].Action), code+"/"+name)
				Expect(BlockSay(code, name)).To(ContainSubstring(capabilityWords[name].Action), code+"/"+name)
			}
		}
	})

	It("keeps the note of held-back tools in the budget with the longest sentences", func() {
		conn := session.ChatRecord{Origin: session.OriginConnector}
		for _, code := range BlockCodes() {
			var built []builtins.Built
			for _, name := range contract.ConnectorCapabilityNames() {
				built = append(built, builtins.Built{ID: name, Reason: builtins.ReasonNotPermitted + ": " + BlockSay(code, name)})
			}
			note := deniedToolsNote(conn, built)
			Expect(len(note)).To(BeNumerically("<=", 400), code)
			Expect(note).To(HavePrefix(deniedNoteStart), code)
			Expect(note).To(ContainSubstring("relay the reason plainly"), code)
			Expect(note).NotTo(ContainSubstring("  "), code)
			// At least one reason fits, and every one that fits is whole.
			Expect(note).To(MatchRegexp(`: [A-Z][^;]*\.`), code)
		}
	})

	// The six refusals people meet most, written out in full.
	golden := []struct{ code, capability, message, say string }{
		{CodeConnectionPermissionOff, contract.CapTriggersPropose,
			"This connection does not allow the bot to set reminders and scheduled tasks. Tick the matching permission on the connection.",
			"The owner has not allowed this bot to set reminders and scheduled tasks."},
		{CodeAgentPermissionOff, contract.CapMemoryWrite,
			"The agent is not allowed to save notes to memory. Turn on the matching permission in the agent.",
			"The owner has not allowed the agent to save notes to memory."},
		{CodeSenderNeedsPairing, contract.CapTalkbackNewChan,
			"This person needs to be paired with this bot before the bot can start conversations in new channels for them.",
			"You need to be paired with this bot by the owner before it can start conversations in new channels for you."},
		{CodeChannelNotTrusted, contract.CapMemoryWrite,
			"This channel is set to Answer only, so the bot cannot save notes to memory for its members. Add the person, or set it to Let everyone here ask for actions.",
			"This bot can save notes to memory only for people the owner added by name or paired, and you are not one of them."},
		{CodeRoleNotGranted, contract.CapTalkbackChannels,
			"The role of this person does not let the bot write to other channels. Allow it for that role on the connection.",
			"The owner has not allowed this bot to write to other channels for your role."},
		{CodeToolsSwitchOff, contract.CapToolsWrite,
			"The tools the bot needs to change things in other services with add-on tools are switched off for the agent. Switch them on in the agent.",
			"The owner switched off the tools the bot needs to change things in other services with add-on tools."},
	}
	for _, g := range golden {
		It("says "+g.code+" for "+g.capability+" the same way every time", func() {
			Expect(blockMessage(g.code, g.capability)).To(Equal(g.message))
			Expect(BlockSay(g.code, g.capability)).To(Equal(g.say))
		})
	}
})

// sampleOther fills {other} of a sentence with a name, so the sentence is read as a person reads it.
func sampleOther(text string) string { return strings.ReplaceAll(text, "{other}", "Alpha") }
