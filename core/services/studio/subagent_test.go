package studio_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func searchTool() session.Tool {
	return session.Tool{
		Name: "search", Description: "search the workspace", Schema: json.RawMessage(`{"type":"object"}`),
		Run: func(context.Context, json.RawMessage) (string, error) { return "found", nil },
	}
}

func newToolService(t *testing.T, script func(workspace.Actor, string) []studiotest.ToolStep, opts ...func(*studio.Deps)) (*studio.Service, *studiotest.ToolHarness) {
	t.Helper()
	h := &studiotest.ToolHarness{Script: script}
	st, err := store.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	d := studio.Deps{
		Store: st, Provider: provider.New(), StateDir: t.TempDir(), Harness: h,
		HostTools: func(context.Context, workspace.Actor) ([]session.Tool, error) {
			return []session.Tool{searchTool()}, nil
		},
	}
	for _, o := range opts {
		o(&d)
	}
	svc, err := studio.New(d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close(context.Background()) })
	return svc, h
}

// childrenOf lists the sub-agent conversations of a parent.
func childrenOf(t *testing.T, svc *studio.Service, user, parent string) []session.ChatRecord {
	t.Helper()
	all, err := svc.List(context.Background(), user, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []session.ChatRecord
	for _, r := range all {
		if r.ParentID == parent {
			out = append(out, r)
		}
	}
	return out
}

func idle(t *testing.T, svc *studio.Service, user, id string) {
	t.Helper()
	until(t, "the turn to end", func() bool { return !turn(t, svc, user, id).Running && turn(t, svc, user, id).Last != "" })
}

func TestAForegroundSubAgentAnswersInsideTheParentsTurn(t *testing.T) {
	svc, h := newToolService(t, func(a workspace.Actor, text string) []studiotest.ToolStep {
		switch text {
		case "go":
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"find the answer","wait":true}`}, {Say: "parent done"}}
		case "find the answer":
			return []studiotest.ToolStep{{Say: "42"}}
		}
		return nil
	})
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "go")
	idle(t, svc, alice, id)

	res := h.ResultsFor("spawn_agent")
	if len(res) != 1 || res[0].Err != nil || !strings.Contains(res[0].Out, "42") {
		t.Fatalf("spawn_agent result = %+v, want the child's answer 42", res)
	}
	kids := childrenOf(t, svc, alice, id)
	if len(kids) != 1 || kids[0].Kind != session.KindSubAgent || kids[0].Principal != alice || kids[0].Model != "m" {
		t.Fatalf("children = %+v, want one sub-agent of the same user and model", kids)
	}
}

func TestABackgroundSubAgentReportsBackAsAMessageAndAnEvent(t *testing.T) {
	release := make(chan struct{})
	svc, h := newToolService(t, func(a workspace.Actor, text string) []studiotest.ToolStep {
		switch {
		case text == "go":
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"slow job","wait":false}`}, {Say: "parent moved on"}}
		case text == "slow job":
			return []studiotest.ToolStep{{Block: release}, {Say: "child done: 7"}}
		case strings.Contains(text, "child done: 7"):
			return []studiotest.ToolStep{{Say: "thanks"}}
		}
		return nil
	})
	id := create(t, svc, alice)
	events, cancel, err := svc.Events(context.Background(), alice, id)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	sent(t, svc, alice, id, "go")
	idle(t, svc, alice, id)
	res := h.ResultsFor("spawn_agent")
	if len(res) != 1 || res[0].Err != nil || !strings.Contains(res[0].Out, "Started") {
		t.Fatalf("background spawn returned %+v, want an immediate 'Started'", res)
	}
	if len(h.Sent(id)) != 1 {
		t.Fatalf("the parent has taken another turn too early: %v", h.Sent(id))
	}

	close(release)
	until(t, "the result to reach the parent as a new message", func() bool { return len(h.Sent(id)) == 2 })
	if got := h.Sent(id)[1]; !strings.Contains(got, "child done: 7") {
		t.Fatalf("parent's second message = %q", got)
	}
	timeout := time.After(20 * time.Second)
	for {
		select {
		case e := <-events:
			if e.Kind == session.KindSubAgentDone {
				if len(e.IDs) != 1 || e.IDs[0] != childrenOf(t, svc, alice, id)[0].ID {
					t.Fatalf("sub_agent_done = %+v", e)
				}
				return
			}
		case <-timeout:
			t.Fatal("no sub_agent_done event")
		}
	}
}

func TestASubAgentCannotSpawnAndGetsTheParentsToolsUnlessNarrowed(t *testing.T) {
	svc, h := newToolService(t, func(a workspace.Actor, text string) []studiotest.ToolStep {
		switch text {
		case "inherit":
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"a","wait":true}`}}
		case "none":
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"b","tools":[],"wait":true}`}}
		case "bad":
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"c","tools":["nope"],"wait":true}`}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	})
	id := create(t, svc, alice)

	sent(t, svc, alice, id, "inherit")
	idle(t, svc, alice, id)
	kids := childrenOf(t, svc, alice, id)
	if len(kids) != 1 {
		t.Fatalf("children = %d, want 1", len(kids))
	}
	if got := h.ToolNames(kids[0].ID); !eq(got, []string{"search"}) {
		t.Fatalf("a sub-agent that was not narrowed has tools %v, want only search (never spawn_agent)", got)
	}
	if got := h.ToolNames(id); !eq(got, []string{"search", "spawn_agent"}) {
		t.Fatalf("the parent has tools %v, want search and spawn_agent", got)
	}

	sent(t, svc, alice, id, "none")
	idle(t, svc, alice, id)
	kids = childrenOf(t, svc, alice, id)
	if len(kids) != 2 {
		t.Fatalf("children = %d, want 2", len(kids))
	}
	var narrowed session.ChatRecord
	for _, k := range kids {
		if len(h.ToolNames(k.ID)) == 0 {
			narrowed = k
		}
	}
	if narrowed.ID == "" {
		t.Fatalf("no sub-agent was started with an empty tool list; tool lists: %v / %v", h.ToolNames(kids[0].ID), h.ToolNames(kids[1].ID))
	}

	sent(t, svc, alice, id, "bad")
	idle(t, svc, alice, id)
	bad := h.ResultsFor("spawn_agent")
	last := bad[len(bad)-1]
	if last.Err == nil || !strings.Contains(last.Err.Error(), "nope") {
		t.Fatalf("a tool the parent lacks must be refused by name; got %+v", last)
	}
	if n := len(childrenOf(t, svc, alice, id)); n != 2 {
		t.Fatalf("a refused spawn created a conversation: %d children", n)
	}
}

func TestAtMostFourSubAgentsRunInTheBackground(t *testing.T) {
	never := make(chan struct{})
	t.Cleanup(func() { close(never) })
	svc, h := newToolService(t, func(a workspace.Actor, text string) []studiotest.ToolStep {
		if text == "many" {
			var steps []studiotest.ToolStep
			for _, task := range []string{"t1", "t2", "t3", "t4", "t5"} {
				steps = append(steps, studiotest.ToolStep{Tool: "spawn_agent", Args: `{"task":"` + task + `","wait":false}`})
			}
			return steps
		}
		return []studiotest.ToolStep{{Block: never}}
	})
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "many")
	idle(t, svc, alice, id)
	res := h.ResultsFor("spawn_agent")
	if len(res) != 5 {
		t.Fatalf("spawn calls = %d", len(res))
	}
	for i := 0; i < 4; i++ {
		if res[i].Err != nil {
			t.Errorf("spawn %d failed: %v", i+1, res[i].Err)
		}
	}
	if res[4].Err == nil || !strings.Contains(strings.ToLower(res[4].Err.Error()), "limit") {
		t.Fatalf("the fifth background sub-agent must hit the limit, got %+v", res[4])
	}
	if n := len(childrenOf(t, svc, alice, id)); n != 4 {
		t.Fatalf("children = %d, want 4", n)
	}
}

func TestStoppingTheParentStopsItsRunningSubAgents(t *testing.T) {
	never := make(chan struct{})
	t.Cleanup(func() { close(never) })
	svc, h := newToolService(t, func(a workspace.Actor, text string) []studiotest.ToolStep {
		if text == "go" {
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"blocked child","wait":false}`}, {Block: never}}
		}
		return []studiotest.ToolStep{{Block: never}}
	})
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "go")
	until(t, "the child to run", func() bool {
		k := childrenOf(t, svc, alice, id)
		return len(k) == 1 && turn(t, svc, alice, k[0].ID).Running && len(h.Sent(k[0].ID)) == 1
	})
	if err := svc.Stop(context.Background(), alice, id); err != nil {
		t.Fatal(err)
	}
	child := childrenOf(t, svc, alice, id)[0].ID
	until(t, "the child to stop", func() bool { return turn(t, svc, alice, child).Last == store.TurnStopped })
}

// A sub-agent conversation runs with the tools its parent chose. A message
// from the user would start it with none, so it is read-only.
func TestAUserCannotSendToASubAgentConversation(t *testing.T) {
	svc, _ := newToolService(t, func(a workspace.Actor, text string) []studiotest.ToolStep {
		if text == "go" {
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"job","wait":true}`}, {Say: "done"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	})
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "go")
	idle(t, svc, alice, id)
	kids := childrenOf(t, svc, alice, id)
	if len(kids) != 1 {
		t.Fatalf("children = %d, want 1", len(kids))
	}
	if _, err := svc.Send(context.Background(), alice, kids[0].ID, "more"); !errors.Is(err, studio.ErrSubAgentReadOnly) {
		t.Fatalf("Send to a sub-agent conversation = %v, want ErrSubAgentReadOnly", err)
	}
}

// A turn that the server's shutdown cuts short is interrupted, not done: its
// text is partial, and a run of a trigger or a connector must not report it as
// finished.
func TestATurnCutShortByShutdownIsInterrupted(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	svc, _ := newToolService(t, func(workspace.Actor, string) []studiotest.ToolStep {
		return []studiotest.ToolStep{{Block: block}}
	})
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "work")
	until(t, "the turn to run", func() bool { return turn(t, svc, alice, id).Running })
	if err := svc.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	until(t, "the turn to be settled", func() bool { return !turn(t, svc, alice, id).Running })
	if got := turn(t, svc, alice, id).Last; got != store.TurnInterrupted {
		t.Fatalf("the turn ended as %q, want interrupted", got)
	}
}
