package studio_test

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/testutil"
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
			Expect(rec.HostConsent).NotTo(BeNil())
			Expect(*rec.HostConsent).To(SatisfyAll(HaveField("ID", cardID), HaveField("Command", "ls"), HaveField("Turn", turn(c.svc, alice, id).RunID),
				HaveField("AskedAt", Not(BeZero()))), "the card names the turn that asked")
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
			Expect(c.svc.HostExecDecide(context.Background(), bob, id, "x", studio.HostExecDeny)).To(MatchError(studio.ErrHostExecExpired), "no card waits")
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

	Describe("the answer across frontends", func() {
		// twoFrontends runs the turn on A and answers through B; both share
		// the chat store. linked says whether their buses talk.
		twoFrontends := func(linked bool) {
			st, err := store.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			busA, busB := studio.Bus(studio.NewLocalBus()), studio.Bus(studio.NewLocalBus())
			if linked {
				net := testutil.NewFakeBus()
				busA, busB = studio.NewBroadcastBus(net, nil), studio.NewBroadcastBus(net, nil)
			}
			fx := &fakeHostExec{}
			f := newFiles()
			a, _ := newToolService(runBash, func(d *studio.Deps) {
				d.Store, d.Bus, d.Files, d.HostExec = st, busA, f, fx
				d.HostExecMode = func() string { return "ask" }
				d.IsAdmin = func(_ context.Context, user string) bool { return user == alice }
			})
			b := newServiceOn(st, studiotest.NewGateHarness(), func(d *studio.Deps) {
				d.Bus, d.Files = busB, f
				d.IsAdmin = func(_ context.Context, user string) bool { return user == alice }
			})
			id := create(a, alice)
			sent(a, alice, id, "run")
			var card *session.HostConsent
			Eventually(func() *session.HostConsent {
				rec, _ := st.GetChat(context.Background(), alice, id)
				card = rec.HostConsent
				return card
			}, 20*time.Second, 10*time.Millisecond).ShouldNot(BeNil())
			start := time.Now()
			Expect(b.HostExecDecide(context.Background(), alice, id, card.ID, studio.HostExecOnce)).To(Succeed())
			idle(a, alice, id)
			Expect(fx.commands()).To(HaveLen(1), "the command ran on A")
			Expect(toolOf(a, alice, id).ID).To(Equal("once"))
			if linked {
				Expect(time.Since(start)).To(BeNumerically("<", 2*time.Second), "the bus message woke it")
			} else {
				Expect(time.Since(start)).To(BeNumerically("<", 5*time.Second), "the re-read of the card found the answer")
			}
			rec, _ := st.GetChat(context.Background(), alice, id)
			Expect(rec.HostConsent).To(BeNil(), "the call cleared its card")
		}

		It("an answer through frontend B reaches the call on frontend A over the bus", func() { twoFrontends(true) })
		It("an answer whose bus message is lost reaches the call within the re-read", func() { twoFrontends(false) })

		It("keeps the first answer: the same answer again is fine, the other one is a conflict", func() {
			st, err := store.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			svc := newServiceOn(st, studiotest.NewGateHarness())
			id := create(svc, alice)
			sent(svc, alice, id, "hi")
			until("the turn to start", func() bool { return turn(svc, alice, id).Running })
			ctx := context.Background()
			Expect(st.SetHostConsent(ctx, alice, id, &session.HostConsent{ID: "k1", Command: "ls", Turn: turn(svc, alice, id).RunID, AskedAt: time.Now()})).To(Succeed())
			Expect(svc.HostExecDecide(ctx, alice, id, "k1", studio.HostExecOnce)).To(Succeed())
			Expect(svc.HostExecDecide(ctx, alice, id, "k1", studio.HostExecOnce)).To(Succeed())
			Expect(svc.HostExecDecide(ctx, alice, id, "k1", studio.HostExecDeny)).To(MatchError(studio.ErrHostExecAnswered))
			rec, _ := st.GetChat(ctx, alice, id)
			Expect(rec.HostConsent.Decision).To(Equal(studio.HostExecOnce), "the answer is in the record")
			info := turn(svc, alice, id)
			Expect(svc.HostConsent(ctx, alice, rec, info)).To(BeNil(), "an answered card is not shown as waiting")
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
