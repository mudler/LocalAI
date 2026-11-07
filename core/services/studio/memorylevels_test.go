package studio_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
)

// hostShared is a shared rig whose person has chosen the save level in the
// host's own setting (no add-on declares one).
func hostShared(st *saveSetting) *shared {
	sh := changeShared(grantAll)
	inner := sh.deps
	sh.deps = func(d *studio.Deps) {
		inner(d)
		d.UserSetting = st.get
	}
	return sh
}

var _ = Describe("the confirmation levels of a memory note", func() {
	ctx := context.Background()

	Describe("Always confirm in a chat of the person", func() {
		It("asks for each note, whatever an agent says, and the card never loosens", func() {
			st := &saveSetting{}
			st.putHost(studio.MemorySaveKey, studio.SaveAlways)
			r := hostShared(st).rig(changeScript("user", "- home: Rome\n"), studio.TriggerOptions{})
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "remember")
			idle(r.svc, alice, id)
			p := onlyProposal(r)
			Expect(p.Status).To(Equal(contract.ProposalPending))
			Expect(p.Auto).To(BeFalse())
			_, ok := readHome(r, "Memory/index.md")
			Expect(ok).To(BeFalse())
		})

		It("keeps the model from undoing or forgetting a note, and the note stays", func() {
			st := &saveSetting{}
			st.putHost(studio.MemorySaveKey, studio.SaveAuto)
			r := hostShared(st).rig(changeScript("user", "- home: Rome\n"), studio.TriggerOptions{})
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "remember")
			idle(r.svc, alice, id)
			Expect(onlyProposal(r).Status).To(Equal(contract.ProposalApplied))
			// The person now chooses Always confirm; the model cannot undo.
			st.putHost(studio.MemorySaveKey, studio.SaveAlways)
			sent(r.svc, alice, id, "forget")
			idle(r.svc, alice, id)
			res := r.h.ResultsFor("revert_change")
			Expect(res).To(HaveLen(1))
			Expect(res[0].Err).To(MatchError(ContainSubstring("Always confirm")))
			Expect(onlyProposal(r).Status).To(Equal(contract.ProposalApplied), "nothing was undone")
			_, ok := readHome(r, "Memory/index.md")
			Expect(ok).To(BeTrue())
		})

		It("lets the model undo when the level is auto, as before", func() {
			st := &saveSetting{}
			st.putHost(studio.MemorySaveKey, studio.SaveAuto)
			r := hostShared(st).rig(changeScript("user", "- home: Rome\n"), studio.TriggerOptions{})
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "remember")
			idle(r.svc, alice, id)
			sent(r.svc, alice, id, "forget")
			idle(r.svc, alice, id)
			Expect(r.h.ResultsFor("revert_change")[0].Err).To(BeNil())
			Expect(onlyProposal(r).Status).To(Equal(contract.ProposalReverted))
		})
	})

	Describe("the save rule of a saved agent", func() {
		run := func(user, rule string) (*trigRig, string) {
			st := &saveSetting{}
			if user != "" {
				st.putHost(studio.MemorySaveKey, user)
			}
			r := hostShared(st).rig(changeScript("agent", "- rule\n"), studio.TriggerOptions{})
			ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Helper", Model: "m", Memory: agents.MemoryOwn, MemorySave: rule, Builtins: []string{"memory.write"}})
			Expect(err).NotTo(HaveOccurred())
			rec, err := r.svc.Create(ctx, alice, studio.CreateInput{AgentID: ag.ID})
			Expect(err).NotTo(HaveOccurred())
			sent(r.svc, alice, rec.ID, "remember")
			idle(r.svc, alice, rec.ID)
			return r, ag.ID
		}

		It("saves at once when the person said auto and the agent has no rule", func() {
			r, _ := run(studio.SaveAuto, "")
			Expect(onlyProposal(r).Status).To(Equal(contract.ProposalApplied))
		})

		It("always confirm: the agent asks although the person said auto", func() {
			r, agent := run(studio.SaveAuto, agents.MemorySaveAlways)
			p := onlyProposal(r)
			Expect(p.Status).To(Equal(contract.ProposalPending))
			Expect(p.Auto).To(BeFalse())
			Expect(p.Agent).To(Equal(agent))
		})

		It("never: the agent reads and has no tool to save, although the person said auto", func() {
			r, _ := run(studio.SaveAuto, agents.MemorySaveNever)
			list, _ := r.svc.Changes().List(ctx, alice)
			Expect(list).To(BeEmpty())
			res := r.h.ResultsFor("propose_change")
			Expect(res).To(HaveLen(1))
			Expect(res[0].Err).To(MatchError(ContainSubstring("no tool named")))
		})

		It("cannot be looser than the person: the person's never wins over always", func() {
			r, _ := run(studio.SaveNever, agents.MemorySaveAlways)
			list, _ := r.svc.Changes().List(ctx, alice)
			Expect(list).To(BeEmpty())
			Expect(r.h.ResultsFor("propose_change")[0].Err).To(MatchError(ContainSubstring("no tool named")))
		})

		Describe("Save it and tell me (auto)", func() {
			runWith := func(script func(workspace.Actor, string) []studiotest.ToolStep, user, rule string) *trigRig {
				st := &saveSetting{}
				if user != "" {
					st.putHost(studio.MemorySaveKey, user)
				}
				r := hostShared(st).rig(script, studio.TriggerOptions{})
				ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Helper", Model: "m", Memory: agents.MemoryOwn, MemorySave: rule, Builtins: []string{"memory.write"}})
				Expect(err).NotTo(HaveOccurred())
				rec, err := r.svc.Create(ctx, alice, studio.CreateInput{AgentID: ag.ID})
				Expect(err).NotTo(HaveOccurred())
				sent(r.svc, alice, rec.ID, "remember")
				idle(r.svc, alice, rec.ID)
				return r
			}

			DescribeTable("loosens only the choices of the person that are not Always confirm or Never",
				func(user, rule string, want contract.ProposalStatus, auto bool) {
					r := runWith(changeScript("agent", "- rule\n"), user, rule)
					p := onlyProposal(r)
					Expect(p.Status).To(Equal(want))
					Expect(p.Auto).To(Equal(auto))
				},
				Entry("the person said ask: the agent saves at once", studio.SaveAsk, agents.MemorySaveAuto, contract.ProposalApplied, true),
				Entry("the person never chose: the agent saves at once", "", agents.MemorySaveAuto, contract.ProposalApplied, true),
				Entry("the person said auto: the same", studio.SaveAuto, agents.MemorySaveAuto, contract.ProposalApplied, true),
				Entry("the person said Always confirm: it still waits", studio.SaveAlways, agents.MemorySaveAuto, contract.ProposalPending, false),
				Entry("no agent rule and ask: it waits, as before", studio.SaveAsk, "", contract.ProposalPending, false),
			)

			It("has no tool to save when the person said never", func() {
				r := runWith(changeScript("agent", "- rule\n"), studio.SaveNever, agents.MemorySaveAuto)
				list, _ := r.svc.Changes().List(ctx, alice)
				Expect(list).To(BeEmpty())
				Expect(r.h.ResultsFor("propose_change")[0].Err).To(MatchError(ContainSubstring("no tool named")))
			})

			It("still waits for a note that looks like a secret", func() {
				r := runWith(changeScript("agent", "- password: hunter2hunter2\n"), studio.SaveAsk, agents.MemorySaveAuto)
				Expect(onlyProposal(r).Status).To(Equal(contract.ProposalPending))
			})

			It("still waits for a note over the size limit", func() {
				r := runWith(changeScript("agent", strings.Repeat("a note ", 700)), studio.SaveAsk, agents.MemorySaveAuto)
				Expect(len(strings.Repeat("a note ", 700))).To(BeNumerically(">", studio.MaxAutoWriteBytes))
				Expect(onlyProposal(r).Status).To(Equal(contract.ProposalPending))
			})

			It("can be undone by the person, and the note is one change set", func() {
				r := runWith(changeScript("agent", "- rule\n"), studio.SaveAsk, agents.MemorySaveAuto)
				p := onlyProposal(r)
				Expect(p.Rev).To(BeNumerically(">", 0))
			})

			It("never saves a knowledge change at once, whatever the agent says", func() {
				r := runWith(changeScript("agent", "- rule\n"), studio.SaveAsk, agents.MemorySaveAuto)
				list, err := r.svc.Changes().List(ctx, alice)
				Expect(err).NotTo(HaveOccurred())
				agentID := list[0].Agent
				_, err = r.svc.Changes().Propose(ctx, studio.ProposeInput{User: alice, Agent: agentID, Kind: "knowledge", Scope: "user",
					Target: "wiki", Content: "t", Summary: "s", Auto: true})
				Expect(err).To(HaveOccurred())
			})
		})

		It("is kept by a write that leaves it out, cleared by use_mine, accepts auto and refuses ask", func() {
			r := hostShared(&saveSetting{}).rig(changeScript("agent", "- x\n"), studio.TriggerOptions{})
			ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Helper", Model: "m", MemorySave: agents.MemorySaveNever})
			Expect(err).NotTo(HaveOccurred())
			Expect(ag.MemorySave).To(Equal("never"))
			// An older client writes without the field: the rule stays.
			ag, err = r.svc.SaveAgent(ctx, alice, agents.Agent{ID: ag.ID, Name: "Helper", Model: "m", Instructions: "x"})
			Expect(err).NotTo(HaveOccurred())
			Expect(ag.MemorySave).To(Equal("never"), "an older client cannot loosen the agent")
			_, err = r.svc.SaveAgent(ctx, alice, agents.Agent{ID: ag.ID, Name: "Helper", Model: "m", MemorySave: "ask"})
			Expect(err).To(MatchError(agents.ErrInvalid))
			ag, err = r.svc.SaveAgent(ctx, alice, agents.Agent{ID: ag.ID, Name: "Helper", Model: "m", MemorySave: agents.MemorySaveAuto})
			Expect(err).NotTo(HaveOccurred())
			Expect(ag.MemorySave).To(Equal("auto"))
			ag, err = r.svc.SaveAgent(ctx, alice, agents.Agent{ID: ag.ID, Name: "Helper", Model: "m", MemorySave: agents.MemorySaveUseMine})
			Expect(err).NotTo(HaveOccurred())
			Expect(ag.MemorySave).To(BeEmpty())
		})
	})

	Describe("when the setting cannot be read", func() {
		It("asks, whatever was stored", func() {
			st := &saveSetting{hostErr: fmt.Errorf("store down")}
			st.putHost(studio.MemorySaveKey, studio.SaveAuto)
			r := hostShared(st).rig(changeScript("user", "- home: Rome\n"), studio.TriggerOptions{})
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "remember")
			idle(r.svc, alice, id)
			Expect(onlyProposal(r).Status).To(Equal(contract.ProposalPending))
		})
	})

	Describe("in a messaging app", func() {
		const (
			listed = "11" // on the allowlist, not paired
			paired = "12" // paired
		)
		var (
			st  *saveSetting
			bot *scriptedBot
			r   *connRig
			b   connectors.Binding
		)
		// Every kind of change is allowed, so the model can undo the last one.
		caps := []string{contract.CapMemoryWrite, contract.CapProfileWrite, contract.CapSkillsWrite, contract.CapPagesWrite, contract.CapDashboardWrite, contract.CapProposalsConfirm}
		AfterEach(func() {
			if CurrentSpecReport().Failed() && r != nil {
				GinkgoWriter.Printf("ANSWERS %q\nRESULTS %+v\n", bot.answers(), r.h.ResultsFor("propose_change"))
			}
		})
		note := func(scope, name, content string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "propose_change", Args: proposeArgs("memory", scope, name, content, "A note")}, {Say: "ok"}}
		}
		setup := func(user, row string, agent agents.Agent) {
			st, bot = &saveSetting{}, newScriptedBot()
			if user != "" {
				st.putHost(studio.MemorySaveKey, user)
			}
			if row != "" {
				st.putHost(studio.MemorySaveMessagingKey, row)
			}
			addon := studiobot.Telegram(&transport.API{BaseURL: bot.srv.URL, Client: bot.srv.Client()})
			addon.Kind.Permissions = caps
			script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
				switch {
				case text == "remember":
					return note("", "index.md", "- standup at 09:30\n")
				case text == "remember channel":
					return note("channel", "index.md", "- standup at 09:30\n")
				case text == "remember user":
					return note("user", "index.md", "- standup at 09:30\n")
				case text == "remember agent":
					return note("agent", "index.md", "- standup at 09:30\n")
				case text == "remember secret":
					return note("channel", "index.md", "- password: hunter2hunter2\n")
				case text == "remember many":
					var steps []studiotest.ToolStep
					for i := range 6 {
						steps = append(steps, note("channel", fmt.Sprintf("n%d.md", i), fmt.Sprintf("- note %d\n", i))[0])
					}
					return append(steps, studiotest.ToolStep{Say: "ok"})
				case text == "forget":
					return []studiotest.ToolStep{{Tool: "revert_change", Args: `{}`}, {Say: "ok"}}
				}
				return []studiotest.ToolStep{{Say: "model"}}
			}
			sh := hostShared(st)
			r = newConnRigWith(sh, connStore(), studiobot.NewTransport(addon), script, nil)
			r.bot, r.untrusted = bot, true
			r.svc.SetCapabilityGate(r.conns.CapabilityGate())
			r.grantBuiltins(contract.CapMemoryWrite)
			if agent.Memory != "" || agent.MemorySave != "" {
				ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{ID: r.agentID(), Name: "Support", Model: "m", Addons: []string{"office"},
					Memory: agent.Memory, MemorySave: agent.MemorySave, Builtins: []string{contract.CapMemoryWrite}})
				Expect(err).NotTo(HaveOccurred())
				r.agent = ag
			}
			b = r.bind(studio.ConnectorInput{Allow: []string{listed, paired, "-100"}, Caps: caps})
			got, err := r.store.GetBinding(ctx, b.ID)
			Expect(err).NotTo(HaveOccurred())
			got.Paired = append(got.Paired, connectors.PairedSender{SenderID: paired, At: sh.clock.Now()})
			Expect(r.store.PutBinding(ctx, got)).To(Succeed())
			b = got
		}
		// say writes in a private chat (direct) or in the group and waits for the answers.
		say := func(direct bool, sender, text string, n int) {
			before := len(bot.answers())
			m := contract.ConnectorMessage{Chat: sender, Direct: true, SenderID: sender, SenderName: "S", Text: text}
			if !direct {
				m = contract.ConnectorMessage{Chat: "-100", Mention: true, SenderID: sender, SenderName: "S", Text: text}
			}
			Expect(r.conns.Ingest(ctx, "telegram", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: m})).To(Succeed())
			waitFor("the answers", func() bool { return len(bot.answers()) >= before+n })
			r.sh.clock.Advance(15 * time.Second)
		}
		list := func() []contract.ChangeProposal {
			l, err := r.svc.Changes().List(ctx, alice)
			Expect(err).NotTo(HaveOccurred())
			return l
		}
		channelNote := func() (string, bool) {
			l := list()
			Expect(l).NotTo(BeEmpty())
			return readHome(r.trigRig, "Memory/channels/"+channelDirOf(l[0])+"/index.md")
		}

		It("asks by default, and a note goes to the conversation when no scope is named", func() {
			setup(studio.SaveAuto, "", agents.Agent{})
			say(true, paired, "remember", 2)
			p := list()[0]
			Expect(p.Status).To(Equal(contract.ProposalPending), "the row is ask: a paired sender in a private chat still asks")
			Expect(p.Scope).To(Equal(contract.ScopeChannel))
			Expect(p.Auto).To(BeFalse())
		})

		It("saves at once to the conversation for a paired sender in a private chat, with an Undo", func() {
			setup(studio.SaveAsk, studio.MessagingPairedPrivate, agents.Agent{})
			say(true, paired, "remember", 1)
			p := list()[0]
			Expect(p.Status).To(Equal(contract.ProposalApplied))
			Expect(p.Auto).To(BeTrue())
			Expect(p.Scope).To(Equal(contract.ScopeChannel))
			Expect(p.Rev).To(BeNumerically(">", 0), "one change set that can be undone")
			text, ok := channelNote()
			Expect(ok).To(BeTrue())
			Expect(text).To(Equal("- standup at 09:30\n"))
			_, ok = readHome(r.trigRig, "Memory/index.md")
			Expect(ok).To(BeFalse(), "nothing reached the notes of the person")
			// The model can take it back: the level is not always.
			say(true, paired, "forget", 1)
			Expect(list()[0].Status).To(Equal(contract.ProposalReverted))
		})

		DescribeTable("asks in every other case of the row paired_private",
			func(direct bool, sender string) {
				setup(studio.SaveAuto, studio.MessagingPairedPrivate, agents.Agent{})
				say(direct, sender, "remember", 2)
				p := list()[0]
				Expect(p.Status).To(Equal(contract.ProposalPending))
				Expect(p.Auto).To(BeFalse())
			},
			Entry("a listed sender who is not paired, in a private chat", true, listed),
			Entry("a paired sender in a group", false, paired),
			Entry("a listed sender in a group", false, listed),
		)

		It("is not loosened by the row when the person chose Always confirm", func() {
			setup(studio.SaveAlways, studio.MessagingPairedPrivate, agents.Agent{})
			say(true, paired, "remember", 2)
			Expect(list()[0].Status).To(Equal(contract.ProposalPending))
			Expect(list()[0].Auto).To(BeFalse())
		})

		It("is not loosened by the row when the agent says always", func() {
			setup(studio.SaveAuto, studio.MessagingPairedPrivate, agents.Agent{Memory: agents.MemoryMineAndOwn, MemorySave: agents.MemorySaveAlways})
			say(true, paired, "remember", 2)
			Expect(list()[0].Status).To(Equal(contract.ProposalPending))
		})

		Describe("with the agent rule Save it and tell me", func() {
			auto := agents.Agent{Memory: agents.MemoryOwn, MemorySave: agents.MemorySaveAuto}
			DescribeTable("lifts the messaging row only for a paired sender in a one-to-one chat",
				func(user, row string, direct bool, sender string, want contract.ProposalStatus) {
					setup(user, row, auto)
					say(direct, sender, "remember", map[bool]int{true: 1, false: 2}[want == contract.ProposalApplied])
					p := list()[0]
					Expect(p.Status).To(Equal(want))
					Expect(p.Auto).To(Equal(want == contract.ProposalApplied))
				},
				Entry("row ask, person ask, paired private chat", studio.SaveAsk, studio.MessagingAsk, true, paired, contract.ProposalApplied),
				Entry("row unset, paired private chat", studio.SaveAsk, "", true, paired, contract.ProposalApplied),
				Entry("an allowlisted sender who is not paired", studio.SaveAsk, studio.MessagingAsk, true, listed, contract.ProposalPending),
				Entry("a paired sender in a group", studio.SaveAsk, studio.MessagingAsk, false, paired, contract.ProposalPending),
				Entry("an allowlisted sender in a group", studio.SaveAsk, studio.MessagingAsk, false, listed, contract.ProposalPending),
				Entry("Always confirm of the person holds", studio.SaveAlways, studio.MessagingAsk, true, paired, contract.ProposalPending),
			)

			It("shows in the access view that a paired sender's notes are saved at once and the others wait", func() {
				setup(studio.SaveAsk, studio.MessagingAsk, auto)
				v, err := r.conns.Access(ctx, alice, b.ID)
				Expect(err).NotTo(HaveOccurred())
				got := map[string]string{}
				for _, sub := range v.Subjects {
					got[sub.ID] = sub.NoteSave
				}
				Expect(got).To(HaveKeyWithValue("paired", "now"))
				Expect(got).To(HaveKeyWithValue("listed", "ask"))
				Expect(got).To(HaveKeyWithValue("anyone", "ask"))
			})

			It("shows nothing for an agent that has no such rule", func() {
				setup(studio.SaveAsk, studio.MessagingAsk, agents.Agent{Memory: agents.MemoryOwn})
				v, err := r.conns.Access(ctx, alice, b.ID)
				Expect(err).NotTo(HaveOccurred())
				for _, sub := range v.Subjects {
					Expect(sub.NoteSave).To(BeEmpty(), sub.ID)
				}
			})

			It("keeps the secret check, with the Undo for what is saved", func() {
				setup(studio.SaveAsk, studio.MessagingAsk, auto)
				say(true, paired, "remember secret", 2)
				Expect(list()[0].Status).To(Equal(contract.ProposalPending))
			})

			It("keeps the limit of five notes a reply", func() {
				setup(studio.SaveAsk, studio.MessagingAsk, auto)
				say(true, paired, "remember many", 1)
				applied := 0
				for _, p := range list() {
					if p.Status == contract.ProposalApplied {
						applied++
					}
				}
				Expect(applied).To(Equal(studio.MaxAutoWritesPerTurn))
			})

			It("saves to the conversation and can be undone", func() {
				setup(studio.SaveAsk, studio.MessagingAsk, auto)
				say(true, paired, "remember", 1)
				Expect(list()[0].Rev).To(BeNumerically(">", 0))
				say(true, paired, "forget", 1)
				Expect(list()[0].Status).To(Equal(contract.ProposalReverted))
			})

			It("does not reach the notes of the person or of the agent without a confirmation", func() {
				setup(studio.SaveAsk, studio.MessagingAsk, agents.Agent{Memory: agents.MemoryMine, MemorySave: agents.MemorySaveAuto})
				say(true, paired, "remember user", 2)
				Expect(list()[0].Status).To(Equal(contract.ProposalPending))
			})
		})

		DescribeTable("offers no way to save when the rule is never",
			func(user, row string, agent agents.Agent) {
				setup(user, row, agent)
				say(true, paired, "hi", 1)
				chats, err := r.svc.List(ctx, alice, 10)
				Expect(err).NotTo(HaveOccurred())
				Expect(chats).NotTo(BeEmpty())
				Expect(r.h.ToolNames(chats[0].ID)).NotTo(ContainElement("propose_change"))
			},
			Entry("the row is never", studio.SaveAuto, studio.MessagingNever, agents.Agent{}),
			Entry("the person said never", studio.SaveNever, studio.MessagingPairedPrivate, agents.Agent{}),
			Entry("the agent says never", studio.SaveAuto, studio.MessagingPairedPrivate, agents.Agent{Memory: agents.MemoryOwn, MemorySave: agents.MemorySaveNever}),
		)

		It("still refuses for an agent whose memory is none", func() {
			setup(studio.SaveAsk, studio.MessagingPairedPrivate, agents.Agent{Memory: agents.MemoryNone})
			say(true, paired, "hi", 1)
			chats, err := r.svc.List(ctx, alice, 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.h.ToolNames(chats[0].ID)).NotTo(ContainElement("propose_change"))
		})

		It("keeps the limit on a secret: a note that looks like one waits", func() {
			setup(studio.SaveAsk, studio.MessagingPairedPrivate, agents.Agent{})
			say(true, paired, "remember secret", 2)
			Expect(list()[0].Status).To(Equal(contract.ProposalPending), "a note that looks like a secret still waits")
		})

		It("keeps the limit of five notes a reply: no sixth note is saved at once", func() {
			setup(studio.SaveAsk, studio.MessagingPairedPrivate, agents.Agent{})
			say(true, paired, "remember many", 1)
			applied := 0
			for _, p := range list() {
				if p.Status == contract.ProposalApplied {
					applied++
				}
			}
			Expect(applied).To(Equal(studio.MaxAutoWritesPerTurn))
			Expect(list()).To(HaveLen(studio.MaxAutoWritesPerTurn), "the sixth is refused by the hourly limit, not saved")
		})

		It("saves to the agent's or the person's notes only after a paired sender confirms, never at once", func() {
			setup(studio.SaveAsk, studio.MessagingPairedPrivate, agents.Agent{})
			say(true, paired, "remember user", 2)
			p := list()[0]
			Expect(p.Scope).To(Equal(contract.ScopeUser))
			Expect(p.Status).To(Equal(contract.ProposalPending), "the row saves to the conversation only")
			_, ok := readHome(r.trigRig, "Memory/index.md")
			Expect(ok).To(BeFalse())
		})

		It("refuses a note for the person from a sender who is not paired, and says use the conversation", func() {
			setup(studio.SaveAsk, studio.MessagingAsk, agents.Agent{})
			say(true, listed, "remember user", 1)
			Expect(list()).To(BeEmpty())
			res := r.h.ResultsFor("propose_change")
			Expect(res).To(HaveLen(1))
			Expect(res[0].Err).To(MatchError(ContainSubstring("paired")))
		})

		It("lets only the paired sender who asked answer a note under Always confirm", func() {
			setup(studio.SaveAlways, "", agents.Agent{})
			say(true, paired, "remember channel", 2)
			Expect(list()[0].Status).To(Equal(contract.ProposalPending))
			// A listed sender who is not paired cannot confirm it in a group.
			say(false, listed, "yes", 1)
			Expect(list()[0].Status).To(Equal(contract.ProposalPending))
			// The paired sender who asked can.
			say(true, paired, "yes", 1)
			Expect(list()[0].Status).To(Equal(contract.ProposalApplied))
		})

		It("holds a note for the person for a paired answer even when the level is ask", func() {
			setup(studio.SaveAsk, "", agents.Agent{})
			say(true, paired, "remember agent", 2)
			p := list()[0]
			Expect(p.Scope).To(Equal(contract.ScopeAgent))
			Expect(p.Status).To(Equal(contract.ProposalPending))
			say(true, listed, "yes", 1)
			Expect(list()[0].Status).To(Equal(contract.ProposalPending), "a listed sender who is not paired may not confirm it")
			Expect(p.Source.Kind).To(Equal(session.OriginConnector))
		})
	})
})

// channelDirOf is the folder name of the conversation of a proposal.
func channelDirOf(p contract.ChangeProposal) string {
	return proposals.ChannelDir(p.Source)
}
