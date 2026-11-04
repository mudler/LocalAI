package studio_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// postAddon is a connector add-on of a made-up platform. It records what the
// host writes, where, so the specs show what a person reads in a chat.
type postAddon struct {
	mu   sync.Mutex
	sent []contract.ConnectorRequest
}

func (a *postAddon) Connector(ctx context.Context, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	switch req.Op {
	case contract.ConnectorOpPoll:
		select {
		case <-ctx.Done():
		case <-time.After(20 * time.Millisecond):
		}
		return contract.ConnectorResponse{NextOffset: req.Offset}, nil
	case contract.ConnectorOpSend:
		a.mu.Lock()
		defer a.mu.Unlock()
		a.sent = append(a.sent, req)
		return contract.ConnectorResponse{MessageID: "out"}, nil
	}
	return contract.ConnectorResponse{}, nil
}

// texts are the messages written to a chat and thread, oldest first.
func (a *postAddon) texts(chat, thread string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, r := range a.sent {
		if r.Chat == chat && r.Thread == thread {
			out = append(out, r.Text)
		}
	}
	return out
}

func (a *postAddon) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.sent)
}

// capSwitch is a CapabilityGate that a spec turns on and off by name.
type capSwitch struct {
	mu  sync.Mutex
	on  map[string]bool
	ask []studio.CapabilityRequest
}

func (g *capSwitch) Allow(_ context.Context, r studio.CapabilityRequest) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ask = append(g.ask, r)
	return g.on[r.Capability]
}

func (g *capSwitch) set(name string, on bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.on == nil {
		g.on = map[string]bool{}
	}
	g.on[name] = on
}

func (g *capSwitch) asked(name string) []studio.CapabilityRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []studio.CapabilityRequest
	for _, r := range g.ask {
		if r.Capability == name {
			out = append(out, r)
		}
	}
	return out
}

var _ = Describe("triggers proposed from a connector chat", func() {
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
		args string // the arguments of the next propose_trigger
		seq  int
	)
	script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
		switch {
		case text == "propose":
			return []studiotest.ToolStep{{Tool: "propose_trigger", Args: args}, {Say: "I proposed it."}}
		case text == "list":
			return []studiotest.ToolStep{{Tool: "list_triggers", Args: `{}`}, {Say: "listed"}}
		case strings.Contains(text, "RUN-SILENT"):
			return []studiotest.ToolStep{{Say: "[SILENT]"}}
		case strings.Contains(text, "RUN-ERR"):
			return []studiotest.ToolStep{{Err: errors.New("the model broke")}}
		case strings.Contains(text, "RUN-FAIL"):
			return []studiotest.ToolStep{{Say: "@channel done: 42"}}
		case strings.Contains(text, "RUN-LONG"):
			return []studiotest.ToolStep{{Say: strings.Repeat("x", 5000)}}
		case strings.Contains(text, "RUN"):
			return []studiotest.ToolStep{{Say: "result is 42"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
	proposal := func(name, delivery string) string {
		return `{"name":"` + name + `","prompt":"RUN do the thing","when":"daily 08:00","time_zone":"UTC"` + delivery + `}`
	}
	origin := `,"delivery":{"target":"origin"}`

	setup := func() {
		fake = &postAddon{}
		gate = &capSwitch{}
		tr := studiobot.NewTransport(studiobot.Addon{ID: "chat-addon", Kind: contract.ConnectorKind{ID: "chatty", Title: "Chatty", Delivery: contract.DeliveryPull}, Runtime: fake})
		r = newConnRigOpts(newShared(), connStore(), tr, script, nil, func(o *studio.ConnectorOptions) {
			o.OutboxPoll = 10 * time.Millisecond
			o.RatePerMinute = 100
		})
		r.svc.SetCapabilityGate(gate)
		b = r.bind(studio.ConnectorInput{Kind: "chatty", Token: "tok", Allow: []string{"11"}})
		args = proposal("Digest", origin)
		seq = 0
	}
	say := func(text string) {
		seq++
		id := "m" + string(rune('a'+seq))
		Expect(r.conns.Ingest(ctx, "chat-addon", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{
			Chat: chatID, Thread: thread, Mention: true, MessageID: id, SenderID: "11", SenderName: "Cy", Text: text}})).To(Succeed())
	}
	answered := func(n int) {
		waitFor("the answer", func() bool { return len(fake.texts(chatID, thread)) >= n })
	}
	proposals := func() []triggers.Trigger {
		ps, err := r.host.Proposals(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		return ps
	}
	// propose runs a turn that proposes and waits for the code message and the answer.
	propose := func() triggers.Trigger {
		before := len(fake.texts(chatID, thread))
		say("propose")
		answered(before + 2)
		ps := proposals()
		Expect(ps).NotTo(BeEmpty())
		return ps[len(ps)-1]
	}
	chatTools := func() []string {
		l, err := r.svc.List(ctx, alice, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(l).NotTo(BeEmpty())
		return r.h.ToolNames(l[0].ID)
	}

	Describe("the tools", func() {
		It("are not offered until the capability is on", func() {
			setup()
			say("hello")
			answered(1)
			names := chatTools()
			Expect(names).NotTo(ContainElement("propose_trigger"))
			Expect(names).NotTo(ContainElement("list_triggers"))
			asked := gate.asked(studio.CapTriggersPropose)
			Expect(asked).NotTo(BeEmpty())
			Expect(asked[0].Binding).To(Equal(b.ID))
			Expect(asked[0].Agent).To(Equal(r.agent.ID))
			Expect(asked[0].Owner).To(Equal(alice))
		})

		It("are propose and list once it is on, and never change or delete", func() {
			setup()
			gate.set(studio.CapTriggersPropose, true)
			say("hello")
			answered(1)
			names := chatTools()
			Expect(names).To(ContainElement("propose_trigger"))
			Expect(names).To(ContainElement("list_triggers"))
			Expect(names).NotTo(ContainElement("update_trigger"))
			Expect(names).NotTo(ContainElement("delete_trigger"))
		})

		It("are not offered to the run of a trigger", func() {
			setup()
			gate.set(studio.CapTriggersPropose, true)
			gate.set(studio.CapTriggersRun, true)
			tr := r.schedule(studio.TriggerInput{AgentID: r.agent.ID, Prompt: "RUN go"})
			r.due()
			run := r.waitEnded(tr.ID)
			Expect(r.h.ToolNames(run.ChatID)).NotTo(ContainElement("propose_trigger"))
		})
	})

	Describe("a proposal", func() {
		It("is shown in the chat with its code and waits for an answer", func() {
			setup()
			gate.set(studio.CapTriggersPropose, true)
			p := propose()
			Expect(p.Enabled).To(BeFalse())
			Expect(p.Route.Binding).To(Equal(b.ID))
			Expect(p.Route.Chat).To(Equal(chatID))
			Expect(p.Route.Thread).To(Equal(thread))
			Expect(p.Route.Target).To(Equal(triggers.TargetOrigin))
			Expect(p.Route.Code).To(MatchRegexp(`^[A-Z2-9]{4}$`))
			Expect(p.Route.Expires.Sub(p.Route.ProposedAt)).To(Equal(24 * time.Hour))
			Expect(p.AgentID).To(Equal(r.agent.ID))
			texts := fake.texts(chatID, thread)
			Expect(texts[0]).To(ContainSubstring("Proposal " + p.Route.Code))
			Expect(texts[0]).To(ContainSubstring(`confirm ` + p.Route.Code))
			Expect(texts[0]).To(ContainSubstring("this chat"))
			Expect(texts[0]).To(ContainSubstring("RUN do the thing"), "the person reads what the run will do")
			res := r.h.ResultsFor("propose_trigger")
			Expect(res).To(HaveLen(1))
			Expect(res[0].Out).To(ContainSubstring("not active"))
			Expect(res[0].Out).NotTo(ContainSubstring(p.Route.Code), "the model does not repeat the code")
		})

		It("defaults to the Inbox", func() {
			setup()
			gate.set(studio.CapTriggersPropose, true)
			args = proposal("Quiet", "")
			p := propose()
			Expect(p.Route.TargetOrDefault()).To(Equal(triggers.TargetInbox))
			Expect(fake.texts(chatID, thread)[0]).To(ContainSubstring("the Inbox"))
		})

		It("refuses a fourth proposal in an hour from one binding", func() {
			setup()
			gate.set(studio.CapTriggersPropose, true)
			for i, n := range []string{"One", "Two", "Three"} {
				args = proposal(n, origin)
				propose()
				Expect(proposals()).To(HaveLen(i + 1))
			}
			args = proposal("Four", origin)
			before := len(fake.texts(chatID, thread))
			say("propose")
			answered(before + 1)
			Expect(proposals()).To(HaveLen(3))
			res := r.h.ResultsFor("propose_trigger")
			Expect(res[len(res)-1].Err).To(MatchError(ContainSubstring("at most 3")))
			r.sh.clock.Advance(61 * time.Minute)
			args = proposal("Five", origin)
			propose()
			Expect(proposals()).To(HaveLen(4))
		})

		It("lapses after 24 hours and cannot be confirmed", func() {
			setup()
			gate.set(studio.CapTriggersPropose, true)
			gate.set(studio.CapProposalsConfirm, true)
			p := propose()
			r.sh.clock.Advance(24*time.Hour + time.Minute)
			Expect(proposals()).To(BeEmpty(), "an expired proposal is not listed as waiting")
			_, err := r.host.ConfirmProposal(ctx, alice, p.ID, "UTC")
			Expect(err).To(MatchError(studio.ErrProposalExpired))
			v, err := r.host.ProposalStatus(ctx, alice, p.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(v.State).To(Equal(studio.ProposalExpired))
			before := len(fake.texts(chatID, thread))
			say("confirm " + p.Route.Code)
			Expect(fake.texts(chatID, thread)).To(HaveLen(before), "a code that no waiting proposal has goes to the agent: it answers")
		})

		It("is swept to expired by a pass", func() {
			setup()
			gate.set(studio.CapTriggersPropose, true)
			p := propose()
			r.sh.clock.Advance(25 * time.Hour)
			r.pass()
			got, err := r.sh.trg.GetTrigger(ctx, p.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Outcome).To(Equal(triggers.OutcomeExpired))
		})
	})

	Describe("an answer by text", func() {
		It("from an allowed sender in the same chat and thread turns the proposal into a trigger, before any model turn", func() {
			setup()
			gate.set(studio.CapTriggersPropose, true)
			gate.set(studio.CapProposalsConfirm, true)
			p := propose()
			chats, _ := r.svc.List(ctx, alice, 10)
			sentBefore := len(r.h.Sent(chats[0].ID))
			n := len(fake.texts(chatID, thread))
			say("Confirm " + strings.ToLower(p.Route.Code))
			answered(n + 1)
			last := fake.texts(chatID, thread)
			Expect(last[len(last)-1]).To(ContainSubstring("Confirmed"))
			Expect(r.h.Sent(chats[0].ID)).To(HaveLen(sentBefore), "the model never read the phrase")
			list, err := r.host.ListTriggers(ctx, alice)
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(1))
			Expect(list[0].Enabled).To(BeTrue())
			Expect(list[0].Route.Target).To(Equal(triggers.TargetOrigin))
			asked := gate.asked(studio.CapProposalsConfirm)
			Expect(asked).NotTo(BeEmpty())
			Expect(asked[0]).To(Equal(studio.CapabilityRequest{Capability: studio.CapProposalsConfirm, Binding: b.ID, Owner: alice, Agent: r.agent.ID, Sender: "11", Kind: "trigger"}))
			// Single use: the code is spent.
			n = len(fake.texts(chatID, thread))
			say("confirm " + p.Route.Code)
			answered(n + 1)
			Expect(r.host.Proposals(ctx, alice)).To(BeEmpty())
			list, _ = r.host.ListTriggers(ctx, alice)
			Expect(list).To(HaveLen(1))
		})

		It("cancel declines the proposal", func() {
			setup()
			gate.set(studio.CapTriggersPropose, true)
			gate.set(studio.CapProposalsConfirm, true)
			p := propose()
			n := len(fake.texts(chatID, thread))
			say("cancel " + p.Route.Code)
			answered(n + 1)
			v, err := r.host.ProposalStatus(ctx, alice, p.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(v.State).To(Equal(studio.ProposalDeclined))
		})

		It("is refused when the gate does not allow the sender", func() {
			setup()
			gate.set(studio.CapTriggersPropose, true)
			p := propose()
			n := len(fake.texts(chatID, thread))
			say("confirm " + p.Route.Code)
			answered(n + 1)
			last := fake.texts(chatID, thread)
			Expect(last[len(last)-1]).To(ContainSubstring("Studio"))
			Expect(proposals()).To(HaveLen(1), "it still waits")
		})

		It("is refused from another thread", func() {
			setup()
			gate.set(studio.CapTriggersPropose, true)
			gate.set(studio.CapProposalsConfirm, true)
			p := propose()
			Expect(r.conns.Ingest(ctx, "chat-addon", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{
				Chat: chatID, Thread: "T2", Mention: true, MessageID: "x1", SenderID: "11", Text: "confirm " + p.Route.Code}})).To(Succeed())
			waitFor("the refusal", func() bool { return len(fake.texts(chatID, "T2")) == 1 })
			Expect(fake.texts(chatID, "T2")[0]).To(ContainSubstring("same place"))
			Expect(proposals()).To(HaveLen(1))
		})

		It("with an unknown code is an ordinary message for the agent", func() {
			setup()
			gate.set(studio.CapTriggersPropose, true)
			gate.set(studio.CapProposalsConfirm, true)
			propose()
			chats, _ := r.svc.List(ctx, alice, 10)
			before := len(r.h.Sent(chats[0].ID))
			say("confirm ZZZZ")
			waitFor("the agent", func() bool { return len(r.h.Sent(chats[0].ID)) == before+1 })
			Expect(proposals()).To(HaveLen(1))
		})

		It("is audited without any text of the prompt", func() {
			setup()
			gate.set(studio.CapTriggersPropose, true)
			gate.set(studio.CapProposalsConfirm, true)
			p := propose()
			n := len(fake.texts(chatID, thread))
			say("confirm " + p.Route.Code)
			answered(n + 1)
			evs, err := r.svc.Changes().Events(ctx, alice, 20)
			Expect(err).NotTo(HaveOccurred())
			var actions []string
			for _, e := range evs {
				if e.Kind == "trigger" {
					actions = append(actions, e.Action)
					Expect(e.Hash).NotTo(BeEmpty())
					Expect(e.Detail).NotTo(ContainSubstring("RUN do the thing"))
					Expect(e.Source.ID).To(Equal(b.ID))
				}
			}
			Expect(actions).To(ContainElements("propose", "confirm"))
		})
	})

	Describe("the report of a run", func() {
		// confirmed makes a confirmed trigger that reports as asked, and
		// returns it. The clock stands before its first due time.
		confirmed := func(name, delivery string) triggers.Trigger {
			args = proposal(name, delivery)
			p := propose()
			t, err := r.host.ConfirmProposal(ctx, alice, p.ID, "UTC")
			Expect(err).NotTo(HaveOccurred())
			return t
		}
		runOnce := func(t triggers.Trigger) triggers.Run {
			r.sh.clock.Advance(25 * time.Hour)
			r.pass()
			return r.waitEnded(t.ID)
		}
		reports := func() []string {
			var out []string
			for _, s := range fake.texts(chatID, thread) {
				if strings.HasPrefix(s, "Digest\n\n") {
					out = append(out, s)
				}
			}
			return out
		}

		It("is written back to the chat and thread of the proposal", func() {
			setup()
			gate.set(studio.CapTriggersPropose, true)
			gate.set(studio.CapTriggersRun, true)
			t := confirmed("Digest", origin)
			run := runOnce(t)
			Expect(run.Status).To(Equal(triggers.StatusDone))
			waitFor("the report", func() bool { return len(reports()) == 1 })
			Expect(reports()[0]).To(Equal("Digest\n\nresult is 42"))
			Expect(r.notes()).To(BeEmpty(), "a report that reached the chat is not repeated in the Inbox")
		})

		It("goes to the Inbox by default", func() {
			setup()
			gate.set(studio.CapTriggersPropose, true)
			gate.set(studio.CapTriggersRun, true)
			t := confirmed("Digest", "")
			runOnce(t)
			waitFor("the notice", func() bool { return len(r.notes()) == 1 })
			Expect(reports()).To(BeEmpty())
		})

		It("says nothing for a silent run, and logs it", func() {
			setup()
			gate.set(studio.CapTriggersPropose, true)
			gate.set(studio.CapTriggersRun, true)
			args = `{"name":"Digest","prompt":"RUN-SILENT","when":"daily 08:00","time_zone":"UTC","delivery":{"target":"origin"}}`
			p := propose()
			t, err := r.host.ConfirmProposal(ctx, alice, p.ID, "UTC")
			Expect(err).NotTo(HaveOccurred())
			run := runOnce(t)
			Expect(run.Status).To(Equal(triggers.StatusDone))
			time.Sleep(100 * time.Millisecond)
			Expect(reports()).To(BeEmpty())
			Expect(r.notes()).To(BeEmpty())
		})

		It("breaks mentions of everyone and cuts a long text", func() {
			setup()
			gate.set(studio.CapTriggersPropose, true)
			gate.set(studio.CapTriggersRun, true)
			args = `{"name":"Digest","prompt":"RUN-FAIL","when":"daily 08:00","time_zone":"UTC","delivery":{"target":"origin"}}`
			p := propose()
			t, _ := r.host.ConfirmProposal(ctx, alice, p.ID, "UTC")
			runOnce(t)
			waitFor("the report", func() bool { return len(reports()) == 1 })
			Expect(reports()[0]).NotTo(ContainSubstring("@channel"))
			Expect(reports()[0]).To(ContainSubstring("done: 42"))
		})

		It("cuts a long answer", func() {
			setup()
			gate.set(studio.CapTriggersPropose, true)
			gate.set(studio.CapTriggersRun, true)
			args = `{"name":"Digest","prompt":"RUN-LONG","when":"daily 08:00","time_zone":"UTC","delivery":{"target":"origin"}}`
			p := propose()
			t, _ := r.host.ConfirmProposal(ctx, alice, p.ID, "UTC")
			runOnce(t)
			waitFor("the report", func() bool { return len(reports()) == 1 })
			Expect([]rune(reports()[0])).To(HaveLen(len("Digest\n\n") + studio.MaxReportRunes + 1))
		})

		It("on_problem stays silent for a good run", func() {
			setup()
			gate.set(studio.CapTriggersPropose, true)
			gate.set(studio.CapTriggersRun, true)
			t := confirmed("Digest", `,"delivery":{"target":"origin","mode":"on_problem"}`)
			runOnce(t)
			time.Sleep(100 * time.Millisecond)
			Expect(reports()).To(BeEmpty())
			Expect(r.notes()).To(BeEmpty())
		})

		It("on_change reports the first run and then only a different answer", func() {
			setup()
			gate.set(studio.CapTriggersPropose, true)
			gate.set(studio.CapTriggersRun, true)
			t := confirmed("Digest", `,"delivery":{"target":"origin","mode":"on_change"}`)
			runOnce(t)
			waitFor("the first report", func() bool { return len(reports()) == 1 })
			runOnce(t)
			time.Sleep(150 * time.Millisecond)
			Expect(reports()).To(HaveLen(1), "same answer, nothing new")
		})

		It("falls back to the Inbox when the binding is off", func() {
			setup()
			gate.set(studio.CapTriggersPropose, true)
			gate.set(studio.CapTriggersRun, true)
			t := confirmed("Digest", origin)
			_, err := r.conns.SetConnectorEnabled(ctx, alice, b.ID, false)
			Expect(err).NotTo(HaveOccurred())
			runOnce(t)
			waitFor("the notice", func() bool { return len(r.notes()) == 1 })
			Expect(reports()).To(BeEmpty())
			Expect(r.notes()[0].Kind).To(Equal(notify.KindRunEnded))
		})

		It("pauses with the capability off, keeps the record, and resumes without replay", func() {
			setup()
			gate.set(studio.CapTriggersPropose, true)
			gate.set(studio.CapTriggersRun, false)
			t := confirmed("Digest", origin)
			r.sh.clock.Advance(25 * time.Hour)
			r.pass()
			run := r.waitEnded(t.ID)
			Expect(run.Status).To(Equal(triggers.StatusSkipped))
			Expect(run.Reason).To(Equal("paused: capability off"))
			got, err := r.host.GetTrigger(ctx, alice, t.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Enabled).To(BeTrue(), "the record is unchanged")
			Expect(r.notes()).To(BeEmpty(), "a pause is not a failure")
			// Three more days pass while it is off: nothing is replayed once it is on.
			r.sh.clock.Advance(72 * time.Hour)
			gate.set(studio.CapTriggersRun, true)
			r.pass()
			runs, _ := r.host.TriggerRuns(ctx, alice, t.ID, 10)
			ran := 0
			for _, x := range runs {
				if x.Status == triggers.StatusDone || x.Status == triggers.StatusRunning {
					ran++
				}
			}
			Expect(ran).To(BeNumerically("<=", 1), "runs = %+v", runs)
		})

		It("alerts once after three failures in a row and not again within six hours", func() {
			setup()
			gate.set(studio.CapTriggersPropose, true)
			gate.set(studio.CapTriggersRun, true)
			args = `{"name":"Digest","prompt":"RUN-ERR","when":"0 * * * *","time_zone":"UTC","delivery":{"target":"origin"}}`
			p := propose()
			t, err := r.host.ConfirmProposal(ctx, alice, p.ID, "UTC")
			Expect(err).NotTo(HaveOccurred())
			alerts := func() int {
				n := 0
				for _, s := range fake.texts(chatID, thread) {
					if strings.Contains(s, "failed 3 times in a row") {
						n++
					}
				}
				return n
			}
			failRun := func(advance time.Duration, want int) {
				r.sh.clock.Advance(advance)
				r.pass()
				until("the run to fail", func() bool {
					runs, _ := r.host.TriggerRuns(ctx, alice, t.ID, 20)
					failed := 0
					for _, x := range runs {
						if x.Status == triggers.StatusFailed {
							failed++
						}
					}
					return failed >= want
				})
			}
			failRun(61*time.Minute, 1)
			failRun(60*time.Minute, 2)
			Expect(alerts()).To(Equal(0))
			failRun(60*time.Minute, 3)
			waitFor("the alert", func() bool { return alerts() == 1 })
			failRun(60*time.Minute, 4)
			time.Sleep(150 * time.Millisecond)
			Expect(alerts()).To(Equal(1), "throttled")
			failRun(6*time.Hour, 5)
			waitFor("the second alert", func() bool { return alerts() == 2 })
		})
	})

	Describe("the outbox", func() {
		It("sends 20 messages a minute for a binding and queues the rest in order", func() {
			setup()
			for i := 0; i < 25; i++ {
				_, err := r.conns.Post(ctx, studio.Post{Binding: b.ID, Chat: "Q", Text: "m" + string(rune('A'+i))})
				Expect(err).NotTo(HaveOccurred())
			}
			Expect(fake.texts("Q", "")).To(HaveLen(20))
			Expect(r.conns.Queued(b.ID)).To(Equal(5))
			r.sh.clock.Advance(61 * time.Second)
			waitFor("the queue to drain", func() bool { return len(fake.texts("Q", "")) == 25 })
			got := fake.texts("Q", "")
			Expect(got[20:]).To(Equal([]string{"mU", "mV", "mW", "mX", "mY"}))
			Expect(r.conns.Queued(b.ID)).To(Equal(0))
		})

		It("refuses a binding that is off", func() {
			setup()
			_, err := r.conns.SetConnectorEnabled(ctx, alice, b.ID, false)
			Expect(err).NotTo(HaveOccurred())
			_, err = r.conns.Post(ctx, studio.Post{Binding: b.ID, Chat: "Q", Text: "x"})
			Expect(err).To(MatchError(studio.ErrBindingOff))
		})
	})
})
