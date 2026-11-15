package studio_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
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
})
