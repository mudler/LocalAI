package studio_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var errFail = errors.New("the model is down")

// withAgents gives the service a store of saved agents.
func withAgents(d *studio.Deps) {
	ag, err := agents.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	d.Agents = ag
}

// watchScript makes a task that starts with "watch" arm a loop, the first run
// of a loop ask for another, and the second one end it.
func watchScript(runs *atomic.Int32) func(workspace.Actor, string) []studiotest.ToolStep {
	return func(_ workspace.Actor, text string) []studiotest.ToolStep {
		switch {
		case strings.Contains(text, "Loop run"):
			if runs.Add(1) == 1 {
				return []studiotest.ToolStep{{Tool: "schedule_next", Args: `{"after":"1m","reason":"again"}`}, {Say: "still waiting"}}
			}
			return []studiotest.ToolStep{{Tool: "loop_done", Args: `{"summary":"it arrived"}`}, {Say: "the file arrived"}}
		case strings.HasPrefix(text, "watch"):
			return []studiotest.ToolStep{{Tool: "start_loop", Args: `{"prompt":"Check the file.","first_run_after":"1m","reason":"the file"}`}, {Say: "armed"}}
		case text == "go":
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"watch the file","wait":false}`}, {Say: "spawned"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
}

var _ = Describe("loops of agents and sub-agents", func() {
	ctx := context.Background()

	Describe("chats of saved agents and of the system", func() {
		It("gives an agent chat the loop tools whatever the agent restricts", func() {
			r := newLoopRig(startLoopScript(armArgs), studio.LoopOptions{}, withAgents)
			ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Narrow", Model: "m",
				BuiltinsOff: []string{builtins.GroupWeb, builtins.GroupFiles, builtins.GroupShell, builtins.GroupTriggers, builtins.GroupSavedAgents, builtins.GroupMedia}})
			Expect(err).NotTo(HaveOccurred())
			rec, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "m", AgentID: ag.ID})
			Expect(err).NotTo(HaveOccurred())
			Expect(rec.Origin).To(BeEmpty())
			sent(r.svc, alice, rec.ID, "arm")
			idle(r.svc, alice, rec.ID)
			Expect(r.h.ToolNames(rec.ID)).To(ContainElement("start_loop"))
			Expect(r.h.ToolNames(rec.ID)).NotTo(ContainElement("read_file"), "the agent's restriction still holds")
			res := r.h.ResultsFor("start_loop")
			Expect(res).To(HaveLen(1))
			Expect(res[0].Err).To(BeNil())

			// The run of the loop has the other two tools.
			list, _ := r.host.List(ctx, alice, rec.ID)
			r.fire(list[0].ID, 2*time.Minute)
			Expect(r.h.ToolNames(rec.ID)).To(ContainElements("schedule_next", "loop_done"))
		})

		for _, kind := range []string{session.OriginTrigger, session.OriginJob, session.OriginPage} {
			It("offers start_loop in a chat with the origin "+kind, func() {
				r := newLoopRig(startLoopScript(armArgs), studio.LoopOptions{Limits: func() studio.LoopLimits { return studio.LoopLimits{MaxRuns: 10} }})
				rec, err := r.svc.CreateAs(ctx, alice, studio.CreateInput{Model: "m"}, studio.Origin{Kind: kind, ID: "o1"})
				Expect(err).NotTo(HaveOccurred())
				sent(r.svc, alice, rec.ID, "arm")
				idle(r.svc, alice, rec.ID)
				Expect(r.h.ToolNames(rec.ID)).To(ContainElement("start_loop"))
				Expect(r.h.ResultsFor("start_loop")[0].Err).To(BeNil())
				list, err := r.host.List(ctx, alice, rec.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(list).To(HaveLen(1))

				// The person-facing start stays closed for such a chat.
				_, err = r.host.Start(ctx, alice, rec.ID, studio.LoopInput{Interval: "5m", Prompt: "x"})
				Expect(err).To(MatchError(studio.ErrLoopChat))

				id := list[0].ID
				Expect(r.fire(id, 2*time.Minute).Runs).To(Equal(1), "the loop of a system run runs")
			})
		}

		It("ends a loop of a system run at a fifth of the run limit", func() {
			r := newLoopRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
				if strings.Contains(text, "Loop run") {
					return []studiotest.ToolStep{{Tool: "schedule_next", Args: `{"after":"1m"}`}, {Say: "later"}}
				}
				return []studiotest.ToolStep{{Tool: "start_loop", Args: `{"prompt":"Check it.","first_run_after":"1m"}`}, {Say: "armed"}}
			}, studio.LoopOptions{Limits: func() studio.LoopLimits { return studio.LoopLimits{MaxRuns: 10} }})
			rec, err := r.svc.CreateAs(ctx, alice, studio.CreateInput{Model: "m"}, studio.Origin{Kind: session.OriginTrigger, ID: "t1"})
			Expect(err).NotTo(HaveOccurred())
			sent(r.svc, alice, rec.ID, "arm")
			idle(r.svc, alice, rec.ID)
			list, _ := r.host.List(ctx, alice, rec.ID)
			id := list[0].ID
			for range 5 {
				r.sh.clock.Advance(time.Minute)
				r.pass()
				r.settled(id)
			}
			l := r.get(id)
			Expect(l.State).To(Equal(loops.StateEnded))
			Expect(l.Runs).To(Equal(2))
			Expect(l.Reason).To(ContainSubstring("2 runs"))
		})

		It("keeps the loop tools from a connector chat that has no sender turn", func() {
			r := newLoopRig(startLoopScript(armArgs), studio.LoopOptions{})
			rec, err := r.svc.CreateAs(ctx, alice, studio.CreateInput{Model: "m"}, studio.Origin{Kind: session.OriginConnector, ID: "c1"})
			Expect(err).NotTo(HaveOccurred())
			sent(r.svc, alice, rec.ID, "hello")
			idle(r.svc, alice, rec.ID)
			Expect(r.h.ToolNames(rec.ID)).NotTo(BeEmpty())
			Expect(r.h.ToolNames(rec.ID)).NotTo(ContainElements("start_loop"))
			Expect(r.h.ToolNames(rec.ID)).NotTo(ContainElements("schedule_next"))
		})
	})

	Describe("a sub-agent that waits for its loop", func() {
		var (
			r      *loopRig
			parent string
			runs   atomic.Int32
		)
		spawn := func(script func(workspace.Actor, string) []studiotest.ToolStep, o studio.LoopOptions) {
			r = newLoopRig(script, o)
			parent = create(r.svc, alice)
			sent(r.svc, alice, parent, "go")
			idle(r.svc, alice, parent)
		}
		child := func() studio.SubAgent {
			subs, err := r.svc.SubAgents(ctx, alice, parent)
			Expect(err).NotTo(HaveOccurred())
			Expect(subs).To(HaveLen(1))
			return subs[0]
		}
		reported := func() bool {
			for _, t := range r.h.Sent(parent) {
				if strings.Contains(t, "[Sub-agent finished") {
					return true
				}
			}
			return false
		}
		armed := func() loops.Loop {
			until("the sub-agent to arm its loop", func() bool {
				subs, _ := r.svc.SubAgents(ctx, alice, parent)
				if len(subs) != 1 {
					return false
				}
				list, _ := r.host.List(ctx, alice, subs[0].Chat.ID)
				return len(list) == 1
			})
			// The loop is armed inside the child's first turn. Wait for that
			// turn to end: a loop whose chat still runs a turn is skipped.
			idle(r.svc, alice, child().Chat.ID)
			list, err := r.host.List(ctx, alice, child().Chat.ID)
			Expect(err).NotTo(HaveOccurred())
			return list[0]
		}

		BeforeEach(func() { runs.Store(0) })

		It("stays running for the parent until loop_done, then reports the last result", func() {
			spawn(watchScript(&runs), studio.LoopOptions{})
			l := armed()
			until("the child's first turn to end", func() bool {
				return !turn(r.svc, alice, child().Chat.ID).Running && turn(r.svc, alice, child().Chat.ID).Last != ""
			})
			Expect(r.h.ToolNames(child().Chat.ID)).To(ContainElement("start_loop"))
			Expect(child().Turn.Running).To(BeTrue(), "a waiting sub-agent looks finished in the list")
			Consistently(reported, 300*time.Millisecond).Should(BeFalse())

			// First run: it asks for another, the child is still not done.
			r.fire(l.ID, time.Minute)
			Expect(r.h.ToolNames(child().Chat.ID)).To(ContainElements("schedule_next", "loop_done"))
			Expect(r.h.ToolNames(child().Chat.ID)).NotTo(ContainElement("start_loop"))
			Expect(child().Turn.Running).To(BeTrue())
			Consistently(reported, 300*time.Millisecond).Should(BeFalse())

			// Second run: loop_done. The parent gets the last result.
			r.fire(l.ID, time.Minute)
			Eventually(reported, 10*time.Second, 10*time.Millisecond).Should(BeTrue())
			Expect(child().Turn.Running).To(BeFalse())
			var msg string
			for _, t := range r.h.Sent(parent) {
				if strings.Contains(t, "[Sub-agent finished") {
					msg = t
				}
			}
			Expect(msg).To(ContainSubstring("the file arrived"))
		})

		It("is told of the end when the run limit ends the loop", func() {
			spawn(func(_ workspace.Actor, text string) []studiotest.ToolStep {
				if strings.Contains(text, "Loop run") {
					return []studiotest.ToolStep{{Tool: "schedule_next", Args: `{"after":"1m"}`}, {Say: "no news"}}
				}
				return watchScript(&runs)(workspace.Actor{}, text)
			}, studio.LoopOptions{Limits: func() studio.LoopLimits { return studio.LoopLimits{MaxRuns: 2} }})
			l := armed()
			for range 2 {
				r.fire(l.ID, time.Minute)
			}
			Eventually(reported, 10*time.Second, 10*time.Millisecond).Should(BeTrue())
			Expect(r.get(l.ID).Reason).To(ContainSubstring("2 runs"))
		})

		It("stops its loop when the parent is stopped", func() {
			spawn(watchScript(&runs), studio.LoopOptions{})
			l := armed()
			until("the child's first turn to end", func() bool { return turn(r.svc, alice, child().Chat.ID).Last != "" })
			Expect(child().Turn.Running).To(BeTrue())
			Expect(r.svc.Stop(ctx, alice, parent)).To(Succeed())
			Eventually(func() bool { return child().Turn.Running }, 10*time.Second, 10*time.Millisecond).Should(BeFalse())
			list, err := r.host.List(ctx, alice, child().Chat.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(BeEmpty(), "the child's loop %s is still there", l.ID)
			r.sh.clock.Advance(time.Hour)
			r.pass()
			Expect(r.runs(child().Chat.ID)).To(BeZero())
			Expect(reported()).To(BeFalse(), "a stopped sub-agent sends no report")
		})

		It("does not let the person start a loop in a sub-agent chat", func() {
			spawn(watchScript(&runs), studio.LoopOptions{})
			armed()
			_, err := r.host.Start(ctx, alice, child().Chat.ID, studio.LoopInput{Interval: "5m", Prompt: "x"})
			Expect(err).To(MatchError(studio.ErrSubAgentReadOnly))
		})

		It("refuses a second loop of the child while the first goes on", func() {
			r = newLoopRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
				if text == "go" {
					return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"watch the file","wait":false}`}, {Say: "spawned"}}
				}
				return []studiotest.ToolStep{{Tool: "start_loop", Args: `{"prompt":"Check.","first_run_after":"1m"}`}, {Tool: "start_loop", Args: `{"prompt":"Again.","first_run_after":"1m"}`}, {Say: "armed"}}
			}, studio.LoopOptions{})
			parent = create(r.svc, alice)
			sent(r.svc, alice, parent, "go")
			idle(r.svc, alice, parent)
			until("both start_loop calls", func() bool { return len(r.h.ResultsFor("start_loop")) == 2 })
			res := r.h.ResultsFor("start_loop")
			Expect(res[0].Err).To(BeNil())
			Expect(res[1].Err).To(MatchError(ContainSubstring("schedule_next")))
		})

		It("counts a looping child once against the limit of background sub-agents", func() {
			r = newLoopRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
				if text == "go" {
					var steps []studiotest.ToolStep
					for _, n := range []string{"a", "b", "c", "d", "e"} {
						steps = append(steps, studiotest.ToolStep{Tool: "spawn_agent", Args: `{"task":"watch ` + n + `","wait":false}`})
					}
					return append(steps, studiotest.ToolStep{Say: "spawned"})
				}
				if !strings.HasPrefix(text, "watch") {
					return []studiotest.ToolStep{{Say: "ok"}}
				}
				return []studiotest.ToolStep{{Tool: "start_loop", Args: `{"prompt":"Check.","first_run_after":"1m"}`}, {Say: "armed"}}
			}, studio.LoopOptions{Limits: func() studio.LoopLimits { return studio.LoopLimits{PerUser: 20} }})
			parent = create(r.svc, alice)
			sent(r.svc, alice, parent, "go")
			idle(r.svc, alice, parent)
			res := r.h.ResultsFor("spawn_agent")
			Expect(res).To(HaveLen(5))
			failed := 0
			for _, x := range res {
				if x.Err != nil {
					Expect(x.Err).To(MatchError(studio.ErrSubAgentLimit))
					failed++
				}
			}
			Expect(failed).To(Equal(1), "the fifth is over the limit of %d", r.svc.SubAgentLimit())
			subs, err := r.svc.SubAgents(ctx, alice, parent)
			Expect(err).NotTo(HaveOccurred())
			Expect(subs).To(HaveLen(4))
			until("all four to arm", func() bool {
				n := 0
				for _, s := range subs {
					if list, _ := r.host.List(ctx, alice, s.Chat.ID); len(list) == 1 {
						n++
					}
				}
				return n == 4
			})
			for _, s := range subs {
				idle(r.svc, alice, s.Chat.ID)
			}
			subs, _ = r.svc.SubAgents(ctx, alice, parent)
			for _, s := range subs {
				Expect(s.Turn.Running).To(BeTrue())
			}
		})

		It("is bound by the caps of the person", func() {
			r = newLoopRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
				if text == "go" {
					return []studiotest.ToolStep{
						{Tool: "spawn_agent", Args: `{"task":"watch one","wait":false}`},
						{Tool: "spawn_agent", Args: `{"task":"watch two","wait":false}`}, {Say: "spawned"}}
				}
				if !strings.HasPrefix(text, "watch") {
					return []studiotest.ToolStep{{Say: "ok"}}
				}
				return []studiotest.ToolStep{{Tool: "start_loop", Args: `{"prompt":"Check.","first_run_after":"1m"}`}, {Say: "armed"}}
			}, studio.LoopOptions{Limits: func() studio.LoopLimits { return studio.LoopLimits{PerUser: 1} }})
			parent = create(r.svc, alice)
			sent(r.svc, alice, parent, "go")
			idle(r.svc, alice, parent)
			until("both children to try", func() bool { return len(r.h.ResultsFor("start_loop")) == 2 })
			errs := 0
			for _, x := range r.h.ResultsFor("start_loop") {
				if x.Err != nil {
					Expect(x.Err).To(MatchError(ContainSubstring("the person has 1 loops")))
					errs++
				}
			}
			Expect(errs).To(Equal(1))
		})

		It("is released and reported failed after three failed runs", func() {
			spawn(func(_ workspace.Actor, text string) []studiotest.ToolStep {
				switch {
				case strings.Contains(text, "Loop run"):
					return []studiotest.ToolStep{{Err: errFail}}
				case strings.HasPrefix(text, "watch"):
					return []studiotest.ToolStep{{Tool: "start_loop", Args: `{"prompt":"Check.","first_run_after":"1m"}`}, {Say: "armed"}}
				case text == "go":
					return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"watch the file","wait":false}`}, {Say: "spawned"}}
				}
				return []studiotest.ToolStep{{Say: "ok"}}
			}, studio.LoopOptions{})
			l := armed()
			for range 3 {
				l = r.fire(l.ID, time.Minute)
				Expect(child().Turn.Running).To(Equal(l.State == loops.StateActive), "the child is running while its loop goes on")
			}
			Expect(l.State).To(Equal(loops.StatePaused))
			Consistently(reported, 300*time.Millisecond).Should(BeFalse(), "a failed sub-agent sends no message")
		})
	})
})
