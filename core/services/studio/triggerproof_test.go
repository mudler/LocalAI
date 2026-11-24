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

var _ = Describe("proposals in list_triggers and delete_trigger", func() {
	ctx := context.Background()

	// proposalRig proposes once, then runs the scripted word with the ref of
	// the proposal in its args.
	type proposalRig struct {
		r   *trigRig
		id  string
		ref string
	}
	setup := func() *proposalRig {
		pr := &proposalRig{}
		pr.r = newTrigRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			switch {
			case strings.Contains(text, "WORD propose"):
				return []studiotest.ToolStep{{Tool: "propose_trigger", Args: goodProposal}, {Say: "ok"}}
			case strings.Contains(text, "WORD list"):
				return []studiotest.ToolStep{{Tool: "list_triggers", Args: `{"ref":"` + pr.ref + `","include_prompt":true}`}, {Tool: "list_triggers", Args: `{}`}, {Say: "ok"}}
			case strings.Contains(text, "WORD update"):
				return []studiotest.ToolStep{{Tool: "update_trigger", Args: `{"ref":"` + pr.ref + `","name":"X"}`}, {Say: "ok"}}
			case strings.Contains(text, "WORD withdraw"):
				return []studiotest.ToolStep{{Tool: "delete_trigger", Args: `{"ref":"` + pr.ref + `"}`}, {Say: "ok"}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		})
		pr.id = create(pr.r.svc, alice)
		sent(pr.r.svc, alice, pr.id, "WORD propose")
		idle(pr.r.svc, alice, pr.id)
		props, err := pr.r.host.Proposals(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(props).To(HaveLen(1))
		pr.ref = studio.TriggerRef(props[0].ID)
		return pr
	}
	word := func(pr *proposalRig, w string) {
		sent(pr.r.svc, alice, pr.id, "WORD "+w)
		idle(pr.r.svc, alice, pr.id)
	}

	It("lists a waiting proposal with its ref and state proposed, and gives its prompt on request", func() {
		pr := setup()
		word(pr, "list")
		res := pr.r.h.ResultsFor("list_triggers")
		Expect(res).To(HaveLen(2))
		Expect(res[0].Out).To(ContainSubstring("Summarise yesterday."), "include_prompt works on a proposal")
		Expect(res[1].Out).To(ContainSubstring(`"ref":"` + pr.ref + `"`))
		Expect(res[1].Out).To(ContainSubstring(`"state":"proposed"`))
		Expect(res[1].Out).To(ContainSubstring(`"on":false`))
	})

	It("withdraws a waiting proposal with its ref, and proves it", func() {
		pr := setup()
		word(pr, "withdraw")
		res := pr.r.h.ResultsFor("delete_trigger")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).To(BeNil(), "result = %+v", res)
		Expect(res[0].Out).To(ContainSubstring("Withdrew"))
		Expect(res[0].Out).To(ContainSubstring(`"state":"withdrawn"`))
		Expect(res[0].Out).To(ContainSubstring(`"active":false`))
		props, _ := pr.r.host.Proposals(ctx, alice)
		Expect(props).To(BeEmpty())
		_, err := pr.r.host.ConfirmProposal(ctx, alice, mustProposalID(pr.r, pr.ref), "")
		Expect(err).To(HaveOccurred(), "a withdrawn proposal cannot be confirmed")
	})

	It("refuses to change a proposal and says how to correct it", func() {
		pr := setup()
		word(pr, "update")
		res := pr.r.h.ResultsFor("update_trigger")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).To(HaveOccurred())
		Expect(res[0].Err.Error()).To(ContainSubstring("withdraw it with delete_trigger"))
	})
})

// mustProposalID finds the ID of the stored record that a ref names.
func mustProposalID(r *trigRig, ref string) string {
	all, err := r.sh.trg.ListTriggers(context.Background(), alice)
	Expect(err).NotTo(HaveOccurred())
	for _, t := range all {
		if studio.TriggerRef(t.ID) == ref {
			return t.ID
		}
	}
	Fail("no record for " + ref)
	return ""
}
