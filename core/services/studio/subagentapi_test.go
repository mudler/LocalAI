package studio_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// toolFrontends builds two services that share one store and one bus, each with
// a scripted tool harness.
func toolFrontends(t *testing.T, scriptA, scriptB func(workspace.Actor, string) []studiotest.ToolStep) (a, b *studio.Service, ha, hb *studiotest.ToolHarness) {
	t.Helper()
	st, err := store.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	net := testutil.NewFakeBus()
	mk := func(script func(workspace.Actor, string) []studiotest.ToolStep) (*studio.Service, *studiotest.ToolHarness) {
		h := &studiotest.ToolHarness{Script: script}
		svc, err := studio.New(studio.Deps{
			Store: st, Provider: provider.New(), StateDir: t.TempDir(), Harness: h,
			Bus: studio.NewBroadcastBus(net, nil), HeartbeatEvery: 10 * time.Millisecond,
			HostTools: func(context.Context, workspace.Actor) ([]session.Tool, error) {
				return []session.Tool{searchTool()}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = svc.Close(context.Background()) })
		return svc, h
	}
	a, ha = mk(scriptA)
	b, hb = mk(scriptB)
	return a, b, ha, hb
}

// The limit of four background sub-agents and the rule that stopping a parent
// stops its children must hold when the turns run on different frontends.
func TestSubAgentLimitAndStopHoldAcrossFrontends(t *testing.T) {
	never := make(chan struct{})
	t.Cleanup(func() { close(never) })
	a, b, _, hb := toolFrontends(t,
		func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "many" {
				var steps []studiotest.ToolStep
				for _, task := range []string{"t1", "t2", "t3", "t4"} {
					steps = append(steps, studiotest.ToolStep{Tool: "spawn_agent", Args: `{"task":"` + task + `","wait":false}`})
				}
				return steps
			}
			return []studiotest.ToolStep{{Block: never}}
		},
		func(_ workspace.Actor, text string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"t5","wait":false}`}}
		})
	id := create(t, a, alice)
	sent(t, a, alice, id, "many")
	idle(t, a, alice, id)
	until(t, "the four children to run", func() bool {
		runs, err := b.SubAgents(context.Background(), alice, id)
		if err != nil || len(runs) != 4 {
			return false
		}
		for _, r := range runs {
			if !r.Turn.Running {
				return false
			}
		}
		return true
	})

	// B runs the next turn of the parent and tries a fifth background child.
	sent(t, b, alice, id, "one more")
	idle(t, b, alice, id)
	res := hb.ResultsFor("spawn_agent")
	if len(res) != 1 || res[0].Err == nil || !strings.Contains(strings.ToLower(res[0].Err.Error()), "limit") {
		t.Fatalf("the fifth sub-agent, started from another frontend, got %+v; want the limit error", res)
	}
	runs, _ := b.SubAgents(context.Background(), alice, id)
	if len(runs) != 4 {
		t.Fatalf("sub-agents = %d, want 4: the refused spawn must not leave a run", len(runs))
	}

	// B stops the parent. The children run on A and still stop.
	if err := b.Stop(context.Background(), alice, id); err != nil {
		t.Fatal(err)
	}
	until(t, "the children to stop", func() bool {
		runs, _ := b.SubAgents(context.Background(), alice, id)
		for _, r := range runs {
			if r.Turn.Running || r.Turn.Last != store.TurnStopped {
				return false
			}
		}
		return len(runs) == 4
	})
}

func TestSubAgentRunsAreListedReadAndStoppedByTheirParentOnly(t *testing.T) {
	never := make(chan struct{})
	t.Cleanup(func() { close(never) })
	svc, _ := newToolService(t, func(_ workspace.Actor, text string) []studiotest.ToolStep {
		if text == "go" {
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"look around","wait":false}`}}
		}
		return []studiotest.ToolStep{{Say: "found it"}, {Block: never}}
	})
	id := create(t, svc, alice)
	other := create(t, svc, alice)
	sent(t, svc, alice, id, "go")
	idle(t, svc, alice, id)
	until(t, "the child to speak", func() bool {
		runs, err := svc.SubAgents(context.Background(), alice, id)
		if err != nil || len(runs) != 1 {
			return false
		}
		msgs, err := svc.SubAgentMessages(context.Background(), alice, id, runs[0].Chat.ID, 0, 100)
		return err == nil && len(msgs) >= 1 && msgs[0].Role == session.RoleUser && msgs[0].Text == "look around"
	})
	runs, _ := svc.SubAgents(context.Background(), alice, id)
	child := runs[0].Chat.ID
	if !runs[0].Turn.Running || !strings.Contains(runs[0].Chat.Title, "look around") {
		t.Fatalf("run = %+v", runs[0])
	}
	if svc.SubAgentLimit() != 4 {
		t.Fatalf("limit = %d", svc.SubAgentLimit())
	}

	// A child belongs to its own parent, its owner and nobody else.
	if _, err := svc.SubAgentMessages(context.Background(), alice, other, child, 0, 10); err == nil {
		t.Error("read a sub-agent through the wrong parent")
	}
	if err := svc.StopSubAgent(context.Background(), alice, other, child); err == nil {
		t.Error("stopped a sub-agent through the wrong parent")
	}
	if _, err := svc.SubAgents(context.Background(), "bob", id); err == nil {
		t.Error("listed another user's sub-agents")
	}
	if err := svc.StopSubAgent(context.Background(), alice, id, child); err != nil {
		t.Fatal(err)
	}
	until(t, "the child to stop", func() bool { return turn(t, svc, alice, child).Last == store.TurnStopped })
}

func TestStoppingAParentWithNoTurnStopsItsBackgroundSubAgents(t *testing.T) {
	never := make(chan struct{})
	t.Cleanup(func() { close(never) })
	svc, _ := newToolService(t, func(_ workspace.Actor, text string) []studiotest.ToolStep {
		if text == "go" {
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"long job","wait":false}`}}
		}
		return []studiotest.ToolStep{{Block: never}}
	})
	id := create(t, svc, alice)
	sent(t, svc, alice, id, "go")
	idle(t, svc, alice, id) // the parent's turn is over; the child still runs
	runs, _ := svc.SubAgents(context.Background(), alice, id)
	if len(runs) != 1 {
		t.Fatalf("runs = %d", len(runs))
	}
	if err := svc.Stop(context.Background(), alice, id); err != nil {
		t.Fatal(err)
	}
	until(t, "the child to stop", func() bool { return turn(t, svc, alice, runs[0].Chat.ID).Last == store.TurnStopped })
}
