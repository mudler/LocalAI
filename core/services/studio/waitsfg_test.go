package studio_test

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var _ = Describe("a foreground sub-agent that arms a loop", func() {
	ctx := context.Background()

	It("returns to the parent after the bound, and the child goes on in the background", func() {
		DeferCleanup(studio.FastWaitsForTest(20*time.Millisecond, 50*time.Millisecond, 20*time.Millisecond, 150*time.Millisecond))
		var runs int
		r := newLoopRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			switch {
			case strings.Contains(text, "Loop run"):
				runs++
				return []studiotest.ToolStep{{Tool: "loop_done", Args: `{"summary":"it arrived"}`}, {Say: "the file arrived"}}
			case strings.HasPrefix(text, "watch"):
				return []studiotest.ToolStep{{Tool: "start_loop", Args: `{"prompt":"Check the file.","first_run_after":"1m"}`}, {Say: "armed"}}
			case text == "go":
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"watch the file","wait":true}`}, {Say: "parent moved on"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, studio.LoopOptions{})
		parent := create(r.svc, alice)
		sent(r.svc, alice, parent, "go")
		until("spawn_agent to return", func() bool { return len(r.h.ResultsFor("spawn_agent")) == 1 })
		res := r.h.ResultsFor("spawn_agent")[0]
		Expect(res.Err).To(BeNil())
		Expect(res.Out).To(ContainSubstring("keeps running"))
		Expect(res.Out).To(ContainSubstring("arrive as a message"))
		idle(r.svc, alice, parent)

		subs, err := r.svc.SubAgents(ctx, alice, parent)
		Expect(err).NotTo(HaveOccurred())
		Expect(subs).To(HaveLen(1))
		Expect(subs[0].Turn.Running).To(BeTrue(), "the child stopped with the parent's call")
		list, _ := r.host.List(ctx, alice, subs[0].Chat.ID)
		Expect(list).To(HaveLen(1))

		// The loop runs, the child ends, and the parent hears of it.
		r.fire(list[0].ID, 2*time.Minute)
		Expect(r.get(list[0].ID).State).To(Equal(loops.StateEnded))
		until("the result to reach the parent", func() bool {
			for _, t := range r.h.Sent(parent) {
				if strings.Contains(t, "[Sub-agent finished") && strings.Contains(t, "the file arrived") {
					return true
				}
			}
			return false
		})
		Expect(runs).To(Equal(1))
	})
})
