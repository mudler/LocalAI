package studio_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// loopShared is what the frontends of one cluster share for the loops.
type loopShared struct {
	*shared
	loops loops.Store
	users func(string) error
}

func newLoopShared() *loopShared {
	ls, err := loops.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	return &loopShared{shared: newShared(), loops: ls}
}

type loopRig struct {
	sh   *loopShared
	svc  *studio.Service
	h    *studiotest.ToolHarness
	host *studio.Loops
}

func (sh *loopShared) rig(script func(workspace.Actor, string) []studiotest.ToolStep, o studio.LoopOptions) *loopRig {
	svc, h := newToolService(script, func(d *studio.Deps) {
		d.Store, d.Files = sh.chats, sh.files
		if sh.users != nil {
			d.Users = func(_ context.Context, id string) (*auth.User, error) { return &auth.User{ID: id}, sh.users(id) }
		}
	})
	o.Store, o.Clock = sh.loops, sh.clock.Now
	if o.Tick == 0 {
		o.Tick = time.Hour // tests call Pass
	}
	host, err := studio.NewLoops(svc, o)
	Expect(err).NotTo(HaveOccurred())
	svc.SetLoops(host)
	DeferCleanup(host.Close)
	return &loopRig{sh: sh, svc: svc, h: h, host: host}
}

func newLoopRig(script func(workspace.Actor, string) []studiotest.ToolStep, o studio.LoopOptions) *loopRig {
	return newLoopShared().rig(script, o)
}

// say answers every message with ok.
func say(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Say: "ok"}} }

func (r *loopRig) pass() { Expect(r.host.Pass(context.Background())).To(Succeed()) }

// start starts a loop and confirms it when it waits for a yes.
func (r *loopRig) start(chat, interval, prompt string) loops.Loop {
	l, err := r.host.Start(context.Background(), alice, chat, studio.LoopInput{Interval: interval, Prompt: prompt})
	Expect(err).NotTo(HaveOccurred())
	if l.State == loops.StatePending {
		l, err = r.host.SetState(context.Background(), alice, chat, l.ID, loops.StateActive)
		Expect(err).NotTo(HaveOccurred())
	}
	return l
}

func (r *loopRig) get(id string) loops.Loop {
	l, err := r.sh.loops.Get(context.Background(), id)
	Expect(err).NotTo(HaveOccurred())
	return l
}

// settled waits until the run of a loop has been recorded as ended.
func (r *loopRig) settled(id string) loops.Loop {
	var l loops.Loop
	until("the loop run to end", func() bool {
		l = r.get(id)
		return !l.Running
	})
	return l
}

func (r *loopRig) runs(chat string) int {
	n := 0
	for _, t := range r.h.Sent(chat) {
		if strings.Contains(t, "[Loop run") {
			n++
		}
	}
	return n
}

var _ = Describe("Loops of a chat", func() {
	ctx := context.Background()

	It("asks for a yes the first time in a chat, then starts at once", func() {
		r := newLoopRig(say, studio.LoopOptions{})
		chat := create(r.svc, alice)
		first, err := r.host.Start(ctx, alice, chat, studio.LoopInput{Interval: "5m", Prompt: "Check the build."})
		Expect(err).NotTo(HaveOccurred())
		Expect(first.State).To(Equal(loops.StatePending))
		r.pass()
		Expect(r.runs(chat)).To(Equal(0), "a loop that waits for a yes ran")
		_, err = r.host.SetState(ctx, alice, chat, first.ID, loops.StatePaused)
		Expect(err).To(MatchError(studio.ErrLoopState), "a loop that waits for a yes cannot be paused")

		got, err := r.host.SetState(ctx, alice, chat, first.ID, loops.StateActive)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(loops.StateActive))
		r.pass()
		r.settled(first.ID)
		Expect(r.runs(chat)).To(Equal(1))

		second, err := r.host.Start(ctx, alice, chat, studio.LoopInput{Prompt: "Watch the queue."})
		Expect(err).NotTo(HaveOccurred())
		Expect(second.State).To(Equal(loops.StateActive), "the second loop of a chat asked again")
		Expect(second.SelfPaced()).To(BeTrue())
	})

	It("repeats a prompt at its interval and labels the messages it sent", func() {
		r := newLoopRig(say, studio.LoopOptions{})
		chat := create(r.svc, alice)
		l := r.start(chat, "5m", "Check the build.")
		r.pass()
		l = r.settled(l.ID)
		Expect(l.Runs).To(Equal(1))
		Expect(l.Next).To(Equal(r.sh.clock.Now().UTC().Add(5 * time.Minute)))

		r.sh.clock.Advance(4 * time.Minute)
		r.pass()
		Expect(r.get(l.ID).Runs).To(Equal(1), "it ran before its interval")
		r.sh.clock.Advance(time.Minute)
		r.pass()
		l = r.settled(l.ID)
		Expect(l.Runs).To(Equal(2))

		sent := r.h.Sent(chat)
		Expect(sent).To(HaveLen(2))
		Expect(sent[1]).To(HavePrefix("[Loop run 2:"))
		Expect(sent[1]).To(HaveSuffix("Check the build."))
		msgs, err := r.svc.Messages(ctx, alice, chat, 0, 100)
		Expect(err).NotTo(HaveOccurred())
		var marks []session.LoopMark
		for _, m := range msgs {
			if m.Role == session.RoleUser {
				Expect(m.Text).To(Equal("Check the build."), "the transcript shows the prompt as the person wrote it")
				Expect(m.Loop).NotTo(BeNil())
				marks = append(marks, *m.Loop)
			}
		}
		Expect(marks).To(Equal([]session.LoopMark{{ID: l.ID, Run: 1}, {ID: l.ID, Run: 2}}))
	})

	It("never starts a run while the chat is busy and skips the missed ones", func() {
		block := make(chan struct{})
		r := newLoopRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "long task" {
				return []studiotest.ToolStep{{Block: block}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, studio.LoopOptions{})
		chat := create(r.svc, alice)
		l := r.start(chat, "5m", "Check the build.")
		r.pass()
		r.settled(l.ID)
		sent(r.svc, alice, chat, "long task")
		until("the turn to run", func() bool { return turn(r.svc, alice, chat).Running })

		r.sh.clock.Advance(17 * time.Minute) // three slots missed
		r.pass()
		got := r.get(l.ID)
		Expect(got.Runs).To(Equal(1), "a run started in a busy chat")
		Expect(got.Running).To(BeFalse())
		Expect(got.Next).To(Equal(time.Date(2026, 10, 27, 7, 20, 0, 0, time.UTC)), "the next slot after the busy time, not a backlog")

		close(block)
		idle(r.svc, alice, chat)
		r.sh.clock.Advance(3 * time.Minute)
		r.pass()
		got = r.settled(l.ID)
		Expect(got.Runs).To(Equal(2))
		Expect(r.runs(chat)).To(Equal(2), "the missed slots were run")
	})

	Describe("self-paced", func() {
		It("runs again when the model asks, with the wait clamped to the minimum", func() {
			r := newLoopRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
				return []studiotest.ToolStep{{Tool: "schedule_next", Args: `{"after":"10s","reason":"the build"}`}, {Say: "later"}}
			}, studio.LoopOptions{})
			chat := create(r.svc, alice)
			l := r.start(chat, "", "Check the build.")
			r.pass()
			l = r.settled(l.ID)
			Expect(l.State).To(Equal(loops.StateActive))
			Expect(l.Wait).To(Equal("the build"))
			Expect(l.Next).To(Equal(r.sh.clock.Now().UTC().Add(time.Minute)), "10s is below the minimum of 1m")
			res := r.h.ResultsFor("schedule_next")
			Expect(res).To(HaveLen(1))
			Expect(res[0].Err).To(BeNil())
			Expect(res[0].Out).To(ContainSubstring("set to 1m"))
			Expect(r.h.ToolNames(chat)).To(ContainElements("schedule_next", "loop_done"))

			r.sh.clock.Advance(time.Minute)
			r.pass()
			Expect(r.settled(l.ID).Runs).To(Equal(2))
		})

		It("clamps a wait to one day", func() {
			r := newLoopRig(func(workspace.Actor, string) []studiotest.ToolStep {
				return []studiotest.ToolStep{{Tool: "schedule_next", Args: `{"after":"72h"}`}}
			}, studio.LoopOptions{})
			chat := create(r.svc, alice)
			l := r.start(chat, "", "Check the build.")
			r.pass()
			Expect(r.settled(l.ID).Next).To(Equal(r.sh.clock.Now().UTC().Add(24 * time.Hour)))
		})

		It("stops when the model says the task is done", func() {
			r := newLoopRig(func(workspace.Actor, string) []studiotest.ToolStep {
				return []studiotest.ToolStep{{Tool: "loop_done", Args: `{"summary":"the build is green"}`}, {Say: "done"}}
			}, studio.LoopOptions{})
			chat := create(r.svc, alice)
			l := r.start(chat, "", "Check the build.")
			r.pass()
			l = r.settled(l.ID)
			Expect(l.State).To(Equal(loops.StateEnded))
			Expect(l.Reason).To(Equal("Done: the build is green"))
			r.sh.clock.Advance(time.Hour)
			r.pass()
			Expect(r.runs(chat)).To(Equal(1))
		})

		It("stops with a note when the model asks for no further run", func() {
			r := newLoopRig(say, studio.LoopOptions{})
			chat := create(r.svc, alice)
			l := r.start(chat, "", "Check the build.")
			r.pass()
			l = r.settled(l.ID)
			Expect(l.State).To(Equal(loops.StateEnded))
			Expect(l.Reason).To(ContainSubstring("did not ask for another run"))
		})

		It("refuses the tools outside a loop run", func() {
			r := newLoopRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
				if text == "hello" {
					return []studiotest.ToolStep{{Tool: "schedule_next", Args: `{"after":"5m"}`}}
				}
				return []studiotest.ToolStep{{Tool: "schedule_next", Args: `{"after":"5m"}`}}
			}, studio.LoopOptions{})
			chat := create(r.svc, alice)
			l := r.start(chat, "", "Check the build.")
			r.pass()
			r.settled(l.ID)
			sent(r.svc, alice, chat, "hello")
			idle(r.svc, alice, chat)
			res := r.h.ResultsFor("schedule_next")
			Expect(res).To(HaveLen(2))
			Expect(res[1].Err).To(HaveOccurred(), "a turn of the person scheduled the loop")
		})
	})

	It("gives a chat without a self-paced loop no loop tools", func() {
		r := newLoopRig(say, studio.LoopOptions{})
		chat := create(r.svc, alice)
		l := r.start(chat, "5m", "Check the build.")
		r.pass()
		r.settled(l.ID)
		Expect(r.h.ToolNames(chat)).NotTo(ContainElement("schedule_next"))
	})

	Describe("limits", func() {
		It("ends a loop after its most runs", func() {
			r := newLoopRig(say, studio.LoopOptions{Limits: func() studio.LoopLimits { return studio.LoopLimits{MaxRuns: 2} }})
			chat := create(r.svc, alice)
			l := r.start(chat, "1m", "Check the build.")
			r.pass()
			r.settled(l.ID)
			r.sh.clock.Advance(time.Minute)
			r.pass()
			l = r.settled(l.ID)
			Expect(l.State).To(Equal(loops.StateEnded))
			Expect(l.Reason).To(ContainSubstring("2 runs"))
		})

		It("keeps at most the loops a chat and a user may have", func() {
			r := newLoopRig(say, studio.LoopOptions{Limits: func() studio.LoopLimits { return studio.LoopLimits{PerChat: 2, PerUser: 3} }})
			a, b := create(r.svc, alice), create(r.svc, alice)
			r.start(a, "5m", "one")
			r.start(a, "5m", "two")
			_, err := r.host.Start(ctx, alice, a, studio.LoopInput{Interval: "5m", Prompt: "three"})
			Expect(err).To(MatchError(loops.ErrTooManyInChat))
			r.start(b, "5m", "three")
			_, err = r.host.Start(ctx, alice, b, studio.LoopInput{Interval: "5m", Prompt: "four"})
			Expect(err).To(MatchError(loops.ErrTooManyForOwner))
		})

		It("refuses an interval below the minimum and a command that is not a skill", func() {
			r := newLoopRig(say, studio.LoopOptions{})
			chat := create(r.svc, alice)
			_, err := r.host.Start(ctx, alice, chat, studio.LoopInput{Interval: "30s", Prompt: "x"})
			Expect(err).To(MatchError(loops.ErrInvalid))
			_, err = r.host.Start(ctx, alice, chat, studio.LoopInput{Interval: "5m", Prompt: "/goal finish it"})
			Expect(err).To(MatchError(studio.ErrLoopCommand))
		})

		It("waits for the next day when the daily budget is used up", func() {
			r := newLoopRig(say, studio.LoopOptions{Limits: func() studio.LoopLimits { return studio.LoopLimits{DailyRuns: 1} }})
			chat := create(r.svc, alice)
			l := r.start(chat, "1m", "Check the build.")
			r.pass()
			r.settled(l.ID)
			r.sh.clock.Advance(time.Minute)
			r.pass()
			l = r.get(l.ID)
			Expect(l.Runs).To(Equal(1))
			Expect(l.Running).To(BeFalse())
			Expect(l.Reason).To(ContainSubstring("limit of 1 loop runs"))
			Expect(l.Next).To(Equal(time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)))
		})

		It("pauses after three failed runs in a row", func() {
			r := newLoopRig(func(workspace.Actor, string) []studiotest.ToolStep {
				return []studiotest.ToolStep{{Err: errors.New("the model is down")}}
			}, studio.LoopOptions{})
			chat := create(r.svc, alice)
			l := r.start(chat, "1m", "Check the build.")
			for i := range 3 {
				r.pass()
				l = r.settled(l.ID)
				Expect(l.Failures).To(Equal(i + 1))
				r.sh.clock.Advance(time.Minute)
			}
			Expect(l.State).To(Equal(loops.StatePaused))
			Expect(l.Reason).To(ContainSubstring("3 failed runs"))
			r.pass()
			Expect(r.runs(chat)).To(Equal(3), "a paused loop ran")

			l, err := r.host.SetState(ctx, alice, chat, l.ID, loops.StateActive)
			Expect(err).NotTo(HaveOccurred())
			Expect(l.Failures).To(Equal(0), "resuming starts the count again")
		})
	})

	It("pauses and resumes at the person's word", func() {
		r := newLoopRig(say, studio.LoopOptions{})
		chat := create(r.svc, alice)
		l := r.start(chat, "1m", "Check the build.")
		r.pass()
		r.settled(l.ID)
		l, err := r.host.SetState(ctx, alice, chat, l.ID, loops.StatePaused)
		Expect(err).NotTo(HaveOccurred())
		Expect(l.Next.IsZero()).To(BeTrue())
		r.sh.clock.Advance(time.Hour)
		r.pass()
		Expect(r.runs(chat)).To(Equal(1))
		_, err = r.host.SetState(ctx, alice, chat, l.ID, loops.StateActive)
		Expect(err).NotTo(HaveOccurred())
		r.pass()
		r.settled(l.ID)
		Expect(r.runs(chat)).To(Equal(2))
	})

	It("keeps loops apart per user", func() {
		r := newLoopRig(say, studio.LoopOptions{})
		chat := create(r.svc, alice)
		l := r.start(chat, "5m", "Check the build.")
		_, err := r.host.List(ctx, bob, chat)
		Expect(err).To(MatchError(session.ErrNotFound))
		_, err = r.host.Start(ctx, bob, chat, studio.LoopInput{Prompt: "x"})
		Expect(err).To(MatchError(session.ErrNotFound))
		Expect(r.host.Remove(ctx, bob, chat, l.ID)).To(MatchError(session.ErrNotFound))
		bobChat := create(r.svc, bob)
		Expect(r.host.Remove(ctx, bob, bobChat, l.ID)).To(MatchError(loops.ErrNotFound))
		_, err = r.host.SetState(ctx, bob, bobChat, l.ID, loops.StatePaused)
		Expect(err).To(MatchError(loops.ErrNotFound))
		list, err := r.host.List(ctx, alice, chat)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
	})

	It("removes the loops of a deleted chat, also when the chat went another way", func() {
		r := newLoopRig(say, studio.LoopOptions{})
		a, b := create(r.svc, alice), create(r.svc, alice)
		la := r.start(a, "5m", "one")
		lb := r.start(b, "5m", "two")
		Expect(r.svc.Delete(ctx, alice, a)).To(Succeed())
		_, err := r.sh.loops.Get(ctx, la.ID)
		Expect(err).To(MatchError(loops.ErrNotFound))
		// The chat goes without the service: the next due run finds it gone.
		Expect(r.sh.chats.DeleteChat(ctx, alice, b)).To(Succeed())
		r.pass()
		_, err = r.sh.loops.Get(ctx, lb.ID)
		Expect(err).To(MatchError(loops.ErrNotFound))
	})

	It("resumes after a restart and finishes the run the old process watched", func() {
		block := make(chan struct{})
		DeferCleanup(func() { close(block) })
		sh := newLoopShared()
		blocking := func(workspace.Actor, string) []studiotest.ToolStep { return []studiotest.ToolStep{{Block: block}} }
		a := sh.rig(blocking, studio.LoopOptions{})
		chat := create(a.svc, alice)
		l := a.start(chat, "5m", "Check the build.")
		a.pass()
		until("the run to start", func() bool { return turn(a.svc, alice, chat).Running })
		// The process dies: its host stops, and the reaper frees the chat.
		a.host.Close()
		_, err := sh.chats.ReapTurns(ctx, time.Now().Add(time.Hour))
		Expect(err).NotTo(HaveOccurred())

		b := sh.rig(say, studio.LoopOptions{})
		b.pass()
		Expect(b.get(l.ID).Running).To(BeTrue(), "a run claimed a moment ago may still be starting on another frontend")
		sh.clock.Advance(time.Minute)
		b.pass()
		got := b.get(l.ID)
		Expect(got.Running).To(BeFalse())
		Expect(got.Failures).To(Equal(1), "an interrupted run counts as failed")
		Expect(got.Next).To(Equal(sh.clock.Now().UTC().Add(4 * time.Minute)))
		sh.clock.Advance(4 * time.Minute)
		b.pass()
		Expect(b.settled(l.ID).Runs).To(Equal(2))
	})

	It("fires a due loop once when two frontends pass at the same time", func() {
		sh := newLoopShared()
		var mu sync.Mutex
		leader := ""
		lead := func(name string) func(context.Context) bool {
			return func(context.Context) bool {
				mu.Lock()
				defer mu.Unlock()
				if leader == "" {
					leader = name
				}
				return leader == name
			}
		}
		a := sh.rig(say, studio.LoopOptions{Lead: lead("a")})
		b := sh.rig(say, studio.LoopOptions{Lead: lead("b")})
		chat := create(a.svc, alice)
		l := a.start(chat, "5m", "Check the build.")
		// Run uses the lead: only the leader passes.
		runCtx, cancel := context.WithCancel(ctx)
		var wg sync.WaitGroup
		for _, r := range []*loopRig{a, b} {
			wg.Go(func() { r.host.Run(runCtx) })
		}
		until("the run to be recorded", func() bool { g := a.get(l.ID); return g.Runs == 1 && !g.Running })
		cancel()
		wg.Wait()
		// A split brain: both pass at once on a due loop; the claim is unique.
		sh.clock.Advance(5 * time.Minute)
		for _, r := range []*loopRig{a, b} {
			wg.Go(func() { _ = r.host.Pass(ctx) })
		}
		wg.Wait()
		until("the second run to end", func() bool { return !a.get(l.ID).Running })
		Expect(a.get(l.ID).Runs).To(Equal(2), "the due time was claimed more or less than once")
		until("the second message to reach a model", func() bool { return a.runs(chat)+b.runs(chat) >= 2 })
		Expect(a.runs(chat) + b.runs(chat)).To(Equal(2), "a run started twice")
	})
})
