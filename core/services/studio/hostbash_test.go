package studio_test

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeHostExec is the built-in bash without a machine: it records commands.
type fakeHostExec struct {
	mu   sync.Mutex
	ran  []string
	down string
}

func (f *fakeHostExec) Unavailable() string { return f.down }

func (f *fakeHostExec) Tool(a workspace.Actor) session.Tool {
	return session.Tool{Name: "bash", Description: "Run on this machine.", Schema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`),
		Run: func(_ context.Context, raw json.RawMessage) (string, error) {
			var in struct{ Command string }
			_ = json.Unmarshal(raw, &in)
			f.mu.Lock()
			defer f.mu.Unlock()
			f.ran = append(f.ran, a.Principal+"|"+a.ChatID+"|"+in.Command)
			return "ran " + in.Command, nil
		}}
}

func (f *fakeHostExec) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.ran...)
}

type hostChat struct {
	svc  *studio.Service
	h    *studiotest.ToolHarness
	fx   *fakeHostExec
	mode *string
}

func runBash(_ workspace.Actor, text string) []studiotest.ToolStep {
	switch text {
	case "run":
		return []studiotest.ToolStep{{Tool: "bash", Args: `{"command":"ls"}`}, {Say: "done"}}
	case "spawn":
		return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"look around","wait":true}`}, {Say: "done"}}
	case "spawn again":
		return []studiotest.ToolStep{{Tool: "spawn_agent", Args: `{"task":"look around once more","wait":true}`}, {Say: "done"}}
	}
	return []studiotest.ToolStep{{Say: "ok"}}
}

// newHostChat is a chat service with files, the built-in bash in a mode, an
// optional add-on broker, and alice as the only administrator.
func newHostChat(mode string, broker studio.AddonTools) hostChat {
	m := mode
	fx := &fakeHostExec{}
	f := newFiles()
	svc, h := newToolService(runBash, func(d *studio.Deps) {
		d.Files = f
		d.HostExec = fx
		d.HostExecMode = func() string { return m }
		d.IsAdmin = func(_ context.Context, user string) bool { return user == alice }
		if broker != nil {
			d.AddonTools = broker
		}
	})
	return hostChat{svc: svc, h: h, fx: fx, mode: &m}
}

func toolOf(svc *studio.Service, user, id string) *session.ToolCard {
	tools := toolEntries(svc, user, id)
	Expect(tools).NotTo(BeEmpty())
	return tools[len(tools)-1].Card
}

var _ = Describe("the built-in bash on this machine", func() {
	It("serves a chat with no Sandbox when the setting is on, says so to the model and labels the call", func() {
		c := newHostChat("on", nil)
		id := create(c.svc, bob)
		sent(c.svc, bob, id, "run")
		idle(c.svc, bob, id)
		Expect(c.h.ToolNames(id)).To(ContainElement("bash"))
		Expect(c.fx.commands()).To(Equal([]string{bob + "|" + id + "|ls"}))
		card := toolOf(c.svc, bob, id)
		Expect(card).NotTo(BeNil())
		Expect(card.Kind).To(Equal(studio.HostExecCardKind))
		Expect(card.ID).To(Equal("ran"))
		prompt := c.h.TurnPrompt(id)
		Expect(prompt).To(ContainSubstring("runs commands directly on this machine, with no isolation"))
		Expect(prompt).To(ContainSubstring("reach the internet"))
		cmds, err := c.svc.CommandRunner(context.Background(), bob, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(cmds.State).To(Equal(studio.CommandsAvailable))
		Expect(cmds.Provider).To(Equal(studio.BuiltinProvider))
		Expect(cmds.Isolation).To(Equal("host"))
	})

	It("is not offered when an administrator switched it off", func() {
		c := newHostChat("off", nil)
		id := create(c.svc, alice)
		sent(c.svc, alice, id, "hi")
		idle(c.svc, alice, id)
		Expect(c.h.ToolNames(id)).NotTo(ContainElement("bash"))
		Expect(c.h.TurnPrompt(id)).To(ContainSubstring("nothing in this chat runs code or commands"))
	})

	It("gives way to the Sandbox when it serves bash: one bash, the add-on's", func() {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: shadowingSandbox()}}
		c := newHostChat("on", fa)
		id := create(c.svc, alice)
		sent(c.svc, alice, id, "run")
		idle(c.svc, alice, id)
		Expect(c.fx.commands()).To(BeEmpty(), "the built-in bash ran although the Sandbox serves bash")
		Expect(fa.calls).To(HaveLen(1))
		cmds, _ := c.svc.CommandRunner(context.Background(), alice, id)
		Expect(cmds.Provider).To(Equal("sandbox"))
	})

	It("serves when the chat switched the Sandbox off, and says which add-on it stands in for", func() {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: shadowingSandbox()}}
		c := newHostChat("on", fa)
		id := create(c.svc, alice)
		Expect(c.svc.SetAddonsOff(context.Background(), alice, id, []string{"sandbox"})).To(Succeed())
		sent(c.svc, alice, id, "run")
		idle(c.svc, alice, id)
		Expect(c.fx.commands()).To(HaveLen(1))
		Expect(fa.calls).To(BeEmpty())
		cmds, _ := c.svc.CommandRunner(context.Background(), alice, id)
		Expect(cmds.Provider).To(Equal(studio.BuiltinProvider))
		Expect(cmds.Fallback).To(Equal("sandbox"))
		Expect(cmds.FallbackReason).To(Equal("off"))
	})

	It("serves instead of a Sandbox that cannot run, without its stand-in tool", func() {
		fa := &fakeAddons{tools: map[string][]studio.AddonTool{alice: shadowingSandbox()}}
		c := newHostChat("on", gatedAddons{fa, map[string]string{"sandbox": "no backend works"}})
		id := create(c.svc, alice)
		sent(c.svc, alice, id, "run")
		idle(c.svc, alice, id)
		Expect(c.h.ToolNames(id)).NotTo(ContainElement("addon__sandbox__unavailable"))
		Expect(c.fx.commands()).To(HaveLen(1))
		cmds, _ := c.svc.CommandRunner(context.Background(), alice, id)
		Expect(cmds.Fallback).To(Equal("sandbox"))
		Expect(cmds.FallbackReason).To(Equal("no backend works"))
	})

	Describe("asking first", func() {
		// answer waits for the card of the running call and answers it.
		answer := func(c hostChat, user, id, decision string) {
			events, cancel, err := c.svc.Events(context.Background(), user, id)
			Expect(err).NotTo(HaveOccurred())
			go func() {
				defer GinkgoRecover()
				defer cancel()
				deadline := time.After(20 * time.Second)
				for {
					select {
					case ev, ok := <-events:
						if !ok {
							return
						}
						if ev.Kind == session.KindHostExecConsent {
							Expect(ev.Text).To(Equal("ls"))
							Expect(c.svc.HostExecDecide(context.Background(), user, id, ev.IDs[0], decision)).To(Succeed())
							return
						}
					case <-deadline:
						return
					}
				}
			}()
		}

		It("runs a command once when the administrator allows it", func() {
			c := newHostChat("ask", nil)
			id := create(c.svc, alice)
			answer(c, alice, id, studio.HostExecOnce)
			sent(c.svc, alice, id, "run")
			idle(c.svc, alice, id)
			Expect(c.fx.commands()).To(HaveLen(1))
			Expect(toolOf(c.svc, alice, id).ID).To(Equal("once"))
			Expect(c.h.TurnPrompt(id)).To(ContainSubstring("waits for the user's yes"))
		})

		It("keeps the card with the chat while it waits, so a reload shows it, and clears it after the answer", func() {
			c := newHostChat("ask", nil)
			id := create(c.svc, alice)
			events, cancel, err := c.svc.Events(context.Background(), alice, id)
			Expect(err).NotTo(HaveOccurred())
			defer cancel()
			sent(c.svc, alice, id, "run")
			var cardID string
			Eventually(func() string {
				select {
				case ev := <-events:
					if ev.Kind == session.KindHostExecConsent {
						cardID = ev.IDs[0]
					}
				case <-time.After(50 * time.Millisecond):
				}
				return cardID
			}, 20*time.Second).ShouldNot(BeEmpty())
			// A reader that did not see the event (a reload, another frontend) finds the card in the record.
			rec, err := c.svc.Get(context.Background(), alice, id)
			Expect(err).NotTo(HaveOccurred())
			Expect(rec.HostConsent).To(Equal(&session.HostConsent{ID: cardID, Command: "ls"}))
			Expect(c.svc.HostExecDecide(context.Background(), alice, id, cardID, studio.HostExecOnce)).To(Succeed())
			idle(c.svc, alice, id)
			rec, err = c.svc.Get(context.Background(), alice, id)
			Expect(err).NotTo(HaveOccurred())
			Expect(rec.HostConsent).To(BeNil())
			Expect(toolOf(c.svc, alice, id).ID).To(Equal("once"), "an answered card stays answered on the tool line")
		})

		It("runs nothing when the person refuses", func() {
			c := newHostChat("ask", nil)
			id := create(c.svc, alice)
			answer(c, alice, id, studio.HostExecDeny)
			sent(c.svc, alice, id, "run")
			idle(c.svc, alice, id)
			Expect(c.fx.commands()).To(BeEmpty())
			Expect(toolOf(c.svc, alice, id).ID).To(Equal("denied"))
		})

		It("is not offered to a user who is not an administrator, who cannot allow a command either", func() {
			c := newHostChat("ask", nil)
			id := create(c.svc, bob)
			sent(c.svc, bob, id, "hi")
			idle(c.svc, bob, id)
			Expect(c.h.ToolNames(id)).NotTo(ContainElement("bash"))
			Expect(c.svc.HostExecDecide(context.Background(), bob, id, "x", studio.HostExecOnce)).To(MatchError(studio.ErrHostExecDecision))
			Expect(c.svc.HostExecDecide(context.Background(), bob, id, "x", studio.HostExecDeny)).To(Succeed())
			Expect(c.svc.HostExecDecide(context.Background(), alice, id, "x", studio.HostExecOnce)).NotTo(Succeed(), "another user's chat")
		})

		It("is not given to a sub-agent, which nobody watches; with the setting on it is", func() {
			c := newHostChat("ask", nil)
			id := create(c.svc, alice)
			sent(c.svc, alice, id, "spawn")
			idle(c.svc, alice, id)
			kids := childrenOf(c.svc, alice, id)
			Expect(kids).To(HaveLen(1))
			Expect(c.h.ToolNames(kids[0].ID)).NotTo(ContainElement("bash"))

			*c.mode = "on"
			sent(c.svc, alice, id, "spawn again")
			idle(c.svc, alice, id)
			kids = childrenOf(c.svc, alice, id)
			Expect(kids).To(HaveLen(2))
			var lists [][]string
			for _, k := range kids {
				lists = append(lists, c.h.ToolNames(k.ID))
			}
			Expect(lists).To(ContainElement(ContainElement("bash")))
		})
	})

	It("lists bash in the registry of built-in tools, switched by chats_host_exec", func() {
		c := newHostChat("off", nil)
		t, ok := c.svc.Builtins().Lookup("bash")
		Expect(ok).To(BeTrue())
		Expect(t.Setting).To(Equal("chats_host_exec"))
		Expect(t.On()).To(BeFalse())
		*c.mode = "ask"
		Expect(t.On()).To(BeTrue())
	})
})
