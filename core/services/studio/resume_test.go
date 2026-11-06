package studio_test

import (
	"context"
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
