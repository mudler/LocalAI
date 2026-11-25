package studio_test

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/tests/studiobot"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A run of a trigger writes to a connection of the owner with send_message
// only when the trigger holds act.send. The kind is a made-up platform.
var _ = Describe("send_message in a run of a trigger", func() {
	ctx := context.Background()
	var (
		r     *connRig
		fake  *postAddon
		gate  *capSwitch
		team  connectors.Binding
		spare connectors.Binding
		agent agents.Agent

		mu    sync.Mutex
		steps []studiotest.ToolStep // what the run does
	)
	general := connectors.Channel{ID: "G1", Label: "General", Approved: true}
	ops := connectors.Channel{ID: "G2", Label: "Ops"} // listed, not approved
	other := connectors.Channel{ID: "S1", Label: "Elsewhere", Approved: true}

	script := func(_ workspace.Actor, text string) []studiotest.ToolStep {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(text, "CHILD"):
			return []studiotest.ToolStep{{Say: "child done"}}
		case strings.Contains(text, "CHECK"):
			return append(append([]studiotest.ToolStep{}, steps...), studiotest.ToolStep{Say: "all checked"})
		}
		return []studiotest.ToolStep{{Say: "ok"}}
	}
	do := func(s ...studiotest.ToolStep) {
		mu.Lock()
		steps = s
		mu.Unlock()
	}

	// setup binds two connections of alice and saves an agent with the grants.
	setup := func(grants ...string) {
		fake = &postAddon{}
		tp := studiobot.NewTransport(studiobot.Addon{ID: "chat-addon", Kind: contract.ConnectorKind{ID: "chatty", Title: "Chatty", Delivery: contract.DeliveryPull}, Runtime: fake})
		r = newConnRigOpts(newShared(), connStore(), tp, script, nil, func(o *studio.ConnectorOptions) { o.OutboxPoll = 10 * time.Millisecond })
		gate = &capSwitch{}
		gate.set(contract.CapTalkbackChannels, true)
		gate.set(studio.CapTriggersRun, true)
		r.svc.SetCapabilityGate(gate)
		var err error
		agent, err = r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Watcher", Model: "m", Builtins: grants})
		Expect(err).NotTo(HaveOccurred())
		team = r.bind(studio.ConnectorInput{Kind: "chatty", Name: "Team chat", Token: "secret-token-1", Allow: []string{"11"}, AgentID: agent.ID, Channels: []connectors.Channel{general, ops}})
		spare = r.bind(studio.ConnectorInput{Kind: "chatty", Name: "Spare", Token: "secret-token-2", Allow: []string{"11"}, AgentID: agent.ID, Channels: []connectors.Channel{other}})
		do()
	}
	// run makes a trigger, lets it run once and returns the chat of the run.
	run := func(in studio.TriggerInput) string {
		in.Prompt = "CHECK the service; message me only when it is down"
		if in.AgentID == "" {
			in.AgentID = agent.ID
		}
		tr := r.schedule(in)
		r.due()
		end := r.waitEnded(tr.ID)
		ExpectWithOffset(1, end.Status.Final()).To(BeTrue())
		ExpectWithOffset(1, end.ChatID).NotTo(BeEmpty(), "run = %+v", end)
		return end.ChatID
	}
	posted := func(channel string) func() []string {
		return func() []string { return fake.texts(channel, "") }
	}
	sendArgs := func(conn, channel, text string) string {
		return `{"connection":"` + conn + `","channel":"` + channel + `","text":"` + text + `"}`
	}

	It("is offered with act.send, says when to send, and writes in an approved channel", func() {
		setup("act.send")
		do(studiotest.ToolStep{Tool: "send_message", Args: sendArgs(team.ID, "General", "The service is down")})
		chat := run(studio.TriggerInput{Acts: []string{"act.send"}})
		Expect(r.h.ToolNames(chat)).To(ContainElement("send_message"))
		d := r.h.ToolDescription(chat, "send_message")
		Expect(d).To(ContainSubstring("run of a trigger"))
		Expect(d).To(ContainSubstring("goes to the delivery target"))
		Expect(d).To(ContainSubstring(team.ID))
		Expect(d).To(ContainSubstring(spare.ID))
		Expect(d).NotTo(ContainSubstring("G2"), "a channel that is not approved is not listed")
		res := r.h.ResultsFor("send_message")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).NotTo(HaveOccurred())
		Expect(res[0].Out).To(ContainSubstring("General (G1)"), "the trace of the run says where the message went")
		Expect(res[0].Out).To(ContainSubstring(team.ID))
		Eventually(posted("G1")).Should(ConsistOf(ContainSubstring("The service is down")))
		Expect(r.h.ToolSchema(chat, "send_message")).To(ContainSubstring(`"required":["text","connection"]`))
		v, err := r.conns.Access(ctx, alice, team.ID)
		Expect(err).NotTo(HaveOccurred())
		var audited bool
		for _, e := range v.Recent {
			audited = audited || (e.Action == studio.SendTriggerRun && e.Allowed)
		}
		Expect(audited).To(BeTrue(), "the access log of the connection records the send: %+v", v.Recent)
	})

	It("refuses a connection of another owner, and says nothing about it", func() {
		setup("act.send")
		agB, err := r.svc.SaveAgent(ctx, "bob", agents.Agent{Name: "Other", Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		ob, err := r.conns.CreateConnector(ctx, "bob", studio.ConnectorInput{Kind: "chatty", Name: "Not yours", Token: "secret-token-3", AgentID: agB.ID, Enabled: true,
			Allow: []string{"11"}, Channels: []connectors.Channel{{ID: "B1", Label: "Bob room", Approved: true}}, ConfirmDuplicate: studio.DuplicateConfirmation, Reply: connectors.ReplyOff})
		Expect(err).NotTo(HaveOccurred())
		r.trust(ob.ID)
		do(studiotest.ToolStep{Tool: "send_message", Args: sendArgs(ob.ID, "B1", "hello")})
		chat := run(studio.TriggerInput{Acts: []string{"act.send"}})
		Expect(r.h.ToolDescription(chat, "send_message")).NotTo(ContainSubstring(ob.ID))
		res := r.h.ResultsFor("send_message")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).To(MatchError(ContainSubstring("not one of the person's connections")))
		for _, secret := range []string{"Not yours", "Bob room"} {
			Expect(res[0].Err.Error()).NotTo(ContainSubstring(secret))
		}
		Consistently(posted("B1"), 100*time.Millisecond).Should(BeEmpty())
	})

	It("is not offered without act.send, so the run cannot write", func() {
		setup("act.send")
		chat := run(studio.TriggerInput{})
		Expect(r.h.ToolNames(chat)).NotTo(ContainElement("send_message"))
		Expect(fake.count()).To(BeZero())
	})

	It("is not offered when the agent does not hold act.send, whatever the trigger lists", func() {
		setup()
		chat := run(studio.TriggerInput{Acts: []string{"act.send"}})
		Expect(r.h.ToolNames(chat)).NotTo(ContainElement("send_message"))
	})

	It("is not offered to a trigger that declared its runs only read", func() {
		setup("act.send")
		chat := run(studio.TriggerInput{Acts: []string{"act.send"}, Route: triggers.Route{Binding: team.ID, ReadOnly: true}})
		Expect(r.h.ToolNames(chat)).NotTo(ContainElement("send_message"))
	})

	It("refuses a listed channel that the owner did not approve", func() {
		setup("act.send")
		do(studiotest.ToolStep{Tool: "send_message", Args: sendArgs(team.ID, "Ops", "hello")})
		run(studio.TriggerInput{Acts: []string{"act.send"}})
		res := r.h.ResultsFor("send_message")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Err).To(HaveOccurred())
		Consistently(posted("G2"), 100*time.Millisecond).Should(BeEmpty())
	})

	It("keeps a trigger that a connection proposed to the channels of that connection", func() {
		setup("act.send")
		do(studiotest.ToolStep{Tool: "send_message", Args: sendArgs(spare.ID, "Elsewhere", "hello")},
			studiotest.ToolStep{Tool: "send_message", Args: `{"channel":"General","text":"the service is down"}`})
		chat := run(studio.TriggerInput{Acts: []string{"act.send"}, Route: triggers.Route{Binding: team.ID, Chat: "11"}})
		d := r.h.ToolDescription(chat, "send_message")
		Expect(d).To(ContainSubstring(team.ID))
		Expect(d).NotTo(ContainSubstring(spare.ID))
		Expect(r.h.ToolSchema(chat, "send_message")).To(ContainSubstring(`"required":["text"]`), "the connection may be left out")
		res := r.h.ResultsFor("send_message")
		Expect(res).To(HaveLen(2))
		Expect(res[0].Err).To(MatchError(ContainSubstring("only to the connection")))
		Expect(res[1].Err).NotTo(HaveOccurred(), "the connection of the trigger is taken when it is left out")
		Eventually(posted("G1")).Should(ConsistOf(ContainSubstring("the service is down")))
		Consistently(posted("S1"), 100*time.Millisecond).Should(BeEmpty())
	})

	It("is not offered to a sub-agent of the run", func() {
		setup("act.send")
		do(studiotest.ToolStep{Tool: "spawn_agent", Args: `{"task":"CHILD look at the logs","wait":true}`})
		chat := run(studio.TriggerInput{Acts: []string{"act.send"}})
		Expect(r.h.ToolNames(chat)).To(ContainElement("send_message"))
		kids := childrenOf(r.svc, alice, chat)
		Expect(kids).To(HaveLen(1), "spawn_agent = %+v", r.h.ResultsFor("spawn_agent"))
		Expect(kids[0].Kind).To(Equal(session.KindSubAgent))
		Expect(r.h.ToolNames(kids[0].ID)).NotTo(ContainElement("send_message"))
	})

	It("limits the messages that one trigger sends", func() {
		setup("act.send")
		var many []studiotest.ToolStep
		for range 6 {
			many = append(many, studiotest.ToolStep{Tool: "send_message", Args: sendArgs(team.ID, "General", "down")})
		}
		do(many...)
		run(studio.TriggerInput{Acts: []string{"act.send"}})
		res := r.h.ResultsFor("send_message")
		Expect(res).To(HaveLen(6))
		for _, x := range res[:5] {
			Expect(x.Err).NotTo(HaveOccurred())
		}
		Expect(res[5].Err).To(MatchError(ContainSubstring("the most it may")))
	})
})
