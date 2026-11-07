package studio_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// startLoopScript makes "arm" call start_loop with args and every other text
// answer with ok.
func startLoopScript(args string) func(workspace.Actor, string) []studiotest.ToolStep {
	return func(_ workspace.Actor, text string) []studiotest.ToolStep {
		if text == "arm" {
			return []studiotest.ToolStep{{Tool: "start_loop", Args: args}, {Say: "armed"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
}

const armArgs = `{"prompt":"Check the download and move the file when it is done.","first_run_after":"2m","reason":"the download"}`

var _ = Describe("start_loop", func() {
	ctx := context.Background()

	arm := func(r *loopRig, chat string) {
		sent(r.svc, alice, chat, "arm")
		idle(r.svc, alice, chat)
	}

	It("is offered in an ordinary chat and starts a self-paced loop with no yes from the person", func() {
		r := newLoopRig(startLoopScript(armArgs), studio.LoopOptions{})
		chat := create(r.svc, alice)
		arm(r, chat)
		Expect(r.h.ToolNames(chat)).To(ContainElement("start_loop"))
		Expect(r.h.ToolNames(chat)).NotTo(ContainElement("schedule_next"))
		res := r.h.ResultsFor("start_loop")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).To(BeNil())
		Expect(res[0].Out).To(ContainSubstring("first run is in 2m"))

		list, err := r.host.List(ctx, alice, chat)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		l := list[0]
		Expect(l.SelfPaced()).To(BeTrue())
		Expect(l.State).To(Equal(loops.StateActive), "the first loop of a chat waited for a yes")
		Expect(l.Wait).To(Equal("the download"))
		Expect(l.Next).To(Equal(r.sh.clock.Now().UTC().Add(2 * time.Minute)))

		// Not due yet; due after two minutes. The run has the loop tools and
		// no start_loop.
		r.pass()
		Expect(r.runs(chat)).To(Equal(0))
		r.sh.clock.Advance(2 * time.Minute)
		r.pass()
		r.settled(l.ID)
		Expect(r.runs(chat)).To(Equal(1))
		Expect(r.h.ToolNames(chat)).To(ContainElements("schedule_next", "loop_done"))
		Expect(r.h.ToolNames(chat)).NotTo(ContainElement("start_loop"))
	})

	It("clamps the first wait to the minimum and to one day", func() {
		r := newLoopRig(startLoopScript(`{"prompt":"Check it.","first_run_after":"5s"}`), studio.LoopOptions{})
		chat := create(r.svc, alice)
		arm(r, chat)
		list, _ := r.host.List(ctx, alice, chat)
		Expect(list).To(HaveLen(1))
		Expect(list[0].Next).To(Equal(r.sh.clock.Now().UTC().Add(time.Minute)))
		Expect(r.h.ResultsFor("start_loop")[0].Out).To(ContainSubstring("set to 1m"))

		r = newLoopRig(startLoopScript(`{"prompt":"Check it.","first_run_after":"72h"}`), studio.LoopOptions{})
		chat = create(r.svc, alice)
		arm(r, chat)
		list, _ = r.host.List(ctx, alice, chat)
		Expect(list[0].Next).To(Equal(r.sh.clock.Now().UTC().Add(24 * time.Hour)))
	})

	It("refuses an empty prompt and a bad duration with a readable error", func() {
		for _, args := range []string{`{"prompt":"  ","first_run_after":"2m"}`, `{"prompt":"x","first_run_after":"soon"}`} {
			r := newLoopRig(startLoopScript(args), studio.LoopOptions{})
			chat := create(r.svc, alice)
			arm(r, chat)
			res := r.h.ResultsFor("start_loop")
			Expect(res).To(HaveLen(1))
			Expect(res[0].Err).To(HaveOccurred())
			list, _ := r.host.List(ctx, alice, chat)
			Expect(list).To(BeEmpty())
		}
	})

	It("refuses a second start in the chat and points to schedule_next", func() {
		r := newLoopRig(startLoopScript(armArgs), studio.LoopOptions{})
		chat := create(r.svc, alice)
		arm(r, chat)
		// Two calls in one turn: the second finds the loop of the first.
		r2 := newLoopRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "start_loop", Args: armArgs}, {Tool: "start_loop", Args: armArgs}, {Say: "ok"}}
		}, studio.LoopOptions{})
		chat2 := create(r2.svc, alice)
		sent(r2.svc, alice, chat2, "arm")
		idle(r2.svc, alice, chat2)
		res := r2.h.ResultsFor("start_loop")
		Expect(res).To(HaveLen(2))
		Expect(res[0].Err).To(BeNil())
		Expect(res[1].Err).To(MatchError(ContainSubstring("schedule_next")))
		list, _ := r2.host.List(ctx, alice, chat2)
		Expect(list).To(HaveLen(1))

		// The next turn of the chat does not offer the tool.
		sent(r.svc, alice, chat, "hello")
		idle(r.svc, alice, chat)
		Expect(r.h.ToolNames(chat)).NotTo(ContainElement("start_loop"))
	})

	It("is offered again once the loop ended", func() {
		r := newLoopRig(startLoopScript(armArgs), studio.LoopOptions{})
		chat := create(r.svc, alice)
		arm(r, chat)
		list, _ := r.host.List(ctx, alice, chat)
		r.sh.clock.Advance(2 * time.Minute)
		r.pass()
		Expect(r.settled(list[0].ID).State).To(Equal(loops.StateEnded), "say() does not ask for another run")
		sent(r.svc, alice, chat, "hello")
		idle(r.svc, alice, chat)
		Expect(r.h.ToolNames(chat)).To(ContainElement("start_loop"))
	})

	It("returns the caps as readable errors", func() {
		r := newLoopRig(startLoopScript(armArgs), studio.LoopOptions{Limits: func() studio.LoopLimits { return studio.LoopLimits{PerChat: 1, PerUser: 1} }})
		a, b := create(r.svc, alice), create(r.svc, alice)
		r.start(a, "5m", "one") // an interval loop does not block start_loop but counts
		arm(r, a)
		res := r.h.ResultsFor("start_loop")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).To(MatchError(ContainSubstring("this chat has 1 loops")))
		arm(r, b)
		res = r.h.ResultsFor("start_loop")
		Expect(res).To(HaveLen(2))
		Expect(res[1].Err).To(MatchError(ContainSubstring("the person has 1 loops")))
	})

	It("stops a model-started loop at the run budget", func() {
		r := newLoopRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "arm" {
				return []studiotest.ToolStep{{Tool: "start_loop", Args: `{"prompt":"Check it.","first_run_after":"1m"}`}, {Say: "armed"}}
			}
			return []studiotest.ToolStep{{Tool: "schedule_next", Args: `{"after":"1m"}`}, {Say: "later"}}
		}, studio.LoopOptions{Limits: func() studio.LoopLimits { return studio.LoopLimits{MaxRuns: 2} }})
		chat := create(r.svc, alice)
		arm(r, chat)
		list, _ := r.host.List(ctx, alice, chat)
		id := list[0].ID
		for range 2 {
			r.sh.clock.Advance(time.Minute)
			r.pass()
			r.settled(id)
		}
		l := r.get(id)
		Expect(l.State).To(Equal(loops.StateEnded))
		Expect(l.Reason).To(ContainSubstring("2 runs"))
	})

	It("is not offered without a loop host", func() {
		svc, h := newToolService(say)
		off := create(svc, alice)
		sent(svc, alice, off, "hello")
		idle(svc, alice, off)
		Expect(h.ToolNames(off)).NotTo(BeEmpty())
		Expect(h.ToolNames(off)).NotTo(ContainElement("start_loop"))
	})
})
