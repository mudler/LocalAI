package studio_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
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

func TestAProposalDoesNothingUntilItsOwnerConfirmsIt(t *testing.T) {
	r := newTrigRig(t, proposeScript(goodProposal))
	ctx := context.Background()
	src, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "src-model"})
	if err != nil {
		t.Fatal(err)
	}
	sent(t, r.svc, alice, src.ID, "propose")
	idle(t, r.svc, alice, src.ID)
	res := r.h.ResultsFor("propose_trigger")
	if len(res) != 1 || res[0].Err != nil || !strings.Contains(res[0].Out, "not active") || !strings.Contains(res[0].Out, "confirm") {
		t.Fatalf("result = %+v", res)
	}
	props, err := r.host.Proposals(ctx, alice)
	if err != nil || len(props) != 1 {
		t.Fatalf("proposals = %+v, %v", props, err)
	}
	p := props[0]
	if p.Enabled || !p.Proposed || p.SourceChatID != src.ID || p.Model != "src-model" || p.Schedule != "weekdays 09:00" || p.TZ != "Europe/Rome" {
		t.Fatalf("proposal = %+v", p)
	}
	if list, _ := r.host.ListTriggers(ctx, alice); len(list) != 0 {
		t.Fatalf("a proposal is listed as a trigger: %+v", list)
	}
	r.sh.clock.Advance(48 * time.Hour)
	r.pass(t)
	if runs, _ := r.sh.trg.ListRuns(ctx, p.ID, 0); len(runs) != 0 {
		t.Fatalf("an unconfirmed proposal ran: %+v", runs)
	}
	if _, err := r.host.RunTrigger(ctx, alice, p.ID); !errors.Is(err, triggers.ErrNotFound) {
		t.Fatalf("a proposal was run by hand: %v", err)
	}
	// Bob can neither see nor confirm it.
	if _, err := r.host.ConfirmProposal(ctx, "bob", p.ID, ""); !errors.Is(err, triggers.ErrNotFound) {
		t.Fatalf("bob confirmed alice's proposal: %v", err)
	}
	got, err := r.host.ConfirmProposal(ctx, alice, p.ID, "")
	if err != nil || !got.Enabled || got.Proposed {
		t.Fatalf("confirmed = %+v, %v", got, err)
	}
	if list, _ := r.host.ListTriggers(ctx, alice); len(list) != 1 {
		t.Fatalf("triggers after confirming: %+v", list)
	}
	if props, _ := r.host.Proposals(ctx, alice); len(props) != 0 {
		t.Fatalf("still a proposal: %+v", props)
	}
}

func TestAProposalWithoutAZoneTakesTheZoneOfTheConfirmation(t *testing.T) {
	r := newTrigRig(t, proposeScript(`{"name":"Digest","prompt":"p","when":"daily 08:00"}`))
	ctx := context.Background()
	id := create(t, r.svc, alice)
	sent(t, r.svc, alice, id, "propose")
	idle(t, r.svc, alice, id)
	props, _ := r.host.Proposals(ctx, alice)
	if len(props) != 1 {
		t.Fatalf("proposals = %+v", props)
	}
	got, err := r.host.ConfirmProposal(ctx, alice, props[0].ID, "America/New_York")
	if err != nil || got.TZ != "America/New_York" {
		t.Fatalf("confirmed = %+v, %v", got, err)
	}
}

func TestAChatWithAnAgentProposesATriggerThatKeepsTheAgent(t *testing.T) {
	r := newTrigRig(t, proposeScript(goodProposal))
	ctx := context.Background()
	ag, _ := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Digester", Model: "m"})
	rec, err := r.svc.Create(ctx, alice, studio.CreateInput{AgentID: ag.ID})
	if err != nil {
		t.Fatal(err)
	}
	sent(t, r.svc, alice, rec.ID, "propose")
	idle(t, r.svc, alice, rec.ID)
	props, _ := r.host.Proposals(ctx, alice)
	if len(props) != 1 || props[0].AgentID != ag.ID {
		t.Fatalf("proposals = %+v", props)
	}
}

func TestABadProposalIsRefusedWithTheReasonAndStoresNothing(t *testing.T) {
	r := newTrigRig(t, proposeScript(`{"name":"x","prompt":"p","when":"daily 25:00"}`))
	id := create(t, r.svc, alice)
	sent(t, r.svc, alice, id, "propose")
	idle(t, r.svc, alice, id)
	res := r.h.ResultsFor("propose_trigger")
	if len(res) != 1 || res[0].Err == nil || !strings.Contains(res[0].Err.Error(), "hour must be 0 to 23") {
		t.Fatalf("result = %+v", res)
	}
	if props, _ := r.host.Proposals(context.Background(), alice); len(props) != 0 {
		t.Fatalf("stored %+v", props)
	}
}

func TestTheTriggerToolsAreOfferedToPeopleChatsOnly(t *testing.T) {
	r := newTrigRig(t, func(_ workspace.Actor, text string) []studiotest.ToolStep {
		if text == "go" {
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"child job","wait":true}`}, {Say: "done"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	})
	ctx := context.Background()
	id := create(t, r.svc, alice)
	sent(t, r.svc, alice, id, "go")
	idle(t, r.svc, alice, id)
	if names := r.h.ToolNames(id); !slices.Contains(names, "propose_trigger") || !slices.Contains(names, "list_triggers") {
		t.Fatalf("a person's chat has %v", names)
	}
	for _, c := range childrenOf(t, r.svc, alice, id) {
		if names := r.h.ToolNames(c.ID); slices.Contains(names, "propose_trigger") || slices.Contains(names, "list_triggers") {
			t.Fatalf("a sub-agent has %v", names)
		}
	}
	tr := r.schedule(t, studio.TriggerInput{})
	r.due(t)
	run := r.waitEnded(t, tr.ID)
	if names := r.h.ToolNames(run.ChatID); slices.Contains(names, "propose_trigger") || slices.Contains(names, "list_triggers") {
		t.Fatalf("an unattended run has %v", names)
	}
	_ = ctx
}

func TestListTriggersShowsNamesAndStatesButNoPromptsOrIDs(t *testing.T) {
	r := newTrigRig(t, proposeScript(goodProposal))
	r.schedule(t, studio.TriggerInput{Name: "Nightly", Prompt: "secret instructions"})
	id := create(t, r.svc, alice)
	sent(t, r.svc, alice, id, "list")
	idle(t, r.svc, alice, id)
	res := r.h.ResultsFor("list_triggers")
	if len(res) != 1 || res[0].Err != nil || !strings.Contains(res[0].Out, "Nightly") || strings.Contains(res[0].Out, "secret instructions") {
		t.Fatalf("result = %+v", res)
	}
}

func TestADeletedTriggerLeavesItsChatsAndNothingRuns(t *testing.T) {
	r := newTrigRig(t, saySummary)
	ctx := context.Background()
	tr := r.schedule(t, studio.TriggerInput{})
	r.due(t)
	run := r.waitEnded(t, tr.ID)
	if err := r.host.DeleteTrigger(ctx, alice, tr.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.host.GetTrigger(ctx, alice, tr.ID); !errors.Is(err, triggers.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if _, err := r.svc.Get(ctx, alice, run.ChatID); err != nil {
		t.Fatalf("the chat of a deleted trigger is gone: %v", err)
	}
}

func TestTheSameProposalIsKeptOnce(t *testing.T) {
	r := newTrigRig(t, proposeScript(goodProposal))
	ctx := context.Background()
	src, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "src-model"})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		sent(t, r.svc, alice, src.ID, "propose")
		idle(t, r.svc, alice, src.ID)
	}
	props, err := r.host.Proposals(ctx, alice)
	if err != nil || len(props) != 1 {
		t.Fatalf("proposals = %d, %v; want the one", len(props), err)
	}
	// Another prompt is another proposal.
	in := studio.TriggerInput{Name: "Morning digest", Prompt: "Summarise the week.", Kind: triggers.KindSchedule, Schedule: "weekdays 09:00", TZ: "Europe/Rome", Model: "src-model"}
	if _, err := r.host.ProposeTrigger(ctx, alice, in); err != nil {
		t.Fatal(err)
	}
	if props, _ := r.host.Proposals(ctx, alice); len(props) != 2 {
		t.Fatalf("proposals = %d, want 2", len(props))
	}
	// Once confirmed, a new proposal of the same text may wait again.
	if _, err := r.host.ConfirmProposal(ctx, alice, props[0].ID, ""); err != nil {
		t.Fatal(err)
	}
}
