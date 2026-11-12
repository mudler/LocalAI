package studio_test

import (
	"context"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// proofScript makes a chat call one tool with the args that go with the text.
func proofScript(calls map[string][2]string) func(workspace.Actor, string) []studiotest.ToolStep {
	return func(_ workspace.Actor, text string) []studiotest.ToolStep {
		if c, ok := calls[text]; ok {
			return []studiotest.ToolStep{{Tool: c[0], Args: c[1]}, {Say: "ok"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
}

var _ = Describe("the stored state in the result of a trigger tool", func() {
	ctx := context.Background()
	// The clock of a rig starts at 2026-10-27 07:00 UTC.
	const stored = `Stored state: {`

	It("proves a proposal with its ref, state, activity and next run, read from the store", func() {
		r := newTrigRig(proofScript(map[string][2]string{"p": {"propose_trigger", goodProposal}}))
		id := create(r.svc, alice)
		sent(r.svc, alice, id, "p")
		idle(r.svc, alice, id)
		props, _ := r.host.Proposals(ctx, alice)
		Expect(props).To(HaveLen(1))
		out := r.h.ResultsFor("propose_trigger")[0].Out
		Expect(out).To(ContainSubstring("not active"), "the warning stays")
		Expect(out).To(ContainSubstring(stored))
		Expect(out).To(ContainSubstring(fmt.Sprintf(`"ref":%q`, studio.TriggerRef(props[0].ID))))
		Expect(out).To(ContainSubstring(`"state":"proposed"`))
		Expect(out).To(ContainSubstring(`"active":false`))
		// weekdays 09:00 in Rome: 2026-10-27 is a Tuesday, and Rome is on UTC+1.
		Expect(out).To(ContainSubstring(`"next_run":"2026-10-27T09:00:00+01:00"`))
	})

	It("says that a repeated proposal is the one that waits", func() {
		r := newTrigRig(proofScript(map[string][2]string{"p": {"propose_trigger", goodProposal}}))
		id := create(r.svc, alice)
		sent(r.svc, alice, id, "p")
		idle(r.svc, alice, id)
		sent(r.svc, alice, id, "p")
		idle(r.svc, alice, id)
		res := r.h.ResultsFor("propose_trigger")
		Expect(res).To(HaveLen(2))
		Expect(res[0].Out).NotTo(ContainSubstring("already waits"))
		Expect(res[1].Out).To(ContainSubstring("already waits"))
		props, _ := r.host.Proposals(ctx, alice)
		Expect(props).To(HaveLen(1))
	})

	It("proves a pause, a resume, a change that waits and a delete that waits", func() {
		sh := newShared()
		var ref string
		r := sh.rig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			args := map[string]string{
				"pause":  `{"ref":"` + ref + `","enabled":false}`,
				"resume": `{"ref":"` + ref + `","enabled":true}`,
				"change": `{"ref":"` + ref + `","name":"Renamed"}`,
			}
			// A turn can start with a note of the host: match the word inside.
			for _, w := range []string{"pause", "resume", "change", "delete"} {
				if strings.Contains(text, "WORD "+w) {
					text = w
				}
			}
			if text == "delete" {
				return []studiotest.ToolStep{{Tool: "delete_trigger", Args: `{"ref":"` + ref + `"}`}, {Say: "ok"}}
			}
			if a, ok := args[text]; ok {
				return []studiotest.ToolStep{{Tool: "update_trigger", Args: a}, {Say: "ok"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, studio.TriggerOptions{})
		tr := r.schedule(studio.TriggerInput{})
		ref = studio.TriggerRef(tr.ID)
		id := create(r.svc, alice)
		run := func(word string) string {
			n := len(r.h.ResultsFor("update_trigger")) + len(r.h.ResultsFor("delete_trigger"))
			sent(r.svc, alice, id, "WORD "+word)
			idle(r.svc, alice, id)
			all := append(r.h.ResultsFor("update_trigger"), r.h.ResultsFor("delete_trigger")...)
			Expect(all).To(HaveLen(n+1), "results = %+v", all)
			out := all[len(all)-1]
			Expect(out.Err).To(BeNil(), "result = %+v", out)
			return out.Out
		}
		got := run("pause")
		Expect(got).To(ContainSubstring(`"state":"paused"`))
		Expect(got).To(ContainSubstring(`"active":false`))
		Expect(got).NotTo(ContainSubstring("next_run"))
		got = run("resume")
		Expect(got).To(ContainSubstring(`"state":"active"`))
		Expect(got).To(ContainSubstring(`"active":true`))
		Expect(got).To(ContainSubstring(`"next_run":"2026-10-27T08:00:00Z"`))
		got = run("change")
		Expect(got).To(ContainSubstring(`"state":"active"`))
		Expect(got).To(ContainSubstring(`"change":"pending"`))
		Expect(got).To(ContainSubstring(`"ref":"` + ref + `"`))
		got = run("delete")
		Expect(got).To(ContainSubstring(`"state":"deleted_pending"`))
		Expect(got).To(ContainSubstring(`"active":true`), "it still runs until the person deletes it")
	})
})
