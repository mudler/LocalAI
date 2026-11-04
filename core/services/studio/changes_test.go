package studio_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/slack/slacktest"
	slack "github.com/mudler/LocalAI/addons/slack/transport"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
)

// proposeArgs is a propose_change call as a model writes it.
func proposeArgs(kind, scope, target, content, summary string) string {
	raw, _ := json.Marshal(map[string]string{"kind": kind, "scope": scope, "target": target, "content": content, "summary": summary})
	return string(raw)
}

// changeScript proposes a memory note on "remember" and reverts on "forget".
func changeScript(scope, content string) func(workspace.Actor, string) []studiotest.ToolStep {
	return func(_ workspace.Actor, text string) []studiotest.ToolStep {
		switch text {
		case "remember":
			return []studiotest.ToolStep{{Tool: "propose_change", Args: proposeArgs("memory", scope, "index.md", content, "Where the user lives")}, {Say: "ok"}}
		case "forget":
			return []studiotest.ToolStep{{Tool: "revert_change", Args: `{}`}, {Say: "ok"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
}

// grantAll is the gate a grants feature would give: every chat may.
var grantAll = proposals.GateFunc(func(context.Context, proposals.Request) error { return nil })

// changeShared is a shared rig whose service has a clock and a gate.
func changeShared(gate proposals.Gate) *shared {
	sh := newShared()
	sh.deps = func(d *studio.Deps) {
		d.Clock = sh.clock.Now
		d.ProposalGate = gate
	}
	return sh
}

// grantMemory makes the saved agent of the rig one that may propose memory.
func (c *connRig) grantMemory() { c.grantBuiltins("memory.write") }

// grantBuiltins gives the saved agent of the rig the builtin grants.
func (c *connRig) grantBuiltins(grants ...string) {
	ag, err := c.svc.SaveAgent(context.Background(), alice, agents.Agent{Name: "Support", Model: "m", Addons: []string{"office"}, Builtins: grants})
	Expect(err).NotTo(HaveOccurred())
	c.agent = ag
}

func channelFile(src contract.ChangeSource) string {
	return "Memory/channels/" + proposals.ChannelDir(src) + "/index.md"
}

func readHome(r *trigRig, rel string) (string, bool) {
	text, ok, err := r.sh.files.ReadHome(context.Background(), alice, rel, 1<<20)
	Expect(err).NotTo(HaveOccurred())
	return text, ok
}

// onlyProposal returns the one proposal of alice.
func onlyProposal(r *trigRig) contract.ChangeProposal {
	list, err := r.svc.Changes().List(context.Background(), alice)
	Expect(err).NotTo(HaveOccurred())
	Expect(list).To(HaveLen(1), "proposals = %+v", list)
	return list[0]
}

// expectProposeConfirmRevert drives the whole life of one proposal that a
// chat made: nothing is written until the owner confirms, a confirmation
// writes one change set, and a revert by the model takes it back.
func expectProposeConfirmRevert(r *trigRig, send func(string), wantKind, wantID string, file func(contract.ChangeSource) string) {
	ctx := context.Background()
	send("remember")
	until("the proposal", func() bool {
		res := r.h.ResultsFor("propose_change")
		return len(res) == 1
	})
	res := r.h.ResultsFor("propose_change")
	Expect(res[0].Err).To(BeNil(), "result = %+v", res)
	Expect(res[0].Out).To(ContainSubstring("Nothing is saved yet"))
	if wantKind == "" {
		// A chat the person watches in Studio keeps the Studio wording; a
		// connector chat is told the host asked in the chat (connectorchange_test.go).
		Expect(res[0].Out).To(ContainSubstring("confirm it in Studio"))
		Expect(res[0].Out).NotTo(ContainSubstring("yes or no"))
	} else {
		Expect(res[0].Out).To(ContainSubstring("the person answers there with yes or no"))
		Expect(res[0].Out).NotTo(ContainSubstring("Studio"))
	}
	p := onlyProposal(r)
	Expect(p.Status).To(Equal(contract.ProposalPending))
	wantSource := p.Source
	Expect(wantSource.Kind).To(Equal(wantKind))
	Expect(wantSource.ID).To(Equal(wantID))
	Expect(wantSource.Chat).NotTo(BeEmpty())
	wantFile := file(p.Source)
	Expect(p.Target).To(Equal(wantFile))
	_, ok := readHome(r, wantFile)
	Expect(ok).To(BeFalse(), "the proposal wrote the file")

	_, err := r.svc.Changes().Confirm(ctx, "bob", p.ID)
	Expect(err).To(MatchError(proposals.ErrNotFound), "another user confirmed it")

	done, err := r.svc.Changes().Confirm(ctx, alice, p.ID)
	Expect(err).NotTo(HaveOccurred())
	Expect(done.Status).To(Equal(contract.ProposalApplied))
	Expect(done.Rev).To(BeNumerically(">", 0))
	text, ok := readHome(r, wantFile)
	Expect(ok).To(BeTrue())
	Expect(text).To(Equal("- home: Rome\n"))
	_, err = r.svc.Changes().Confirm(ctx, alice, p.ID)
	Expect(err).To(MatchError(studio.ErrChangeAnswered))

	send("forget")
	until("the revert", func() bool { return len(r.h.ResultsFor("revert_change")) == 1 })
	Expect(r.h.ResultsFor("revert_change")[0].Err).To(BeNil())
	_, ok = readHome(r, wantFile)
	Expect(ok).To(BeFalse(), "the revert left the file")
	back, _ := r.svc.Changes().Get(ctx, alice, p.ID)
	Expect(back.Status).To(Equal(contract.ProposalReverted))
	Expect(back.RevertRev).To(BeNumerically(">", back.Rev))

	events, err := r.svc.Changes().Events(ctx, alice, 20)
	Expect(err).NotTo(HaveOccurred())
	var actions []string
	for _, e := range events {
		actions = append(actions, e.Action)
		Expect(e.Source).To(Equal(wantSource))
		Expect(e.Hash).NotTo(BeEmpty())
		Expect(fmt.Sprintf("%+v", e)).NotTo(ContainSubstring("Rome"), "the audit holds content")
	}
	Expect(actions).To(Equal([]string{"revert", "confirm", "propose"}))
}

var _ = Describe("change proposals", func() {
	Describe("from a plain Studio chat", func() {
		It("stores, confirms and reverts a memory change", func() {
			r := changeShared(nil).rig(changeScript("user", "- home: Rome\n"), studio.TriggerOptions{})
			id := create(r.svc, alice)
			expectProposeConfirmRevert(r, func(t string) { sent(r.svc, alice, id, t); idle(r.svc, alice, id) }, "", "",
				func(contract.ChangeSource) string { return "Memory/index.md" })
		})

		It("refuses a target outside Memory, a profile without an agent and too much text", func() {
			r := changeShared(nil).rig(func(workspace.Actor, string) []studiotest.ToolStep { return nil }, studio.TriggerOptions{})
			ctx := context.Background()
			c := r.svc.Changes()
			in := studio.ProposeInput{User: alice, Kind: "memory", Scope: "user", Target: "index.md", Content: "x", Summary: "s"}
			bad := func(mod func(*studio.ProposeInput)) error {
				x := in
				mod(&x)
				_, err := c.Propose(ctx, x)
				return err
			}
			for name, mod := range map[string]func(*studio.ProposeInput){
				"parent":      func(x *studio.ProposeInput) { x.Target = "../../x.md" },
				"profile":     func(x *studio.ProposeInput) { x.Kind, x.Scope = "profile", "agent" },
				"big":         func(x *studio.ProposeInput) { x.Content = strings.Repeat("a", contract.MaxChangeBytes+1) },
				"unknown":     func(x *studio.ProposeInput) { x.Kind = "page" },
				"scope":       func(x *studio.ProposeInput) { x.Scope = "team" },
				"no summary":  func(x *studio.ProposeInput) { x.Summary = " " },
				"binary":      func(x *studio.ProposeInput) { x.Content = "a\x00b" },
				"agent scope": func(x *studio.ProposeInput) { x.Scope = "agent" },
			} {
				Expect(bad(mod)).To(MatchError(studio.ErrBadChange), name)
			}
			list, _ := c.List(ctx, alice)
			Expect(list).To(BeEmpty())
		})

		It("holds a profile to 4 KiB", func() {
			r := changeShared(grantAll).rig(func(workspace.Actor, string) []studiotest.ToolStep { return nil }, studio.TriggerOptions{})
			ag, err := r.svc.SaveAgent(context.Background(), alice, agents.Agent{Name: "Helper", Model: "m", Builtins: []string{"profile.write"}})
			Expect(err).NotTo(HaveOccurred())
			in := studio.ProposeInput{User: alice, Agent: ag.ID, Kind: "profile", Scope: "agent", Content: strings.Repeat("a", contract.MaxProfileBytes+1), Summary: "s"}
			_, err = r.svc.Changes().Propose(context.Background(), in)
			Expect(err).To(MatchError(studio.ErrBadChange))
			in.Content = strings.Repeat("a", contract.MaxProfileBytes)
			p, err := r.svc.Changes().Propose(context.Background(), in)
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Target).To(Equal("LocalAI/agents/" + ag.ID + "/profile.md"))
		})

		It("limits 10 pending per user and 5 per hour per agent", func() {
			sh := changeShared(nil)
			r := sh.rig(func(workspace.Actor, string) []studiotest.ToolStep { return nil }, studio.TriggerOptions{})
			ctx := context.Background()
			c := r.svc.Changes()
			propose := func(user, name string) (contract.ChangeProposal, error) {
				return c.Propose(ctx, studio.ProposeInput{User: user, Kind: "memory", Scope: "user", Target: name + ".md", Content: name, Summary: name})
			}
			var made []contract.ChangeProposal
			for i := range contract.MaxChangesPerHour {
				p, err := propose(alice, fmt.Sprintf("n%d", i))
				Expect(err).NotTo(HaveOccurred())
				made = append(made, p)
			}
			_, err := propose(alice, "extra")
			Expect(err).To(MatchError(proposals.ErrRateLimited))
			_, err = propose("bob", "bobs")
			Expect(err).NotTo(HaveOccurred(), "the rate is per person")
			sh.clock.Advance(61 * time.Minute)
			for i := range contract.MaxChangesPerHour {
				p, err := propose(alice, fmt.Sprintf("m%d", i))
				Expect(err).NotTo(HaveOccurred(), "after an hour, number %d", i)
				made = append(made, p)
			}
			_, err = propose(alice, "over")
			Expect(err).To(MatchError(proposals.ErrTooManyPending), "10 are waiting")
			_, err = c.Dismiss(ctx, alice, made[0].ID)
			Expect(err).NotTo(HaveOccurred())
			_, err = c.Dismiss(ctx, alice, made[0].ID)
			Expect(err).To(MatchError(studio.ErrChangeAnswered))
		})

		It("expires a proposal after 24 hours", func() {
			sh := changeShared(nil)
			r := sh.rig(changeScript("user", "- home: Rome\n"), studio.TriggerOptions{})
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "remember")
			idle(r.svc, alice, id)
			p := onlyProposal(r)
			Expect(p.Expires.Sub(p.Created)).To(Equal(24 * time.Hour))
			sh.clock.Advance(25 * time.Hour)
			_, err := r.svc.Changes().Confirm(context.Background(), alice, p.ID)
			Expect(err).To(MatchError(studio.ErrChangeExpired))
			got, _ := r.svc.Changes().Get(context.Background(), alice, p.ID)
			Expect(got.Status).To(Equal(contract.ProposalExpired))
			_, ok := readHome(r, "Memory/index.md")
			Expect(ok).To(BeFalse())
		})

		It("writes the owner's own text when the owner edits before confirming", func() {
			r := changeShared(nil).rig(changeScript("user", "- home: Rome\n"), studio.TriggerOptions{})
			ctx := context.Background()
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "remember")
			idle(r.svc, alice, id)
			p := onlyProposal(r)
			_, err := r.svc.Changes().ConfirmEdited(ctx, alice, p.ID, strings.Repeat("x", 16<<10+1))
			Expect(err).To(MatchError(studio.ErrBadChange))
			got, _ := r.svc.Changes().Get(ctx, alice, p.ID)
			Expect(got.Status).To(Equal(contract.ProposalPending), "a refused edit leaves the proposal waiting")
			done, err := r.svc.Changes().ConfirmEdited(ctx, alice, p.ID, "- home: Milan\n")
			Expect(err).NotTo(HaveOccurred())
			Expect(done.Status).To(Equal(contract.ProposalApplied))
			text, _ := readHome(r, "Memory/index.md")
			Expect(text).To(Equal("- home: Milan\n"))
			_, err = r.svc.Changes().Revert(ctx, alice, p.ID)
			Expect(err).NotTo(HaveOccurred())
			_, ok := readHome(r, "Memory/index.md")
			Expect(ok).To(BeFalse())
		})

		It("refuses a stale proposal and keeps the newer file", func() {
			r := changeShared(nil).rig(changeScript("user", "- home: Rome\n"), studio.TriggerOptions{})
			ctx := context.Background()
			_, err := r.sh.files.WriteHome(ctx, alice, "Memory/index.md", "- old\n", "seed", 1024)
			Expect(err).NotTo(HaveOccurred())
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "remember")
			idle(r.svc, alice, id)
			p := onlyProposal(r)
			Expect(p.BaseHash).NotTo(BeEmpty())
			_, err = r.sh.files.WriteHome(ctx, alice, "Memory/index.md", "- edited by hand\n", "edit", 1024)
			Expect(err).NotTo(HaveOccurred())
			_, err = r.svc.Changes().Confirm(ctx, alice, p.ID)
			Expect(err).To(MatchError(studio.ErrTargetChanged))
			text, _ := readHome(r, "Memory/index.md")
			Expect(text).To(Equal("- edited by hand\n"))
			got, _ := r.svc.Changes().Get(ctx, alice, p.ID)
			Expect(got.Status).To(Equal(contract.ProposalStale))
		})

		It("refuses a revert when the file was edited since", func() {
			r := changeShared(nil).rig(changeScript("user", "- home: Rome\n"), studio.TriggerOptions{})
			ctx := context.Background()
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "remember")
			idle(r.svc, alice, id)
			p, err := r.svc.Changes().Confirm(ctx, alice, onlyProposal(r).ID)
			Expect(err).NotTo(HaveOccurred())
			_, err = r.sh.files.WriteHome(ctx, alice, "Memory/index.md", "- edited\n", "edit", 1024)
			Expect(err).NotTo(HaveOccurred())
			_, err = r.svc.Changes().Revert(ctx, alice, p.ID)
			Expect(err).To(MatchError(studio.ErrCannotRevert))
			got, _ := r.svc.Changes().Get(ctx, alice, p.ID)
			Expect(got.Status).To(Equal(contract.ProposalApplied), "a failed revert must leave the change applied")
		})

		It("is not offered to a sub-agent or an unattended run", func() {
			r := changeShared(nil).rig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
				if text == "go" {
					return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"child job","wait":true}`}, {Say: "done"}}
				}
				return []studiotest.ToolStep{{Say: "ok"}}
			}, studio.TriggerOptions{})
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "go")
			idle(r.svc, alice, id)
			Expect(r.h.ToolNames(id)).To(ContainElements("propose_change", "revert_change"))
			for _, c := range childrenOf(r.svc, alice, id) {
				Expect(r.h.ToolNames(c.ID)).NotTo(ContainElement("propose_change"))
			}
			tr := r.schedule(studio.TriggerInput{})
			r.due()
			run := r.waitEnded(tr.ID)
			Expect(r.h.ToolNames(run.ChatID)).NotTo(ContainElement("propose_change"))
			Expect(r.h.ToolNames(run.ChatID)).NotTo(ContainElement("revert_change"))
		})
	})

	Describe("from a chat that runs a saved agent", func() {
		It("is refused without the grant and allowed with it", func() {
			for name, grant := range map[string][]string{"default": nil, "granted": {"memory.write"}} {
				r := changeShared(nil).rig(changeScript("agent", "- rule\n"), studio.TriggerOptions{})
				ag, err := r.svc.SaveAgent(context.Background(), alice, agents.Agent{Name: "Helper", Model: "m", Builtins: grant})
				Expect(err).NotTo(HaveOccurred())
				rec, err := r.svc.Create(context.Background(), alice, studio.CreateInput{AgentID: ag.ID})
				Expect(err).NotTo(HaveOccurred())
				sent(r.svc, alice, rec.ID, "remember")
				idle(r.svc, alice, rec.ID)
				res := r.h.ResultsFor("propose_change")
				list, _ := r.svc.Changes().List(context.Background(), alice)
				if name == "default" {
					// Without a grant the tool does not exist in the chat.
					Expect(r.h.ToolNames(rec.ID)).NotTo(ContainElement("propose_change"))
					Expect(res).To(HaveLen(1))
					Expect(res[0].Err).To(MatchError(ContainSubstring("no tool named")))
					Expect(list).To(BeEmpty())
					continue
				}
				Expect(res).To(HaveLen(1), name)
				Expect(res[0].Err).To(BeNil())
				Expect(list).To(HaveLen(1))
				Expect(list[0].Agent).To(Equal(ag.ID))
				Expect(list[0].Target).To(Equal("Memory/agents/" + ag.ID + "/index.md"))
			}
		})

		It("cannot revert the change of another agent by its id", func() {
			var other string
			r := changeShared(grantAll).rig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
				if text == "forget" {
					return []studiotest.ToolStep{{Tool: "revert_change", Args: `{"id":"` + other + `"}`}, {Say: "ok"}}
				}
				return []studiotest.ToolStep{{Say: "ok"}}
			}, studio.TriggerOptions{})
			ctx := context.Background()
			p, err := r.svc.Changes().Propose(ctx, studio.ProposeInput{User: alice, Kind: "memory", Scope: "user", Target: "a.md", Content: "x", Summary: "s"})
			Expect(err).NotTo(HaveOccurred())
			_, err = r.svc.Changes().Confirm(ctx, alice, p.ID)
			Expect(err).NotTo(HaveOccurred())
			other = p.ID
			ag, _ := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Helper", Model: "m", Builtins: []string{"memory.write"}})
			rec, _ := r.svc.Create(ctx, alice, studio.CreateInput{AgentID: ag.ID})
			sent(r.svc, alice, rec.ID, "forget")
			idle(r.svc, alice, rec.ID)
			res := r.h.ResultsFor("revert_change")
			Expect(res).To(HaveLen(1))
			Expect(res[0].Err).To(HaveOccurred())
			got, _ := r.svc.Changes().Get(ctx, alice, p.ID)
			Expect(got.Status).To(Equal(contract.ProposalApplied))
		})
	})

	Describe("from connector chats of two platforms", func() {
		// The host has no platform code: the same spec runs over the Telegram
		// and the Slack fakes, and the proposal differs only in the binding.
		It("works the same over Telegram", func() {
			sh, bot := changeShared(grantAll), newScriptedBot()
			r := newConnRig(sh, connStore(), bot, changeScript("channel", "- home: Rome\n"), nil)
			r.grantMemory()
			b := r.bind(studio.ConnectorInput{Allow: []string{"11"}})
			n, want := 0, 0
			send := func(text string) {
				n++
				want++
				if text == "remember" {
					want++ // the host also writes the proposal in the chat
				}
				bot.say(n, 11, "Ada", "ada", text)
				waitFor("the answer", func() bool { return len(bot.answers()) == want })
			}
			expectProposeConfirmRevert(r.trigRig, send, session.OriginConnector, b.ID, channelFile)
		})

		It("works the same over Slack", func() {
			sh := changeShared(grantAll)
			f := slacktest.New()
			DeferCleanup(f.Close)
			f.AddApp(slacktest.App{Bot: slackBot, AppToken: slackApp, UserID: "UBOT"})
			f.AddUser("U1", "ada", "Ada")
			rt := slack.New(&slack.API{BaseURL: f.URL, FilesHost: f.FilesHost})
			DeferCleanup(rt.Close)
			r := newConnRigWith(sh, connStore(), studiobot.NewTransport(studiobot.Slack(rt)), changeScript("channel", "- home: Rome\n"), nil)
			r.grantMemory()
			b := r.bind(studio.ConnectorInput{Kind: "slack", Token: slackBot + " " + slackApp, Allow: []string{"C1"}})
			waitFor("the Socket Mode link", func() bool { return f.Connected(slackApp) })
			posted := func() int {
				return len((&slackRig{connRig: r, slack: f}).answers())
			}
			n := 0
			send := func(text string) {
				n++
				if text == "remember" {
					n++ // the host also writes the proposal in the chat
				}
				f.Push(slackApp, slacktest.Mention("C1", "U1", "UBOT", text, ""))
				waitFor("the answer", func() bool { return posted() == n })
			}
			expectProposeConfirmRevert(r.trigRig, send, session.OriginConnector, b.ID, channelFile)
		})

		It("keeps the connector chat out of the files of its sender when it only proposes", func() {
			// A connector chat that runs an agent without a grant gets the
			// default refusal: the safe default for every platform.
			sh, bot := changeShared(nil), newScriptedBot()
			r := newConnRig(sh, connStore(), bot, changeScript("channel", "x"), nil)
			r.bind(studio.ConnectorInput{Allow: []string{"11"}})
			bot.say(1, 11, "Ada", "ada", "remember")
			waitFor("the answer", func() bool { return len(bot.answers()) == 1 })
			list, _ := r.svc.Changes().List(context.Background(), alice)
			Expect(list).To(BeEmpty())
		})
	})
})
