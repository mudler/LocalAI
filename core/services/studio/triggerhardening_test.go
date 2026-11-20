package studio_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
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

// The delivery of a trigger from a chat in Studio to a connection is checked
// again at each step that can change what is true: confirming, delivering, and
// the chats that must never reach it.
var _ = Describe("a trigger that reports to a connection, hardening", func() {
	ctx := context.Background()
	var (
		r     *connRig
		c     *callScript
		gate  *capSwitch
		fake  *postAddon
		chat  string
		chatB connectors.Binding

		mu     sync.Mutex
		answer string
		fail   bool
	)
	channels := []connectors.Channel{{ID: "G1", Label: "General", Approved: true}}
	script := func(a workspace.Actor, text string) []studiotest.ToolStep {
		switch {
		case strings.Contains(text, "RUN"):
			mu.Lock()
			defer mu.Unlock()
			if fail {
				return []studiotest.ToolStep{{Err: fmt.Errorf("the model broke")}}
			}
			return []studiotest.ToolStep{{Say: answer}}
		case text == "go":
			return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"child job","wait":true}`}, {Say: "done"}}
		}
		return c.script(a, text)
	}
	setup := func() {
		c = &callScript{}
		fake = &postAddon{}
		mu.Lock()
		answer, fail = "answer A", false
		mu.Unlock()
		tp := studiobot.NewTransport(
			studiobot.Addon{ID: "chat-addon", Kind: contract.ConnectorKind{ID: "chatty", Title: "Chatty", Delivery: contract.DeliveryPull}, Runtime: fake},
			studiobot.Addon{ID: "service-addon", Kind: contract.ConnectorKind{ID: "servicey", Title: "Servicey", Delivery: contract.DeliveryPull, ToolOnly: true}, Runtime: &postAddon{}},
		)
		r = newConnRigOpts(newShared(), connStore(), tp, script, nil, func(o *studio.ConnectorOptions) {
			o.OutboxPoll = 10 * time.Millisecond
			o.RatePerMinute = 100
		})
		gate = &capSwitch{}
		r.svc.SetCapabilityGate(gate)
		gate.set(contract.CapTalkbackChannels, true)
		chatB = r.bind(studio.ConnectorInput{Kind: "chatty", Name: "Team chat", Token: "secret-token-1", Allow: []string{"11"}, Channels: channels})
		chat = create(r.svc, alice)
	}
	proposeTo := func(mode string) triggers.Trigger {
		GinkgoHelper()
		d := fmt.Sprintf(`{"target":"channel","connection":%q,"channel":"G1"`, chatB.ID)
		if mode != "" {
			d += fmt.Sprintf(`,"mode":%q`, mode)
		}
		res := c.call(r.trigRig, chat, "propose_trigger", `{"name":"Digest","prompt":"RUN go","when":"daily 08:00","time_zone":"UTC","delivery":`+d+`}}`)
		Expect(res.Err).To(BeNil(), "%+v", res)
		p, err := r.host.Proposals(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(p).NotTo(BeEmpty())
		return p[len(p)-1]
	}
	confirmed := func(mode string) triggers.Trigger {
		GinkgoHelper()
		t, err := r.host.ConfirmProposal(ctx, alice, proposeTo(mode).ID, "UTC")
		Expect(err).NotTo(HaveOccurred())
		return t
	}
	edit := func(f func(b *connectors.Binding)) {
		GinkgoHelper()
		b, err := r.store.GetBinding(ctx, chatB.ID)
		Expect(err).NotTo(HaveOccurred())
		f(&b)
		Expect(r.store.PutBinding(ctx, b)).To(Succeed())
	}
	// runNth makes the nth run of the trigger and waits until it ended.
	runNth := func(t triggers.Trigger, n int) triggers.Run {
		GinkgoHelper()
		r.sh.clock.Advance(25 * time.Hour)
		r.pass()
		var run triggers.Run
		until("the run to end", func() bool {
			runs, _ := r.host.TriggerRuns(ctx, alice, t.ID, 0)
			if len(runs) != n || !runs[0].Status.Final() {
				return false
			}
			run = runs[0]
			return true
		})
		return run
	}
	posts := func() int { return len(fake.texts("G1", "")) }

	Describe("delivery after the person confirmed", func() {
		fallsBack := func(change func()) {
			GinkgoHelper()
			setup()
			t := confirmed("")
			change()
			runNth(t, 1)
			waitFor("the notice in the Inbox", func() bool { return len(r.notes()) == 1 })
			Expect(r.notes()[0].Kind).To(Equal(notify.KindRunEnded))
			Consistently(posts, "300ms", "50ms").Should(BeZero(), "nothing may reach the channel")
		}
		It("goes to the Inbox when the connection was switched off", func() {
			fallsBack(func() { edit(func(b *connectors.Binding) { b.Enabled = false }) })
		})
		It("goes to the Inbox when the connection belongs to another owner", func() {
			setup()
			agB, err := r.svc.SaveAgent(ctx, "bob", agents.Agent{Name: "Other", Model: "m"})
			Expect(err).NotTo(HaveOccurred())
			ob, err := r.conns.CreateConnector(ctx, "bob", studio.ConnectorInput{Kind: "chatty", Name: "Not yours", Token: "secret-token-3", AgentID: agB.ID, Enabled: true,
				Allow: []string{"11"}, Channels: []connectors.Channel{{ID: "B1", Label: "Bob room", Approved: true}}, ConfirmDuplicate: studio.DuplicateConfirmation, Reply: connectors.ReplyOff})
			Expect(err).NotTo(HaveOccurred())
			// A route that names a connection of another owner cannot come from the
			// tools; the stored route is what delivery must still distrust.
			t := r.schedule(studio.TriggerInput{Name: "Digest", Prompt: "RUN go",
				Route: triggers.Route{Target: triggers.TargetChannel, Connection: ob.ID, Channel: "B1"}})
			runNth(t, 1)
			waitFor("the notice in the Inbox", func() bool { return len(r.notes()) == 1 })
			Consistently(func() int { return len(fake.texts("B1", "")) }, "300ms", "50ms").Should(BeZero(), "nothing may reach another owner's channel")
		})
		It("goes to the Inbox when the kind of the connection can no longer receive reports", func() {
			fallsBack(func() {
				// The connection now runs on the add-on of a service that tools use.
				edit(func(b *connectors.Binding) { b.Kind, b.AddonID = "servicey", "service-addon" })
			})
		})
		It("reports in the channel while nothing changed", func() {
			setup()
			t := confirmed("")
			runNth(t, 1)
			waitFor("the report", func() bool { return posts() == 1 })
			Expect(r.notes()).To(BeEmpty())
		})
	})

	It("refuses to confirm when talk-back was switched off after the proposal", func() {
		setup()
		p := proposeTo("")
		gate.set(contract.CapTalkbackChannels, false)
		_, err := r.host.ConfirmProposal(ctx, alice, p.ID, "UTC")
		Expect(err).To(MatchError(ContainSubstring("write in channels")))
		props, _ := r.host.Proposals(ctx, alice)
		Expect(props).To(HaveLen(1), "the proposal still waits")
	})

	Describe("the mode of a web trigger, end to end", func() {
		It("always reports every run", func() {
			setup()
			t := confirmed("always")
			runNth(t, 1)
			waitFor("the first report", func() bool { return posts() == 1 })
			runNth(t, 2)
			waitFor("the second report", func() bool { return posts() == 2 })
		})
		It("on_change reports the first run, skips an equal answer and reports a different one", func() {
			setup()
			t := confirmed("on_change")
			runNth(t, 1)
			waitFor("the first report", func() bool { return posts() == 1 })
			runNth(t, 2)
			Consistently(posts, "300ms", "50ms").Should(Equal(1), "the same answer is not reported")
			mu.Lock()
			answer = "answer B"
			mu.Unlock()
			runNth(t, 3)
			waitFor("the changed answer", func() bool { return posts() == 2 })
			Expect(fake.texts("G1", "")[1]).To(ContainSubstring("answer B"))
		})
		// A failed run never goes to the channel: the host tells the Inbox, in
		// every mode. on_problem is the mode that keeps the channel and the Inbox
		// silent for a good run.
		It("on_problem is silent for a good run and tells the Inbox about a failed one", func() {
			setup()
			t := confirmed("on_problem")
			runNth(t, 1)
			Consistently(posts, "300ms", "50ms").Should(BeZero(), "a good run is silent")
			Expect(r.notes()).To(BeEmpty(), "a good run leaves no notice either")
			mu.Lock()
			fail = true
			mu.Unlock()
			run := runNth(t, 2)
			Expect(run.Status).NotTo(Equal(triggers.StatusDone))
			waitFor("the notice of the failure", func() bool { return len(r.notes()) == 1 })
			Expect(posts()).To(BeZero())
		})
	})

	Describe("list_connections", func() {
		It("is offered to a chat in Studio and not to a sub-agent or the run of a trigger", func() {
			setup()
			sent(r.svc, alice, chat, "go")
			idle(r.svc, alice, chat)
			Expect(r.h.ToolNames(chat)).To(ContainElement("list_connections"))
			kids := childrenOf(r.svc, alice, chat)
			Expect(kids).NotTo(BeEmpty(), "the sub-agent did not start")
			for _, k := range kids {
				Expect(r.h.ToolNames(k.ID)).NotTo(ContainElement("list_connections"), "a sub-agent has %v", r.h.ToolNames(k.ID))
			}
			t := r.schedule(studio.TriggerInput{Name: "Plain", Prompt: "RUN go"})
			run := runNth(t, 1)
			Expect(r.h.ToolNames(run.ChatID)).NotTo(ContainElement("list_connections"), "a run has %v", r.h.ToolNames(run.ChatID))
		})
	})

	Describe("a proposal from a chat in Studio next to a pending one from a messaging app", func() {
		It("is kept apart when the delivery differs", func() {
			setup()
			in := studio.TriggerInput{Name: "Digest", Prompt: "same prompt", Kind: triggers.KindSchedule, Schedule: "daily 08:00", TZ: "UTC", Model: "m"}
			fromApp := in
			fromApp.Route = triggers.Route{Binding: chatB.ID, Chat: "C1", Thread: "T1", Target: triggers.TargetOrigin}
			_, err := r.host.ProposeTrigger(ctx, alice, fromApp)
			Expect(err).NotTo(HaveOccurred())
			fromStudio := in
			fromStudio.Route = triggers.Route{Target: triggers.TargetChannel, Connection: chatB.ID, Channel: "G1"}
			got, err := r.host.ProposeTrigger(ctx, alice, fromStudio)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Route.Connection).To(Equal(chatB.ID), "the pending proposal of the app was handed back instead")
			props, err := r.host.Proposals(ctx, alice)
			Expect(err).NotTo(HaveOccurred())
			Expect(props).To(HaveLen(2))
		})
	})
})

// A chat of a messaging app never sees the connections of the owner and never
// sets where a trigger of Studio reports.
var _ = Describe("a chat of a messaging app and the connections of the owner", func() {
	ctx := context.Background()
	const (
		chatID = "C1"
		thread = "T1"
	)
	var (
		fake *postAddon
		r    *connRig
		b    connectors.Binding
		args map[string]string // tool arguments by the word that starts the turn
		seq  int
	)
	script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
		for _, tool := range []string{"list_connections", "propose_trigger", "update_trigger"} {
			if text == "ACT-"+tool {
				return []studiotest.ToolStep{{Tool: tool, Args: args[tool]}, {Say: "done"}}
			}
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
	setup := func() {
		fake = &postAddon{}
		args = map[string]string{"list_connections": `{}`}
		tr := studiobot.NewTransport(studiobot.Addon{ID: "chat-addon", Kind: contract.ConnectorKind{ID: "chatty", Title: "Chatty", Delivery: contract.DeliveryPull}, Runtime: fake})
		sh := newShared()
		sh.deps = func(d *studio.Deps) { d.Logger = slog.New(slog.NewTextHandler(&safeBuf{}, nil)) }
		r = newConnRigOpts(sh, connStore(), tr, script, nil, func(o *studio.ConnectorOptions) {
			o.OutboxPoll = 10 * time.Millisecond
			o.RatePerMinute = 100
		})
		r.svc.SetCapabilityGate(studio.CapabilityGateFunc(func(_ context.Context, q studio.CapabilityRequest) bool {
			switch q.Capability {
			case studio.CapTriggersPropose, studio.CapTriggersChange, contract.CapTalkbackChannels:
				return true
			}
			return false
		}))
		b = r.bind(studio.ConnectorInput{Kind: "chatty", Token: "tok", Allow: []string{"11"}, ReplyLanguage: englishReplies(),
			Channels: []connectors.Channel{{ID: "G1", Label: "General", Approved: true}}})
		seq = 0
	}
	turn := func(text string) {
		before := len(fake.texts(chatID, thread))
		seq++
		Expect(r.conns.Ingest(ctx, "chat-addon", contract.ConnectorInbound{BindingID: b.ID, ConnectorMessage: contract.ConnectorMessage{
			Chat: chatID, Thread: thread, Mention: true, MessageID: fmt.Sprintf("m%d", seq), SenderID: "11", SenderName: "Cy", Text: text}})).To(Succeed())
		waitFor("the answer", func() bool {
			for _, t := range fake.texts(chatID, thread)[before:] {
				if s := strings.TrimSpace(t); s == "done" || s == "ok" {
					return true
				}
			}
			return false
		})
	}
	last := func(tool string) studiotest.ToolResult {
		res := r.h.ResultsFor(tool)
		Expect(res).NotTo(BeEmpty(), "no result of %s", tool)
		return res[len(res)-1]
	}

	It("is not offered list_connections, though it may propose triggers", func() {
		setup()
		turn("hello")
		l, err := r.svc.List(ctx, alice, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(l).NotTo(BeEmpty())
		names := r.h.ToolNames(l[0].ID)
		Expect(names).To(ContainElement("propose_trigger"))
		Expect(names).NotTo(ContainElement("list_connections"))
	})

	It("is refused delivery.connection on a proposal", func() {
		setup()
		args["propose_trigger"] = fmt.Sprintf(`{"name":"Digest","prompt":"p","when":"daily 08:00","time_zone":"UTC","delivery":{"target":"channel","connection":%q,"channel":"G1"}}`, b.ID)
		turn("ACT-propose_trigger")
		Expect(last("propose_trigger").Err).To(MatchError(ContainSubstring("delivery.connection is for a chat in Studio")))
		ps, err := r.host.Proposals(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(ps).To(BeEmpty())
	})

	It("is refused delivery.connection on an update of its own trigger", func() {
		setup()
		own := r.schedule(studio.TriggerInput{AgentID: r.agent.ID, Name: "Own", Prompt: "p",
			Route: triggers.Route{Binding: b.ID, Chat: chatID, Thread: thread, Target: triggers.TargetOrigin}})
		args["update_trigger"] = fmt.Sprintf(`{"ref":%q,"delivery":{"target":"channel","connection":%q,"channel":"G1"}}`, studio.TriggerRef(own.ID), b.ID)
		turn("ACT-update_trigger")
		Expect(last("update_trigger").Err).To(MatchError(ContainSubstring("delivery.connection is for a trigger that was proposed in Studio")))
	})

	It("cannot redirect a trigger of Studio that reports to a connection", func() {
		setup()
		web := r.schedule(studio.TriggerInput{AgentID: r.agent.ID, Name: "Web", Prompt: "p",
			Route: triggers.Route{Connection: b.ID, Channel: "G1", Target: triggers.TargetChannel}})
		for _, d := range []string{`{"target":"inbox"}`, `{"mode":"on_problem"}`, fmt.Sprintf(`{"target":"channel","connection":%q,"channel":"G1"}`, b.ID)} {
			args["update_trigger"] = fmt.Sprintf(`{"ref":%q,"delivery":%s}`, studio.TriggerRef(web.ID), d)
			turn("ACT-update_trigger")
			Expect(last("update_trigger").Err).To(MatchError(ContainSubstring("was proposed in a messaging app")), "delivery %s", d)
		}
		got, err := r.host.GetTrigger(ctx, alice, web.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Route).To(Equal(web.Route))
	})
})

// An add-on tool or an MCP tool cannot take the name of a built-in trigger tool.
var _ = Describe("the names of the trigger tools", func() {
	It("cannot be taken by a tool of an MCP connector", func() {
		fc := newFakeMCPConnectors()
		fc.tools["c1"] = []string{"list_connections", "update_trigger", "delete_trigger", "fine"}
		r := newMCPConnRig(sayOK, fc)
		id := create(r.svc, alice)
		r.turn(id, "hi")
		names := r.h.ToolNames(id)
		Expect(names).To(ContainElement("fine"), "tools = %v", names)
		for _, n := range []string{"list_connections", "update_trigger", "delete_trigger"} {
			Expect(names).NotTo(ContainElement(n), "tools = %v", names)
		}
		Expect(fc.calls).To(BeEmpty())
	})
})
