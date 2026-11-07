package studio_test

import (
	"context"
	"encoding/json"
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
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/testutil"
)

// resumeRig is a service with AutoResume on, over a store the test keeps, so a
// test can play the part of the frontend that died.
type resumeRig struct {
	svc   *studio.Service
	st    store.Store
	calls *[]string
}

func newResumeRig(auto bool) *resumeRig {
	var calls []string
	var mu sync.Mutex
	st, err := store.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	svc, _ := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
		mu.Lock()
		calls = append(calls, text)
		mu.Unlock()
		return []studiotest.ToolStep{{Say: "finished"}}
	}, func(d *studio.Deps) { d.Store, d.AutoResume = st, auto })
	return &resumeRig{svc: svc, st: st, calls: &calls}
}

// died leaves the chat as a frontend that was killed in the middle of a turn
// does: the question is in the transcript and the claim is still held.
func (r *resumeRig) died(user, id string) string {
	GinkgoHelper()
	ctx := context.Background()
	_, err := r.st.AppendEntry(ctx, id, session.Entry{Turn: 1, Role: session.RoleUser, Text: "build the report"})
	Expect(err).NotTo(HaveOccurred())
	Expect(r.st.ClaimTurn(ctx, user, id, "dead")).To(Succeed())
	return "dead"
}

var _ = Describe("resume of an interrupted turn by the server", func() {
	ctx := context.Background()

	It("shows a reaped turn as one the server will resume, and keeps the goal active", func() {
		r := newResumeRig(true)
		id := create(r.svc, alice)
		r.died(alice, id)
		Expect(r.st.UpdateGoal(ctx, alice, id, func(*session.Goal) (*session.Goal, error) {
			return &session.Goal{Text: "report", State: session.GoalActive}, nil
		})).To(Succeed())

		n, err := r.svc.ReapStale(ctx, -time.Hour)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))
		info := turn(r.svc, alice, id)
		Expect(info.Last).To(Equal(store.TurnInterrupted))
		Expect(info.WillResume).To(BeTrue())
		Expect(goalOf(r.svc, id).State).To(Equal(session.GoalActive), "the goal waits for the resume")
		started, err := r.svc.SweepResumes(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(started).To(BeZero(), "the first wait is not over")
	})

	It("goes on with a due turn, once, and tells the model to go on", func() {
		r := newResumeRig(true)
		id := create(r.svc, alice)
		run := r.died(alice, id)
		Expect(r.st.MarkInterrupted(ctx, alice, id, run, 0)).To(Succeed())

		started, err := r.svc.SweepResumes(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(started).To(Equal(1))
		idle(r.svc, alice, id)
		info := turn(r.svc, alice, id)
		Expect(info.Last).To(Equal(store.TurnDone))
		Expect(info.WillResume).To(BeFalse())
		Expect(*r.calls).To(Equal([]string{session.ResumePrompt}))

		again, _ := r.svc.SweepResumes(ctx)
		Expect(again).To(BeZero(), "a finished turn is not resumed again")
		Expect(*r.calls).To(HaveLen(1))
	})

	It("does nothing when the flag is off", func() {
		r := newResumeRig(false)
		id := create(r.svc, alice)
		run := r.died(alice, id)
		Expect(r.st.MarkInterrupted(ctx, alice, id, run, 0)).To(Succeed())
		Expect(turn(r.svc, alice, id).WillResume).To(BeFalse(), "the page must not wait for a resume that never comes")
		started, err := r.svc.SweepResumes(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(started).To(BeZero())
		Expect(turn(r.svc, alice, id).Last).To(Equal(store.TurnInterrupted))
	})

	It("lets a message of the person win: it starts a normal turn and the sweep finds nothing", func() {
		r := newResumeRig(true)
		id := create(r.svc, alice)
		run := r.died(alice, id)
		Expect(r.st.MarkInterrupted(ctx, alice, id, run, 0)).To(Succeed())
		sent(r.svc, alice, id, "never mind, do this instead")
		idle(r.svc, alice, id)
		started, _ := r.svc.SweepResumes(ctx)
		Expect(started).To(BeZero())
		Expect(*r.calls).To(Equal([]string{"never mind, do this instead"}))
		Expect(turn(r.svc, alice, id).ResumeAttempts).To(BeZero())
	})

	It("gives up after the last try: the goal pauses and nothing starts", func() {
		r := newResumeRig(true)
		id := create(r.svc, alice)
		run := r.died(alice, id)
		Expect(r.st.UpdateGoal(ctx, alice, id, func(*session.Goal) (*session.Goal, error) {
			return &session.Goal{Text: "report", State: session.GoalActive}, nil
		})).To(Succeed())
		Expect(r.st.MarkInterrupted(ctx, alice, id, run, 0)).To(Succeed())
		for i := range store.MaxResumeAttempts {
			Expect(r.st.ClaimResume(ctx, alice, id, "try")).To(Succeed(), "try %d", i+1)
			if i < store.MaxResumeAttempts-1 {
				Expect(r.st.MarkInterrupted(ctx, alice, id, "try", 0)).To(Succeed())
			}
		}
		// The last try dies as well.
		n, err := r.svc.ReapStale(ctx, -time.Hour)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(1))
		info := turn(r.svc, alice, id)
		Expect(info.Last).To(Equal(store.TurnInterrupted))
		Expect(info.WillResume).To(BeFalse())
		Expect(goalOf(r.svc, id).State).To(Equal(session.GoalPaused))
		started, _ := r.svc.SweepResumes(ctx)
		Expect(started).To(BeZero())
		// A person can still resume it.
		res, err := r.svc.Resume(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Started).To(BeTrue())
		idle(r.svc, alice, id)
	})

	It("leaves a chat that a connection started to the sender's reply", func() {
		r := newResumeRig(true)
		Expect(r.st.CreateChat(ctx, session.ChatRecord{ID: "c1", Principal: alice, Title: "Slack", Model: "m", Origin: session.OriginConnector, OriginID: "b1",
			Created: time.Now(), Updated: time.Now()})).To(Succeed())
		r.died(alice, "c1")
		n, _ := r.svc.ReapStale(ctx, -time.Hour)
		Expect(n).To(Equal(1))
		Expect(turn(r.svc, alice, "c1").WillResume).To(BeFalse())
		info, _ := r.st.Turn(ctx, alice, "c1")
		Expect(info.WillResume).To(BeFalse(), "the schedule is dropped, not only hidden")
		started, _ := r.svc.SweepResumes(ctx)
		Expect(started).To(BeZero())
	})

	It("two frontends that sweep at the same time start one turn", func() {
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		net := testutil.NewFakeBus()
		var runs atomic.Int32
		mk := func() *studio.Service {
			svc, _ := newToolService(func(workspace.Actor, string) []studiotest.ToolStep {
				runs.Add(1)
				return []studiotest.ToolStep{{Say: "finished"}}
			}, func(d *studio.Deps) {
				d.Store, d.AutoResume, d.Bus = st, true, studio.NewBroadcastBus(net, nil)
			})
			return svc
		}
		a, b := mk(), mk()
		id := create(a, alice)
		_, err = st.AppendEntry(ctx, id, session.Entry{Turn: 1, Role: session.RoleUser, Text: "build the report"})
		Expect(err).NotTo(HaveOccurred())
		Expect(st.ClaimTurn(ctx, alice, id, "dead")).To(Succeed())
		Expect(st.MarkInterrupted(ctx, alice, id, "dead", 0)).To(Succeed())

		var total atomic.Int32
		var wg sync.WaitGroup
		for _, svc := range []*studio.Service{a, b} {
			wg.Go(func() {
				defer GinkgoRecover()
				n, err := svc.SweepResumes(ctx)
				Expect(err).NotTo(HaveOccurred())
				total.Add(int32(n)) // #nosec G115 -- a count of one or two
			})
		}
		wg.Wait()
		Expect(total.Load()).To(BeEquivalentTo(1))
		idle(a, alice, id)
		Expect(runs.Load()).To(BeEquivalentTo(1))
	})
})

var _ = Describe("a trigger run across a resume", func() {
	It("stays running while the chat waits for its resume, and ends with the resumed turn", func() {
		ctx := context.Background()
		block := make(chan struct{})
		DeferCleanup(func() { close(block) })
		sh := newShared()
		sh.deps = func(d *studio.Deps) { d.AutoResume = true }
		blocking := func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Block: block}} }
		a := sh.rig(blocking, studio.TriggerOptions{})
		tr := a.schedule(studio.TriggerInput{})
		sh.clock.Advance(61 * time.Minute)
		a.pass()
		var run triggers.Run
		until("the run to start", func() bool {
			runs, _ := sh.trg.ListRuns(ctx, tr.ID, 0)
			if len(runs) != 1 {
				return false
			}
			run = runs[0]
			return run.Status == triggers.StatusRunning && run.ChatID != ""
		})
		// The frontend dies: its host stops and its turn is cut short, with the
		// resume due at once.
		a.host.Close()
		info, err := sh.chats.Turn(ctx, alice, run.ChatID)
		Expect(err).NotTo(HaveOccurred())
		Expect(sh.chats.MarkInterrupted(ctx, alice, run.ChatID, info.RunID, 0)).To(Succeed())

		finished := func(text string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: text}} }
		b := sh.rig(func(workspace.Actor, string) []studiotest.ToolStep { return finished("all done") }, studio.TriggerOptions{})
		b.pass()
		got, _ := sh.trg.GetRun(ctx, run.ID)
		Expect(got.Status).To(Equal(triggers.StatusRunning), "the run is not failed while its chat resumes")

		started, err := b.svc.SweepResumes(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(started).To(Equal(1))
		idle(b.svc, alice, run.ChatID)
		b.pass()
		until("the run to end", func() bool {
			got, _ = sh.trg.GetRun(ctx, run.ID)
			return got.Status == triggers.StatusDone
		})
		Expect(got.Reason).To(BeEmpty())
	})
})

var _ = Describe("the text of an answer that a death cut off", func() {
	ctx := context.Background()

	// A service on a shared store; script is what its model does.
	type node struct {
		svc   *studio.Service
		calls *[]string
	}
	mk := func(st store.Store, auto bool, script func(text string) []studiotest.ToolStep) node {
		var calls []string
		var mu sync.Mutex
		svc, _ := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			mu.Lock()
			calls = append(calls, text)
			mu.Unlock()
			return script(text)
		}, func(d *studio.Deps) { d.Store, d.AutoResume = st, auto })
		return node{svc: svc, calls: &calls}
	}

	It("is saved while it streams, becomes a line on resume and the model is told where it stopped", func() {
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		gate1, gate2 := make(chan struct{}), make(chan struct{})
		DeferCleanup(func() { close(gate2) })
		long := strings.Repeat("The quarterly totals are ", 100) // about 2.5 KiB
		a := mk(st, false, func(text string) []studiotest.ToolStep {
			if text == "write the report" {
				// Two pieces with a gap: the second one makes 5 KiB, past the
				// size that is saved without waiting two seconds.
				return []studiotest.ToolStep{{Say: long}, {Block: gate1}, {Say: long + "and the last words"}, {Block: gate2}}
			}
			return []studiotest.ToolStep{{Say: "finished"}}
		})
		b := mk(st, true, func(string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "finished"}} })
		id := create(a.svc, alice)
		sent(a.svc, alice, id, "write the report")
		time.Sleep(400 * time.Millisecond) // past the floor between two saves
		close(gate1)
		saved := func() string {
			// Peek without taking: the store gives the text only through Take, so
			// take it and put it back.
			turn, text, err := st.TakePartial(ctx, id)
			Expect(err).NotTo(HaveOccurred())
			if text != "" {
				Expect(st.SavePartial(ctx, id, turn, text)).To(Succeed())
			}
			return text
		}
		until("the text so far to be saved", func() bool { return strings.HasSuffix(saved(), "and the last words") })

		// The frontend dies: another one resumes the turn at once.
		info := turn(a.svc, alice, id)
		Expect(st.MarkInterrupted(ctx, alice, id, info.RunID, 0)).To(Succeed())
		started, err := b.svc.SweepResumes(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(started).To(Equal(1))
		idle(b.svc, alice, id)

		var cut []session.Entry
		for _, e := range transcript(b.svc, alice, id) {
			if e.Role == session.RoleAssistant && e.Note == session.NoteCut {
				cut = append(cut, e)
			}
		}
		Expect(cut).To(HaveLen(1), "the text that was written is in the transcript, once")
		Expect(cut[0].Text).To(HaveSuffix("and the last words"))
		Expect(*b.calls).To(HaveLen(1))
		Expect((*b.calls)[0]).To(ContainSubstring("Your last message was cut off"))
		Expect((*b.calls)[0]).To(ContainSubstring("and the last words"))
		Expect(st.TakePartial(ctx, id)).Error().NotTo(HaveOccurred())
		_, left, _ := st.TakePartial(ctx, id)
		Expect(left).To(BeEmpty())
	})

	It("writes nothing for a message that ends quickly, and nothing is left after a finished turn", func() {
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		n := mk(st, false, func(string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Say: strings.Repeat("a long answer ", 400)}}
		})
		id := create(n.svc, alice)
		sent(n.svc, alice, id, "go")
		idle(n.svc, alice, id)
		_, text, err := st.TakePartial(ctx, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(text).To(BeEmpty())
		for _, e := range transcript(n.svc, alice, id) {
			Expect(e.Note).NotTo(Equal(session.NoteCut))
		}
	})

	It("keeps what a person's new message follows: the cut text goes in before it", func() {
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		n := mk(st, false, func(string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} })
		id := create(n.svc, alice)
		_, err = st.AppendEntry(ctx, id, session.Entry{Turn: 1, Role: session.RoleUser, Text: "first question"})
		Expect(err).NotTo(HaveOccurred())
		Expect(st.SavePartial(ctx, id, 1, "an answer that stopped mid")).To(Succeed())
		Expect(st.ClaimTurn(ctx, alice, id, "dead")).To(Succeed())
		Expect(st.MarkInterrupted(ctx, alice, id, "dead", -1)).To(Succeed())
		sent(n.svc, alice, id, "second question")
		idle(n.svc, alice, id)
		var seen []string
		for _, e := range transcript(n.svc, alice, id) {
			if e.Role == session.RoleUser || e.Role == session.RoleAssistant {
				seen = append(seen, e.Role+": "+e.Text)
			}
		}
		Expect(seen).To(Equal([]string{"user: first question", "assistant: an answer that stopped mid", "user: second question", "assistant: ok"}))
	})
})

var _ = Describe("Close with a limit", func() {
	ctx := context.Background()

	// stuckTool is a tool that ignores its context: it holds the turn until the
	// test lets go.
	stuckTool := func(release <-chan struct{}) session.Tool {
		return session.Tool{
			Name: "deploy", Description: "deploy", Schema: json.RawMessage(`{"type":"object"}`),
			Run: func(context.Context, json.RawMessage) (string, error) { <-release; return "done", nil },
		}
	}

	It("returns in time, marks the turn that does not stop as interrupted, and the turn can resume", func() {
		release := make(chan struct{})
		DeferCleanup(func() { close(release) })
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		svc, _ := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "deploy", Args: `{}`}, {Say: "done"}}
		}, func(d *studio.Deps) {
			d.Store, d.AutoResume, d.CloseTimeout = st, true, 300*time.Millisecond
			d.HostTools = func(context.Context, workspace.Actor) ([]session.Tool, error) {
				return []session.Tool{stuckTool(release)}, nil
			}
		})
		id := create(svc, alice)
		Expect(st.UpdateGoal(ctx, alice, id, func(*session.Goal) (*session.Goal, error) {
			return &session.Goal{Text: "ship", State: session.GoalActive}, nil
		})).To(Succeed())
		sent(svc, alice, id, "ship it")
		until("the tool to run", func() bool {
			es, _ := st.Entries(ctx, id, 0, 0)
			for _, e := range es {
				if e.Role == session.RoleToolIntent {
					return true
				}
			}
			return false
		})

		began := time.Now()
		err = svc.Close(ctx)
		Expect(err).To(MatchError(studio.ErrCloseTimedOut))
		Expect(time.Since(began)).To(BeNumerically("<", 3*time.Second), "Close did not wait for the stuck tool")
		info, err := st.Turn(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Running).To(BeFalse())
		Expect(info.Last).To(Equal(store.TurnInterrupted))
		Expect(info.WillResume).To(BeTrue(), "a turn that Close cut is resumable")
		Expect(goalOf(svc, id).State).To(Equal(session.GoalActive), "the goal waits for the resume")
	})

	It("does not mark anything when the turns stop in time", func() {
		svc, _ := newToolService(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Say: "ok"}}
		}, func(d *studio.Deps) { d.CloseTimeout = 5 * time.Second })
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		Expect(svc.Close(ctx)).To(Succeed())
		Expect(turn(svc, alice, id).Last).To(Equal(store.TurnDone))
	})
})
