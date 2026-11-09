package studio_test

import (
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/coordinator"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// coordRig is a service with a coordinator host over a store of its own.
type coordRig struct {
	*trigRig
	conns *connRig
	host  *studio.Coordinators
	store coordinator.Store
}

func coordScript(_ workspace.Actor, text string) []studiotest.ToolStep {
	if strings.Contains(text, "WRITE") {
		return []studiotest.ToolStep{
			{Tool: "write_file", Args: `{"path":"/note.txt","content":"x"}`},
			{Say: "finished"},
		}
	}
	return []studiotest.ToolStep{{Say: "ok"}}
}

func newCoordHost(svc *studio.Service, lim func() studio.CoordinatorLimits) (*studio.Coordinators, coordinator.Store) {
	st, err := coordinator.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	host, err := studio.NewCoordinators(svc, studio.CoordinatorOptions{Store: st, Limits: lim})
	Expect(err).NotTo(HaveOccurred())
	svc.SetCoordinators(host)
	return host, st
}

func newCoordRig(sh *shared) *coordRig {
	r := sh.rig(coordScript, studio.TriggerOptions{})
	host, st := newCoordHost(r.svc, nil)
	return &coordRig{trigRig: r, host: host, store: st}
}

// newCoordConnRig is a coordinator rig that also has connectors, so external
// contacts can be checked against bindings.
func newCoordConnRig() *coordRig {
	sh, bot := newShared(), newScriptedBot()
	cr := newConnRig(sh, connStore(), bot, coordScript, nil)
	host, st := newCoordHost(cr.svc, nil)
	return &coordRig{trigRig: cr.trigRig, conns: cr, host: host, store: st}
}

func coordInput() studio.CoordinatorInput {
	return studio.CoordinatorInput{
		Agent:  agents.Agent{Name: "Desk", Model: "m"},
		Config: coordinator.Config{Page: "board"},
	}
}

var _ = Describe("coordinator agents", func() {
	ctx := context.Background()

	It("Create makes the agent, the config and the chat", func() {
		r := newCoordRig(newShared())
		v, err := r.host.Create(ctx, alice, coordInput())
		Expect(err).NotTo(HaveOccurred())
		Expect(v.Agent.Builtins).To(ContainElement("act.files"))
		Expect(v.Agent.Instructions).To(Equal(studio.StarterInstructions))
		Expect(v.Config.AgentID).To(Equal(v.Agent.ID))
		Expect(v.Config.Principal).To(Equal(alice))
		Expect(v.Config.ChatID).NotTo(BeEmpty())
		chat, err := r.svc.Get(ctx, alice, v.Config.ChatID)
		Expect(err).NotTo(HaveOccurred())
		Expect(chat.Origin).To(Equal(session.OriginCoordinator))
		Expect(chat.OriginID).To(Equal(v.Agent.ID))
		Expect(chat.AgentID).To(Equal(v.Agent.ID))
		Expect(r.host.Is(ctx, v.Agent.ID)).To(BeTrue())
		Expect(r.host.Is(ctx, "nothing")).To(BeFalse())
	})

	It("keeps the instructions and the grants the creator wrote", func() {
		r := newCoordRig(newShared())
		in := coordInput()
		in.Agent.Instructions = "Watch the queue."
		in.Agent.Builtins = []string{"memory.write"}
		v, err := r.host.Create(ctx, alice, in)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.Agent.Instructions).To(Equal("Watch the queue."))
		Expect(v.Agent.Builtins).To(ConsistOf("memory.write", "act.files"))
		Expect(studio.Preset(v.Agent, "board").Builtins).To(ConsistOf("memory.write", "act.files"), "no grant is added twice")
	})

	It("Create with a page makes pages/<name> and fences the agent to it", func() {
		r := newCoordRig(newShared())
		v, err := r.host.Create(ctx, alice, coordInput())
		Expect(err).NotTo(HaveOccurred())
		text, ok, err := r.sh.files.ReadHome(ctx, alice, "/pages/board/index.html", 1<<20)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(text).To(ContainSubstring("<html"))
		Expect(v.Agent.Folders).To(HaveLen(1))
		Expect(v.Agent.Folders[0].Mode).To(Equal(agents.FolderWrite))
		fs, ok := r.svc.AgentFolder(ctx, alice, v.Agent)
		Expect(ok).To(BeTrue())
		Expect(fs.Path).To(Equal("/pages/board"))

		// A page that exists is not overwritten.
		_, err = r.host.Create(ctx, alice, studio.CoordinatorInput{Agent: agents.Agent{Name: "Desk 2", Model: "m"}, Config: coordinator.Config{Page: "board"}})
		Expect(err).NotTo(HaveOccurred())
		again, _, _ := r.sh.files.ReadHome(ctx, alice, "/pages/board/index.html", 1<<20)
		Expect(again).To(Equal(text))
	})

	It("Create without a page leaves the folders of the agent alone", func() {
		r := newCoordRig(newShared())
		v, err := r.host.Create(ctx, alice, studio.CoordinatorInput{Agent: agents.Agent{Name: "Desk", Model: "m"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(v.Agent.Folders).To(BeEmpty())
	})

	It("refuses an agent with no model and an invalid config, and leaves nothing behind", func() {
		r := newCoordRig(newShared())
		_, err := r.host.Create(ctx, alice, studio.CoordinatorInput{Agent: agents.Agent{Name: "Desk"}})
		Expect(err).To(MatchError(studio.ErrNoModel))
		in := coordInput()
		in.Config.WakeEvery = time.Minute
		_, err = r.host.Create(ctx, alice, in)
		Expect(err).To(MatchError(coordinator.ErrInvalid))
		list, err := r.svc.Agents(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(BeEmpty())
		cfgs, _ := r.store.List(ctx)
		Expect(cfgs).To(BeEmpty())
	})

	It("EnsureChat recreates a deleted chat and stores the new id", func() {
		r := newCoordRig(newShared())
		v, err := r.host.Create(ctx, alice, coordInput())
		Expect(err).NotTo(HaveOccurred())
		first := v.Config.ChatID
		got, err := r.host.EnsureChat(ctx, v.Config)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ID).To(Equal(first), "an existing chat is returned as it is")

		Expect(r.svc.Delete(ctx, alice, first)).To(Succeed())
		again, err := r.host.EnsureChat(ctx, v.Config)
		Expect(err).NotTo(HaveOccurred())
		Expect(again.ID).NotTo(Equal(first))
		Expect(again.Origin).To(Equal(session.OriginCoordinator))
		cfg, _ := r.store.Get(ctx, v.Agent.ID)
		Expect(cfg.ChatID).To(Equal(again.ID))
		third, err := r.host.EnsureChat(ctx, cfg)
		Expect(err).NotTo(HaveOccurred())
		Expect(third.ID).To(Equal(again.ID))
	})

	It("EnsureChat makes one chat when two callers race for it", func() {
		r := newCoordRig(newShared())
		v, err := r.host.Create(ctx, alice, coordInput())
		Expect(err).NotTo(HaveOccurred())
		Expect(r.svc.Delete(ctx, alice, v.Config.ChatID)).To(Succeed())
		ids := make(chan string, 6)
		for range 6 {
			go func() {
				defer GinkgoRecover()
				rec, err := r.host.EnsureChat(ctx, v.Config)
				Expect(err).NotTo(HaveOccurred())
				ids <- rec.ID
			}()
		}
		first := <-ids
		for range 5 {
			Expect(<-ids).To(Equal(first))
		}
		var chats int
		all, _ := r.svc.List(ctx, alice, 100)
		for _, c := range all {
			if c.Origin == session.OriginCoordinator {
				chats++
			}
		}
		Expect(chats).To(Equal(1))
	})

	It("EnsureChat makes nothing for a coordinator that was deleted", func() {
		r := newCoordRig(newShared())
		v, err := r.host.Create(ctx, alice, coordInput())
		Expect(err).NotTo(HaveOccurred())
		Expect(r.svc.DeleteAgent(ctx, alice, v.Agent.ID)).To(Succeed())
		_, err = r.host.EnsureChat(ctx, v.Config)
		Expect(err).To(MatchError(studio.ErrNotACoordinator))
		all, _ := r.svc.List(ctx, alice, 100)
		for _, c := range all {
			Expect(c.ID).To(Equal(v.Config.ChatID), "only the old chat stays: no other one is made")
		}
	})

	Describe("contacts", func() {
		bindFor := func(r *coordRig, in studio.ConnectorInput) connectors.Binding {
			return r.conns.bind(in)
		}
		extContact := func(b connectors.Binding, sender string) coordinator.Contact {
			return coordinator.Contact{ID: "ops", Kind: coordinator.ContactExternal, Binding: b.ID, Chat: "c1", Sender: sender}
		}
		n := 0
		create := func(r *coordRig, cts ...coordinator.Contact) error {
			n++
			in := coordInput()
			in.Agent.Name = fmt.Sprintf("Desk %d", n)
			in.Config.Contacts = cts
			_, err := r.host.Create(ctx, alice, in)
			return err
		}

		It("accepts an external contact on an own binding that accepts the sender", func() {
			r := newCoordConnRig()
			b := bindFor(r, studio.ConnectorInput{Allow: []string{"11"}})
			Expect(create(r, extContact(b, "11"))).To(Succeed())
		})

		It("takes the chat as the sender when none is given, and accepts a listed chat", func() {
			r := newCoordConnRig()
			b := bindFor(r, studio.ConnectorInput{Allow: []string{"55"}})
			ct := extContact(b, "")
			ct.Chat = "55"
			Expect(create(r, ct)).To(Succeed())
			ct.Sender = "99"
			Expect(create(r, ct)).To(Succeed(), "anyone in a listed chat is accepted")
		})

		It("refuses an external contact on another user's binding", func() {
			r := newCoordConnRig()
			b := bindFor(r, studio.ConnectorInput{Allow: []string{"11"}})
			raw, err := r.conns.store.GetBinding(ctx, b.ID)
			Expect(err).NotTo(HaveOccurred())
			raw.Owner = bob
			Expect(r.conns.store.PutBinding(ctx, raw)).To(Succeed())
			Expect(create(r, extContact(b, "11"))).To(MatchError(coordinator.ErrInvalid))
			Expect(create(r, coordinator.Contact{ID: "x", Kind: coordinator.ContactExternal, Binding: "nope", Chat: "c", Sender: "11"})).To(MatchError(coordinator.ErrInvalid))
		})

		It("refuses a binding whose conversation mode is thread or window", func() {
			r := newCoordConnRig()
			for _, mode := range []string{connectors.ConversationThread, connectors.ConversationWindow} {
				b := bindFor(r, studio.ConnectorInput{Allow: []string{"11"}, Conversation: mode, WindowMinutes: 30})
				err := create(r, extContact(b, "11"))
				Expect(err).To(MatchError(coordinator.ErrInvalid), mode)
				Expect(err).To(MatchError(ContainSubstring(mode)))
			}
			b := bindFor(r, studio.ConnectorInput{Allow: []string{"11"}, Conversation: connectors.ConversationChat})
			Expect(create(r, extContact(b, "11"))).To(Succeed())
		})

		It("refuses a sender the binding does not accept", func() {
			r := newCoordConnRig()
			b := bindFor(r, studio.ConnectorInput{Allow: []string{"11"}})
			Expect(create(r, extContact(b, "22"))).To(MatchError(coordinator.ErrInvalid))
			open := bindFor(r, studio.ConnectorInput{AcceptAny: true})
			Expect(create(r, extContact(open, "22"))).To(Succeed())
		})

		It("refuses an external contact when there are no connectors", func() {
			r := newCoordRig(newShared())
			Expect(create(r, coordinator.Contact{ID: "x", Kind: coordinator.ContactExternal, Binding: "b", Chat: "c"})).To(MatchError(coordinator.ErrInvalid))
		})

		It("accepts the owner as a user contact and refuses any other when no user lookup exists", func() {
			r := newCoordRig(newShared())
			Expect(create(r, coordinator.Contact{ID: "me", Kind: coordinator.ContactUser, User: alice})).To(Succeed())
			Expect(create(r, coordinator.Contact{ID: "boss", Kind: coordinator.ContactUser, User: bob})).To(MatchError(coordinator.ErrInvalid))
		})

		It("refuses an unknown user when it can look users up, and accepts a known one", func() {
			sh := newShared()
			sh.deps = func(d *studio.Deps) {
				d.Users = func(_ context.Context, id string) (*auth.User, error) {
					if id == bob {
						return &auth.User{ID: id}, nil
					}
					return nil, studio.ErrNoUser
				}
			}
			r := newCoordRig(sh)
			Expect(create(r, coordinator.Contact{ID: "boss", Kind: coordinator.ContactUser, User: bob})).To(Succeed())
			Expect(create(r, coordinator.Contact{ID: "ghost", Kind: coordinator.ContactUser, User: "ghost"})).To(MatchError(coordinator.ErrInvalid))
		})
	})

	It("refuses helper limits above the operator ceiling", func() {
		r := newCoordRig(newShared())
		in := coordInput()
		in.Config.MaxHelpers = 17
		_, err := r.host.Create(ctx, alice, in)
		Expect(err).To(MatchError(coordinator.ErrInvalid))
		in.Config.MaxHelpers, in.Config.MaxDepth = 16, 4
		_, err = r.host.Create(ctx, alice, in)
		Expect(err).To(MatchError(coordinator.ErrInvalid))
		in.Config.MaxDepth = 3
		_, err = r.host.Create(ctx, alice, in)
		Expect(err).NotTo(HaveOccurred())
	})

	It("follows an operator ceiling that changes", func() {
		sh := newShared()
		r := sh.rig(coordScript, studio.TriggerOptions{})
		host, _ := newCoordHost(r.svc, func() studio.CoordinatorLimits { return studio.CoordinatorLimits{HelpersCeiling: 4} })
		in := coordInput()
		in.Config.MaxHelpers = 5
		_, err := host.Create(ctx, alice, in)
		Expect(err).To(MatchError(coordinator.ErrInvalid))
		in.Config.MaxHelpers = 4
		_, err = host.Create(ctx, alice, in)
		Expect(err).NotTo(HaveOccurred())
	})

	It("deleting the agent removes the config, by DeleteAgent, DeleteAgentWithNotes and AdminDeleteAgent", func() {
		r := newCoordRig(newShared())
		mk := func(name string) string {
			in := coordInput()
			in.Agent.Name, in.Config.Page = name, ""
			v, err := r.host.Create(ctx, alice, in)
			Expect(err).NotTo(HaveOccurred())
			return v.Agent.ID
		}
		a, b, c := mk("A"), mk("B"), mk("C")
		Expect(r.svc.DeleteAgent(ctx, alice, a)).To(Succeed())
		_, err := r.svc.DeleteAgentWithNotes(ctx, alice, b, studio.AgentNotesKeep)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.svc.AdminDeleteAgent(ctx, c)).To(Succeed())
		for _, id := range []string{a, b, c} {
			_, err := r.store.Get(ctx, id)
			Expect(err).To(MatchError(coordinator.ErrNotFound), id)
			Expect(r.host.Is(ctx, id)).To(BeFalse())
		}
		all, err := r.host.ListAll(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(BeEmpty())
	})

	It("Update keeps the chat, the pause state and the next wake", func() {
		r := newCoordRig(newShared())
		v, err := r.host.Create(ctx, alice, coordInput())
		Expect(err).NotTo(HaveOccurred())
		at := time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC)
		_, _, err = r.store.SetPaused(ctx, v.Agent.ID, true, "admin", at)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.store.ClaimWake(ctx, v.Agent.ID, at, at.Add(time.Hour))).To(BeTrue())

		in := coordInput()
		in.Agent.Name = "Desk renamed"
		in.Agent.Instructions = "New rules."
		in.Config.WakeEvery = time.Hour
		in.Config.ChatID, in.Config.Paused, in.Config.NextWake = "forged", false, time.Time{}
		up, err := r.host.Update(ctx, alice, v.Agent.ID, in)
		Expect(err).NotTo(HaveOccurred())
		Expect(up.Agent.Name).To(Equal("Desk renamed"))
		Expect(up.Agent.Instructions).To(Equal("New rules."))
		Expect(up.Agent.Builtins).To(ContainElement("act.files"))
		Expect(up.Config.WakeEvery).To(Equal(time.Hour))
		Expect(up.Config.ChatID).To(Equal(v.Config.ChatID))
		Expect(up.Config.Paused).To(BeTrue())
		Expect(up.Config.PausedBy).To(Equal("admin"))
		Expect(up.Config.NextWake).To(Equal(at.Add(time.Hour)))
		Expect(up.Config.Created).To(Equal(v.Config.Created))
	})

	It("another user gets not found; ListAll returns every owner", func() {
		r := newCoordRig(newShared())
		v, err := r.host.Create(ctx, alice, coordInput())
		Expect(err).NotTo(HaveOccurred())
		_, err = r.host.Get(ctx, bob, v.Agent.ID)
		Expect(err).To(MatchError(agents.ErrNotFound))
		_, err = r.host.Update(ctx, bob, v.Agent.ID, coordInput())
		Expect(err).To(MatchError(agents.ErrNotFound))
		bobs, err := r.host.List(ctx, bob)
		Expect(err).NotTo(HaveOccurred())
		Expect(bobs).To(BeEmpty())

		in := coordInput()
		in.Config.Page = ""
		_, err = r.host.Create(ctx, bob, in)
		Expect(err).NotTo(HaveOccurred())
		mine, err := r.host.List(ctx, alice)
		Expect(err).NotTo(HaveOccurred())
		Expect(mine).To(HaveLen(1))
		all, err := r.host.ListAll(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(all).To(HaveLen(2))
	})

	It("a plain agent is not a coordinator", func() {
		r := newCoordRig(newShared())
		ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Plain", Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		_, err = r.host.Get(ctx, alice, ag.ID)
		Expect(err).To(MatchError(studio.ErrNotACoordinator))
		Expect(err).To(MatchError(coordinator.ErrNotFound))
		_, err = r.host.Update(ctx, alice, ag.ID, coordInput())
		Expect(err).To(MatchError(studio.ErrNotACoordinator))
	})

	It("reports what a coordinator used today against its limits", func() {
		r := newCoordRig(newShared())
		in := coordInput()
		in.Config.SendPerDay = 7
		v, err := r.host.Create(ctx, alice, in)
		Expect(err).NotTo(HaveOccurred())
		Expect(v.Usage).To(Equal(studio.CoordinatorUsage{RunsLimit: 48, SendLimit: 7}))
		now := time.Now()
		Expect(r.store.Reserve(ctx, v.Agent.ID, coordinator.DayBucket("runs", now), 48)).To(BeTrue())
		Expect(r.store.Reserve(ctx, v.Agent.ID, coordinator.DayBucket("send", now), 7)).To(BeTrue())
		got, err := r.host.Get(ctx, alice, v.Agent.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Usage.RunsToday).To(Equal(1))
		Expect(got.Usage.SentToday).To(Equal(1))
	})

	Describe("the rights of its chat", func() {
		const refused = "I did not run write_file"
		write := func(r *coordRig, chat string) studiotest.ToolResult {
			sent(r.svc, alice, chat, "WRITE the note")
			idle(r.svc, alice, chat)
			rs := r.h.ResultsFor("write_file")
			Expect(rs).To(HaveLen(1))
			return rs[0]
		}

		It("allows a write_file with act.files", func() {
			r := newCoordRig(newShared())
			v, err := r.host.Create(ctx, alice, coordInput())
			Expect(err).NotTo(HaveOccurred())
			res := write(r, v.Config.ChatID)
			Expect(res.Err).NotTo(HaveOccurred())
			Expect(res.Out).NotTo(ContainSubstring(refused))
			text, ok, err := r.sh.files.ReadHome(ctx, alice, "/pages/board/note.txt", 1<<20)
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue(), "the write landed in the page folder")
			Expect(text).To(Equal("x"))
		})

		It("refuses it, with the sentence, when the agent does not hold act.files", func() {
			r := newCoordRig(newShared())
			v, err := r.host.Create(ctx, alice, coordInput())
			Expect(err).NotTo(HaveOccurred())
			a := v.Agent
			a.Builtins = []string{"memory.write"}
			_, err = r.svc.SaveAgentWithFolders(ctx, alice, a, nil)
			Expect(err).NotTo(HaveOccurred())
			res := write(r, v.Config.ChatID)
			Expect(res.Err).NotTo(HaveOccurred(), "a refusal is an answer, not an error")
			Expect(res.Out).To(ContainSubstring(refused))
			Expect(res.Out).To(ContainSubstring("act.files"))
		})
	})
})
