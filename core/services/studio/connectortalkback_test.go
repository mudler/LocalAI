package studio_test

import (
	"context"
	"encoding/json"
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

// The agent of a connector chat writes to a chat of the platform with
// send_message. The kind in these specs is a made-up platform; nothing here
// depends on which add-on serves it.
var _ = Describe("talk-back from a connector chat", func() {
	ctx := context.Background()
	const (
		chatID  = "C1"
		thread  = "T1"
		general = "G1"
		ops     = "G2"
	)
	var (
		fake *postAddon
		gate *capSwitch
		r    *connRig
		b    connectors.Binding
		args string // the arguments of the next send_message
		seq  int
	)
	script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
		switch {
		case text == "send":
			return []studiotest.ToolStep{{Tool: "send_message", Args: args}, {Say: "done"}}
		case text == "propose":
			return []studiotest.ToolStep{{Tool: "propose_trigger", Args: args}, {Say: "proposed"}}
		case strings.Contains(text, "RUN"):
			return []studiotest.ToolStep{{Say: "result is 42"}}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
	channels := []connectors.Channel{{ID: general, Label: "General", Approved: true}, {ID: ops, Label: "Ops"}}
	setup := func(opt func(*studio.ConnectorOptions)) {
		fake = &postAddon{}
		gate = &capSwitch{}
		tr := studiobot.NewTransport(studiobot.Addon{ID: "chat-addon", Kind: contract.ConnectorKind{ID: "chatty", Title: "Chatty", Delivery: contract.DeliveryPull}, Runtime: fake})
		r = newConnRigOpts(newShared(), connStore(), tr, script, nil, func(o *studio.ConnectorOptions) {
			o.OutboxPoll = 10 * time.Millisecond
			if opt != nil {
				opt(o)
			}
		})
		r.svc.SetCapabilityGate(gate)
		b = r.bind(studio.ConnectorInput{Kind: "chatty", Token: "tok", Allow: []string{"11"}, Channels: channels})
		seq = 0
	}
	update := func(f func(*studio.ConnectorInput)) {
		in := studio.ConnectorInput{Name: b.Name, AgentID: b.AgentID, Enabled: true, Allow: b.Allow}
		f(&in)
		_, err := r.conns.UpdateConnector(ctx, alice, b.ID, in)
		Expect(err).NotTo(HaveOccurred())
	}
	say := func(text string) {
		seq++
		Expect(r.conns.Ingest(ctx, "chat-addon", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{
			Chat: chatID, Thread: thread, Mention: true, MessageID: "m" + string(rune('a'+seq)), SenderID: "11", SenderName: "Cy", Text: text}})).To(Succeed())
	}
	// answers counts the replies of the chat. The working message goes out
	// when a turn starts, so it is not an answer: counting it would end a
	// wait before the tools of the turn have run.
	answers := func() int {
		n := 0
		for _, t := range fake.texts(chatID, thread) {
			if t != "Working…" {
				n++
			}
		}
		return n
	}
	// turn runs a turn and waits for its answer.
	turn := func(text string) {
		before := answers()
		say(text)
		waitFor("the answer", func() bool { return answers() > before })
	}
	chatTools := func() []string {
		l, err := r.svc.List(ctx, alice, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(l).NotTo(BeEmpty())
		return r.h.ToolNames(l[0].ID)
	}
	lastSend := func() studiotest.ToolResult {
		res := r.h.ResultsFor("send_message")
		Expect(res).NotTo(BeEmpty())
		return res[len(res)-1]
	}
	recent := func() []studio.AccessEvent {
		v, err := r.conns.Access(ctx, alice, b.ID)
		Expect(err).NotTo(HaveOccurred())
		return v.Recent
	}

	Describe("the tool", func() {
		It("is offered when the sender holds talkback.channels", func() {
			setup(nil)
			turn("hello")
			Expect(chatTools()).To(ContainElement("send_message"))
		})

		It("is absent when the owner did not enable the capability for the bot", func() {
			setup(nil)
			update(func(in *studio.ConnectorInput) { in.Caps = []string{contract.CapMemoryWrite} })
			turn("hello")
			Expect(chatTools()).NotTo(ContainElement("send_message"))
		})

		It("is offered to a sender on the allowlist who is not paired", func() {
			setup(nil)
			bb, _ := r.store.GetBinding(ctx, b.ID)
			bb.Paired = nil
			Expect(r.store.PutBinding(ctx, bb)).To(Succeed())
			turn("hello")
			Expect(chatTools()).To(ContainElement("send_message"))
		})

		It("is absent when the sender only came in through a listed channel and is not paired", func() {
			setup(nil)
			bb, _ := r.store.GetBinding(ctx, b.ID)
			bb.Paired = nil
			bb.Allow = []string{chatID}
			Expect(r.store.PutBinding(ctx, bb)).To(Succeed())
			turn("hello")
			Expect(chatTools()).NotTo(ContainElement("send_message"))
			l, _ := r.svc.List(ctx, alice, 10)
			prompt := strings.Join(r.h.SentRaw(l[0].ID), "\n")
			Expect(prompt).To(ContainSubstring("This bot can write to other channels only for people the owner added by name or paired"), "the model is told why")
			Expect(prompt).To(ContainSubstring("never say it does not exist"))
		})

		It("is absent when the server switch is off", func() {
			setup(nil)
			r.conns.AddCapabilitySwitch(studio.TalkbackSwitch("off"))
			turn("hello")
			Expect(chatTools()).NotTo(ContainElement("send_message"))
		})

		It("is absent in a chat that no connector started", func() {
			setup(nil)
			c, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "m"})
			Expect(err).NotTo(HaveOccurred())
			Expect(r.h.ToolNames(c.ID)).NotTo(ContainElement("send_message"))
		})

		It("is absent for the run of a trigger", func() {
			setup(nil)
			gate.set(studio.CapTriggersRun, true)
			tr := r.schedule(studio.TriggerInput{AgentID: r.agent.ID, Prompt: "RUN go"})
			r.due()
			run := r.waitEnded(tr.ID)
			Expect(r.h.ToolNames(run.ChatID)).NotTo(ContainElement("send_message"))
		})
	})

	Describe("the description of the tool", func() {
		It("lists the approved channels by name and ID, and says where each target writes", func() {
			setup(nil)
			turn("hello")
			d := r.h.LastToolDescription("send_message")
			Expect(d).To(ContainSubstring("General (" + general + ")"))
			Expect(d).NotTo(ContainSubstring("Ops"), "a channel the owner did not approve is not offered")
			Expect(d).To(ContainSubstring("not in this thread"))
		})

		It("says so in one sentence when no channel is approved", func() {
			setup(nil)
			update(func(in *studio.ConnectorInput) { in.Channels = []connectors.Channel{{ID: ops, Label: "Ops"}} })
			turn("hello")
			d := r.h.LastToolDescription("send_message")
			Expect(d).To(ContainSubstring("approved no channel"))
			Expect(d).NotTo(ContainSubstring("Ops"))
		})
	})

	Describe("a message", func() {
		It("to a channel named by its label, with a # or another case, goes to its ID", func() {
			setup(nil)
			args = `{"text":"by name","channel":"#general"}`
			turn("send")
			waitFor("the message", func() bool { return len(fake.texts(general, "")) == 1 })
			args = `{"text":"by name again","channel":"GENERAL"}`
			turn("send")
			waitFor("the message", func() bool { return len(fake.texts(general, "")) == 2 })
		})

		It("without channel goes to the conversation chat and thread", func() {
			setup(nil)
			args = `{"text":"hello there"}`
			turn("send")
			waitFor("the message", func() bool { return len(fake.texts(chatID, thread)) >= 2 })
			Expect(fake.texts(chatID, thread)).To(ContainElement("hello there"))
			Expect(lastSend().Out).To(Equal("Sent."))
		})

		It("to an approved channel is a top-level message there", func() {
			setup(nil)
			args = `{"text":"news for @channel and <!here>","channel":"` + general + `"}`
			turn("send")
			waitFor("the message", func() bool { return len(fake.texts(general, "")) == 1 })
			got := fake.texts(general, "")[0]
			Expect(got).NotTo(MatchRegexp(`(@|<!)(channel|here)`), "mentions of everyone are broken")
			Expect(got).To(ContainSubstring("news for"))
		})

		It("to a thread of an approved channel carries the thread", func() {
			setup(nil)
			args = `{"text":"in thread","channel":"` + general + `","thread":"177.1"}`
			turn("send")
			waitFor("the message", func() bool { return len(fake.texts(general, "177.1")) == 1 })
		})

		It("to a channel that is not listed is refused with the list", func() {
			setup(nil)
			args = `{"text":"x","channel":"SECRET"}`
			turn("send")
			res := lastSend()
			Expect(res.Err).To(MatchError(ContainSubstring("not a channel the owner listed")))
			Expect(res.Err).To(MatchError(ContainSubstring(general)))
			Expect(fake.texts("SECRET", "")).To(BeEmpty())
		})

		It("to a listed channel the owner did not approve needs a paired sender and the grant of the agent", func() {
			setup(nil)
			// The sensitive grant is never implied: the agent must hold it.
			ag := r.agent
			ag.Builtins = []string{contract.CapTalkbackChannels, contract.CapTalkbackNewChan}
			saved, err := r.svc.SaveAgent(ctx, alice, ag)
			Expect(err).NotTo(HaveOccurred())
			r.agent = saved
			args = `{"text":"first","channel":"` + ops + `"}`
			turn("send")
			waitFor("the message", func() bool { return len(fake.texts(ops, "")) == 1 })
			evs := recent()
			var found bool
			for _, e := range evs {
				if e.Action == studio.SendNew {
					found = true
					Expect(e.Capability).To(Equal(contract.CapTalkbackNewChan))
				}
			}
			Expect(found).To(BeTrue())
		})

		It("to an unapproved channel is refused when new_channel is not enabled", func() {
			setup(nil)
			update(func(in *studio.ConnectorInput) {
				in.Caps = []string{contract.CapTalkbackChannels}
			})
			args = `{"text":"first","channel":"` + ops + `"}`
			turn("send")
			Expect(lastSend().Err).To(MatchError(ContainSubstring("not permitted")))
			Expect(fake.texts(ops, "")).To(BeEmpty())
			// The approved channel still works.
			args = `{"text":"fine","channel":"` + general + `"}`
			turn("send")
			waitFor("the message", func() bool { return len(fake.texts(general, "")) == 1 })
		})

		It("can not be sent when it is empty or over 4000 characters", func() {
			setup(nil)
			args = `{"text":"   "}`
			turn("send")
			Expect(lastSend().Err).To(MatchError(studio.ErrMessageEmpty))
			args = `{"text":"` + strings.Repeat("x", 4001) + `"}`
			turn("send")
			Expect(lastSend().Err).To(MatchError(studio.ErrMessageTooLong))
		})

		It("is queued by the rate limit of the binding, and goes when the minute allows", func() {
			setup(nil)
			for i := 0; i < 20; i++ {
				q, err := r.conns.Send(ctx, studio.Outgoing{Binding: b.ID, Chat: general, Text: "m", Kind: studio.SendChannel})
				Expect(err).NotTo(HaveOccurred())
				Expect(q).To(BeFalse())
			}
			q, err := r.conns.Send(ctx, studio.Outgoing{Binding: b.ID, Chat: general, Text: "late", Kind: studio.SendChannel})
			Expect(err).NotTo(HaveOccurred())
			Expect(q).To(BeTrue())
			Expect(r.conns.Queued(b.ID)).To(Equal(1))
			r.sh.clock.Advance(61 * time.Second)
			waitFor("the queue", func() bool { return r.conns.Queued(b.ID) == 0 && len(fake.texts(general, "")) == 21 })
		})

		It("is refused when the binding is off", func() {
			setup(nil)
			_, err := r.conns.SetConnectorEnabled(ctx, alice, b.ID, false)
			Expect(err).NotTo(HaveOccurred())
			_, err = r.conns.Send(ctx, studio.Outgoing{Binding: b.ID, Chat: general, Text: "x", Kind: studio.SendChannel})
			Expect(err).To(MatchError(studio.ErrBindingOff))
		})
	})

	Describe("the audit", func() {
		It("has a row for each attempt with the length and a hash, and never the text", func() {
			setup(nil)
			args = `{"text":"a secret plan","channel":"` + general + `"}`
			turn("send")
			waitFor("the message", func() bool { return len(fake.texts(general, "")) == 1 })
			var row *studio.AccessEvent
			for _, e := range recent() {
				if e.Action == studio.SendChannel {
					row = &e
				}
			}
			Expect(row).NotTo(BeNil())
			Expect(row.Channel).To(Equal(general))
			Expect(row.Length).To(Equal(len("a secret plan")))
			Expect(row.Hash).To(HaveLen(64))
			Expect(row.Allowed).To(BeTrue())
			Expect(row.Senders).To(ContainElement("11"))
			raw, _ := json.Marshal(recent())
			Expect(string(raw)).NotTo(ContainSubstring("secret plan"))
			Expect(r.logs.String()).NotTo(ContainSubstring("secret plan"))
		})
	})

	Describe("the list of channels", func() {
		It("is set by the owner and validated", func() {
			setup(nil)
			got, err := r.conns.GetConnector(ctx, alice, b.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Channels).To(Equal(channels))
			_, err = r.conns.UpdateConnector(ctx, alice, b.ID, studio.ConnectorInput{Name: "x", AgentID: b.AgentID, Enabled: true, Allow: b.Allow,
				Channels: []connectors.Channel{{ID: "a b"}}})
			Expect(err).To(MatchError(connectors.ErrInvalid))
			_, err = r.conns.UpdateConnector(ctx, alice, b.ID, studio.ConnectorInput{Name: "x", AgentID: b.AgentID, Enabled: true, Allow: b.Allow,
				Channels: []connectors.Channel{{ID: "a"}, {ID: "a"}}})
			Expect(err).To(MatchError(connectors.ErrInvalid))
			many := make([]connectors.Channel, 11)
			for i := range many {
				many[i] = connectors.Channel{ID: string(rune('a' + i))}
			}
			_, err = r.conns.UpdateConnector(ctx, alice, b.ID, studio.ConnectorInput{Name: "x", AgentID: b.AgentID, Enabled: true, Allow: b.Allow, Channels: many})
			Expect(err).To(MatchError(connectors.ErrInvalid))
		})

		It("keeps when an update sends none, and clears with an empty list", func() {
			setup(nil)
			update(func(in *studio.ConnectorInput) {})
			got, _ := r.conns.GetConnector(ctx, alice, b.ID)
			Expect(got.Channels).To(HaveLen(2))
			update(func(in *studio.ConnectorInput) { in.Channels = []connectors.Channel{} })
			got, _ = r.conns.GetConnector(ctx, alice, b.ID)
			Expect(got.Channels).To(BeEmpty())
		})

		It("can not be changed by the model, whatever a fetched page says", func() {
			setup(nil)
			// The only tool that names a channel takes one that is listed; an
			// unknown ID is refused and the list does not change.
			args = `{"text":"x","channel":"NEW-ONE"}`
			turn("send")
			Expect(lastSend().Err).To(HaveOccurred())
			got, _ := r.conns.GetConnector(ctx, alice, b.ID)
			Expect(got.Channels).To(Equal(channels))
		})
	})

	Describe("the report of a trigger to a channel", func() {
		toChannel := func(id string) string {
			return `{"name":"Digest","prompt":"RUN do it","when":"daily 08:00","time_zone":"UTC","delivery":{"target":"channel","channel":"` + id + `"}}`
		}
		confirmed := func(a string) triggers.Trigger {
			args = a
			turn("propose")
			ps, err := r.host.Proposals(ctx, alice)
			Expect(err).NotTo(HaveOccurred())
			Expect(ps).NotTo(BeEmpty())
			t, err := r.host.ConfirmProposal(ctx, alice, ps[len(ps)-1].ID, "UTC")
			Expect(err).NotTo(HaveOccurred())
			return t
		}
		runOnce := func(t triggers.Trigger) {
			r.sh.clock.Advance(25 * time.Hour)
			r.pass()
			r.waitEnded(t.ID)
		}
		BeforeEach(func() {
			setup(func(o *studio.ConnectorOptions) { o.RatePerMinute = 100 })
			gate.set(studio.CapTriggersPropose, true)
			gate.set(studio.CapTriggersRun, true)
			gate.set(contract.CapTalkbackChannels, true)
		})

		It("is written in the approved channel, with the same text rules", func() {
			t := confirmed(toChannel(general))
			Expect(t.Route.Target).To(Equal(triggers.TargetChannel))
			Expect(t.Route.Channel).To(Equal(general))
			runOnce(t)
			waitFor("the report", func() bool { return len(fake.texts(general, "")) == 1 })
			Expect(fake.texts(general, "")[0]).To(Equal("Digest\n\nresult is 42"))
			Expect(r.notes()).To(BeEmpty())
			// The text reaches the fake before the access row is written, so
			// wait for the row instead of reading it once.
			var row studio.AccessEvent
			waitFor("the delivery row", func() bool {
				for _, e := range recent() {
					if e.Action == studio.SendDelivery {
						row = e
						return true
					}
				}
				return false
			})
			Expect(row.Hash).NotTo(BeEmpty())
		})

		It("is refused at proposal time for a channel that is not listed or not approved", func() {
			args = toChannel("SECRET")
			turn("propose")
			err := r.h.ResultsFor("propose_trigger")[0].Err
			Expect(err).To(MatchError(ContainSubstring("not a channel the owner approved")))
			Expect(err).To(MatchError(ContainSubstring("General (" + general + ")")), "the approved channels are listed")
			args = toChannel(ops)
			turn("propose")
			err = r.h.ResultsFor("propose_trigger")[1].Err
			Expect(err).To(MatchError(ContainSubstring("not a channel the owner approved")))
			Expect(err).NotTo(MatchError(ContainSubstring("Ops")), "an unapproved channel is not offered")
			args = toChannel("Ops")
			turn("propose")
			Expect(r.h.ResultsFor("propose_trigger")[2].Err).To(HaveOccurred(), "the label of an unapproved channel is refused too")
			ps, _ := r.host.Proposals(ctx, alice)
			Expect(ps).To(BeEmpty())
		})

		It("names the approved channels in the description of propose_trigger", func() {
			turn("hello")
			d := r.h.LastToolDescription("propose_trigger")
			Expect(d).To(ContainSubstring("General (" + general + ")"))
			Expect(d).NotTo(ContainSubstring("Ops"))
			Expect(d).To(ContainSubstring("posts in the channel itself, at the top and not in a thread; origin posts in this thread"))
		})

		It("says in one sentence that channels are off when the sender may not write in them", func() {
			update(func(in *studio.ConnectorInput) { in.Caps = []string{studio.CapTriggersPropose} })
			turn("hello")
			d := r.h.LastToolDescription("propose_trigger")
			Expect(d).To(ContainSubstring("may not write in channels"))
			Expect(d).NotTo(ContainSubstring("General"))
		})

		for _, name := range []string{"General", "#general", "GENERAL", general} {
			It("takes the channel "+name+" and keeps its ID", func() {
				t := confirmed(toChannel(name))
				Expect(t.Route.Channel).To(Equal(general))
			})
		}

		It("writes in the proposal where the results go, by the label of the channel", func() {
			args = toChannel("#general")
			turn("propose")
			waitFor("the proposal text", func() bool {
				for _, t := range fake.texts(chatID, thread) {
					if strings.Contains(t, "Results go to General, at the top of the channel") {
						return true
					}
				}
				return false
			})
			args = `{"name":"Here","prompt":"RUN it","when":"daily 09:00","time_zone":"UTC","delivery":{"target":"origin"}}`
			turn("propose")
			waitFor("the proposal text", func() bool {
				for _, t := range fake.texts(chatID, thread) {
					if strings.Contains(t, "Results go to this thread") {
						return true
					}
				}
				return false
			})
		})

		It("posts the report at the top of the channel and never in the thread of the chat", func() {
			t := confirmed(toChannel("General"))
			runOnce(t)
			waitFor("the report", func() bool { return len(fake.texts(general, "")) == 1 })
			Expect(fake.texts(general, thread)).To(BeEmpty())
			for _, text := range fake.texts(chatID, thread) {
				Expect(text).NotTo(ContainSubstring("result is 42"))
			}
			Expect(r.host.DeliveryOf(ctx, t)).To(Equal(studio.DeliveryView{Target: triggers.TargetChannel, ChannelLabel: "General"}))
		})

		It("goes to the Inbox when the capability is off at delivery time", func() {
			t := confirmed(toChannel(general))
			gate.set(contract.CapTalkbackChannels, false)
			runOnce(t)
			waitFor("the notice", func() bool { return len(r.notes()) == 1 })
			Expect(fake.texts(general, "")).To(BeEmpty())
		})

		It("goes to the Inbox when the owner took the channel off the list", func() {
			t := confirmed(toChannel(general))
			update(func(in *studio.ConnectorInput) { in.Channels = []connectors.Channel{{ID: ops}} })
			runOnce(t)
			waitFor("the notice", func() bool { return len(r.notes()) == 1 })
			Expect(fake.texts(general, "")).To(BeEmpty())
		})
	})
})
