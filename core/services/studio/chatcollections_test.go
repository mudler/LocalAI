package studio_test

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var _ = Describe("a chat restricted to some collections", func() {
	ctx := context.Background()

	threeCollections := func() *fakeCollections {
		hit := []studio.CollectionHit{{Content: "found it"}}
		return &fakeCollections{mine: map[string]map[string][]studio.CollectionHit{
			alice: {"a": hit, "b": hit, "c": hit},
		}}
	}
	chatWith := func(svc *studio.Service, in studio.CreateInput) string {
		in.Model = "m"
		rec, err := svc.Create(ctx, alice, in)
		Expect(err).NotTo(HaveOccurred())
		return rec.ID
	}

	It("stores the restriction given at creation", func() {
		svc, _ := collectionChat(threeCollections(), sayOK)
		id := chatWith(svc, studio.CreateInput{Collections: []string{"a", "b"}})
		got, err := svc.Get(ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Collections).To(Equal([]string{"a", "b"}))
	})

	It("refuses a malformed list", func() {
		svc, _ := collectionChat(threeCollections(), sayOK)
		many := make([]string, 101)
		for i := range many {
			many[i] = "n" + strings.Repeat("x", i%5) + string(rune('a'+i%26)) + string(rune('a'+i/26))
		}
		for _, bad := range [][]string{many, {"a", ""}, {strings.Repeat("é", 201)}} {
			_, err := svc.Create(ctx, alice, studio.CreateInput{Model: "m", Collections: bad})
			Expect(err).To(MatchError(session.ErrInvalid))
		}
	})

	It("shows only the restricted collections to list_collections", func() {
		svc, h := collectionChat(threeCollections(), func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "list_collections", Args: `{}`}, {Say: "done"}}
		})
		id := chatWith(svc, studio.CreateInput{Collections: []string{"a", "c"}})
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		res := h.ResultsFor("list_collections")
		Expect(res).To(HaveLen(1))
		Expect(res[0].Out).To(MatchJSON(`{"collections":["a","c"]}`))
	})

	It("refuses an excluded collection exactly like an unknown one", func() {
		svc, h := collectionChat(threeCollections(), func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{
				{Tool: "search_collection", Args: `{"collection":"a","query":"x"}`},
				{Tool: "search_collection", Args: `{"collection":"b","query":"x"}`},
				{Tool: "search_collection", Args: `{"collection":"nosuch","query":"x"}`},
				{Say: "done"},
			}
		})
		id := chatWith(svc, studio.CreateInput{Collections: []string{"a"}})
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		res := h.ResultsFor("search_collection")
		Expect(res).To(HaveLen(3))
		Expect(res[0].Err).To(BeNil())
		Expect(res[0].Out).To(ContainSubstring("found it"))
		Expect(res[1].Err).To(MatchError("collection not found"))
		Expect(res[2].Err).To(MatchError("collection not found"))
		Expect(res[1].Out).To(Equal(res[2].Out))
	})

	It("is the intersection with the collections of the agent", func() {
		as, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		fc := threeCollections()
		svc, h := newToolService(func(workspace.Actor, string) []studiotest.ToolStep {
			return []studiotest.ToolStep{
				{Tool: "list_collections", Args: `{}`},
				{Tool: "search_collection", Args: `{"collection":"a","query":"x"}`},
				{Tool: "search_collection", Args: `{"collection":"b","query":"x"}`},
				{Tool: "search_collection", Args: `{"collection":"c","query":"x"}`},
				{Say: "done"},
			}
		}, func(d *studio.Deps) { d.Collections, d.Agents = fc, as })
		ag, err := svc.SaveAgent(ctx, alice, agents.Agent{Name: "Lib", Model: "m", Collections: []string{"a", "b"}})
		Expect(err).NotTo(HaveOccurred())
		id := chatWith(svc, studio.CreateInput{AgentID: ag.ID, Collections: []string{"b", "c"}})
		sent(svc, alice, id, "go")
		idle(svc, alice, id)
		Expect(h.ResultsFor("list_collections")[0].Out).To(MatchJSON(`{"collections":["b"]}`))
		res := h.ResultsFor("search_collection")
		Expect(res).To(HaveLen(3))
		Expect(res[0].Err).To(MatchError("collection not found"), "a is outside the chat restriction")
		Expect(res[1].Err).To(BeNil(), "b is in both")
		Expect(res[2].Err).To(MatchError("collection not found"), "c is outside the agent's list")
	})

	It("refuses a knowledge write to an excluded collection like an unknown one", func() {
		fk := newFakeKnowledge(alice, "manuals", "wiki")
		r := knowledgeShared(fk, nil).rig(func(_ workspace.Actor, text string) []studiotest.ToolStep {
			return []studiotest.ToolStep{{Tool: "propose_change", Args: knowledgeArgs(text, "Hold reset.", "Reset steps")}, {Say: "ok"}}
		}, studio.TriggerOptions{})
		id := chatWith(r.svc, studio.CreateInput{Collections: []string{"wiki"}})
		for _, name := range []string{"wiki", "manuals", "nosuch"} {
			sent(r.svc, alice, id, name)
			idle(r.svc, alice, id)
		}
		res := r.h.ResultsFor("propose_change")
		Expect(res).To(HaveLen(3))
		Expect(res[0].Err).To(BeNil(), "wiki is allowed")
		Expect(res[1].Err).NotTo(BeNil())
		Expect(res[2].Err).NotTo(BeNil())
		strip := func(err error, name string) string { return strings.ReplaceAll(err.Error(), name, "X") }
		Expect(strip(res[1].Err, "manuals")).To(Equal(strip(res[2].Err, "nosuch")))
		list, _ := r.svc.Changes().List(ctx, alice)
		Expect(list).To(HaveLen(1), "only the write to wiki was proposed")
		Expect(list[0].Target).To(Equal("wiki"))
	})

	Describe("sub-agents and forks", func() {
		spawnScript := func(task string) func(workspace.Actor, string) []studiotest.ToolStep {
			return func(_ workspace.Actor, text string) []studiotest.ToolStep {
				switch text {
				case "go":
					return []studiotest.ToolStep{{Tool: "spawn_agent", Args: task}, {Say: "done"}}
				case "look":
					return []studiotest.ToolStep{
						{Tool: "list_collections", Args: `{}`},
						{Tool: "search_collection", Args: `{"collection":"a","query":"x"}`},
						{Tool: "search_collection", Args: `{"collection":"b","query":"x"}`},
						{Tool: "search_collection", Args: `{"collection":"nosuch","query":"x"}`},
						{Say: "seen"},
					}
				}
				return nil
			}
		}
		expectChildRestricted := func(h *studiotest.ToolHarness) {
			lists := h.ResultsFor("list_collections")
			Expect(lists).NotTo(BeEmpty())
			Expect(lists[len(lists)-1].Out).To(MatchJSON(`{"collections":["a"]}`))
			res := h.ResultsFor("search_collection")
			Expect(res).To(HaveLen(3))
			Expect(res[0].Err).To(BeNil())
			Expect(res[1].Err).To(MatchError("collection not found"))
			Expect(res[2].Err).To(MatchError("collection not found"))
		}

		It("keeps a plain sub-agent inside the restriction of its parent", func() {
			svc, h := collectionChat(threeCollections(), spawnScript(`{"task":"look","wait":true,"tools":["list_collections","search_collection"]}`))
			id := chatWith(svc, studio.CreateInput{Collections: []string{"a"}})
			sent(svc, alice, id, "go")
			idle(svc, alice, id)
			kids := childrenOf(svc, alice, id)
			Expect(kids).To(HaveLen(1))
			Expect(kids[0].Collections).To(Equal([]string{"a"}))
			expectChildRestricted(h)
		})

		It("keeps a sub-agent with its own agent inside the restriction of its parent", func() {
			as, err := agents.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			svc, h := newToolService(spawnScript(`{"task":"look","wait":true,"agent":"Wide"}`),
				func(d *studio.Deps) { d.Collections, d.Agents = threeCollections(), as })
			_, err = svc.SaveAgent(ctx, alice, agents.Agent{Name: "Wide", Model: "m", Collections: []string{"a", "b"}})
			Expect(err).NotTo(HaveOccurred())
			id := chatWith(svc, studio.CreateInput{Collections: []string{"a"}})
			allowSavedAgents(svc, id)
			sent(svc, alice, id, "go")
			idle(svc, alice, id)
			Expect(childrenOf(svc, alice, id)).To(HaveLen(1))
			expectChildRestricted(h)
		})

		It("keeps the restriction in a fork and leaves an unrestricted chat unrestricted", func() {
			svc, _ := collectionChat(threeCollections(), sayOK)
			id := chatWith(svc, studio.CreateInput{Collections: []string{"a", "b"}})
			fork, err := svc.Fork(ctx, alice, id, 0, "")
			Expect(err).NotTo(HaveOccurred())
			got, err := svc.Get(ctx, alice, fork.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Collections).To(Equal([]string{"a", "b"}))

			plain := chatWith(svc, studio.CreateInput{})
			fork, err = svc.Fork(ctx, alice, plain, 0, "")
			Expect(err).NotTo(HaveOccurred())
			got, err = svc.Get(ctx, alice, fork.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Collections).To(BeEmpty())
		})
	})

	Describe("project defaults", func() {
		newRig := func(fc *fakeCollections) (*studio.Service, *projects.Service) {
			f := newFiles()
			ps, err := projects.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			proj := projects.NewService(projects.Options{Store: ps, Files: f})
			st, err := store.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			d := studio.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(),
				Harness: studiotest.NewGateHarness(), Files: f, Projects: proj}
			if fc != nil {
				d.Collections = fc
			}
			svc, err := studio.New(d)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = svc.Close(context.Background()) })
			return svc, proj
		}
		start := func(svc *studio.Service, proj *projects.Service, d projects.Defaults, req []string) session.ChatRecord {
			p, err := proj.Create(ctx, alice, projects.CreateInput{Name: "Acme", Defaults: d})
			Expect(err).NotTo(HaveOccurred())
			rec, err := svc.Create(ctx, alice, studio.CreateInput{Model: "m", ProjectID: p.ID, Collections: req})
			Expect(err).NotTo(HaveOccurred())
			return rec
		}

		It("restricts the chat to the listed collections that exist, skipping a ghost", func() {
			svc, proj := newRig(threeCollections())
			rec := start(svc, proj, projects.Defaults{Collections: []string{"a", "ghost"}}, nil)
			Expect(rec.Collections).To(Equal([]string{"a"}))
		})

		It("leaves the chat unrestricted when none of the listed collections exists", func() {
			svc, proj := newRig(threeCollections())
			rec := start(svc, proj, projects.Defaults{Collections: []string{"ghost"}}, nil)
			Expect(rec.Collections).To(BeEmpty())
		})

		It("leaves the chat unrestricted when the server has no collections", func() {
			svc, proj := newRig(nil)
			rec := start(svc, proj, projects.Defaults{Collections: []string{"a"}}, nil)
			Expect(rec.Collections).To(BeEmpty())
		})

		It("lets an explicit request win", func() {
			svc, proj := newRig(threeCollections())
			rec := start(svc, proj, projects.Defaults{Collections: []string{"a"}}, []string{"b", "c"})
			Expect(rec.Collections).To(Equal([]string{"b", "c"}))
		})
	})
})
