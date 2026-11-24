package studio_test

import (
	"context"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/testutil"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// toolFrontends builds two services that share one store and one bus, each with
// a scripted tool harness.
func toolFrontends(scriptA, scriptB func(workspace.Actor, string) []studiotest.ToolStep) (a, b *studio.Service, ha, hb *studiotest.ToolHarness) {
	st, err := store.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	net := testutil.NewFakeBus()
	mk := func(script func(workspace.Actor, string) []studiotest.ToolStep) (*studio.Service, *studiotest.ToolHarness) {
		h := &studiotest.ToolHarness{Script: script}
		svc, err := studio.New(studio.Deps{
			Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h,
			Bus: studio.NewBroadcastBus(net, nil), HeartbeatEvery: 10 * time.Millisecond,
			HostTools: func(context.Context, workspace.Actor) ([]session.Tool, error) {
				return []session.Tool{searchTool()}, nil
			},
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(context.Background()) })
		return svc, h
	}
	a, ha = mk(scriptA)
	b, hb = mk(scriptB)
	return a, b, ha, hb
}

// The limit of four background sub-agents and the rule that stopping a parent
// stops its children must hold when the turns run on different frontends.
var _ = Describe("subagentapi", func() {
	It("sub agent limit and stop hold across frontends", func() {
		never := make(chan struct{})
		DeferCleanup(func() { close(never) })
		a, b, _, hb := toolFrontends(
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
		id := create(a, alice)
		sent(a, alice, id, "many")
		idle(a, alice, id)
		until("the four children to run", func() bool {
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
		sent(b, alice, id, "one more")
		idle(b, alice, id)
		res := hb.ResultsFor("spawn_agent")
		Expect(res).To(HaveLen(1), "the fifth sub-agent, started from another frontend, got %+v; want the limit error", res)
		Expect(res[0].Err).NotTo(BeNil(), "the fifth sub-agent, started from another frontend, got %+v; want the limit error", res)
		Expect(strings.ToLower(res[0].Err.Error())).To(ContainSubstring("limit"), "the fifth sub-agent, started from another frontend, got %+v; want the limit error", res)
		runs, _ := b.SubAgents(context.Background(), alice, id)
		Expect(runs).To(HaveLen(4), "sub-agents = %d, want 4: the refused spawn must not leave a run", len(runs))

		// B stops the parent. The children run on A and still stop.
		Expect(b.Stop(context.Background(), alice, id)).To(Succeed())
		until("the children to stop", func() bool {
			runs, _ := b.SubAgents(context.Background(), alice, id)
			for _, r := range runs {
				if r.Turn.Running || r.Turn.Last != store.TurnStopped {
					return false
				}
			}
			return len(runs) == 4
		})
	})

	It("sub agent runs are listed read and stopped by their parent only", func() {
		never := make(chan struct{})
		DeferCleanup(func() { close(never) })
		svc, _ := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "go" {
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"look around","wait":false}`}}
			}
			return []studiotest.ToolStep{{Say: "found it"}, {Block: never}}
		})
		id := create(svc, alice)
		other := create(svc, alice)
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		until("the child to speak", func() bool {
			runs, err := svc.SubAgents(context.Background(), alice, id)
			if err != nil || len(runs) != 1 {
				return false
			}
			msgs, err := svc.SubAgentMessages(context.Background(), alice, id, runs[0].Chat.ID, 0, 100)
			return err == nil && len(msgs) >= 1 && msgs[0].Role == session.RoleUser && msgs[0].Text == "look around"
		})
		runs, _ := svc.SubAgents(context.Background(), alice, id)
		child := runs[0].Chat.ID
		Expect(runs[0].Turn.Running).To(BeTrue(), "run = %+v", runs[0])
		Expect(runs[0].Chat.Title).To(ContainSubstring("look around"), "run = %+v", runs[0])
		Expect(svc.SubAgentLimit()).To(BeNumerically("==", 4), "limit = %d", svc.SubAgentLimit())

		// A child belongs to its own parent, its owner and nobody else.
		_, err := svc.SubAgentMessages(context.Background(), alice, other, child, 0, 10)
		Expect(err).To(HaveOccurred(), "read a sub-agent through the wrong parent")
		Expect(svc.StopSubAgent(context.Background(), alice, other, child)).To(HaveOccurred(), "stopped a sub-agent through the wrong parent")
		_, err = svc.SubAgents(context.Background(), "bob", id)
		Expect(err).To(HaveOccurred(), "listed another user's sub-agents")
		Expect(svc.StopSubAgent(context.Background(), alice, id, child)).To(Succeed())
		until("the child to stop", func() bool { return turn(svc, alice, child).Last == store.TurnStopped })
	})

	It("stopping a parent with no turn stops its background sub agents", func() {
		never := make(chan struct{})
		DeferCleanup(func() { close(never) })
		svc, _ := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "go" {
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"long job","wait":false}`}}
			}
			return []studiotest.ToolStep{{Block: never}}
		})
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		idle(svc, alice, id) // the parent's turn is over; the child still runs
		runs, _ := svc.SubAgents(context.Background(), alice, id)
		Expect(runs).To(HaveLen(1), "runs = %d", len(runs))
		Expect(svc.Stop(context.Background(), alice, id)).To(Succeed())
		until("the child to stop", func() bool { return turn(svc, alice, runs[0].Chat.ID).Last == store.TurnStopped })
	})
})
