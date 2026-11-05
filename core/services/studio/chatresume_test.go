package studio_test

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// shape reduces a transcript to what a reader sees, without the sequence
// numbers and the checkpoints, so two reads can be compared.
func shape(es []session.Entry) []string {
	var out []string
	for _, e := range es {
		if e.Role != session.RoleCheckpoint {
			out = append(out, e.Role+": "+e.Text)
		}
	}
	return out
}

// failsAtLastStep scripts a long turn that finishes two steps and then fails,
// the way a provider that errors on its Nth request does. The turn that goes
// on finishes the work. calls collects what the model was sent.
func failsAtLastStep(calls *[]string) func(workspace.Actor, string) []studiotest.ToolStep {
	failed := false
	return func(_ workspace.Actor, text string) []studiotest.ToolStep {
		*calls = append(*calls, text)
		if text == "build the report" && !failed {
			failed = true
			return []studiotest.ToolStep{
				{Say: "step one"}, {Tool: "search", Args: `{"q":"one"}`},
				{Say: "step two"}, {Tool: "search", Args: `{"q":"two"}`},
				{Err: errors.New("the model failed 3 times in a row (HTTP 502)")},
			}
		}
		return []studiotest.ToolStep{{Say: "all done"}}
	}
}

var _ = Describe("retry of a failed turn", func() {
	ctx := context.Background()

	It("goes on from the last complete step and keeps the whole transcript", func() {
		var calls []string
		svc, h := newToolService(failsAtLastStep(&calls))
		id := create(svc, alice)
		sent(svc, alice, id, "build the report")
		idle(svc, alice, id)
		Expect(turn(svc, alice, id).Last).To(Equal(store.TurnFailed))
		failed := transcript(svc, alice, id)
		Expect(shape(failed)[:5]).To(Equal([]string{
			"user: build the report",
			"assistant: step one", "tool: search: \nfound",
			"assistant: step two", "tool: search: \nfound",
		}))

		res, err := svc.Resume(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Started).To(BeTrue())
		idle(svc, alice, id)
		Expect(turn(svc, alice, id).Last).To(Equal(store.TurnDone))

		after := transcript(svc, alice, id)
		// Every line of the failed turn is still there, in place, with the
		// same sequence number.
		for i, e := range failed {
			Expect(after[i].Seq).To(Equal(e.Seq))
			Expect(after[i].Role).To(Equal(e.Role))
			Expect(after[i].Text).To(Equal(e.Text))
		}
		var users, tools int
		for _, e := range after {
			switch e.Role {
			case session.RoleUser:
				users++
			case session.RoleTool:
				tools++
			}
		}
		Expect(users).To(Equal(1), "the question is not asked twice")
		Expect(tools).To(Equal(2), "the finished tool calls are not made again")
		Expect(h.ResultsFor("search")).To(HaveLen(2))
		Expect(after[len(after)-1].Role).To(Equal(session.RoleCheckpoint))
		Expect(after[len(after)-2].Text).To(Equal("all done"))
		// The model got a note to go on, not the question again.
		Expect(calls).To(HaveLen(2))
		Expect(calls[1]).To(Equal(session.ResumePrompt))
	})

	It("keeps the reason of the failure with the chat for a reload", func() {
		var calls []string
		svc, _ := newToolService(failsAtLastStep(&calls))
		id := create(svc, alice)
		sent(svc, alice, id, "build the report")
		idle(svc, alice, id)
		es := transcript(svc, alice, id)
		idx := slices.IndexFunc(es, func(e session.Entry) bool { return e.Role == session.RoleFailure })
		Expect(idx).To(BeNumerically(">", 0), "a failure line ends the failed turn")
		f := session.ParseFailure(es[idx])
		Expect(f.Code).To(Equal(session.CodeUpstream))
		Expect(f.Reason).To(ContainSubstring("HTTP 502"))
		// It comes after the last step of the turn, before the checkpoint.
		Expect(es[idx-1].Role).To(Equal(session.RoleTool))
		Expect(es[idx+1].Role).To(Equal(session.RoleCheckpoint))
	})

	It("starts one run for two clicks, and none for a chat that did not fail", func() {
		var calls []string
		svc, _ := newToolService(failsAtLastStep(&calls))
		id := create(svc, alice)
		none, err := svc.Resume(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(none.Started).To(BeFalse(), "a fresh chat has nothing to go on with")
		sent(svc, alice, id, "build the report")
		idle(svc, alice, id)

		a, errA := svc.Resume(ctx, alice, id)
		b, errB := svc.Resume(ctx, alice, id)
		Expect(errA).NotTo(HaveOccurred())
		Expect(errB).NotTo(HaveOccurred())
		Expect(a.Started).To(BeTrue())
		Expect(b.Started).To(BeFalse(), "the second click finds the run of the first")
		idle(svc, alice, id)
		again, err := svc.Resume(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(again.Started).To(BeFalse(), "the chat is not at a failed turn any more")
		Expect(calls).To(HaveLen(2), "one run for the failure")
	})

	It("keeps what the person typed during the run and delivers it with the retry", func() {
		gate := make(chan struct{})
		var calls []string
		failed := false
		svc, _ := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			calls = append(calls, text)
			if !failed {
				failed = true
				return []studiotest.ToolStep{{Say: "working"}, {Tool: "search", Args: `{}`}, {Block: gate}, {Err: errors.New("the model failed 3 times")}}
			}
			return []studiotest.ToolStep{{Say: "done"}}
		})
		id := create(svc, alice)
		sent(svc, alice, id, "build the report")
		until("the turn to reach its tool", func() bool { return len(transcript(svc, alice, id)) >= 3 })
		// A message sent while the turn runs waits for the next request.
		res, err := svc.Send(ctx, alice, id, "use the blue theme")
		Expect(err).NotTo(HaveOccurred())
		Expect(res.PendingID).NotTo(BeEmpty())
		close(gate)
		idle(svc, alice, id)
		Expect(turn(svc, alice, id).Last).To(Equal(store.TurnFailed))
		Expect(pendingTexts(svc, alice, id)).To(Equal([]string{"use the blue theme"}), "the failed run leaves it waiting")

		_, err = svc.Resume(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		idle(svc, alice, id)
		Expect(pendingTexts(svc, alice, id)).To(BeEmpty())
		Expect(calls).To(HaveLen(2))
		Expect(calls[1]).To(HavePrefix("use the blue theme"))
		Expect(calls[1]).To(HaveSuffix(session.ResumePrompt))
		var texts []string
		for _, e := range transcript(svc, alice, id) {
			if e.Role == session.RoleUser {
				texts = append(texts, e.Text)
			}
		}
		Expect(texts).To(Equal([]string{"build the report", "use the blue theme"}))
	})

	It("keeps the result of a sub-agent that finished before the failure", func() {
		failed := false
		svc, _ := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			switch {
			case text == "find the answer":
				return []studiotest.ToolStep{{Say: "42"}}
			case !failed:
				failed = true
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"find the answer","wait":true}`}, {Err: errors.New("the model failed 3 times")}}
			}
			return []studiotest.ToolStep{{Say: "the answer is 42"}}
		})
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		Expect(childrenOf(svc, alice, id)).To(HaveLen(1))
		before := shape(transcript(svc, alice, id))
		Expect(strings.Join(before, "\n")).To(ContainSubstring("42"))

		_, err := svc.Resume(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		idle(svc, alice, id)
		Expect(childrenOf(svc, alice, id)).To(HaveLen(1), "the sub-agent is not started again")
		after := shape(transcript(svc, alice, id))
		Expect(after[:len(before)]).To(Equal(before))
	})

	It("goes on from the compacted state and compacts nothing again", func() {
		compactions := 0
		failed := false
		svc, h := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "second" && !failed {
				failed = true
				return []studiotest.ToolStep{{Say: "half"}, {Err: errors.New("the model failed 3 times")}}
			}
			return []studiotest.ToolStep{{Say: "ok"}}
		}, func(d *studio.Deps) { d.Files = newFiles() })
		h.Compact = func(string) (int, int, error) { compactions++; return 9000, 3000, nil }
		id := create(svc, alice)
		sent(svc, alice, id, "first")
		idle(svc, alice, id)
		_, _, err := svc.Compact(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		sent(svc, alice, id, "second")
		idle(svc, alice, id)
		Expect(turn(svc, alice, id).Last).To(Equal(store.TurnFailed))
		rec, err := svc.Get(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		saved := string(rec.HarnessState)
		Expect(saved).To(ContainSubstring(`"compacted":true`))

		_, err = svc.Resume(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		idle(svc, alice, id)
		Expect(string(h.Resume(id))).To(Equal(saved), "the model state is the one the failed turn saved, not one rebuilt from the transcript")
		Expect(compactions).To(Equal(1))
		marks := 0
		for _, e := range transcript(svc, alice, id) {
			if e.Role == session.RoleCompacted {
				marks++
			}
		}
		Expect(marks).To(Equal(1))
	})

	It("refuses a sub-agent conversation and another user's chat", func() {
		svc, _ := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			if text == "go" {
				return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"child","wait":true}`}}
			}
			return nil
		})
		id := create(svc, alice)
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		kids := childrenOf(svc, alice, id)
		Expect(kids).To(HaveLen(1))
		_, err := svc.Resume(ctx, alice, kids[0].ID)
		Expect(err).To(MatchError(studio.ErrSubAgentReadOnly))
		_, err = svc.Resume(ctx, "bob", id)
		Expect(err).To(MatchError(session.ErrNotFound))
	})
})
