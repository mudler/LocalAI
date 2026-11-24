package studio_test

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("timers and approval modes in a connector chat", func() {
	ctx := context.Background()
	const (
		chatID = "C1"
		thread = "T1"
	)
	var (
		fake *postAddon
		gate *capSwitch
		r    *connRig
		b    connectors.Binding
		args string
		seq  int
	)
	script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
		switch {
		case strings.HasSuffix(text, "propose"):
			return []studiotest.ToolStep{{Tool: "propose_trigger", Args: args}, {Say: "I did it."}}
		case strings.Contains(text, "RUN-ERR"):
			return []studiotest.ToolStep{{Err: errors.New("the model broke")}}
		case strings.Contains(text, "RUN"):
			return []studiotest.ToolStep{{Say: "result is 42"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
	// setup binds a connection with the approval under test.
	setup := func(ap connectors.Approval) {
		fake = &postAddon{}
		gate = &capSwitch{}
		tr := studiobot.NewTransport(studiobot.Addon{ID: "chat-addon", Kind: contract.ConnectorKind{ID: "chatty", Title: "Chatty", Delivery: contract.DeliveryPull}, Runtime: fake})
		r = newConnRigOpts(newShared(), connStore(), tr, script, nil, func(o *studio.ConnectorOptions) {
			o.OutboxPoll = 10 * time.Millisecond
			o.RatePerMinute = 100
		})
		r.svc.SetCapabilityGate(gate)
		gate.set(studio.CapTriggersPropose, true)
		gate.set(studio.CapTriggersRun, true)
		gate.set(studio.CapProposalsConfirm, true)
		b = r.bind(studio.ConnectorInput{Kind: "chatty", Token: "tok", Allow: []string{"11"}, Approval: &ap, ReplyLanguage: englishReplies()})
		seq = 0
	}
	from := func(sender, text string) {
		seq++
		Expect(r.conns.Ingest(ctx, "chat-addon", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{
			Chat: chatID, Thread: thread, Mention: true, MessageID: "m" + string(rune('a'+seq)), SenderID: sender, SenderName: "Cy", Text: text}})).To(Succeed())
	}
	say := func(text string) { from("11", text) }
	texts := func() []string { return fake.texts(chatID, thread) }
	waitTexts := func(n int) {
		waitFor("the messages", func() bool { return len(texts()) >= n })
	}
	// ask makes the model call propose_trigger with the arguments and waits for
	// the host's message and the model's closing line.
	ask := func(a string) {
		args = a
		before := len(texts())
		say("propose")
		waitTexts(before + 2)
	}
	last := func() string { t := texts(); return t[len(t)-1] }
	timer := func(name, in string) string {
		return `{"name":"` + name + `","prompt":"RUN ` + name + `","when":{"in":"` + in + `"}}`
	}
	live := func() []triggers.Trigger {
		l, err := r.host.ListTriggers(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		return l
	}
	waiting := func() []triggers.Trigger {
		ps, err := r.host.Proposals(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		return ps
	}
	reportsOf := func(name string) []string {
		var out []string
		for _, s := range texts() {
			if strings.HasPrefix(s, name+"\n\n") {
				out = append(out, s)
			}
		}
		return out
	}

	Describe("a one-off timer in ask mode", func() {
		It("is proposed in the chat with its cost, answered with plain yes, runs once, reports and goes away", func() {
			setup(connectors.Approval{})
			ask(timer("Ping", "10 minutes"))
			ps := waiting()
			Expect(ps).To(HaveLen(1))
			p := ps[0]
			Expect(p.OneOff()).To(BeTrue())
			Expect(p.Route.Target).To(Equal(triggers.TargetOrigin), "a timer answers in the chat it was set in")
			at, _ := p.OnceAt()
			Expect(at.Sub(r.sh.clock.Now())).To(BeNumerically(">=", 10*time.Minute))
			Expect(at.Sub(r.sh.clock.Now())).To(BeNumerically("<", 11*time.Minute))
			Expect(texts()[0]).To(ContainSubstring("Cost: one model run."))
			Expect(texts()[0]).To(ContainSubstring(`Reply "yes"`))
			Expect(texts()[0]).To(ContainSubstring("confirm " + p.Route.Code))

			chats, _ := r.svc.List(ctx, alice, 10)
			sent := len(r.h.Sent(chats[0].ID))
			say("Yes!")
			waitTexts(3)
			Expect(last()).To(ContainSubstring("Confirmed"))
			Expect(r.h.Sent(chats[0].ID)).To(HaveLen(sent), "the model never read the answer")
			Expect(live()).To(HaveLen(1))
			Expect(waiting()).To(BeEmpty())

			r.sh.clock.Advance(11 * time.Minute)
			r.pass()
			waitFor("the report", func() bool { return len(reportsOf("Ping")) == 1 })
			Expect(reportsOf("Ping")[0]).To(Equal("Ping\n\nresult is 42"))
			waitFor("the timer to go away", func() bool { return len(live()) == 0 })
			r.sh.clock.Advance(time.Hour)
			r.pass()
			Expect(reportsOf("Ping")).To(HaveLen(1), "a timer runs once")
		})

		It("reads a time of day in the zone the model names", func() {
			setup(connectors.Approval{})
			ask(`{"name":"Eve","prompt":"RUN eve","when":{"at":"18:00"},"time_zone":"Europe/Rome"}`)
			p := waiting()[0]
			Expect(p.TZ).To(Equal("Europe/Rome"))
			Expect(p.Schedule).To(Equal("once 2026-10-27 18:00"))
			Expect(texts()[0]).To(ContainSubstring("2026-10-27 at 18:00 (Europe/Rome)"))
		})

		It("moves a time of day that is past to the next day", func() {
			setup(connectors.Approval{})
			r.sh.clock.Advance(12 * time.Hour) // 19:00 UTC
			ask(`{"name":"Eve","prompt":"RUN eve","when":{"at":"18:00"},"time_zone":"UTC"}`)
			Expect(waiting()[0].Schedule).To(Equal("once 2026-10-28 18:00"))
		})

		It("needs a zone for a time of day", func() {
			setup(connectors.Approval{})
			args = `{"name":"Eve","prompt":"RUN eve","when":{"at":"18:00"}}`
			say("propose")
			waitFor("the model's answer", func() bool { return len(r.h.ResultsFor("propose_trigger")) == 1 })
			Expect(r.h.ResultsFor("propose_trigger")[0].Err).To(MatchError(ContainSubstring("time_zone")))
			Expect(waiting()).To(BeEmpty())
		})

		It("refuses a delay under a minute, one that is not a delay, and one beyond a year", func() {
			setup(connectors.Approval{})
			for i, in := range []string{"30 seconds", "soon", "0 minutes", "400 days"} {
				args = timer("Bad", in)
				say("propose")
				i := i
				waitFor("the model's answer", func() bool { return len(r.h.ResultsFor("propose_trigger")) == i+1 })
				Expect(r.h.ResultsFor("propose_trigger")[i].Err).To(HaveOccurred(), in)
			}
			Expect(waiting()).To(BeEmpty())
		})

		It("refuses a one-off text that is less than a minute away or already past", func() {
			setup(connectors.Approval{})
			for i, when := range []string{"once 2026-10-27 06:00", "once 2026-10-27 07:00"} {
				args = `{"name":"Bad","prompt":"RUN","when":"` + when + `","time_zone":"UTC"}`
				say("propose")
				i := i
				waitFor("the model's answer", func() bool { return len(r.h.ResultsFor("propose_trigger")) == i+1 })
				Expect(r.h.ResultsFor("propose_trigger")[i].Err).To(MatchError(ContainSubstring("at least a minute")))
			}
		})

		It("still refuses a recurring schedule more often than every 5 minutes", func() {
			setup(connectors.Approval{})
			args = `{"name":"Fast","prompt":"RUN","when":"*/2 * * * *","time_zone":"UTC"}`
			say("propose")
			waitFor("the model's answer", func() bool { return len(r.h.ResultsFor("propose_trigger")) == 1 })
			Expect(r.h.ResultsFor("propose_trigger")[0].Err).To(MatchError(ContainSubstring("once every 5 minutes")))
		})

		It("lapses when its time passes before anybody answers", func() {
			setup(connectors.Approval{})
			ask(timer("Ping", "10 minutes"))
			r.sh.clock.Advance(12 * time.Minute)
			Expect(waiting()).To(BeEmpty())
			n := len(texts())
			say("yes")
			waitFor("the reply", func() bool { return len(texts()) > n })
			Expect(last()).To(ContainSubstring("expired"))
			Expect(live()).To(BeEmpty())
		})

		It("says at once that a timer failed, and removes it", func() {
			setup(connectors.Approval{})
			ask(`{"name":"Boom","prompt":"RUN-ERR","when":{"in":"5 minutes"}}`)
			say("ok")
			waitTexts(3)
			r.sh.clock.Advance(6 * time.Minute)
			r.pass()
			waitFor("the failure", func() bool {
				for _, s := range texts() {
					if strings.HasPrefix(s, "Boom failed:") {
						return true
					}
				}
				return false
			})
			waitFor("the timer to go away", func() bool { return len(live()) == 0 })
		})
	})

	Describe("a plain answer", func() {
		It("is ignored from a sender the gate does not allow, and goes to the agent", func() {
			setup(connectors.Approval{})
			ask(timer("Ping", "10 minutes"))
			gate.set(studio.CapProposalsConfirm, false)
			chats, _ := r.svc.List(ctx, alice, 10)
			sent := len(r.h.Sent(chats[0].ID))
			say("yes")
			waitFor("the agent", func() bool { return len(r.h.Sent(chats[0].ID)) == sent+1 })
			Expect(waiting()).To(HaveLen(1))
			Expect(live()).To(BeEmpty())
		})

		It("is ignored from a sender who is not allowed to talk to the bot", func() {
			setup(connectors.Approval{})
			ask(timer("Ping", "10 minutes"))
			n := len(texts())
			from("99", "yes")
			from("99", "confirm "+waiting()[0].Route.Code)
			time.Sleep(150 * time.Millisecond)
			Expect(texts()).To(HaveLen(n))
			Expect(waiting()).To(HaveLen(1))
		})

		It("from another thread does not answer", func() {
			setup(connectors.Approval{})
			ask(timer("Ping", "10 minutes"))
			Expect(r.conns.Ingest(ctx, "chat-addon", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{
				Chat: chatID, Thread: "T2", Mention: true, MessageID: "x1", SenderID: "11", Text: "yes"}})).To(Succeed())
			waitFor("the agent's answer", func() bool { return len(fake.texts(chatID, "T2")) == 1 })
			Expect(waiting()).To(HaveLen(1))
		})

		It("is not taken from longer text", func() {
			setup(connectors.Approval{})
			ask(timer("Ping", "10 minutes"))
			n := len(texts())
			say("yes but make it 20 minutes")
			waitFor("the agent's answer", func() bool { return len(texts()) > n })
			Expect(waiting()).To(HaveLen(1))
			Expect(live()).To(BeEmpty())
		})

		It("no cancels the one proposal that waits", func() {
			setup(connectors.Approval{})
			ask(timer("Ping", "10 minutes"))
			say("no")
			waitTexts(3)
			Expect(last()).To(Equal("Cancelled."))
			Expect(waiting()).To(BeEmpty())
			Expect(live()).To(BeEmpty())
		})

		It("asks for the code when several proposals wait, and a code then answers one", func() {
			setup(connectors.Approval{})
			ask(timer("One", "10 minutes"))
			ask(timer("Two", "20 minutes"))
			ps := waiting()
			Expect(ps).To(HaveLen(2))
			n := len(texts())
			say("yes")
			waitFor("the question", func() bool { return len(texts()) > n })
			Expect(last()).To(ContainSubstring("More than one"))
			Expect(last()).To(ContainSubstring(ps[0].Route.Code))
			Expect(last()).To(ContainSubstring(ps[1].Route.Code))
			Expect(live()).To(BeEmpty())
			n = len(texts())
			say("yes " + strings.ToLower(ps[1].Route.Code))
			waitFor("the confirmation", func() bool { return len(texts()) > n })
			Expect(last()).To(ContainSubstring(`Confirmed. "Two"`))
			Expect(live()).To(HaveLen(1))
			Expect(live()[0].Name).To(Equal("Two"))
			Expect(waiting()).To(HaveLen(1))
		})

		It("is an ordinary message when nothing waits", func() {
			setup(connectors.Approval{})
			chats := func() int { l, _ := r.svc.List(ctx, alice, 10); return len(l) }
			say("yes")
			waitTexts(1)
			Expect(last()).To(Equal("ok"), "the agent answered")
			Expect(chats()).To(Equal(1))
		})
	})

	Describe("auto mode", func() {
		It("makes a timer at once, tells the person, and a bare cancel removes it", func() {
			setup(connectors.Approval{Mode: connectors.ApprovalAuto})
			ask(timer("Ping", "10 minutes"))
			Expect(waiting()).To(BeEmpty())
			l := live()
			Expect(l).To(HaveLen(1))
			Expect(l[0].Enabled).To(BeTrue())
			Expect(l[0].Route.Auto).To(BeTrue())
			Expect(texts()[0]).To(ContainSubstring(`Set up "Ping"`))
			Expect(texts()[0]).To(ContainSubstring("Cost: one model run."))
			Expect(texts()[0]).To(ContainSubstring(`reply "cancel"`))
			say("cancel")
			waitTexts(3)
			Expect(last()).To(ContainSubstring("Cancelled"))
			Expect(live()).To(BeEmpty())
		})

		It("runs the timer once at its time", func() {
			setup(connectors.Approval{Mode: connectors.ApprovalAuto})
			ask(timer("Ping", "10 minutes"))
			r.sh.clock.Advance(11 * time.Minute)
			r.pass()
			waitFor("the report", func() bool { return len(reportsOf("Ping")) == 1 })
			waitFor("the timer to go away", func() bool { return len(live()) == 0 })
		})

		It("cancels by code at any time, from the same thread only", func() {
			setup(connectors.Approval{Mode: connectors.ApprovalAuto})
			ask(timer("Ping", "10 minutes"))
			code := live()[0].Route.Code
			r.sh.clock.Advance(2 * time.Hour) // a bare cancel no longer applies, the code does
			n := len(texts())
			say("cancel")
			waitFor("the agent", func() bool { return len(texts()) > n })
			Expect(live()).To(HaveLen(1))
			n = len(texts())
			say("cancel " + code)
			waitFor("the answer", func() bool { return len(texts()) > n })
			Expect(live()).To(BeEmpty())
		})

		It("asks instead when the connection already has the most timers", func() {
			setup(connectors.Approval{Mode: connectors.ApprovalAuto, MaxPending: 1})
			ask(timer("One", "10 minutes"))
			ask(timer("Two", "20 minutes"))
			Expect(live()).To(HaveLen(1))
			ps := waiting()
			Expect(ps).To(HaveLen(1))
			Expect(ps[0].Name).To(Equal("Two"))
			Expect(texts()[2]).To(ContainSubstring("I did not start it by myself"))
			Expect(texts()[2]).To(ContainSubstring("the most is 1"))
		})

		It("asks instead when the day's runs would pass the limit", func() {
			setup(connectors.Approval{Mode: connectors.ApprovalAuto, MaxRunsPerDay: 2})
			ask(timer("One", "10 minutes"))
			ask(timer("Two", "20 minutes"))
			ask(timer("Three", "30 minutes"))
			Expect(live()).To(HaveLen(2))
			Expect(waiting()).To(HaveLen(1))
			Expect(texts()[4]).To(ContainSubstring("a day"))
		})

		It("asks instead for a timer beyond the lifetime", func() {
			setup(connectors.Approval{Mode: connectors.ApprovalAuto, MaxLifetimeDays: 2})
			ask(timer("Far", "3 days"))
			Expect(live()).To(BeEmpty())
			Expect(waiting()).To(HaveLen(1))
			Expect(texts()[0]).To(ContainSubstring("more than 2 days"))
		})

		It("lets a daily trigger through, with an end date and the daily limit, and removes it at the end", func() {
			setup(connectors.Approval{Mode: connectors.ApprovalAuto, MaxLifetimeDays: 7, MaxRunsPerDay: 3})
			ask(`{"name":"Daily","prompt":"RUN","when":"daily 08:00","time_zone":"UTC","daily_cap":50}`)
			l := live()
			Expect(l).To(HaveLen(1))
			Expect(l[0].DailyCap).To(Equal(3), "the owner's daily limit bounds it")
			Expect(l[0].Route.Until.Sub(r.sh.clock.Now())).To(Equal(7 * 24 * time.Hour))
			Expect(texts()[0]).To(ContainSubstring("one model run each time"))
			Expect(texts()[0]).To(ContainSubstring("stops by itself on 2026-11-03"))
			r.sh.clock.Advance(8 * 24 * time.Hour)
			r.pass()
			Expect(live()).To(BeEmpty())
			waitFor("the notice", func() bool { return strings.Contains(last(), "end of its lifetime") })
		})

		It("holds the time limit of an automatic trigger to the ceiling of the administrator", func() {
			setup(connectors.Approval{Mode: connectors.ApprovalAuto, MaxLifetimeDays: 7})
			ask(`{"name":"Long","prompt":"RUN","when":"daily 08:00","time_zone":"UTC","run_limit":"2h"}`)
			ask(`{"name":"Short","prompt":"RUN","when":"daily 09:00","time_zone":"UTC","run_limit":"5m"}`)
			got := map[string]time.Duration{}
			for _, t := range live() {
				got[t.Name] = t.RunLimit
			}
			Expect(got).To(Equal(map[string]time.Duration{"Long": studio.DefaultRunLimit, "Short": 5 * time.Minute}))
		})

		It("asks for a recurring trigger that runs more often than the owner allows", func() {
			setup(connectors.Approval{Mode: connectors.ApprovalAuto, MinGapMinutes: 180})
			ask(`{"name":"Hourly","prompt":"RUN","when":"0 * * * *","time_zone":"UTC"}`)
			Expect(live()).To(BeEmpty())
			Expect(waiting()).To(HaveLen(1))
			Expect(texts()[0]).To(ContainSubstring("the least is 180 minutes"))
		})

		It("keeps the proposals of other bindings out of its count", func() {
			setup(connectors.Approval{Mode: connectors.ApprovalAuto, MaxPending: 1})
			other := r.bind(studio.ConnectorInput{Kind: "chatty", Name: "Other", Token: "tok2", Allow: []string{"11"}})
			Expect(other.ID).NotTo(Equal(b.ID))
			ask(timer("One", "10 minutes"))
			Expect(live()).To(HaveLen(1))
		})
	})

	Describe("auto_readonly mode", func() {
		It("makes at once a timer that says its runs only read", func() {
			setup(connectors.Approval{Mode: connectors.ApprovalAutoReadonly})
			ask(`{"name":"Look","prompt":"RUN look","when":{"in":"10 minutes"},"access":"read_only"}`)
			l := live()
			Expect(l).To(HaveLen(1))
			Expect(l[0].Route.ReadOnly).To(BeTrue())
			Expect(waiting()).To(BeEmpty())
		})

		It("asks when the proposal does not say it only reads", func() {
			setup(connectors.Approval{Mode: connectors.ApprovalAutoReadonly})
			ask(timer("Look", "10 minutes"))
			Expect(live()).To(BeEmpty())
			Expect(waiting()).To(HaveLen(1))
			Expect(texts()[0]).To(ContainSubstring("does not say that its runs only read"))
		})

		It("asks when a read-only proposal also writes a file", func() {
			setup(connectors.Approval{Mode: connectors.ApprovalAutoReadonly})
			ask(`{"name":"Look","prompt":"RUN look","when":{"in":"10 minutes"},"access":"read_only","writes":"notes/out.md"}`)
			Expect(live()).To(BeEmpty())
			Expect(waiting()).To(HaveLen(1))
			Expect(texts()[0]).To(ContainSubstring("write a file"))
		})
	})

	Describe("the audit", func() {
		It("records an automatic setup and a cancel, with no text of the prompt", func() {
			setup(connectors.Approval{Mode: connectors.ApprovalAuto})
			ask(timer("Ping", "10 minutes"))
			say("cancel")
			waitTexts(3)
			evs, err := r.svc.Changes().Events(ctx, alice, 30)
			Expect(err).NotTo(HaveOccurred())
			var details []string
			for _, e := range evs {
				if e.Kind == "trigger" {
					details = append(details, e.Action+": "+e.Detail)
					Expect(e.Detail).NotTo(ContainSubstring("RUN Ping"))
				}
			}
			Expect(strings.Join(details, "\n")).To(And(ContainSubstring("confirm: automatic, mode auto"), ContainSubstring("dismiss: removed after automatic setup")))
		})
	})
})
