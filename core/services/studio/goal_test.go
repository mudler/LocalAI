package studio_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func newGoalService(h session.Harness, turns, steps int, opts ...func(*studio.Deps)) (*studio.Service, store.Store) {
	st, err := store.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	svc := newServiceOn(st, h, append([]func(*studio.Deps){func(d *studio.Deps) {
		d.GoalLimits = func() (int, int) { return turns, steps }
	}}, opts...)...)
	return svc, st
}

func goalOf(svc *studio.Service, id string) *session.Goal {
	g, err := svc.Goal(context.Background(), alice, id)
	Expect(err).NotTo(HaveOccurred())
	return g
}

// goalLines returns the goal lines of the transcript as "kind:text".
func goalLines(svc *studio.Service, id string) []string {
	es, err := svc.Messages(context.Background(), alice, id, 0, 1000)
	Expect(err).NotTo(HaveOccurred())
	var out []string
	for _, e := range es {
		if e.Role == session.RoleGoal {
			out = append(out, e.Note+":"+e.Text)
		}
	}
	return out
}

func gateTokens(h *studiotest.GateHarness, n int) {
	for range n {
		h.Gate <- struct{}{}
	}
}

func waitGoal(svc *studio.Service, id string, state session.GoalState) {
	until("the goal to be "+string(state), func() bool {
		g := goalOf(svc, id)
		return g != nil && g.State == state && !turn(svc, alice, id).Running
	})
}

var _ = Describe("goal", func() {
	ctx := context.Background()

	It("starts a turn when set, goes on by itself and pauses at the turn limit", func() {
		h := studiotest.NewGateHarness()
		svc, _ := newGoalService(h, 3, 1000)
		id := create(svc, alice)
		gateTokens(h, 10)

		prev, cur, err := svc.SetGoal(ctx, alice, id, "  write the report  ")
		Expect(err).NotTo(HaveOccurred())
		Expect(prev).To(BeNil())
		Expect(cur.Text).To(Equal("write the report"))
		Expect(cur.State).To(Equal(session.GoalActive))
		waitGoal(svc, id, session.GoalPaused)

		g := goalOf(svc, id)
		Expect(g.PausedBy).To(Equal(session.GoalPausedByTurnLimit))
		Expect(g.Turns).To(Equal(3))
		Expect(h.Sent()).To(HaveLen(3), "the kickoff and two more turns, then the limit")
		Expect(h.Sent()[0]).To(ContainSubstring("Work toward this goal now"))
		Expect(h.Sent()[1]).To(Equal(`Continue toward the goal: "write the report".`))
		Expect(goalLines(svc, id)).To(Equal([]string{
			"set:write the report",
			`continue:Work toward this goal now: "write the report".`,
			`continue:Continue toward the goal: "write the report".`,
			`continue:Continue toward the goal: "write the report".`,
			"paused:turn_limit",
		}))
		es, _ := svc.Messages(ctx, alice, id, 0, 1000)
		var revs []int64
		for _, e := range es {
			Expect(e.Role).NotTo(Equal(session.RoleUser), "an automatic message never reads as typed by the person")
			if e.Role == session.RoleGoal && e.Note == session.GoalLineContinue {
				revs = append(revs, e.Rev)
			}
		}
		Expect(revs).To(Equal([]int64{1, 2, 3}), "each automatic line carries its goal turn")
	})

	It("pauses at the step limit", func() {
		h := studiotest.NewGateHarness()
		h.Requests = 2
		svc, _ := newGoalService(h, 100, 3)
		id := create(svc, alice)
		gateTokens(h, 20)
		_, _, err := svc.SetGoal(ctx, alice, id, "count")
		Expect(err).NotTo(HaveOccurred())
		waitGoal(svc, id, session.GoalPaused)
		g := goalOf(svc, id)
		Expect(g.PausedBy).To(Equal(session.GoalPausedByStepLimit))
		Expect(g.Steps).To(Equal(4))
		Expect(h.Sent()).To(HaveLen(2), "2 steps a turn: the second turn reaches 4 of 3")
	})

	It("yields to a waiting message: the person's message runs before the next automatic turn", func() {
		h := studiotest.NewGateHarness()
		h.End = make(chan struct{}, 8)
		svc, _ := newGoalService(h, 2, 1000)
		id := create(svc, alice)
		gateTokens(h, 10)
		_, _, err := svc.SetGoal(ctx, alice, id, "plan the trip")
		Expect(err).NotTo(HaveOccurred())
		until("the kickoff to run", func() bool { return len(h.Sent()) == 1 && h.Boundaries() == 1 })
		r := sent(svc, alice, id, "prefer trains")
		Expect(r.Started).To(BeFalse(), "a message during a goal turn waits")
		h.End <- struct{}{}
		until("the next turn", func() bool { return len(h.Sent()) == 2 })
		Expect(h.Sent()[1]).To(Equal("prefer trains"), "the person's message runs first")
		h.End <- struct{}{}
		until("the automatic turn after it", func() bool { return len(h.Sent()) == 3 })
		Expect(h.Sent()[2]).To(ContainSubstring("Continue toward the goal"))
		Expect(goalOf(svc, id).Turns).To(Equal(2), "the person's turn is not an automatic one")
		h.End <- struct{}{}
		waitGoal(svc, id, session.GoalPaused)
	})

	It("Stop pauses the goal, Resume goes on with fresh budgets, and a reached goal does not resume", func() {
		h := studiotest.NewGateHarness()
		svc, _ := newGoalService(h, 5, 1000)
		id := create(svc, alice)
		_, _, err := svc.SetGoal(ctx, alice, id, "fix the bug")
		Expect(err).NotTo(HaveOccurred())
		until("the kickoff to run", func() bool { return len(h.Sent()) == 1 })
		Expect(svc.Stop(ctx, alice, id)).To(Succeed())
		waitGoal(svc, id, session.GoalPaused)
		Expect(goalOf(svc, id).PausedBy).To(Equal(session.GoalPausedByUser))
		Consistently(func() int { return len(h.Sent()) }, 200*time.Millisecond, 20*time.Millisecond).Should(Equal(1), "nothing goes on after a Stop")

		g, err := svc.ResumeGoal(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(g.State).To(Equal(session.GoalActive))
		until("the resumed turn", func() bool { return len(h.Sent()) == 2 })
		Expect(h.Sent()[1]).To(Equal(`Resume working toward the goal: "fix the bug".`))
		Expect(goalOf(svc, id).Turns).To(Equal(1), "resume starts a fresh budget")
		Expect(goalLines(svc, id)).To(ContainElements("paused:user", "resumed:fix the bug"))

		Expect(svc.Stop(ctx, alice, id)).To(Succeed())
		waitGoal(svc, id, session.GoalPaused)
		Expect(svc.ClearGoal(ctx, alice, id)).Error().NotTo(HaveOccurred())
		_, err = svc.ResumeGoal(ctx, alice, id)
		Expect(err).To(MatchError(studio.ErrNoGoal))
	})

	It("clearing stops what follows the running turn", func() {
		h := studiotest.NewGateHarness()
		svc, _ := newGoalService(h, 5, 1000)
		id := create(svc, alice)
		_, _, err := svc.SetGoal(ctx, alice, id, "tidy up")
		Expect(err).NotTo(HaveOccurred())
		until("the kickoff to run", func() bool { return len(h.Sent()) == 1 })
		prev, err := svc.ClearGoal(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(prev.Text).To(Equal("tidy up"))
		h.Gate <- struct{}{}
		idle(svc, alice, id)
		Consistently(func() int { return len(h.Sent()) }, 200*time.Millisecond, 20*time.Millisecond).Should(Equal(1))
		Expect(goalOf(svc, id)).To(BeNil())
		Expect(goalLines(svc, id)).To(ContainElement("cleared:tidy up"))
		again, err := svc.ClearGoal(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(BeNil(), "clearing a chat with no goal is not an error")
	})

	It("replacing a goal says so and resets its budget", func() {
		h := studiotest.NewGateHarness()
		svc, _ := newGoalService(h, 5, 1000)
		id := create(svc, alice)
		_, _, err := svc.SetGoal(ctx, alice, id, "first")
		Expect(err).NotTo(HaveOccurred())
		until("the kickoff to run", func() bool { return len(h.Sent()) == 1 })
		prev, cur, err := svc.SetGoal(ctx, alice, id, "second")
		Expect(err).NotTo(HaveOccurred())
		Expect(prev.Text).To(Equal("first"))
		Expect(cur.Turns).To(Equal(0), "the running turn is not the new goal's")
		Expect(goalLines(svc, id)).To(ContainElement("replaced:second"))
		Expect(svc.Stop(ctx, alice, id)).To(Succeed())
		waitGoal(svc, id, session.GoalPaused)
	})

	It("refuses bad text, chats that started by themselves and another user's chat", func() {
		h := studiotest.NewGateHarness()
		svc, _ := newGoalService(h, 5, 1000)
		id := create(svc, alice)
		_, _, err := svc.SetGoal(ctx, alice, id, "   ")
		Expect(err).To(MatchError(studio.ErrGoalText))
		_, _, err = svc.SetGoal(ctx, alice, id, strings.Repeat("x", session.MaxGoalLen+1))
		Expect(err).To(MatchError(studio.ErrGoalText))
		_, _, err = svc.SetGoal(ctx, "bob", id, "steal")
		Expect(err).To(MatchError(session.ErrNotFound))
		_, err = svc.Goal(ctx, "bob", id)
		Expect(err).To(MatchError(session.ErrNotFound))
		_, err = svc.ClearGoal(ctx, "bob", id)
		Expect(err).To(MatchError(session.ErrNotFound))
		_, err = svc.ResumeGoal(ctx, "bob", id)
		Expect(err).To(MatchError(session.ErrNotFound))

		for _, origin := range []string{session.OriginTrigger, session.OriginJob, session.OriginConnector} {
			rec, err := svc.CreateAs(ctx, alice, studio.CreateInput{Model: "m"}, studio.Origin{Kind: origin, ID: "x"})
			Expect(err).NotTo(HaveOccurred())
			_, _, err = svc.SetGoal(ctx, alice, rec.ID, "run forever")
			Expect(err).To(MatchError(studio.ErrGoalNotAllowed), origin)
		}
		Expect(h.Sent()).To(BeEmpty())
	})

	It("pauses the goal of a turn that a dead frontend left, and never continues it twice", func() {
		h := studiotest.NewGateHarness()
		svc, st := newGoalService(h, 5, 1000)
		id := create(svc, alice)
		_, _, err := svc.SetGoal(ctx, alice, id, "long job")
		Expect(err).NotTo(HaveOccurred())
		until("the kickoff to run", func() bool { return len(h.Sent()) == 1 })
		// Another process sees the turn as dead: its claim is reaped.
		n, err := svc.ReapStale(ctx, -time.Hour)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))
		g := goalOf(svc, id)
		Expect(g.State).To(Equal(session.GoalPaused))
		Expect(g.PausedBy).To(Equal(session.GoalPausedByInterrupted))
		// The old turn notices it lost its claim and stops; nothing follows it.
		until("the old turn to stop", func() bool {
			info, _ := st.Turn(ctx, alice, id)
			return !info.Running
		})
		Consistently(func() int { return len(h.Sent()) }, 300*time.Millisecond, 20*time.Millisecond).Should(Equal(1))
	})

	It("two frontends: the goal is shared, and the one that ran the turn alone continues it", func() {
		f := newFrontends(nil)
		id := create(f.a, alice)
		events, cancel, err := f.b.Events(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		defer cancel()
		_, _, err = f.a.SetGoal(ctx, alice, id, "shared goal")
		Expect(err).NotTo(HaveOccurred())
		Eventually(events, 5*time.Second).Should(Receive(HaveField("Kind", session.KindGoal)), "B's watchers hear the goal")
		until("A to run the kickoff", func() bool { return len(f.ha.Sent()) == 1 })
		g, err := f.b.Goal(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(g.Text).To(Equal("shared goal"), "B reads the same goal")
		// Resuming on B while A runs a turn starts nothing on B.
		_, err = f.b.ResumeGoal(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		f.ha.Gate <- struct{}{}
		until("A to continue", func() bool { return len(f.ha.Sent()) == 2 })
		Expect(f.hb.Sent()).To(BeEmpty(), "only the frontend that ran the turn continues the goal")
		Expect(f.b.Stop(ctx, alice, id)).To(Succeed())
		until("the goal to pause", func() bool {
			g, _ := f.a.Goal(ctx, alice, id)
			return g.State == session.GoalPaused && !turn(f.a, alice, id).Running
		})
	})

	It("starts one turn when many try to continue the goal at once", func() {
		f := newFrontends(nil)
		id := create(f.a, alice)
		// A goal stored with no turn running: as after a frontend stopped
		// between two turns.
		Expect(f.st.UpdateGoal(ctx, alice, id, func(*session.Goal) (*session.Goal, error) {
			return &session.Goal{Text: "race", State: session.GoalActive}, nil
		})).To(Succeed())
		var wg sync.WaitGroup
		var errs atomic.Int32
		for i := range 8 {
			wg.Add(1)
			go func(i int) {
				defer GinkgoRecover()
				defer wg.Done()
				svc := f.a
				if i%2 == 1 {
					svc = f.b
				}
				if _, err := svc.ResumeGoal(ctx, alice, id); err != nil {
					errs.Add(1)
				}
			}(i)
		}
		wg.Wait()
		Expect(errs.Load()).To(BeZero())
		until("one turn to start", func() bool { return len(f.ha.Sent())+len(f.hb.Sent()) >= 1 })
		Consistently(func() int { return len(f.ha.Sent()) + len(f.hb.Sent()) }, 200*time.Millisecond, 20*time.Millisecond).Should(Equal(1))
		Expect(goalOf(f.a, id).Turns).To(Equal(1))
		Expect(f.a.Stop(ctx, alice, id)).To(Succeed())
	})

	Describe("the goal tools", func() {
		It("are there only while the goal is active, end it, and never reach a sub-agent", func() {
			svc, h := newToolService(func(a workspace.Actor, text string) []studiotest.ToolStep {
				switch {
				case strings.HasPrefix(text, "Work toward this goal"):
					return []studiotest.ToolStep{
						{Tool: "spawn_agent", Args: `{"task":"child task","wait":true}`},
						{Tool: "goal_done", Args: `{"summary":"all done"}`},
						{Say: "finished"},
					}
				case text == "child task":
					return []studiotest.ToolStep{{Say: "child ok"}}
				}
				return []studiotest.ToolStep{{Say: "ok"}}
			}, func(d *studio.Deps) { d.GoalLimits = func() (int, int) { return 5, 100 } })
			id := create(svc, alice)
			sent(svc, alice, id, "hello")
			idle(svc, alice, id)
			Expect(h.ToolNames(id)).NotTo(ContainElement("goal_done"), "no goal, no goal tools")
			Expect(h.TurnPrompt(id)).NotTo(ContainSubstring("Active goal:"))

			_, _, err := svc.SetGoal(ctx, alice, id, "ship it")
			Expect(err).NotTo(HaveOccurred())
			waitGoal(svc, id, session.GoalDone)
			Expect(h.ToolNames(id)).To(ContainElements("goal_done", "goal_blocked"))
			Expect(h.TurnPrompt(id)).To(ContainSubstring("Active goal: ship it"))
			res := h.ResultsFor("goal_done")
			Expect(res).To(HaveLen(1))
			Expect(res[0].Out).To(ContainSubstring("marked as reached"))
			g := goalOf(svc, id)
			Expect(g.Note).To(Equal("all done"))
			Expect(goalLines(svc, id)).To(ContainElement("done:all done"))
			Expect(h.Sent(id)).To(HaveLen(2), "a reached goal does not go on")

			kids := childrenOf(svc, alice, id)
			Expect(kids).To(HaveLen(1))
			Expect(h.ToolNames(kids[0].ID)).NotTo(ContainElement("goal_done"), "a sub-agent never gets the goal tools")
			Expect(h.ToolNames(kids[0].ID)).NotTo(ContainElement("goal_blocked"))
			Expect(h.TurnPrompt(kids[0].ID)).NotTo(ContainSubstring("Active goal"))

			_, err = svc.ResumeGoal(ctx, alice, id)
			Expect(err).To(MatchError(studio.ErrGoalReached))
			sent(svc, alice, id, "thanks")
			idle(svc, alice, id)
			Expect(h.ToolNames(id)).NotTo(ContainElement("goal_done"), "a reached goal takes the tools away")
		})

		It("goal_blocked stops the goal with the reason", func() {
			svc, h := newToolService(func(a workspace.Actor, text string) []studiotest.ToolStep {
				if strings.HasPrefix(text, "Work toward this goal") {
					return []studiotest.ToolStep{{Tool: "goal_blocked", Args: `{"reason":"need the API key"}`}}
				}
				return []studiotest.ToolStep{{Say: "ok"}}
			})
			id := create(svc, alice)
			_, _, err := svc.SetGoal(ctx, alice, id, "deploy")
			Expect(err).NotTo(HaveOccurred())
			waitGoal(svc, id, session.GoalBlocked)
			Expect(goalOf(svc, id).Note).To(Equal("need the API key"))
			Expect(h.Sent(id)).To(HaveLen(1))
			Expect(goalLines(svc, id)).To(ContainElement("blocked:need the API key"))
			_, err = svc.ResumeGoal(ctx, alice, id)
			Expect(err).NotTo(HaveOccurred(), "a blocked goal resumes once the person answered")
			until("the resumed turn", func() bool { return len(h.Sent(id)) >= 2 })
			Expect(h.Sent(id)[1]).To(HavePrefix("Resume working toward the goal"))
			Expect(svc.Stop(ctx, alice, id)).To(Succeed())
			waitGoal(svc, id, session.GoalPaused)
		})

		It("a trigger run never gets them", func() {
			var st store.Store
			svc, h := newToolService(func(workspace.Actor, string) []studiotest.ToolStep {
				return []studiotest.ToolStep{{Say: "ok"}}
			}, func(d *studio.Deps) { st = d.Store })
			rec, err := svc.CreateAs(ctx, alice, studio.CreateInput{Model: "m"}, studio.Origin{Kind: session.OriginTrigger, ID: "t1"})
			Expect(err).NotTo(HaveOccurred())
			// Even a goal written straight into the store gives no tools.
			Expect(st.UpdateGoal(ctx, alice, rec.ID, func(*session.Goal) (*session.Goal, error) {
				return &session.Goal{Text: "forever", State: session.GoalActive}, nil
			})).To(Succeed())
			sent(svc, alice, rec.ID, "run")
			idle(svc, alice, rec.ID)
			Expect(h.ToolNames(rec.ID)).NotTo(ContainElement("goal_done"))
		})
	})
})
