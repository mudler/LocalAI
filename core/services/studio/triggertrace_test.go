package studio_test

import (
	"context"
	"log/slog"
	"strings"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the trace of the trigger tools", func() {
	const secret = "remind me about the dentist"

	// traced is a trigger rig whose service logs to a buffer.
	traced := func(script func(workspace.Actor, string) []studiotest.ToolStep, mod func(*shared)) (*trigRig, *safeBuf) {
		logs := &safeBuf{}
		sh := newShared()
		sh.deps = func(d *studio.Deps) { d.Logger = slog.New(slog.NewTextHandler(logs, nil)) }
		if mod != nil {
			mod(sh)
		}
		return sh.rig(script, studio.TriggerOptions{}), logs
	}
	turnLines := func(logs *safeBuf) []string {
		var out []string
		for _, l := range strings.Split(logs.String(), "\n") {
			if strings.Contains(l, "trigger tools of the turn") {
				out = append(out, l)
			}
		}
		return out
	}

	It("records the tools a Studio turn was given, with the chat and no message text", func() {
		r, logs := traced(say, nil)
		id := create(r.svc, alice)
		sent(r.svc, alice, id, secret)
		idle(r.svc, alice, id)
		lines := turnLines(logs)
		Expect(lines).NotTo(BeEmpty(), "log = %s", logs.String())
		line := lines[len(lines)-1]
		Expect(line).To(ContainSubstring("chat=" + id))
		Expect(line).To(ContainSubstring("origin=studio"))
		Expect(line).To(ContainSubstring("offered=propose_trigger,list_triggers,update_trigger,delete_trigger"))
		Expect(logs.String()).NotTo(ContainSubstring(secret))
	})

	It("names the reason of each refused tool when the chat switched the group off", func() {
		r, logs := traced(say, nil)
		id := create(r.svc, alice)
		Expect(r.svc.SetBuiltinsOff(context.Background(), alice, id, []string{"triggers"})).To(Succeed())
		sent(r.svc, alice, id, secret)
		idle(r.svc, alice, id)
		lines := turnLines(logs)
		Expect(lines).NotTo(BeEmpty(), "log = %s", logs.String())
		line := lines[len(lines)-1]
		Expect(line).To(ContainSubstring("propose_trigger=off for this chat"))
		Expect(line).To(ContainSubstring("delete_trigger=off for this chat"))
		Expect(line).To(MatchRegexp(`offered=("")? `))
	})

	It("names the reason for a user without the triggers feature", func() {
		r, logs := traced(say, func(sh *shared) {
			sh.users = func(string) error { return nil }
			sh.allowed = func(_ *auth.User, f string) bool { return f != auth.FeatureTriggers }
		})
		id := create(r.svc, alice)
		sent(r.svc, alice, id, "hi")
		idle(r.svc, alice, id)
		lines := turnLines(logs)
		Expect(lines).NotTo(BeEmpty(), "log = %s", logs.String())
		Expect(lines[len(lines)-1]).To(ContainSubstring(studio.ErrTriggersForbidden.Error()))
	})

	It("names the reason for a run that a trigger started", func() {
		r, logs := traced(say, nil)
		tr := r.schedule(studio.TriggerInput{})
		r.due()
		run := r.waitEnded(tr.ID)
		var found string
		for _, l := range turnLines(logs) {
			if strings.Contains(l, "chat="+run.ChatID) {
				found = l
			}
		}
		Expect(found).To(ContainSubstring("origin=trigger"), "log = %s", logs.String())
		Expect(found).To(ContainSubstring("a run that started by itself cannot set triggers"))
	})

	It("logs the outcome of a tool call, the error text and the ref, and not the arguments", func() {
		r, logs := traced(proposeScript(`{"name":"Dentist","prompt":"`+secret+`","when":"daily 25:00"}`), nil)
		id := create(r.svc, alice)
		sent(r.svc, alice, id, "propose")
		idle(r.svc, alice, id)
		var call string
		for _, l := range strings.Split(logs.String(), "\n") {
			if strings.Contains(l, "trigger tool call") {
				call = l
			}
		}
		Expect(call).To(ContainSubstring("tool=propose_trigger"), "log = %s", logs.String())
		Expect(call).To(ContainSubstring("ok=false"))
		Expect(call).To(ContainSubstring("error="))
		Expect(logs.String()).NotTo(ContainSubstring(secret))
	})
})

var _ = Describe("the note of a turn without trigger tools", func() {
	const ask = "set a reminder for tomorrow"
	raw := func(r *trigRig, id string) string { return strings.Join(r.h.SentRaw(id), "\n") }

	It("tells the model the reason in a Studio chat that switched the group off, and never in one that has the tools", func() {
		r := newTrigRig(say)
		id := create(r.svc, alice)
		sent(r.svc, alice, id, ask)
		idle(r.svc, alice, id)
		Expect(raw(r, id)).NotTo(ContainSubstring(studiotest.AbsentNoteStart))

		Expect(r.svc.SetBuiltinsOff(context.Background(), alice, id, []string{"triggers"})).To(Succeed())
		sent(r.svc, alice, id, ask)
		idle(r.svc, alice, id)
		got := raw(r, id)
		Expect(got).To(ContainSubstring(studiotest.AbsentNoteStart + "triggers are switched off for this chat"))
		Expect(got).To(ContainSubstring("Do not say one was set"))
	})

	It("is given to a run that nobody watches", func() {
		r := newTrigRig(say)
		tr := r.schedule(studio.TriggerInput{})
		r.due()
		run := r.waitEnded(tr.ID)
		Expect(strings.Join(r.h.SentRaw(run.ChatID), "\n")).To(ContainSubstring(studiotest.AbsentNoteStart + "a run that started by itself"))
	})

	It("does not depend on the words of the message", func() {
		r := newTrigRig(say)
		id := create(r.svc, alice)
		Expect(r.svc.SetBuiltinsOff(context.Background(), alice, id, []string{"triggers"})).To(Succeed())
		sent(r.svc, alice, id, "hello")
		idle(r.svc, alice, id)
		Expect(raw(r, id)).To(ContainSubstring(studiotest.AbsentNoteStart))
	})
})
