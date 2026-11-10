package studio_test

import (
	"context"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
)

var _ = DescribeTable("a short answer to a proposal",
	func(text, verb, code string, ok bool) {
		v, c, got := studio.ParseReply(text)
		Expect(got).To(Equal(ok))
		Expect(v).To(Equal(verb))
		Expect(c).To(Equal(code))
	},
	Entry("yes", "yes", "confirm", "", true),
	Entry("with marks and capitals", "  Yes! ", "confirm", "", true),
	Entry("confirm", "confirm", "confirm", "", true),
	Entry("ok to apply", "ok", "confirm", "", true),
	Entry("Italian", "sì", "confirm", "", true),
	Entry("a thumb", "👍", "confirm", "", true),
	Entry("no", "no", "cancel", "", true),
	Entry("dismiss with the word cancel", "cancel", "cancel", "", true),
	Entry("Italian no", "annulla", "cancel", "", true),
	Entry("a code, lower case", "yes 7k2q", "confirm", "7K2Q", true),
	Entry("confirm and a code", "confirm 7K2Q", "confirm", "7K2Q", true),
	Entry("cancel and a code", "cancel 7K2Q", "cancel", "7K2Q", true),
	Entry("a sentence is not an answer", "yes please save that and also the other thing", "", "", false),
	Entry("another word", "maybe", "", "", false),
	Entry("empty", "  ", "", "", false),
)

// Change proposals of a connector chat are shown in the chat and answered
// there, before any model turn.
var _ = Describe("change proposals in a connector chat", func() {
	ctx := context.Background()
	const (
		listed = "11" // on the allowlist, not paired
		paired = "12" // paired
	)
	var (
		sh    *shared
		bot   *scriptedBot
		st    connectors.Store
		props proposals.Store
		r     *connRig
		b     connectors.Binding
	)
	script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
		kind := func(name string) []studiotest.ToolStep {
			for _, c := range grantCases {
				if c.args["kind"] == name {
					return []studiotest.ToolStep{{Tool: "propose_change", Args: argsJSON(c.args)}, {Say: "I asked."}}
				}
			}
			return nil
		}
		switch text {
		case "note":
			return []studiotest.ToolStep{{Tool: "propose_change", Args: proposeArgs("memory", "channel", "index.md", "- standup at 09:30\n", "Standup time")}, {Say: "I asked."}}
		case "note two":
			return []studiotest.ToolStep{{Tool: "propose_change", Args: proposeArgs("memory", "channel", "food.md", "- likes tea @channel\n", "Drinks")}, {Say: "I asked."}}
		case "profile", "skill", "page", "dashboard":
			return kind(text)
		}
		return []studiotest.ToolStep{{Say: "model"}}
	}
	allCaps := []string{contract.CapMemoryWrite, contract.CapProfileWrite, contract.CapSkillsWrite, contract.CapPagesWrite, contract.CapDashboardWrite, contract.CapProposalsConfirm}

	newTransport := func() *studiobot.BotTransport {
		addon := studiobot.Telegram(&transport.API{BaseURL: bot.srv.URL, Client: bot.srv.Client()})
		addon.Kind.Permissions = allCaps
		return studiobot.NewTransport(addon)
	}
	setupWith := func(caps []string) {
		sh, bot, st = changeShared(grantAll), newScriptedBot(), connStore()
		var err error
		props, err = proposals.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		prev := sh.deps
		sh.deps = func(d *studio.Deps) { prev(d); d.Proposals = props }
		r = newConnRigWith(sh, st, newTransport(), script, func(context.Context) bool { return false })
		r.bot, r.untrusted = bot, true
		r.svc.SetCapabilityGate(r.conns.CapabilityGate())
		r.grantBuiltins(contract.CapMemoryWrite, contract.CapProfileWrite, contract.CapSkillsWrite, contract.CapPagesWrite, contract.CapDashboardWrite)
		b = r.bind(studio.ConnectorInput{Allow: []string{listed, paired, "-100"}, Caps: caps, ReplyLanguage: englishReplies()})
		got, err := r.store.GetBinding(ctx, b.ID)
		Expect(err).NotTo(HaveOccurred())
		got.Paired = append(got.Paired, connectors.PairedSender{SenderID: paired, At: sh.clock.Now()})
		Expect(r.store.PutBinding(ctx, got)).To(Succeed())
		b = got
	}
	setup := func() { setupWith(allCaps) }

	// say writes in a private chat and waits for n messages of the bot.
	say := func(via *connRig, sender, text string, n int) []string {
		before := len(bot.answers())
		Expect(via.conns.Ingest(ctx, "telegram", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{
			Chat: sender, Direct: true, SenderID: sender, SenderName: "S", Text: text}})).To(Succeed())
		waitFor("the answers", func() bool { return len(bot.answers()) >= before+n })
		sh.clock.Advance(15 * time.Second)
		return bot.answers()[before:]
	}
	// sayIn writes in a group.
	sayIn := func(sender, text string, n int) []string {
		before := len(bot.answers())
		Expect(r.conns.Ingest(ctx, "telegram", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{
			Chat: "-100", Mention: true, SenderID: sender, SenderName: "S", Text: text}})).To(Succeed())
		waitFor("the answers", func() bool { return len(bot.answers()) >= before+n })
		sh.clock.Advance(15 * time.Second)
		return bot.answers()[before:]
	}
	modelTurns := func() int {
		chats, err := r.svc.List(ctx, alice, 50)
		Expect(err).NotTo(HaveOccurred())
		n := 0
		for _, c := range chats {
			n += len(r.h.Sent(c.ID))
		}
		return n
	}
	all := func() []contract.ChangeProposal {
		l, err := r.svc.Changes().List(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		return l
	}
	only := func() contract.ChangeProposal {
		l := all()
		Expect(l).To(HaveLen(1))
		return l[0]
	}
	haveStatus := func(s contract.ProposalStatus) types.GomegaMatcher {
		return WithTransform(func(p contract.ChangeProposal) contract.ProposalStatus { return p.Status }, Equal(s))
	}
	lastResult := func() string {
		res := r.h.ResultsFor("propose_change")
		Expect(res).NotTo(BeEmpty())
		Expect(res[len(res)-1].Err).NotTo(HaveOccurred())
		return res[len(res)-1].Out
	}

	Describe("a memory note from a private chat", func() {
		It("is shown with what will be saved, confirmed by yes without a model turn, and undone by undo", func() {
			setup()
			out := say(r, listed, "note", 2)
			Expect(out[0]).To(ContainSubstring("I can save a note in the memory (this conversation only): Standup time"))
			Expect(out[0]).To(ContainSubstring("+ - standup at 09:30"))
			Expect(out[0]).To(ContainSubstring(`Reply "yes" to save it or "no" to drop it`))
			p := only()
			Expect(out[0]).To(ContainSubstring(`"confirm ` + studio.ChangeCode(p.ID) + `"`))
			Expect(p.Source.Sender).To(Equal(listed))
			Expect(out[1]).To(Equal("I asked."))
			// The model is told what the host did, and is not sent to Studio.
			res := lastResult()
			Expect(res).To(ContainSubstring("The host wrote the proposal in this chat"))
			Expect(res).To(ContainSubstring("yes or no"))
			Expect(res).NotTo(ContainSubstring("Studio"))
			_, found := readHome(r.trigRig, p.Target)
			Expect(found).To(BeFalse(), "nothing is written before the answer")

			turns := modelTurns()
			out = say(r, listed, "yes", 1)
			Expect(out[0]).To(Equal(`Saved: Standup time. Reply "undo" within 30 minutes to take it back.`))
			Expect(modelTurns()).To(Equal(turns), "the model never read the answer")
			Expect(only()).To(haveStatus(contract.ProposalApplied))
			text, found := readHome(r.trigRig, p.Target)
			Expect(found).To(BeTrue())
			Expect(text).To(ContainSubstring("standup at 09:30"))

			out = say(r, listed, "undo", 1)
			Expect(out[0]).To(Equal("Removed: Standup time."))
			Expect(only()).To(haveStatus(contract.ProposalReverted))
			_, found = readHome(r.trigRig, p.Target)
			Expect(found).To(BeFalse())
			Expect(modelTurns()).To(Equal(turns))

			// The answer is used once: a second yes is an ordinary message.
			out = say(r, listed, "yes", 1)
			Expect(out[0]).To(Equal("model"))
		})

		It("breaks the mentions of everyone in the text it writes", func() {
			setup()
			out := say(r, listed, "note two", 2)
			Expect(out[0]).NotTo(ContainSubstring("@channel"))
			Expect(out[0]).To(ContainSubstring("tea"))
		})

		It("is dropped by no", func() {
			setup()
			say(r, listed, "note", 2)
			out := say(r, listed, "no", 1)
			Expect(out[0]).To(Equal("Cancelled."))
			Expect(only()).To(haveStatus(contract.ProposalDismissed))
		})

		It("is confirmed by a word with its code", func() {
			setup()
			say(r, listed, "note", 2)
			p := only()
			out := say(r, listed, "Confirm "+strings.ToLower(studio.ChangeCode(p.ID)), 1)
			Expect(out[0]).To(HavePrefix("Saved:"))
		})

		It("does not undo on a plain no after the save", func() {
			setup()
			say(r, listed, "note", 2)
			say(r, listed, "yes", 1)
			out := say(r, listed, "no", 1)
			Expect(out[0]).To(Equal("model"))
			Expect(only()).To(haveStatus(contract.ProposalApplied))
		})

		It("lets undo through to the model when nothing was saved from the chat", func() {
			setup()
			out := say(r, listed, "undo", 1)
			Expect(out[0]).To(Equal("model"))
		})
	})

	DescribeTable("a proposal of each kind is confirmed in the chat",
		func(kind, wantText, wantDone string) {
			setup()
			out := say(r, paired, kind, 2)
			Expect(out[0]).To(ContainSubstring(wantText))
			p := only()
			Expect(p.Kind).To(Equal(contract.ChangeKind(kind)))
			Expect(lastResult()).NotTo(ContainSubstring("Studio"))
			turns := modelTurns()
			out = say(r, paired, "yes", 1)
			Expect(out[0]).To(HavePrefix(wantDone))
			Expect(modelTurns()).To(Equal(turns))
			Expect(only()).To(haveStatus(contract.ProposalApplied))
			_, found := readHome(r.trigRig, p.Target+pageIndex(kind))
			Expect(found).To(BeTrue())
		},
		Entry("profile", "profile", "I can save a change of the rules of this agent (this agent only)", "Done:"),
		Entry("skill", "skill", "I can save a skill (every chat of the owner)", "Done:"),
		Entry("page", "page", "I can save a page (every chat of the owner)", "Done:"),
		Entry("dashboard", "dashboard", "I can save a change of the Home dashboard", "Done:"),
	)

	Describe("who may answer", func() {
		It("refuses a proposal of a sensitive kind to a sender who is only on the allowlist", func() {
			setup()
			sayIn(paired, "profile", 2)
			Expect(only().Source.Sender).To(Equal(paired))
			out := sayIn(listed, "yes", 1)
			Expect(out[0]).To(Equal("Only the owner can confirm this."))
			Expect(only()).To(haveStatus(contract.ProposalPending))
		})

		It("holds a note of a shared chat for the sender who asked", func() {
			setup()
			sayIn(listed, "note", 2)
			out := sayIn(paired, "yes", 1)
			Expect(out[0]).To(Equal("Only the person who asked can confirm this."))
			Expect(only()).To(haveStatus(contract.ProposalPending))
			out = sayIn(listed, "yes", 1)
			Expect(out[0]).To(HavePrefix("Saved:"))
		})

		It("lets another trusted sender answer in a chat of two", func() {
			setup()
			// Two accounts that write in the same private place.
			ingest := func(sender, text string) {
				Expect(r.conns.Ingest(ctx, "telegram", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{
					Chat: "500", Direct: true, SenderID: sender, SenderName: "S", Text: text}})).To(Succeed())
			}
			ingest(listed, "note")
			waitFor("the proposal", func() bool { return len(bot.answers()) >= 2 })
			ingest(paired, "yes")
			waitFor("the answer", func() bool { return len(bot.answers()) >= 3 })
			Expect(bot.answers()[2]).To(HavePrefix("Saved:"))
		})

		It("tells the model that the owner decides when the sender cannot confirm, writes nothing, and passes an ordinary yes on", func() {
			setupWith([]string{contract.CapMemoryWrite})
			out := say(r, listed, "note", 1)
			Expect(out[0]).To(Equal("I asked."), "the host wrote no proposal")
			res := lastResult()
			Expect(res).To(ContainSubstring("cannot confirm changes from this chat"))
			Expect(res).To(ContainSubstring("waits for the owner in Studio"))
			p := only()
			turns := modelTurns()
			out = say(r, listed, "yes", 1)
			Expect(out[0]).To(Equal("model"))
			Expect(modelTurns()).To(Equal(turns + 1))
			Expect(only()).To(haveStatus(contract.ProposalPending))
			// A word with the code is answered with a refusal.
			out = say(r, listed, "confirm "+studio.ChangeCode(p.ID), 1)
			Expect(out[0]).To(Equal("Only the owner can confirm this."))
			Expect(only()).To(haveStatus(contract.ProposalPending))
		})
	})

	Describe("time", func() {
		It("says that the proposal expired, soon after, and then lets the word through", func() {
			setup()
			say(r, listed, "note", 2)
			sh.clock.Advance(contract.ChangeExpiry + time.Minute)
			out := say(r, listed, "yes", 1)
			Expect(out[0]).To(Equal("That proposal expired. Ask again if you still want it."))
			Expect(only()).To(haveStatus(contract.ProposalExpired))
			_, found := readHome(r.trigRig, only().Target)
			Expect(found).To(BeFalse())
			sh.clock.Advance(2 * time.Hour)
			out = say(r, listed, "yes", 1)
			Expect(out[0]).To(Equal("model"))
		})

		It("does not undo after the window", func() {
			setup()
			say(r, listed, "note", 2)
			say(r, listed, "yes", 1)
			sh.clock.Advance(31 * time.Minute)
			out := say(r, listed, "undo", 1)
			Expect(out[0]).To(Equal("model"))
			Expect(only()).To(haveStatus(contract.ProposalApplied))
		})
	})

	Describe("two proposals at once", func() {
		It("asks which, and a code answers one", func() {
			setup()
			say(r, listed, "note", 2)
			say(r, listed, "note two", 2)
			ps := all()
			Expect(ps).To(HaveLen(2))
			turns := modelTurns()
			out := say(r, listed, "yes", 1)
			Expect(out[0]).To(ContainSubstring("More than one is waiting here."))
			for _, p := range ps {
				Expect(out[0]).To(ContainSubstring(studio.ChangeCode(p.ID) + ": "))
			}
			Expect(modelTurns()).To(Equal(turns))
			for _, p := range all() {
				Expect(p).To(haveStatus(contract.ProposalPending))
			}
			var food contract.ChangeProposal
			for _, p := range ps {
				if strings.HasSuffix(p.Target, "food.md") {
					food = p
				}
			}
			out = say(r, listed, "confirm "+studio.ChangeCode(food.ID), 1)
			Expect(out[0]).To(HavePrefix("Saved: Drinks"))
			for _, p := range all() {
				if p.ID == food.ID {
					Expect(p).To(haveStatus(contract.ProposalApplied))
				} else {
					Expect(p).To(haveStatus(contract.ProposalPending))
				}
			}
			// With one left, a bare word answers it.
			out = say(r, listed, "no", 1)
			Expect(out[0]).To(Equal("Cancelled."))
		})
	})

	Describe("another frontend", func() {
		It("answers a proposal that the first frontend stored, once", func() {
			setup()
			say(r, listed, "note", 2)
			p := only()
			second := newConnRigWith(sh, st, newTransport(), script, func(context.Context) bool { return false })
			second.svc.SetCapabilityGate(second.conns.CapabilityGate())
			out := say(second, listed, "yes", 1)
			Expect(out[0]).To(HavePrefix("Saved:"))
			got, err := second.svc.Changes().Get(ctx, alice, p.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(haveStatus(contract.ProposalApplied))
			Expect(only()).To(haveStatus(contract.ProposalApplied))
			out = say(r, listed, "yes", 1)
			Expect(out[0]).To(Equal("model"), "the answer is used once")
		})
	})
})

// pageIndex is the file of a page to look for after it is saved.
func pageIndex(kind string) string {
	if kind == "page" {
		return "/index.html"
	}
	return ""
}
