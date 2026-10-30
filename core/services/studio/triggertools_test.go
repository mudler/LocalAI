package studio_test

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func proposeScript(args string) func(workspace.Actor, string) []studiotest.ToolStep {
	return func(_ workspace.Actor, text string) []studiotest.ToolStep {
		switch text {
		case "propose":
			return []studiotest.ToolStep{{Tool: "propose_trigger", Args: args}, {Say: "ok"}}
		case "list":
			return []studiotest.ToolStep{{Tool: "list_triggers", Args: `{}`}, {Say: "ok"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
}

const goodProposal = `{"name":"Morning digest","prompt":"Summarise yesterday.","when":"weekdays 09:00","time_zone":"Europe/Rome"}`

var _ = Describe("triggertools", func() {
	It("a proposal does nothing until its owner confirms it", func() {
		r := newTrigRig(proposeScript(goodProposal))
		ctx := context.Background()
		src, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "src-model"})
		Expect(err).NotTo(HaveOccurred())
		sent(r.svc, alice, src.ID, "propose")
		idle(r.svc, alice, src.ID)
		res := r.h.ResultsFor("propose_trigger")
		Expect(res).To(HaveLen(1), "result = %+v", res)
		Expect(res[0].Err).To(BeNil(), "result = %+v", res)
		Expect(res[0].Out).To(ContainSubstring("not active"), "result = %+v", res)
		Expect(res[0].Out).To(ContainSubstring("confirm"), "result = %+v", res)
		props, err := r.host.Proposals(ctx, alice)
		Expect(err).NotTo(HaveOccurred(), "proposals = %+v, %v", props, err)
		Expect(props).To(HaveLen(1), "proposals = %+v, %v", props, err)
		p := props[0]
		Expect(p.Enabled).To(BeFalse(), "proposal = %+v", p)
		Expect(p.Proposed).To(BeTrue(), "proposal = %+v", p)
		Expect(p.SourceChatID).To(Equal(src.ID), "proposal = %+v", p)
		Expect(p.Model).To(Equal("src-model"), "proposal = %+v", p)
		Expect(p.Schedule).To(Equal("weekdays 09:00"), "proposal = %+v", p)
		Expect(p.TZ).To(Equal("Europe/Rome"), "proposal = %+v", p)
		list, _ := r.host.ListTriggers(ctx, alice)
		Expect(list).To(BeEmpty(), "a proposal is listed as a trigger: %+v", list)
		r.sh.clock.Advance(48 * time.Hour)
		r.pass()
		runs, _ := r.sh.trg.ListRuns(ctx, p.ID, 0)
		Expect(runs).To(BeEmpty(), "an unconfirmed proposal ran: %+v", runs)
		_, err = r.host.RunTrigger(ctx, alice, p.ID)
		Expect(err).To(MatchError(triggers.ErrNotFound), "a proposal was run by hand: %v", err)
		// Bob can neither see nor confirm it.
		_, err = r.host.ConfirmProposal(ctx, "bob", p.ID, "")
		Expect(err).To(MatchError(triggers.ErrNotFound), "bob confirmed alice's proposal: %v", err)
		got, err := r.host.ConfirmProposal(ctx, alice, p.ID, "")
		Expect(err).NotTo(HaveOccurred(), "confirmed = %+v, %v", got, err)
		Expect(got.Enabled).To(BeTrue(), "confirmed = %+v, %v", got, err)
		Expect(got.Proposed).To(BeFalse(), "confirmed = %+v, %v", got, err)
		list, _ = r.host.ListTriggers(ctx, alice)
		Expect(list).To(HaveLen(1), "triggers after confirming: %+v", list)
		props, _ = r.host.Proposals(ctx, alice)
		Expect(props).To(BeEmpty(), "still a proposal: %+v", props)
	})

	It("a proposal without a zone takes the zone of the confirmation", func() {
		r := newTrigRig(proposeScript(`{"name":"Digest","prompt":"p","when":"daily 08:00"}`))
		ctx := context.Background()
		id := create(r.svc, alice)
		sent(r.svc, alice, id, "propose")
		idle(r.svc, alice, id)
		props, _ := r.host.Proposals(ctx, alice)
		Expect(props).To(HaveLen(1), "proposals = %+v", props)
		got, err := r.host.ConfirmProposal(ctx, alice, props[0].ID, "America/New_York")
		Expect(err).NotTo(HaveOccurred(), "confirmed = %+v, %v", got, err)
		Expect(got.TZ).To(Equal("America/New_York"), "confirmed = %+v, %v", got, err)
	})

	It("a chat with an agent proposes a trigger that keeps the agent", func() {
		r := newTrigRig(proposeScript(goodProposal))
		ctx := context.Background()
		ag, _ := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Digester", Model: "m"})
		rec, err := r.svc.Create(ctx, alice, studio.CreateInput{AgentID: ag.ID})
		Expect(err).NotTo(HaveOccurred())
		sent(r.svc, alice, rec.ID, "propose")
		idle(r.svc, alice, rec.ID)
		props, _ := r.host.Proposals(ctx, alice)
		Expect(props).To(HaveLen(1), "proposals = %+v", props)
		Expect(props[0].AgentID).To(Equal(ag.ID), "proposals = %+v", props)
	})

	It("a bad proposal is refused with the reason and stores nothing", func() {
		r := newTrigRig(proposeScript(`{"name":"x","prompt":"p","when":"daily 25:00"}`))
		id := create(r.svc, alice)
		sent(r.svc, alice, id, "propose")
		idle(r.svc, alice, id)
		res := r.h.ResultsFor("propose_trigger")
		Expect(res).To(HaveLen(1), "result = %+v", res)
		Expect(res[0].Err).NotTo(BeNil(), "result = %+v", res)
		Expect(res[0].Err.Error()).To(ContainSubstring("hour must be 0 to 23"), "result = %+v", res)
		props, _ := r.host.Proposals(context.Background(), alice)
		Expect(props).To(BeEmpty(), "stored %+v", props)
	})

	It("the trigger tools are offered to people chats only", func() {
		r := newTrigRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "go" {
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"child job","wait":true}`}, {Say: "done"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		})
		ctx := context.Background()
		id := create(r.svc, alice)
		sent(r.svc, alice, id, "go")
		idle(r.svc, alice, id)
		names := r.h.ToolNames(id)
		Expect(names).To(ContainElement("propose_trigger"), "a person's chat has %v", names)
		Expect(names).To(ContainElement("list_triggers"), "a person's chat has %v", names)
		for _, c := range childrenOf(r.svc, alice, id) {
			names := r.h.ToolNames(c.ID)
			Expect(names).NotTo(ContainElement("propose_trigger"), "a sub-agent has %v", names)
			Expect(names).NotTo(ContainElement("list_triggers"), "a sub-agent has %v", names)
		}
		tr := r.schedule(studio.TriggerInput{})
		r.due()
		run := r.waitEnded(tr.ID)
		names = r.h.ToolNames(run.ChatID)
		Expect(names).NotTo(ContainElement("propose_trigger"), "an unattended run has %v", names)
		Expect(names).NotTo(ContainElement("list_triggers"), "an unattended run has %v", names)
		_ = ctx
	})

	It("list triggers shows names and states but no prompts or i ds", func() {
		r := newTrigRig(proposeScript(goodProposal))
		r.schedule(studio.TriggerInput{Name: "Nightly", Prompt: "secret instructions"})
		id := create(r.svc, alice)
		sent(r.svc, alice, id, "list")
		idle(r.svc, alice, id)
		res := r.h.ResultsFor("list_triggers")
		Expect(res).To(HaveLen(1), "result = %+v", res)
		Expect(res[0].Err).To(BeNil(), "result = %+v", res)
		Expect(res[0].Out).To(ContainSubstring("Nightly"), "result = %+v", res)
		Expect(res[0].Out).NotTo(ContainSubstring("secret instructions"), "result = %+v", res)
	})

	It("a deleted trigger leaves its chats and nothing runs", func() {
		r := newTrigRig(saySummary)
		ctx := context.Background()
		tr := r.schedule(studio.TriggerInput{})
		r.due()
		run := r.waitEnded(tr.ID)
		Expect(r.host.DeleteTrigger(ctx, alice, tr.ID)).To(Succeed())
		_, err := r.host.GetTrigger(ctx, alice, tr.ID)
		Expect(err).To(MatchError(triggers.ErrNotFound), "after delete: %v", err)
		_, err = r.svc.Get(ctx, alice, run.ChatID)
		Expect(err).NotTo(HaveOccurred(), "the chat of a deleted trigger is gone")
	})

	It("the same proposal is kept once", func() {
		r := newTrigRig(proposeScript(goodProposal))
		ctx := context.Background()
		src, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "src-model"})
		Expect(err).NotTo(HaveOccurred())
		for range 3 {
			sent(r.svc, alice, src.ID, "propose")
			idle(r.svc, alice, src.ID)
		}
		props, err := r.host.Proposals(ctx, alice)
		Expect(err).NotTo(HaveOccurred(), "proposals = %d, %v; want the one", len(props), err)
		Expect(props).To(HaveLen(1), "proposals = %d, %v; want the one", len(props), err)
		// Another prompt is another proposal.
		in := studio.TriggerInput{Name: "Morning digest", Prompt: "Summarise the week.", Kind: triggers.KindSchedule, Schedule: "weekdays 09:00", TZ: "Europe/Rome", Model: "src-model"}
		_, err = r.host.ProposeTrigger(ctx, alice, in)
		Expect(err).NotTo(HaveOccurred())
		props, _ = r.host.Proposals(ctx, alice)
		Expect(props).To(HaveLen(2), "proposals = %d, want 2", len(props))
		// Once confirmed, a new proposal of the same text may wait again.
		_, err = r.host.ConfirmProposal(ctx, alice, props[0].ID, "")
		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = Describe("triggertools: cadence and plan", func() {
	It("a schedule proposed in a chat runs at most once an hour", func() {
		r := newTrigRig(proposeScript(`{"name":"Every quarter","prompt":"p","when":"*/15 * * * *"}`))
		id := create(r.svc, alice)
		sent(r.svc, alice, id, "propose")
		idle(r.svc, alice, id)
		res := r.h.ResultsFor("propose_trigger")
		Expect(res).To(HaveLen(1), "result = %+v", res)
		Expect(res[0].Err).NotTo(BeNil(), "result = %+v", res)
		Expect(res[0].Err.Error()).To(ContainSubstring("runs at most once an hour"), "result = %+v", res)
		Expect(res[0].Err.Error()).To(ContainSubstring("every 15 minutes"), "result = %+v", res)
		props, _ := r.host.Proposals(context.Background(), alice)
		Expect(props).To(BeEmpty(), "stored %+v", props)
	})

	It("an hourly proposal is fine and keeps what a run reads, writes and costs", func() {
		r := newTrigRig(proposeScript(`{"name":"Inbox","prompt":"Rewrite data/inbox.json.","when":"0 * * * *",` +
			`"reads":"Mail from the last hour","writes":"/data/inbox.json","widget":"inbox","estimated_tokens":1500}`))
		ctx := context.Background()
		id := create(r.svc, alice)
		sent(r.svc, alice, id, "propose")
		idle(r.svc, alice, id)
		props, err := r.host.Proposals(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(props).To(HaveLen(1), "proposals = %+v, results = %+v", props, r.h.ResultsFor("propose_trigger"))
		p := props[0]
		Expect(p.Reads).To(Equal("Mail from the last hour"))
		Expect(p.Writes).To(Equal("data/inbox.json"), "the path is kept as the person sees it")
		Expect(p.Widget).To(Equal("inbox"))
		Expect(p.EstTokens).To(Equal(1500))
		Expect(r.host.MaxRunsPerDay(p)).To(Equal(24))

		// An edit of the proposal keeps the plan and the hourly floor.
		_, err = r.host.EditProposal(ctx, alice, p.ID, studio.TriggerInput{Name: "Inbox", Prompt: p.Prompt, Kind: triggers.KindSchedule, Schedule: "*/30 * * * *", Model: p.Model})
		Expect(err).To(MatchError(ContainSubstring("runs at most once an hour")))
		got, err := r.host.EditProposal(ctx, alice, p.ID, studio.TriggerInput{Name: "Inbox daily", Prompt: p.Prompt, Kind: triggers.KindSchedule, Schedule: "daily 08:00", Model: p.Model})
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Writes).To(Equal("data/inbox.json"), "an edit dropped the plan: %+v", got)
		Expect(got.EstTokens).To(Equal(1500), "an edit dropped the estimate: %+v", got)
		Expect(r.host.MaxRunsPerDay(got)).To(Equal(1))

		// On the Triggers page a person may choose a shorter schedule.
		on, err := r.host.ConfirmProposal(ctx, alice, p.ID, "UTC")
		Expect(err).NotTo(HaveOccurred())
		upd, err := r.host.UpdateTrigger(ctx, alice, on.ID, studio.TriggerInput{Name: "Inbox", Prompt: p.Prompt, Kind: triggers.KindSchedule, Schedule: "*/15 * * * *", Model: "m", Enabled: true})
		Expect(err).NotTo(HaveOccurred(), "a person could not choose 15 minutes")
		Expect(upd.Widget).To(Equal("inbox"), "an update from the page dropped the widget: %+v", upd)
	})

	It("a plan that writes outside the home is refused", func() {
		r := newTrigRig(proposeScript(`{"name":"x","prompt":"p","when":"daily 08:00","writes":"../bob/data.json"}`))
		id := create(r.svc, alice)
		sent(r.svc, alice, id, "propose")
		idle(r.svc, alice, id)
		res := r.h.ResultsFor("propose_trigger")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).NotTo(BeNil(), "result = %+v", res)
		Expect(res[0].Err.Error()).To(ContainSubstring("writes is not a path in your files"))
	})
})

var _ = Describe("triggertools: the Home widget of a trigger", func() {
	It("confirming a proposal names the trigger in its widget and keeps the other keys", func() {
		r := newTrigRig(proposeScript(`{"name":"Inbox","prompt":"Rewrite data/inbox.json.","when":"daily 08:00","writes":"data/inbox.json","widget":"inbox"}`))
		ctx := context.Background()
		fs := r.sh.files
		Expect(fs.EnsureHome(ctx, alice)).To(Succeed())
		home, _ := fs.Abs(alice, "/LocalAI/home.json")
		_, err := fs.Store().Put(ctx, fs.Actor(alice), home, strings.NewReader(
			`{"widgets":[{"id":"inbox","type":"list","title":"Inbox","source":"data/inbox.json","note":"mine"},{"id":"chats","type":"chats","title":"Chats"}],"extra":1}`),
			workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
		id := create(r.svc, alice)
		sent(r.svc, alice, id, "propose")
		idle(r.svc, alice, id)
		props, _ := r.host.Proposals(ctx, alice)
		Expect(props).To(HaveLen(1))
		on, err := r.host.ConfirmProposal(ctx, alice, props[0].ID, "UTC")
		Expect(err).NotTo(HaveOccurred())
		rd, _, err := fs.Store().Open(ctx, fs.Actor(alice), home)
		Expect(err).NotTo(HaveOccurred())
		defer rd.Close()
		var doc struct {
			Widgets []map[string]any `json:"widgets"`
			Extra   int              `json:"extra"`
		}
		Expect(json.NewDecoder(rd).Decode(&doc)).To(Succeed())
		Expect(doc.Extra).To(Equal(1), "a key of the file was lost")
		Expect(doc.Widgets).To(HaveLen(2))
		Expect(doc.Widgets[0]["trigger"]).To(Equal(on.ID))
		Expect(doc.Widgets[0]["note"]).To(Equal("mine"), "a key of the widget was lost")
		Expect(doc.Widgets[1]).NotTo(HaveKey("trigger"))
	})
})
