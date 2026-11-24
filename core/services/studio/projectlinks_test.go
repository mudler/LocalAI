package studio_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

// chatReadStore counts the reads of chats.
type chatReadStore struct {
	store.Store
	gets, lists atomic.Int32
}

func (c *chatReadStore) GetChat(ctx context.Context, p, id string) (session.ChatRecord, error) {
	c.gets.Add(1)
	return c.Store.GetChat(ctx, p, id)
}

func (c *chatReadStore) ListChats(ctx context.Context, p string, cur session.ChatCursor, n int) ([]session.ChatRecord, error) {
	c.lists.Add(1)
	return c.Store.ListChats(ctx, p, cur, n)
}

var _ = Describe("the loops of a project", func() {
	ctx := context.Background()

	It("reads one chat per distinct loop chat, never the chat history", func() {
		var cs *chatReadStore
		var proj *projects.Service
		var ls loops.Store
		r := newShareRig(sayOK, func(d *studio.Deps) {
			cs = &chatReadStore{Store: d.Store}
			if d.Store == nil {
				js, err := store.NewJSON("")
				Expect(err).NotTo(HaveOccurred())
				cs.Store = js
			}
			d.Store = cs
			ps, err := projects.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			proj = projects.NewService(projects.Options{Store: ps, Files: d.Files})
			d.Projects = proj
		})
		var err error
		ls, err = loops.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		host, err := studio.NewLoops(r.svc, studio.LoopOptions{Store: ls, Tick: time.Hour})
		Expect(err).NotTo(HaveOccurred())
		r.svc.SetLoops(host)
		DeferCleanup(host.Close)

		p, err := proj.Create(ctx, alice, projects.CreateInput{Name: "Acme"})
		Expect(err).NotTo(HaveOccurred())
		in, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "In", ProjectID: p.ID})
		Expect(err).NotTo(HaveOccurred())
		out, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Out"})
		Expect(err).NotTo(HaveOccurred())
		now := time.Now()
		chat := func(id, agent, kind string) {
			Expect(cs.CreateChat(ctx, session.ChatRecord{ID: id, Principal: alice, Title: "T " + id, AgentID: agent, Kind: kind, Created: now, Updated: now})).To(Succeed())
		}
		for i := 0; i < 300; i++ {
			chat(fmt.Sprintf("h%d", i), "", session.KindChat)
		}
		chat("c-in", in.ID, session.KindChat)
		chat("c-out", out.ID, session.KindChat)
		chat("c-sub", in.ID, session.KindSubAgent)
		add := func(id, chatID string) {
			Expect(ls.Add(ctx, loops.Loop{ID: id, Owner: alice, ChatID: chatID, Prompt: "go", State: loops.StateActive, Created: now, Updated: now}, loops.Limits{})).To(Succeed())
		}
		add("l-in", "c-in")
		add("l-in2", "c-in")
		add("l-out", "c-out")
		add("l-sub", "c-sub")
		add("l-gone", "c-deleted")

		cs.gets.Store(0)
		cs.lists.Store(0)
		links, err := r.svc.ProjectLoops(ctx, alice, p.ID)
		Expect(err).NotTo(HaveOccurred())
		ids := []string{}
		for _, l := range links {
			ids = append(ids, l.ID)
		}
		Expect(ids).To(ConsistOf("l-in", "l-in2"), "other agents, sub-agent chats and a deleted chat are left out")
		Expect(int(cs.gets.Load())).To(BeNumerically("<=", 5), "one read per distinct loop chat")
		Expect(cs.lists.Load()).To(BeZero(), "the chat history is not paged")
	})
})

var _ = Describe("the triggers of a project", func() {
	It("lists a file trigger's folder as a path of the home, as the trigger API does", func() {
		r := newProjRig()
		acme := r.project("Acme")
		ag := r.agentOf(acme, "Helper")
		r.mkdirs("/Projects/acme/files/inbox")
		tr, err := r.host.CreateTrigger(r.ctx, alice, studio.TriggerInput{Name: "Arrivals", Prompt: "Sort it.", Kind: triggers.KindFile,
			Folder: "/Projects/acme/files/inbox", Model: "m", Quiet: time.Second, AgentID: ag.ID, Enabled: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.host.RelFolder(tr)).To(Equal("/Projects/acme/files/inbox"))
		links, err := r.svc.ProjectTriggers(r.ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(links).To(HaveLen(1))
		Expect(links[0].Folder).To(Equal("/Projects/acme/files/inbox"))
	})
})

// countLoops counts the owner lists of the loop store.
type countLoops struct {
	loops.Store
	n atomic.Int32
}

func (c *countLoops) ListOwner(ctx context.Context, owner string) ([]loops.Loop, error) {
	c.n.Add(1)
	return c.Store.ListOwner(ctx, owner)
}

var _ = Describe("one list for each route of a project", func() {
	var (
		r     *projRig
		ctx   context.Context
		acme  projects.Project
		ls    *countLoops
		chat  session.ChatRecord
		reset func()
	)
	BeforeEach(func() {
		r = newProjRig()
		ctx = r.ctx
		acme = r.project("Acme")
		ag := r.agentOf(acme, "Helper")
		r.connRig.agent = ag
		r.mkdirs("/Projects/acme/files/inbox")
		_, err := r.host.CreateTrigger(ctx, alice, studio.TriggerInput{Name: "Arrivals", Prompt: "Sort it.", Kind: triggers.KindFile,
			Folder: "/Projects/acme/files/inbox", Model: "m", Quiet: time.Second, AgentID: ag.ID, Enabled: true})
		Expect(err).NotTo(HaveOccurred())
		r.bind(studio.ConnectorInput{AgentID: ag.ID, Allow: []string{"11"}})
		base, err := loops.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		ls = &countLoops{Store: base}
		host, err := studio.NewLoops(r.svc, studio.LoopOptions{Store: ls, Tick: time.Hour})
		Expect(err).NotTo(HaveOccurred())
		r.svc.SetLoops(host)
		DeferCleanup(host.Close)
		chat = r.chat(ag.ID)
		now := time.Now()
		add := func(id string, st loops.State) {
			Expect(ls.Add(ctx, loops.Loop{ID: id, Owner: alice, ChatID: chat.ID, Prompt: "go", State: st, Created: now, Updated: now}, loops.Limits{})).To(Succeed())
		}
		add("l-active", loops.StateActive)
		add("l-paused", loops.StatePaused)
		add("l-ended", loops.StateEnded)
		reset = func() {
			r.reads.bindings.Store(0)
			r.reads.triggers.Store(0)
			ls.n.Store(0)
		}
		reset()
	})

	It("reads only the bindings for the connections route", func() {
		rows, err := r.svc.ProjectConnections(ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
		Expect(r.reads.bindings.Load()).To(BeEquivalentTo(1))
		Expect(r.reads.triggers.Load()).To(BeZero())
		Expect(ls.n.Load()).To(BeZero())
	})

	It("reads only the triggers for the triggers route", func() {
		rows, err := r.svc.ProjectTriggers(ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(rows).To(HaveLen(1))
		Expect(r.reads.triggers.Load()).To(BeEquivalentTo(1))
		Expect(r.reads.bindings.Load()).To(BeZero())
		Expect(ls.n.Load()).To(BeZero())
	})

	It("reads only the loops for the loops route, and leaves the ended ones out", func() {
		rows, err := r.svc.ProjectLoops(ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		ids := []string{}
		for _, l := range rows {
			ids = append(ids, l.ID)
		}
		Expect(ids).To(ConsistOf("l-active", "l-paused"))
		Expect(ls.n.Load()).To(BeEquivalentTo(1))
		Expect(r.reads.bindings.Load()).To(BeZero())
		Expect(r.reads.triggers.Load()).To(BeZero())
	})

	It("answers every route with not found for a project of another user", func() {
		_, err := r.svc.ProjectTriggers(ctx, "mallory", acme.ID)
		Expect(err).To(MatchError(projects.ErrNotFound))
		_, err = r.svc.ProjectLoops(ctx, "mallory", acme.ID)
		Expect(err).To(MatchError(projects.ErrNotFound))
		_, err = r.svc.ProjectConnections(ctx, "mallory", acme.ID)
		Expect(err).To(MatchError(projects.ErrNotFound))
		reset()
	})
})
