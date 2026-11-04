package studio_test

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func transcript(svc *studio.Service, user, id string) []session.Entry {
	es, err := svc.Messages(context.Background(), user, id, 0, 0)
	Expect(err).NotTo(HaveOccurred())
	return es
}

func lastUser(es []session.Entry) session.Entry {
	for i := len(es) - 1; i >= 0; i-- {
		if es[i].Role == session.RoleUser {
			return es[i]
		}
	}
	Fail("no user message in the transcript")
	return session.Entry{}
}

// eventOf reads events until one of kind k arrives and returns it.
func eventOf(ch <-chan session.Event, k session.EventKind) session.Event {
	timeout := time.After(20 * time.Second)
	for {
		select {
		case e := <-ch:
			if e.Kind == k {
				return e
			}
		case <-timeout:
			Fail("no " + string(k) + " event")
			return session.Event{}
		}
	}
}

func roles(es []session.Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Role
	}
	return out
}

var _ = Describe("chat commands", func() {
	ctx := context.Background()

	Describe("a message sent with a skill", func() {
		It("gives the model the skill for that turn and keeps only the words and a chip", func() {
			r := newAgentRig(sayOK)
			id := create(r.svc, alice)
			res, err := r.svc.SendCommand(ctx, alice, id, "look at the diff", nil, studio.Command{Skill: "review"})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Started).To(BeTrue())
			idle(r.svc, alice, id)

			got := r.h.Sent(id)
			Expect(got).To(HaveLen(1))
			Expect(got[0]).To(ContainSubstring(`the user invoked the skill "review"`))
			Expect(got[0]).To(ContainSubstring(`<skill name="review">`))
			Expect(got[0]).To(ContainSubstring("check a change before it ships"), "the SKILL.md text reaches the model")
			Expect(got[0]).To(HaveSuffix("look at the diff"), "the words come after the directive")

			u := lastUser(transcript(r.svc, alice, id))
			Expect(u.Text).To(Equal("look at the diff"), "the transcript keeps the words only")
			Expect(u.Invoked).To(Equal(&session.Invocation{Kind: session.InvokeSkill, Name: "review"}))
		})

		It("may come without words and starts the skill", func() {
			r := newAgentRig(sayOK)
			id := create(r.svc, alice)
			_, err := r.svc.SendCommand(ctx, alice, id, "", nil, studio.Command{Skill: "house-style"})
			Expect(err).NotTo(HaveOccurred())
			idle(r.svc, alice, id)
			Expect(r.h.Sent(id)[0]).To(ContainSubstring("start the skill now"))
			u := lastUser(transcript(r.svc, alice, id))
			Expect(u.Text).To(BeEmpty())
			Expect(u.Invoked.Name).To(Equal("house-style"))
		})

		It("uses the nearest layer when two layers have the skill", func() {
			r := newAgentRig(sayOK)
			f := r.svc.Files()
			writeSkill(f, "", "/skills/review/SKILL.md", "review", "the shared review skill")
			id := create(r.svc, alice)
			_, err := r.svc.SendCommand(ctx, alice, id, "go on", nil, studio.Command{Skill: "review"})
			Expect(err).NotTo(HaveOccurred())
			idle(r.svc, alice, id)
			Expect(r.h.Sent(id)[0]).To(ContainSubstring("check a change before it ships"))
			Expect(r.h.Sent(id)[0]).NotTo(ContainSubstring("the shared review skill"))
		})

		It("refuses an unknown or switched off skill and lists the chat's skills", func() {
			r := newAgentRig(sayOK)
			id := create(r.svc, alice)
			_, err := r.svc.SendCommand(ctx, alice, id, "x", nil, studio.Command{Skill: "nope"})
			Expect(err).To(MatchError(studio.ErrBadCommand))
			Expect(err.Error()).To(ContainSubstring("house-style"))
			Expect(err.Error()).To(ContainSubstring("review"))

			Expect(r.svc.SetSkillsOff(ctx, alice, id, []string{"review"})).To(Succeed())
			_, err = r.svc.SendCommand(ctx, alice, id, "x", nil, studio.Command{Skill: "review"})
			Expect(err).To(MatchError(studio.ErrBadCommand))
			Expect(err.Error()).To(ContainSubstring("house-style"))
			Expect(transcript(r.svc, alice, id)).To(BeEmpty(), "a refused command stores nothing")
		})

		It("never reaches another user's skills", func() {
			r := newAgentRig(sayOK)
			writeSkill(r.svc.Files(), "bob", "/.skills/private/SKILL.md", "private", "bob's secret procedure")
			id := create(r.svc, alice)
			_, err := r.svc.SendCommand(ctx, alice, id, "x", nil, studio.Command{Skill: "private"})
			Expect(err).To(MatchError(studio.ErrBadCommand))
			Expect(err.Error()).NotTo(ContainSubstring("secret"))
			_, err = r.svc.SendCommand(ctx, bob, id, "x", nil, studio.Command{Skill: "review"})
			Expect(err).To(MatchError(session.ErrNotFound), "another user's chat")
		})

		It("does not wait for a running turn", func() {
			gate := make(chan struct{})
			r := newAgentRig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
				if text == "slow" {
					return []studiotest.ToolStep{{Block: gate}, {Say: "done"}}
				}
				return []studiotest.ToolStep{{Say: "ok"}}
			})
			id := create(r.svc, alice)
			sent(r.svc, alice, id, "slow")
			until("the turn to run", func() bool { return turn(r.svc, alice, id).Running })
			_, err := r.svc.SendCommand(ctx, alice, id, "x", nil, studio.Command{Skill: "review"})
			Expect(err).To(MatchError(studio.ErrCommandBusy))
			Expect(pendingTexts(r.svc, alice, id)).To(BeEmpty())
			close(gate)
			idle(r.svc, alice, id)
		})

		It("runs the skill again on a regenerate", func() {
			r := newAgentRig(sayOK)
			id := create(r.svc, alice)
			_, err := r.svc.SendCommand(ctx, alice, id, "again", nil, studio.Command{Skill: "review"})
			Expect(err).NotTo(HaveOccurred())
			idle(r.svc, alice, id)
			_, err = r.svc.Regenerate(ctx, alice, id)
			Expect(err).NotTo(HaveOccurred())
			idle(r.svc, alice, id)
			got := r.h.Sent(id)
			Expect(got).To(HaveLen(2))
			Expect(got[1]).To(ContainSubstring(`<skill name="review">`))
			Expect(lastUser(transcript(r.svc, alice, id)).Invoked.Name).To(Equal("review"))
		})
	})

	Describe("a message sent to a saved agent", func() {
		reviewer := agents.Agent{ID: "rev1", Name: "Reviewer", Description: "checks drafts"}

		It("needs the saved agents switched on for the chat", func() {
			r := newAgentRig(sayOK)
			r.save(reviewer)
			id := create(r.svc, alice)
			_, err := r.svc.SendCommand(ctx, alice, id, "check the draft", nil, studio.Command{Agent: "rev1"})
			Expect(err).To(MatchError(studio.ErrBadCommand))
			Expect(err.Error()).To(ContainSubstring("Tools menu"))
		})

		It("asks the model to hand the task to the agent", func() {
			r := newAgentRig(sayOK)
			r.save(reviewer)
			id := create(r.svc, alice)
			allowSavedAgents(r.svc, id)
			_, err := r.svc.SendCommand(ctx, alice, id, "check the draft", nil, studio.Command{Agent: "reviewer"})
			Expect(err).NotTo(HaveOccurred())
			idle(r.svc, alice, id)
			Expect(r.h.Sent(id)[0]).To(ContainSubstring(`spawn_agent with agent="rev1"`))
			u := lastUser(transcript(r.svc, alice, id))
			Expect(u.Text).To(Equal("check the draft"))
			Expect(u.Invoked).To(Equal(&session.Invocation{Kind: session.InvokeAgent, Name: "Reviewer", ID: "rev1"}))
		})

		It("refuses an unknown agent, another user's agent and a missing task", func() {
			r := newAgentRig(sayOK)
			r.save(reviewer)
			r.save(agents.Agent{ID: "bob1", Principal: bob, Name: "Bob's secret"})
			id := create(r.svc, alice)
			allowSavedAgents(r.svc, id)
			for _, ref := range []string{"nobody", "bob1", "Bob's secret"} {
				_, err := r.svc.SendCommand(ctx, alice, id, "do it", nil, studio.Command{Agent: ref})
				Expect(err).To(MatchError(studio.ErrBadCommand), "agent %q", ref)
			}
			_, err := r.svc.SendCommand(ctx, alice, id, "  ", nil, studio.Command{Agent: "rev1"})
			Expect(err).To(MatchError(studio.ErrBadCommand))
			_, err = r.svc.SendCommand(ctx, alice, id, "x", nil, studio.Command{Agent: "rev1", Skill: "review"})
			Expect(err).To(MatchError(studio.ErrBadCommand))
		})
	})

	Describe("compact", func() {
		compactingService := func(before, after int) (*studio.Service, *studiotest.ToolHarness) {
			svc, h := newToolService(sayOK, func(d *studio.Deps) { d.Files = newFiles() })
			h.Compact = func(string) (int, int, error) { return before, after, nil }
			return svc, h
		}

		It("compacts, marks the transcript and the next turn starts from the compacted state", func() {
			svc, h := compactingService(9000, 3000)
			id := create(svc, alice)
			sent(svc, alice, id, "first")
			idle(svc, alice, id)
			events, cancel, err := svc.Events(ctx, alice, id)
			Expect(err).NotTo(HaveOccurred())
			defer cancel()

			before, after, err := svc.Compact(ctx, alice, id)
			Expect(err).NotTo(HaveOccurred())
			Expect([]int{before, after}).To(Equal([]int{9000, 3000}))
			ev := eventOf(events, session.KindCompacted)
			Expect([]int{ev.Before, ev.After}).To(Equal([]int{9000, 3000}))

			es := transcript(svc, alice, id)
			last := es[len(es)-1]
			Expect(last.Role).To(Equal(session.RoleCompacted))
			b, a, ok := session.ParseCompacted(last.Text)
			Expect(ok).To(BeTrue())
			Expect([]int{b, a}).To(Equal([]int{9000, 3000}))

			sent(svc, alice, id, "second")
			idle(svc, alice, id)
			Expect(string(h.Resume(id))).To(Equal(`{"compacted":true}`))
			Expect(turn(svc, alice, id).Last).To(BeEquivalentTo("done"))
		})

		It("refuses a short conversation, a chat that cannot compact and a running turn", func() {
			svc, _ := compactingService(9000, 3000)
			id := create(svc, alice)
			_, _, err := svc.Compact(ctx, alice, id)
			Expect(err).To(MatchError(studio.ErrNothingToCompact), "no message yet")

			same, _ := compactingService(500, 500)
			id2 := create(same, alice)
			sent(same, alice, id2, "hi")
			idle(same, alice, id2)
			_, _, err = same.Compact(ctx, alice, id2)
			Expect(err).To(MatchError(studio.ErrNothingToCompact), "the loop found nothing to compact")
			Expect(roles(transcript(same, alice, id2))).NotTo(ContainElement(session.RoleCompacted))

			plain, _ := newToolService(sayOK)
			id3 := create(plain, alice)
			sent(plain, alice, id3, "hi")
			idle(plain, alice, id3)
			_, _, err = plain.Compact(ctx, alice, id3)
			Expect(err).To(MatchError(studio.ErrCannotCompact))
			Expect(turn(plain, alice, id3).Running).To(BeFalse(), "the claim is given back")

			gate := make(chan struct{})
			busy, h := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
				if text == "slow" {
					return []studiotest.ToolStep{{Block: gate}, {Say: "done"}}
				}
				return []studiotest.ToolStep{{Say: "ok"}}
			})
			h.Compact = func(string) (int, int, error) { return 10, 5, nil }
			id4 := create(busy, alice)
			sent(busy, alice, id4, "hi")
			idle(busy, alice, id4)
			sent(busy, alice, id4, "slow")
			until("the turn to run", func() bool { return turn(busy, alice, id4).Running })
			_, _, err = busy.Compact(ctx, alice, id4)
			Expect(err).To(MatchError(studio.ErrChatBusy))
			close(gate)
			idle(busy, alice, id4)

			_, _, err = busy.Compact(ctx, bob, id4)
			Expect(err).To(MatchError(session.ErrNotFound), "another user's chat")
		})

		It("reports a failed summary and keeps the state", func() {
			svc, h := newToolService(sayOK)
			h.Compact = func(string) (int, int, error) { return 0, 0, errors.New("model down") }
			id := create(svc, alice)
			sent(svc, alice, id, "hi")
			idle(svc, alice, id)
			_, _, err := svc.Compact(ctx, alice, id)
			Expect(err).To(HaveOccurred())
			Expect(turn(svc, alice, id).Running).To(BeFalse())
			sent(svc, alice, id, "again")
			idle(svc, alice, id)
			Expect(string(h.Resume(id))).To(Equal(`{}`))
		})
	})

	Describe("clear", func() {
		It("writes a divider and the next turn sees nothing before it", func() {
			svc, h := newToolService(sayOK)
			id := create(svc, alice)
			sent(svc, alice, id, "first question")
			idle(svc, alice, id)
			events, cancel, err := svc.Events(ctx, alice, id)
			Expect(err).NotTo(HaveOccurred())
			defer cancel()

			Expect(svc.Clear(ctx, alice, id)).To(Succeed())
			eventOf(events, session.KindCleared)
			rec, err := svc.Get(ctx, alice, id)
			Expect(err).NotTo(HaveOccurred())
			Expect(rec.HarnessState).To(BeEmpty(), "the saved state is reset")
			Expect(roles(transcript(svc, alice, id))).To(ContainElement(session.RoleCleared))

			Expect(svc.Clear(ctx, alice, id)).To(MatchError(studio.ErrNothingToClear), "nothing new to clear")

			sent(svc, alice, id, "second question")
			idle(svc, alice, id)
			Expect(h.Resume(id)).To(BeEmpty())
			Expect(h.SystemPrompt(id)).NotTo(ContainSubstring("first question"))
			es := transcript(svc, alice, id)
			Expect(es[0].Text).To(Equal("first question"), "the transcript keeps every message")
			Expect(rec.Title).NotTo(BeEmpty())
		})

		It("is refused while a turn runs and for another user", func() {
			gate := make(chan struct{})
			svc, _ := newToolService(func(_ workspace.Actor, text string) []studiotest.ToolStep {
				if text == "slow" {
					return []studiotest.ToolStep{{Block: gate}, {Say: "done"}}
				}
				return []studiotest.ToolStep{{Say: "ok"}}
			})
			id := create(svc, alice)
			Expect(svc.Clear(ctx, alice, id)).To(MatchError(studio.ErrNothingToClear))
			sent(svc, alice, id, "slow")
			until("the turn to run", func() bool { return turn(svc, alice, id).Running })
			Expect(svc.Clear(ctx, alice, id)).To(MatchError(studio.ErrChatBusy))
			close(gate)
			idle(svc, alice, id)
			Expect(svc.Clear(ctx, bob, id)).To(MatchError(session.ErrNotFound))
		})

		It("a branch keeps what the model saw at its point, and an export shows the divider", func() {
			svc, h := newToolService(sayOK)
			id := create(svc, alice)
			sent(svc, alice, id, "first question")
			idle(svc, alice, id)
			Expect(svc.Clear(ctx, alice, id)).To(Succeed())
			sent(svc, alice, id, "second question")
			idle(svc, alice, id)
			es := transcript(svc, alice, id)
			var firstAnswer int64
			for _, e := range es {
				if e.Role == session.RoleAssistant {
					firstAnswer = e.Seq
					break
				}
			}

			above, err := svc.Fork(ctx, alice, id, firstAnswer, "")
			Expect(err).NotTo(HaveOccurred())
			sent(svc, alice, above.ID, "more")
			idle(svc, alice, above.ID)
			Expect(h.SystemPrompt(above.ID)).To(ContainSubstring("first question"), "a branch above the divider keeps the old context")

			whole, err := svc.Fork(ctx, alice, id, 0, "")
			Expect(err).NotTo(HaveOccurred())
			sent(svc, alice, whole.ID, "more")
			idle(svc, alice, whole.ID)
			Expect(h.SystemPrompt(whole.ID)).To(ContainSubstring("second question"))
			Expect(h.SystemPrompt(whole.ID)).NotTo(ContainSubstring("first question"), "a branch below the divider does not")

			exp, err := svc.Export(ctx, alice, id)
			Expect(err).NotTo(HaveOccurred())
			var exported []string
			for _, m := range exp.Messages {
				exported = append(exported, m.Role)
			}
			Expect(exported).To(ContainElement(session.RoleCleared))
			Expect(exp.Markdown()).To(ContainSubstring("Context cleared"))
			back, err := svc.ImportExport(ctx, alice, exp, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(roles(transcript(svc, alice, back.ID))).To(ContainElement(session.RoleCleared), "an import keeps the divider")
			Expect(strings.Count(exp.Markdown(), "Context cleared")).To(Equal(1))
		})
	})
})
