package studio_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
)

// saveSetting is the stored choice of the person in a spec.
type saveSetting struct {
	mu   sync.Mutex
	val  string
	set  bool
	err  error
	read int
}

func (s *saveSetting) put(v string) { s.mu.Lock(); s.val, s.set = v, v != ""; s.mu.Unlock() }

func (s *saveSetting) get(_ context.Context, user, addon, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.read++
	if addon != "notes-addon" || key != "save-mode" {
		return "", false, nil
	}
	return s.val, s.set, s.err
}

// memorySetting is what an add-on declares to be the memory save setting.
var memorySetting = contract.UserSetting{Key: "save-mode", Values: []string{"ask", "auto", "never"}, Default: "ask", Role: contract.UserSettingRoleMemorySave}

func settingShared(st *saveSetting) *shared {
	sh := changeShared(grantAll)
	sh.addons.userSettings = []studio.AddonUserSettingSet{{AddonID: "notes-addon", Running: true, Settings: []contract.UserSetting{memorySetting}}}
	inner := sh.deps
	sh.deps = func(d *studio.Deps) {
		inner(d)
		d.UserSetting = st.get
	}
	return sh
}

// notes proposes n memory notes in one turn, each in its own file.
func notes(n int, content func(i int) string) func(workspace.Actor, string) []studiotest.ToolStep {
	return func(_ workspace.Actor, text string) []studiotest.ToolStep {
		switch text {
		case "remember":
			var steps []studiotest.ToolStep
			for i := range n {
				steps = append(steps, studiotest.ToolStep{Tool: "propose_change", Args: proposeArgs("memory", "user", fmt.Sprintf("n%d.md", i), content(i), fmt.Sprintf("note %d", i))})
			}
			return append(steps, studiotest.ToolStep{Say: "ok"})
		case "forget":
			return []studiotest.ToolStep{{Tool: "revert_change", Args: `{}`}, {Say: "ok"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
}

var _ = DescribeTable("the mode that applies to a memory write",
	func(stored, origin string, sub bool, want string) {
		Expect(studio.EffectiveSaveMode(stored, origin, sub)).To(Equal(want))
	},
	// The person's own chat reads the setting as it is.
	Entry("chat, unset", "", "", false, studio.SaveAsk),
	Entry("chat, ask", "ask", "", false, studio.SaveAsk),
	Entry("chat, auto", "auto", "", false, studio.SaveAuto),
	Entry("chat, never", "never", "", false, studio.SaveNever),
	Entry("chat, a value nobody declared", "always", "", false, studio.SaveAsk),
	// A connector chat never saves unasked, and never means never.
	Entry("connector, unset", "", session.OriginConnector, false, studio.SaveAsk),
	Entry("connector, ask", "ask", session.OriginConnector, false, studio.SaveAsk),
	Entry("connector, auto", "auto", session.OriginConnector, false, studio.SaveAsk),
	Entry("connector, never", "never", session.OriginConnector, false, studio.SaveNever),
	// An unattended run has no tool; the rule still holds if one ever had.
	Entry("trigger, auto", "auto", session.OriginTrigger, false, studio.SaveAsk),
	Entry("trigger, never", "never", session.OriginTrigger, false, studio.SaveNever),
	Entry("job, auto", "auto", session.OriginJob, false, studio.SaveAsk),
	Entry("job, ask", "ask", session.OriginJob, false, studio.SaveAsk),
	// A sub-agent is not the person.
	Entry("sub-agent, auto", "auto", "", true, studio.SaveAsk),
	Entry("sub-agent, never", "never", "", true, studio.SaveNever),
)

var _ = Describe("how a memory note is saved", func() {
	ctx := context.Background()

	Describe("in a plain Studio chat", func() {
		It("asks first when the person has not chosen", func() {
			st := &saveSetting{}
			r := settingShared(st).rig(changeScript("user", "- home: Rome\n"), studio.TriggerOptions{})
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "remember")
			idle(r.svc, alice, id)
			p := onlyProposal(r)
			Expect(p.Status).To(Equal(contract.ProposalPending))
			Expect(p.Auto).To(BeFalse())
			_, ok := readHome(r, "Memory/index.md")
			Expect(ok).To(BeFalse())
		})

		It("saves at once when the person chose auto, and the note can be undone", func() {
			st := &saveSetting{}
			st.put("auto")
			r := settingShared(st).rig(changeScript("user", "- home: Rome\n"), studio.TriggerOptions{})
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "remember")
			idle(r.svc, alice, id)
			res := r.h.ResultsFor("propose_change")
			Expect(res).To(HaveLen(1))
			Expect(res[0].Err).To(BeNil(), "%+v", res)
			Expect(res[0].Out).To(HavePrefix("Saved to memory"))
			Expect(res[0].Out).NotTo(ContainSubstring("Nothing is saved yet"))
			p := onlyProposal(r)
			Expect(p.Status).To(Equal(contract.ProposalApplied))
			Expect(p.Auto).To(BeTrue())
			Expect(p.Rev).To(BeNumerically(">", 0), "a change set the person can undo")
			text, ok := readHome(r, "Memory/index.md")
			Expect(ok).To(BeTrue())
			Expect(text).To(Equal("- home: Rome\n"))

			// The audit says the write was automatic, and holds no content.
			events, err := r.svc.Changes().Events(ctx, alice, 20)
			Expect(err).NotTo(HaveOccurred())
			var actions []string
			for _, e := range events {
				actions = append(actions, e.Action)
				Expect(e.Detail).To(ContainSubstring("automatic"))
				Expect(fmt.Sprintf("%+v", e)).NotTo(ContainSubstring("Rome"))
			}
			Expect(actions).To(Equal([]string{"confirm", "propose"}))

			// The model undoes it with revert_change.
			sent(r.svc, alice, id, "forget")
			idle(r.svc, alice, id)
			Expect(r.h.ResultsFor("revert_change")).To(HaveLen(1))
			Expect(r.h.ResultsFor("revert_change")[0].Err).To(BeNil())
			_, ok = readHome(r, "Memory/index.md")
			Expect(ok).To(BeFalse(), "the undo left the note")
			back, _ := r.svc.Changes().Get(ctx, alice, p.ID)
			Expect(back.Status).To(Equal(contract.ProposalReverted))
		})

		It("lets the person undo an automatic note from the card", func() {
			st := &saveSetting{}
			st.put("auto")
			r := settingShared(st).rig(changeScript("user", "- home: Rome\n"), studio.TriggerOptions{})
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "remember")
			idle(r.svc, alice, id)
			p := onlyProposal(r)
			back, err := r.svc.Changes().Revert(ctx, alice, p.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(back.Status).To(Equal(contract.ProposalReverted))
			_, ok := readHome(r, "Memory/index.md")
			Expect(ok).To(BeFalse())
		})

		It("offers no way to propose memory when the person chose never", func() {
			st := &saveSetting{}
			st.put("never")
			r := settingShared(st).rig(changeScript("user", "- home: Rome\n"), studio.TriggerOptions{})
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "remember")
			idle(r.svc, alice, id)
			// A plain chat still proposes the other kinds, but memory is not among them.
			schema := r.h.ToolSchema(id, "propose_change")
			Expect(schema).To(ContainSubstring(`"enum":["profile"`))
			Expect(schema).NotTo(ContainSubstring(`"memory"`))
			list, _ := r.svc.Changes().List(ctx, alice)
			Expect(list).To(BeEmpty())
			_, ok := readHome(r, "Memory/index.md")
			Expect(ok).To(BeFalse())
		})

		It("refuses a memory call with a plain sentence when the tool was built before the choice", func() {
			// The person turns memory off between the build of the tools and the call.
			st := &saveSetting{}
			r := settingShared(st).rig(changeScript("user", "- home: Rome\n"), studio.TriggerOptions{})
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "hi")
			idle(r.svc, alice, id)
			st.put("never")
			sent(r.svc, alice, id, "remember")
			idle(r.svc, alice, id)
			list, _ := r.svc.Changes().List(ctx, alice)
			Expect(list).To(BeEmpty())
		})

		It("reads a failing store as ask, never as auto", func() {
			st := &saveSetting{err: errors.New("down")}
			st.put("auto")
			r := settingShared(st).rig(changeScript("user", "- home: Rome\n"), studio.TriggerOptions{})
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "remember")
			idle(r.svc, alice, id)
			Expect(onlyProposal(r).Status).To(Equal(contract.ProposalPending))
		})

		It("asks when no add-on declares the setting, whatever an old answer says", func() {
			st := &saveSetting{}
			st.put("auto")
			sh := settingShared(st)
			sh.addons.userSettings = nil
			r := sh.rig(changeScript("user", "- home: Rome\n"), studio.TriggerOptions{})
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "remember")
			idle(r.svc, alice, id)
			Expect(onlyProposal(r).Status).To(Equal(contract.ProposalPending))
		})

		It("ignores a setting of another role", func() {
			st := &saveSetting{}
			st.put("auto")
			sh := settingShared(st)
			other := memorySetting
			other.Role = ""
			sh.addons.userSettings = []studio.AddonUserSettingSet{{AddonID: "notes-addon", Running: true, Settings: []contract.UserSetting{other}}}
			r := sh.rig(changeScript("user", "- home: Rome\n"), studio.TriggerOptions{})
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "remember")
			idle(r.svc, alice, id)
			Expect(onlyProposal(r).Status).To(Equal(contract.ProposalPending))
		})

		It("does not save at once through an add-on that is not running", func() {
			st := &saveSetting{}
			st.put("auto")
			sh := settingShared(st)
			sh.addons.userSettings[0].Running = false
			r := sh.rig(changeScript("user", "- home: Rome\n"), studio.TriggerOptions{})
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "remember")
			idle(r.svc, alice, id)
			Expect(onlyProposal(r).Status).To(Equal(contract.ProposalPending))
		})

		It("lets the strictest add-on win when two declare the setting", func() {
			st := &saveSetting{}
			st.put("auto")
			sh := settingShared(st)
			// The second add-on's answer is not stored: it counts as ask.
			sh.addons.userSettings = append(sh.addons.userSettings, studio.AddonUserSettingSet{AddonID: "other-addon", Running: true, Settings: []contract.UserSetting{memorySetting}})
			r := sh.rig(changeScript("user", "- home: Rome\n"), studio.TriggerOptions{})
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "remember")
			idle(r.svc, alice, id)
			Expect(onlyProposal(r).Status).To(Equal(contract.ProposalPending))
		})

		It("reads a broker that cannot list the settings as ask", func() {
			st := &saveSetting{}
			st.put("auto")
			sh := settingShared(st)
			sh.addons.settingsErr = errors.New("down")
			r := sh.rig(changeScript("user", "- home: Rome\n"), studio.TriggerOptions{})
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "remember")
			idle(r.svc, alice, id)
			Expect(onlyProposal(r).Status).To(Equal(contract.ProposalPending))
		})

		It("asks without a settings store at all", func() {
			sh := changeShared(grantAll)
			r := sh.rig(changeScript("user", "- home: Rome\n"), studio.TriggerOptions{})
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "remember")
			idle(r.svc, alice, id)
			Expect(onlyProposal(r).Status).To(Equal(contract.ProposalPending))
		})

		It("saves at most five notes in a turn and asks about the rest", func() {
			st := &saveSetting{}
			st.put("auto")
			r := settingShared(st).rig(notes(7, func(i int) string { return fmt.Sprintf("- fact %d\n", i) }), studio.TriggerOptions{})
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "remember")
			idle(r.svc, alice, id)
			res := r.h.ResultsFor("propose_change")
			Expect(res).To(HaveLen(7))
			applied := 0
			for i, x := range res {
				if x.Err == nil && strings.HasPrefix(x.Out, "Saved to memory") {
					applied++
					Expect(i).To(BeNumerically("<", studio.MaxAutoWritesPerTurn))
				}
			}
			Expect(applied).To(Equal(studio.MaxAutoWritesPerTurn))
			list, _ := r.svc.Changes().List(ctx, alice, contract.ProposalApplied)
			Expect(list).To(HaveLen(studio.MaxAutoWritesPerTurn))
			for i := range studio.MaxAutoWritesPerTurn {
				_, ok := readHome(r, fmt.Sprintf("Memory/n%d.md", i))
				Expect(ok).To(BeTrue())
			}
			// Whatever came after the cap was not saved unasked.
			for i := studio.MaxAutoWritesPerTurn; i < 7; i++ {
				_, ok := readHome(r, fmt.Sprintf("Memory/n%d.md", i))
				Expect(ok).To(BeFalse(), "note %d was saved over the cap", i)
			}
		})

		It("asks about a note over the size cap and about one that looks like a secret", func() {
			st := &saveSetting{}
			st.put("auto")
			big := strings.Repeat("a", studio.MaxAutoWriteBytes+1)
			r := settingShared(st).rig(notes(2, func(i int) string {
				if i == 0 {
					return big
				}
				return "- the api key: sk-abcdefghijklmnopqrstuvwxyz0123\n"
			}), studio.TriggerOptions{})
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "remember")
			idle(r.svc, alice, id)
			res := r.h.ResultsFor("propose_change")
			Expect(res).To(HaveLen(2))
			for _, x := range res {
				Expect(x.Err).To(BeNil(), "%+v", x)
				Expect(x.Out).To(ContainSubstring("needs a confirmation"))
				Expect(x.Out).NotTo(HavePrefix("Saved to memory"))
			}
			list, _ := r.svc.Changes().List(ctx, alice, contract.ProposalPending)
			Expect(list).To(HaveLen(2))
			for _, p := range list {
				Expect(p.Auto).To(BeFalse())
			}
		})

		It("still applies the checks of a proposal to an automatic note", func() {
			st := &saveSetting{}
			st.put("auto")
			// A target outside Memory and a file name that is not a note.
			script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
				if text == "remember" {
					return []studiotest.ToolStep{{Tool: "propose_change", Args: proposeArgs("memory", "user", "../../x.md", "x", "bad")}, {Say: "ok"}}
				}
				return []studiotest.ToolStep{{Say: "ok"}}
			}
			r := settingShared(st).rig(script, studio.TriggerOptions{})
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "remember")
			idle(r.svc, alice, id)
			res := r.h.ResultsFor("propose_change")
			Expect(res).To(HaveLen(1))
			Expect(res[0].Err).To(HaveOccurred())
			list, _ := r.svc.Changes().List(ctx, alice)
			Expect(list).To(BeEmpty())
		})

		It("leaves nothing pending when a note is the same as the file", func() {
			st := &saveSetting{}
			st.put("auto")
			r := settingShared(st).rig(changeScript("user", "- home: Rome\n"), studio.TriggerOptions{})
			_, err := r.sh.files.WriteHome(ctx, alice, "Memory/index.md", "- home: Rome\n", "seed", 1024)
			Expect(err).NotTo(HaveOccurred())
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "remember")
			idle(r.svc, alice, id)
			Expect(r.h.ResultsFor("propose_change")[0].Err).To(HaveOccurred())
			list, _ := r.svc.Changes().List(ctx, alice, contract.ProposalPending)
			Expect(list).To(BeEmpty())
		})
	})

	Describe("kinds other than memory", func() {
		It("always asks, whatever the setting", func() {
			st := &saveSetting{}
			st.put("auto")
			script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
				if text == "go" {
					return []studiotest.ToolStep{{Tool: "propose_change", Args: proposeArgs("profile", "agent", "", "- be brief\n", "A rule")}, {Say: "ok"}}
				}
				return []studiotest.ToolStep{{Say: "ok"}}
			}
			r := settingShared(st).rig(script, studio.TriggerOptions{})
			ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "A", Model: "m", Builtins: []string{"profile.write"}})
			Expect(err).NotTo(HaveOccurred())
			rec, err := r.svc.Create(ctx, alice, studio.CreateInput{AgentID: ag.ID})
			Expect(err).NotTo(HaveOccurred())
			sent(r.svc, alice, rec.ID, "go")
			idle(r.svc, alice, rec.ID)
			Expect(onlyProposal(r).Status).To(Equal(contract.ProposalPending))
		})

		It("keeps the other kinds when memory is turned off", func() {
			st := &saveSetting{}
			st.put("never")
			r := settingShared(st).rig(func(workspace.Actor, string) []studiotest.ToolStep { return nil }, studio.TriggerOptions{})
			ag, _ := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "A", Model: "m", Builtins: []string{"memory.write", "pages.write"}})
			rec, _ := r.svc.Create(ctx, alice, studio.CreateInput{AgentID: ag.ID})
			sent(r.svc, alice, rec.ID, "hi")
			idle(r.svc, alice, rec.ID)
			schema := r.h.ToolSchema(rec.ID, "propose_change")
			Expect(schema).To(ContainSubstring(`"enum":["page"]`))
			Expect(schema).NotTo(ContainSubstring(`"memory"`))
		})
	})

	Describe("in a chat that runs a saved agent", func() {
		run := func(setting string, grants ...string) (*trigRig, string) {
			st := &saveSetting{}
			st.put(setting)
			r := settingShared(st).rig(changeScript("agent", "- rule\n"), studio.TriggerOptions{})
			ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Helper", Model: "m", Builtins: grants})
			Expect(err).NotTo(HaveOccurred())
			rec, err := r.svc.Create(ctx, alice, studio.CreateInput{AgentID: ag.ID})
			Expect(err).NotTo(HaveOccurred())
			sent(r.svc, alice, rec.ID, "remember")
			idle(r.svc, alice, rec.ID)
			return r, ag.ID
		}

		It("still asks when the person said ask, even with its own grant", func() {
			r, _ := run("ask", "memory.write")
			Expect(onlyProposal(r).Status).To(Equal(contract.ProposalPending))
		})

		It("saves at once when the person said auto and the agent holds the grant", func() {
			r, agent := run("auto", "memory.write")
			p := onlyProposal(r)
			Expect(p.Status).To(Equal(contract.ProposalApplied))
			Expect(p.Agent).To(Equal(agent))
			_, ok := readHome(r, "Memory/agents/"+agent+"/index.md")
			Expect(ok).To(BeTrue())
		})

		It("is not widened by the setting: an agent without the grant has no tool", func() {
			r, _ := run("auto")
			list, _ := r.svc.Changes().List(ctx, alice)
			Expect(list).To(BeEmpty())
		})

		It("follows never as well, and the tool is not offered to an agent that only holds memory", func() {
			r, _ := run("never", "memory.write")
			list, _ := r.svc.Changes().List(ctx, alice)
			Expect(list).To(BeEmpty())
			res := r.h.ResultsFor("propose_change")
			Expect(res).To(HaveLen(1))
			Expect(res[0].Err).To(MatchError(ContainSubstring("no tool named")))
		})
	})

	Describe("in a chat from a connector", func() {
		for _, setting := range []string{"ask", "auto"} {
			It("keeps asking when the setting is "+setting, func() {
				st := &saveSetting{}
				st.put(setting)
				bot := newScriptedBot()
				addon := studiobot.Telegram(&transport.API{BaseURL: bot.srv.URL, Client: bot.srv.Client()})
				addon.Kind.Permissions = []string{contract.CapMemoryWrite}
				script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
					if text == "remember" {
						return []studiotest.ToolStep{{Tool: "propose_change", Args: proposeArgs("memory", "channel", "index.md", "- home: Rome\n", "Where the user lives")}, {Say: "done"}}
					}
					return []studiotest.ToolStep{{Say: "hello"}}
				}
				r := newConnRigWith(settingShared(st), connStore(), studiobot.NewTransport(addon), script, nil)
				r.bot = bot
				r.grantBuiltins(contract.CapMemoryWrite)
				b := r.bind(studio.ConnectorInput{Allow: []string{"11"}, Caps: []string{contract.CapMemoryWrite}})
				Expect(r.conns.Ingest(ctx, "telegram", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{
					Chat: "11", Direct: true, SenderID: "11", SenderName: "S", Text: "remember"}})).To(Succeed())
				waitFor("the answer", func() bool { return len(bot.answers()) >= 1 })
				res := r.h.ResultsFor("propose_change")
				Expect(res).To(HaveLen(1))
				Expect(res[0].Err).To(BeNil(), "%+v", res)
				Expect(res[0].Out).To(ContainSubstring("Nothing is saved yet"))
				p := onlyProposal(r.trigRig)
				Expect(p.Status).To(Equal(contract.ProposalPending), "a stranger's message must not write memory unconfirmed")
				Expect(p.Auto).To(BeFalse())
				Expect(p.Source.Kind).To(Equal(session.OriginConnector))
			})
		}

		It("hides memory from the connector chat when the person chose never", func() {
			st := &saveSetting{}
			st.put("never")
			bot := newScriptedBot()
			addon := studiobot.Telegram(&transport.API{BaseURL: bot.srv.URL, Client: bot.srv.Client()})
			addon.Kind.Permissions = []string{contract.CapMemoryWrite}
			script := func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "hello"}} }
			r := newConnRigWith(settingShared(st), connStore(), studiobot.NewTransport(addon), script, nil)
			r.bot = bot
			r.grantBuiltins(contract.CapMemoryWrite)
			b := r.bind(studio.ConnectorInput{Allow: []string{"11"}, Caps: []string{contract.CapMemoryWrite}})
			Expect(r.conns.Ingest(ctx, "telegram", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{
				Chat: "11", Direct: true, SenderID: "11", SenderName: "S", Text: "hi"}})).To(Succeed())
			waitFor("the answer", func() bool { return len(bot.answers()) >= 1 })
			chats, err := r.svc.List(ctx, alice, 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(chats).NotTo(BeEmpty())
			Expect(r.h.ToolNames(chats[0].ID)).NotTo(ContainElement("propose_change"))
		})
	})

	Describe("the host entry point", func() {
		It("refuses an automatic write that is not a memory note of a person's own chat", func() {
			st := &saveSetting{}
			r := settingShared(st).rig(func(workspace.Actor, string) []studiotest.ToolStep { return nil }, studio.TriggerOptions{})
			ag, _ := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "A", Model: "m", Builtins: []string{"memory.write", "profile.write"}})
			base := studio.ProposeInput{User: alice, Agent: ag.ID, Kind: "memory", Scope: "agent", Target: "index.md", Content: "- x\n", Summary: "s", Auto: true}
			conn := base
			conn.Source = contract.ChangeSource{Kind: session.OriginConnector, ID: "b1", Chat: "c1"}
			_, err := r.svc.Changes().Propose(ctx, conn)
			Expect(err).To(MatchError(studio.ErrBadChange))
			prof := base
			prof.Kind, prof.Scope, prof.Target = "profile", "agent", ""
			_, err = r.svc.Changes().Propose(ctx, prof)
			Expect(err).To(MatchError(studio.ErrBadChange))
			ok := base
			_, err = r.svc.Changes().Propose(ctx, ok)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
