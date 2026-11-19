package studio_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The loop tools of a connector chat follow the access model of the bot: wait_for
// and wait_agents need loops.wait, and start_loop with its two follow-up tools
// needs loops.start, which only a paired sender holds.
var _ = Describe("loop tools in a connector chat", func() {
	ctx := context.Background()
	now := time.Now()
	binding := connectors.Binding{
		ID: "b1", Kind: "telegram", AgentID: "a1",
		Caps:   []string{contract.CapLoopsWait, contract.CapLoopsStart},
		Allow:  []string{"LISTED"},
		Paired: []connectors.PairedSender{{SenderID: "PAIRED", At: now.Add(-time.Hour)}},
	}

	chat := func(r *loopRig) string {
		rec, err := r.svc.CreateAs(ctx, alice, studio.CreateInput{Model: "m"}, studio.Origin{Kind: session.OriginConnector, ID: "c1"})
		Expect(err).NotTo(HaveOccurred())
		return rec.ID
	}
	talk := func(r *loopRig, id string) []string {
		sent(r.svc, alice, id, "hello")
		idle(r.svc, alice, id)
		return r.h.ToolNames(id)
	}

	It("offers every loop tool to a paired sender", func() {
		r := newLoopRig(startLoopScript(armArgs), studio.LoopOptions{})
		id := chat(r)
		r.svc.HoldSenderForTest(id, binding, "PAIRED", false, now)
		Expect(talk(r, id)).To(ContainElements("start_loop", "wait_for"))
	})

	It("offers the waits but not start_loop to a listed sender who is not paired", func() {
		r := newLoopRig(startLoopScript(armArgs), studio.LoopOptions{})
		id := chat(r)
		r.svc.HoldSenderForTest(id, binding, "LISTED", true, now)
		names := talk(r, id)
		Expect(names).To(ContainElement("wait_for"))
		Expect(names).NotTo(ContainElement("start_loop"))
	})

	It("offers nothing to an unpaired sender of a bot that accepts anyone", func() {
		r := newLoopRig(startLoopScript(armArgs), studio.LoopOptions{})
		id := chat(r)
		b := binding
		b.AcceptAny = true
		r.svc.HoldSenderForTest(id, b, "STRANGER", false, now)
		names := talk(r, id)
		Expect(names).NotTo(ContainElements("start_loop"))
		Expect(names).NotTo(ContainElements("wait_for"))
		Expect(names).NotTo(ContainElements("schedule_next"))
	})

	It("offers nothing when the owner did not enable the capabilities for the bot", func() {
		r := newLoopRig(startLoopScript(armArgs), studio.LoopOptions{})
		id := chat(r)
		b := binding
		b.Caps = nil
		r.svc.HoldSenderForTest(id, b, "PAIRED", false, now)
		names := talk(r, id)
		Expect(names).NotTo(ContainElement("start_loop"))
		Expect(names).NotTo(ContainElement("wait_for"))
	})

	It("says the reduced run limit in the answer of start_loop", func() {
		r := newLoopRig(startLoopScript(armArgs), studio.LoopOptions{Limits: func() studio.LoopLimits { return studio.LoopLimits{MaxRuns: 100} }})
		id := chat(r)
		r.svc.HoldSenderForTest(id, binding, "PAIRED", false, now)
		sent(r.svc, alice, id, "arm")
		idle(r.svc, alice, id)
		res := r.h.ResultsFor("start_loop")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Out).To(ContainSubstring("at most 20 runs"))
		Expect(res[0].Out).To(ContainSubstring("a fifth of the 100"))
	})

	// A wait on a script runs a command at every check. In a connector chat
	// only a paired sender may arm one, even when every sender holds
	// loops.wait; the other sources stay open to any sender with loops.wait.
	Describe("a wait on a command", func() {
		const onScript = `{"script":"true","until":"exit_zero","every":"1m","then":"Tell the person."}`
		const onTool = `{"tool":"list_things","then":"Tell the person."}`

		It("is refused to a listed sender who is not paired, and the refusal says why", func() {
			r := newLoopRig(say, studio.LoopOptions{})
			id := chat(r)
			r.svc.HoldSenderForTest(id, binding, "LISTED", true, now)
			_, err := r.host.StartWaitForTest(ctx, alice, id, onScript)
			Expect(err).To(MatchError(ContainSubstring("verified person")))
			list, _ := r.host.List(ctx, alice, id)
			Expect(list).To(BeEmpty())
			_, err = r.host.StartWaitForTest(ctx, alice, id, onTool)
			Expect(err).NotTo(MatchError(ContainSubstring("verified person")), "another source needs only loops.wait")
		})

		It("passes the pairing check for a paired sender", func() {
			r := newLoopRig(say, studio.LoopOptions{})
			id := chat(r)
			r.svc.HoldSenderForTest(id, binding, "PAIRED", false, now)
			_, err := r.host.StartWaitForTest(ctx, alice, id, onScript)
			Expect(err).NotTo(MatchError(ContainSubstring("verified person")))
		})

		It("passes the pairing check in the run of a wait that a paired sender armed, and not of one a listed sender armed", func() {
			r := newLoopRig(say, studio.LoopOptions{})
			id := chat(r)
			r.svc.HoldSenderForTest(id, binding, "PAIRED", false, now)
			l, err := r.host.BeginByModelForTest(ctx, alice, id, true)
			Expect(err).NotTo(HaveOccurred())
			r.svc.ReleaseSenderForTest(id)
			running(r, l.ID)
			_, err = r.host.StartWaitForTest(ctx, alice, id, onScript)
			Expect(err).NotTo(MatchError(ContainSubstring("verified person")))

			other := chat(r)
			r.svc.HoldSenderForTest(other, binding, "LISTED", true, now)
			m, err := r.host.BeginByModelForTest(ctx, alice, other, true)
			Expect(err).NotTo(HaveOccurred())
			r.svc.ReleaseSenderForTest(other)
			running(r, m.ID)
			_, err = r.host.StartWaitForTest(ctx, alice, other, onScript)
			Expect(err).To(MatchError(ContainSubstring("verified person")))
		})
	})

	// The note of the promise guard names only what the model is offered.
	Describe("the promise guard", func() {
		It("names wait_for without a command for a listed sender, and with one for a paired sender", func() {
			r := newLoopRig(say, studio.LoopOptions{})
			id := chat(r)
			r.svc.HoldSenderForTest(id, binding, "LISTED", true, now)
			got := r.svc.GuardToolsForTest(ctx, alice, id)
			Expect(got).To(HaveLen(1))
			Expect(got[0]).To(HavePrefix("wait_for"))
			Expect(got[0]).NotTo(ContainSubstring("command"))
			r.svc.HoldSenderForTest(id, binding, "PAIRED", false, now)
			got = r.svc.GuardToolsForTest(ctx, alice, id)
			Expect(got).To(HaveLen(2))
			Expect(got[0]).To(HavePrefix("start_loop"))
			Expect(got[1]).To(ContainSubstring("command"))
		})

		It("names no loop tool while a paused loop holds the chat, as the tool list does", func() {
			r := newLoopRig(startLoopScript(armArgs), studio.LoopOptions{})
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "arm")
			idle(r.svc, alice, id)
			list, _ := r.host.List(ctx, alice, id)
			Expect(list).To(HaveLen(1))
			_, err := r.host.SetState(ctx, alice, id, list[0].ID, loops.StatePaused)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.host.ToolNamesForTest(ctx, alice, id)).To(BeEmpty())
			for _, t := range r.svc.GuardToolsForTest(ctx, alice, id) {
				Expect(t).NotTo(HavePrefix("start_loop"))
				Expect(t).NotTo(HavePrefix("wait_for"))
			}
		})
	})

	It("starts a loop for a paired sender with the reduced run limit", func() {
		r := newLoopRig(startLoopScript(armArgs), studio.LoopOptions{Limits: func() studio.LoopLimits { return studio.LoopLimits{MaxRuns: 100} }})
		id := chat(r)
		r.svc.HoldSenderForTest(id, binding, "PAIRED", false, now)
		sent(r.svc, alice, id, "arm")
		idle(r.svc, alice, id)
		res := r.h.ResultsFor("start_loop")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).To(BeNil())
		list, err := r.host.List(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].SelfPaced()).To(BeTrue())
		Expect(r.host.MaxRunsForTest(ctx, alice, id, studio.LoopLimits{MaxRuns: 100})).To(Equal(20))
	})

	It("keeps schedule_next and loop_done in the runs of a loop, which have no sender", func() {
		r := newLoopRig(startLoopScript(armArgs), studio.LoopOptions{})
		id := chat(r)
		r.svc.HoldSenderForTest(id, binding, "PAIRED", false, now)
		sent(r.svc, alice, id, "arm")
		idle(r.svc, alice, id)
		list, _ := r.host.List(ctx, alice, id)
		Expect(list).To(HaveLen(1))
		r.svc.ReleaseSenderForTest(id)
		r.fire(list[0].ID, 2*time.Minute)
		Expect(r.h.ToolNames(id)).To(ContainElements("schedule_next", "loop_done"))
		Expect(r.h.ToolNames(id)).NotTo(ContainElement("start_loop"))
	})

	It("refuses the start path when the sender does not hold the capability", func() {
		r := newLoopRig(startLoopScript(armArgs), studio.LoopOptions{})
		id := chat(r)
		r.svc.HoldSenderForTest(id, binding, "LISTED", true, now)
		_, err := r.host.BeginByModelForTest(ctx, alice, id, false)
		Expect(err).To(MatchError(studio.ErrLoopSender))
		b := binding
		b.AcceptAny = true
		r.svc.HoldSenderForTest(id, b, "STRANGER", false, now)
		_, err = r.host.BeginByModelForTest(ctx, alice, id, true)
		Expect(err).To(MatchError(studio.ErrLoopSender))
		r.svc.ReleaseSenderForTest(id)
		_, err = r.host.BeginByModelForTest(ctx, alice, id, false)
		Expect(err).To(MatchError(studio.ErrLoopSender))
		list, _ := r.host.List(ctx, alice, id)
		Expect(list).To(BeEmpty())
	})

	It("lets a listed sender pass the start path of a wait but not of a loop", func() {
		r := newLoopRig(startLoopScript(armArgs), studio.LoopOptions{})
		id := chat(r)
		r.svc.HoldSenderForTest(id, binding, "LISTED", true, now)
		_, err := r.host.BeginByModelForTest(ctx, alice, id, true)
		Expect(errors.Is(err, studio.ErrLoopSender)).To(BeFalse())
		_, err = r.host.BeginByModelForTest(ctx, alice, id, false)
		Expect(err).To(MatchError(studio.ErrLoopSender))
	})

	// The wake-up of a wait has no sender. It continues the sender who armed
	// the wait: it holds the loop capabilities that sender held, and nothing else.
	Describe("the run of a wait that a sender armed", func() {
		// arm arms a wait in a chat as the sender, then makes it the run that
		// goes on now, with no sender, as a wake-up is.
		arm := func(r *loopRig, id, sender string, listed bool, b connectors.Binding) loops.Loop {
			r.svc.HoldSenderForTest(id, b, sender, listed, now)
			l, err := r.host.BeginByModelForTest(ctx, alice, id, true)
			Expect(err).NotTo(HaveOccurred())
			r.svc.ReleaseSenderForTest(id)
			l = running(r, l.ID)
			return l
		}
		allow := func(r *loopRig, id string) []string { return r.host.ToolNamesForTest(ctx, alice, id) }

		It("stores the loop capabilities of the sender with the wait", func() {
			r := newLoopRig(startLoopScript(armArgs), studio.LoopOptions{})
			l := arm(r, chat(r), "LISTED", true, binding)
			Expect(l.Grant).To(ConsistOf(contract.CapLoopsWait))
			p := arm(r, chat(r), "PAIRED", false, binding)
			Expect(p.Grant).To(ConsistOf(contract.CapLoopsWait, contract.CapLoopsStart, loops.GrantPaired),
				"a paired sender's wait also records the pairing, which a wait on a command needs")
		})

		It("offers wait_for and not start_loop to the wake-up of a listed sender's wait", func() {
			r := newLoopRig(startLoopScript(armArgs), studio.LoopOptions{})
			id := chat(r)
			arm(r, id, "LISTED", true, binding)
			Expect(allow(r, id)).To(ContainElement("wait_for"))
			Expect(allow(r, id)).NotTo(ContainElement("start_loop"))
		})

		It("lets the wake-up arm the next wait, which carries the grant on", func() {
			r := newLoopRig(startLoopScript(armArgs), studio.LoopOptions{})
			id := chat(r)
			first := arm(r, id, "LISTED", true, binding)
			_, err := r.sh.loops.Update(ctx, first.ID, func(c *loops.Loop) error { c.Running, c.State = false, loops.StateEnded; return nil })
			Expect(err).NotTo(HaveOccurred())
			Expect(allow(r, id)).NotTo(ContainElement("wait_for"), "the grant dies with the wait that held it")
			second := arm(r, id, "LISTED", true, binding)
			next, err := r.host.BeginByModelForTest(ctx, alice, id, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(next.Grant).To(Equal(second.Grant))
		})

		It("offers start_loop to the wake-up of a wait that a sender with loops.start armed", func() {
			r := newLoopRig(startLoopScript(armArgs), studio.LoopOptions{})
			id := chat(r)
			arm(r, id, "PAIRED", false, binding)
			Expect(allow(r, id)).To(ContainElements("wait_for", "start_loop"))
		})

		It("offers neither tool to the wake-up of a wait armed by a sender with neither", func() {
			r := newLoopRig(startLoopScript(armArgs), studio.LoopOptions{})
			id := chat(r)
			// Such a sender cannot arm a wait; a wait row with no grant is what is left.
			l := arm(r, id, "LISTED", true, binding)
			_, err := r.sh.loops.Update(ctx, l.ID, func(c *loops.Loop) error { c.Grant = nil; return nil })
			Expect(err).NotTo(HaveOccurred())
			Expect(allow(r, id)).To(BeEmpty(), "an old row with no grant behaves as before")
			_, err = r.sh.loops.Update(ctx, l.ID, func(c *loops.Loop) error { c.Grant = []string{"triggers.propose"}; return nil })
			Expect(err).NotTo(HaveOccurred())
			Expect(allow(r, id)).To(BeEmpty(), "a grant of another capability gives no loop tool")
			_, err = r.host.BeginByModelForTest(ctx, alice, id, true)
			Expect(err).To(MatchError(studio.ErrLoopSender))
		})

		It("does not widen a turn that has a sender", func() {
			r := newLoopRig(startLoopScript(armArgs), studio.LoopOptions{})
			id := chat(r)
			arm(r, id, "PAIRED", false, binding)
			b := binding
			b.AcceptAny = true
			r.svc.HoldSenderForTest(id, b, "STRANGER", false, now)
			Expect(allow(r, id)).To(BeEmpty(), "the grant of the sender decides, not the stored one")
		})

		It("keeps the other capabilities refused in the wake-up", func() {
			r := newLoopRig(startLoopScript(armArgs), studio.LoopOptions{})
			id := chat(r)
			arm(r, id, "PAIRED", false, binding)
			allowed, why := r.svc.TurnAllowForTest(id, contract.CapTriggersPropose)
			Expect(allowed).To(BeFalse())
			Expect(why).To(ContainSubstring("no sender"))
		})
	})
})

// running makes the loop the run that goes on in its chat now.
func running(r *loopRig, id string) loops.Loop {
	l, err := r.sh.loops.Update(context.Background(), id, func(c *loops.Loop) error { c.Running = true; return nil })
	Expect(err).NotTo(HaveOccurred())
	return l
}
