package studio

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connsets"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/resourcesets"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// failingAgents fails the read of the agents named in bad.
type failingAgents struct {
	agents.Store
	bad map[string]bool
}

func (f failingAgents) Get(ctx context.Context, p, id string) (agents.Agent, error) {
	if f.bad[id] {
		return agents.Agent{}, errors.New("store down")
	}
	return f.Store.Get(ctx, p, id)
}

// failingChats fails the read of the chats named in bad.
type failingChats struct {
	store.Store
	bad map[string]bool
}

func (f failingChats) GetChat(ctx context.Context, p, id string) (session.ChatRecord, error) {
	if f.bad[id] {
		return session.ChatRecord{}, errors.New("store down")
	}
	return f.Store.GetChat(ctx, p, id)
}

var _ = Describe("capAgent", func() {
	full := agents.Agent{
		Skills: []string{"a", "b"}, Addons: []string{"x", "y"}, Collections: []string{"k1", "k2"},
		MCPServers: []string{"mail", "git", "connector:c1", "connector:c2"},
	}

	It("intersects each kind with a non-empty ceiling", func() {
		got := capAgent(full, projects.Defaults{Skills: []string{"a"}, Addons: []string{"y"}, Collections: []string{"k2", "other"}})
		Expect(got.Skills).To(Equal([]string{"a"}))
		Expect(got.Addons).To(Equal([]string{"y"}))
		Expect(got.Collections).To(Equal([]string{"k2"}))
		Expect(got.MCPServers).To(Equal(full.MCPServers), "no MCP ceiling")
	})

	It("never touches connections, resources or grants", func() {
		a := full
		a.Connections = connsets.Sets{"x": {"c"}}
		a.Resources = resourcesets.Sets{"x": {{ID: "r", Mode: "read"}}}
		a.Builtins = []string{"act.files"}
		got := capAgent(a, projects.Defaults{Skills: []string{"a"}, Addons: []string{"y"}, MCPServers: []string{"git"}})
		Expect(got.Connections).To(Equal(a.Connections))
		Expect(got.Resources).To(Equal(a.Resources))
		Expect(got.Builtins).To(Equal(a.Builtins))
	})

	It("is a no-op for an empty ceiling", func() {
		Expect(capAgent(full, projects.Defaults{})).To(Equal(full))
	})

	It("drops names outside the ceiling, and a ceiling naming nothing the agent holds empties the list", func() {
		got := capAgent(full, projects.Defaults{Skills: []string{"zzz"}})
		Expect(got.Skills).To(BeEmpty())
		Expect(got.Addons).To(Equal(full.Addons))
	})

	It("handles server names and connector entries separately", func() {
		got := capAgent(full, projects.Defaults{MCPServers: []string{"mail"}})
		Expect(got.MCPServers).To(Equal([]string{"mail", "connector:c1", "connector:c2"}), "server ceiling leaves connectors alone")
		got = capAgent(full, projects.Defaults{Connectors: []string{"c2"}})
		Expect(got.MCPServers).To(Equal([]string{"mail", "git", "connector:c2"}), "connector ceiling leaves servers alone")
		got = capAgent(full, projects.Defaults{MCPServers: []string{"git"}, Connectors: []string{"c1"}})
		Expect(got.MCPServers).To(Equal([]string{"git", "connector:c1"}))
		got = capAgent(full, projects.Defaults{MCPServers: []string{"connector:c1"}})
		Expect(got.MCPServers).To(Equal([]string{"connector:c1", "connector:c2"}), "a server ceiling never matches a connector entry, and leaves connectors alone")
	})

	It("does not change the input slices", func() {
		in := agents.Agent{Skills: []string{"a", "b"}}
		_ = capAgent(in, projects.Defaults{Skills: []string{"b"}})
		Expect(in.Skills).To(Equal([]string{"a", "b"}))
	})
})

var _ = Describe("the project ceiling in chats", func() {
	var (
		ctx   context.Context
		svc   *Service
		ps    *projects.Service
		st    store.Store
		atlas projects.Project
	)
	save := func(a agents.Agent) agents.Agent {
		GinkgoHelper()
		a.Name, a.Model = "Helper", "m"
		out, err := svc.SaveAgent(ctx, "alice", a)
		Expect(err).NotTo(HaveOccurred())
		return out
	}
	chatOf := func(a agents.Agent) session.ChatRecord {
		GinkgoHelper()
		rec, err := svc.CreateAs(ctx, "alice", CreateInput{AgentID: a.ID}, Origin{})
		Expect(err).NotTo(HaveOccurred())
		return rec
	}
	ceiling := func(d projects.Defaults) {
		GinkgoHelper()
		_, err := ps.Update(ctx, "alice", atlas.ID, projects.UpdateInput{Defaults: &d})
		Expect(err).NotTo(HaveOccurred())
	}
	wide := agents.Agent{
		Skills: []string{"a", "b"}, Addons: []string{"x", "y"}, Collections: []string{"k1", "k2"},
		MCPServers: []string{"mail", "git", "connector:c1", "connector:c2"},
	}
	inProject := func() agents.Agent { a := wide; a.ProjectID = atlas.ID; return a }

	BeforeEach(func() {
		ctx = context.Background()
		dsn := "file:" + filepath.Join(GinkgoT().TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
		db, err := sqlitedb.Open(dsn, &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		blobs, _ := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
		Expect(err).NotTo(HaveOccurred())
		f := files.New(ws)
		st, _ = store.NewJSON("")
		ag, _ := agents.NewJSON("")
		pst, err := projects.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		ps = projects.NewService(projects.Options{Store: pst, Files: f})
		svc, err = New(Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Files: f, Agents: ag, Projects: ps,
			ProposalGate: proposals.GateFunc(func(context.Context, proposals.Request) error { return nil })})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = svc.Close(context.Background())
			if raw, err := db.DB(); err == nil {
				_ = raw.Close()
			}
		})
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		atlas, err = ps.Create(ctx, "alice", projects.CreateInput{Name: "Atlas"})
		Expect(err).NotTo(HaveOccurred())
	})

	It("offers only what the ceiling allows, per kind", func() {
		ceiling(projects.Defaults{Skills: []string{"a"}, Addons: []string{"y"}, Collections: []string{"k2"},
			MCPServers: []string{"git"}, Connectors: []string{"c1"}})
		rec := chatOf(save(inProject()))
		sc := svc.scopeOf(ctx, rec)
		Expect(sc).NotTo(BeNil())
		Expect(sc.deleted).To(BeFalse())
		Expect(sc.skillNames).To(Equal([]string{"a"}))
		Expect(sc.skills).To(Equal(map[string]bool{"a": true}), "skill b is not offered")
		Expect(sc.addons).To(Equal(map[string]bool{"y": true}), "add-on x is not callable")
		Expect(sc.collections).To(Equal(map[string]bool{"k2": true}), "collection k1 is invisible")
		Expect(sc.mcp).To(Equal([]string{"git", "connector:c1"}))
	})

	It("combines with the chat restriction: agent, project and chat", func() {
		ceiling(projects.Defaults{Collections: []string{"k1", "k2"}})
		a := wide
		a.ProjectID = atlas.ID
		a.Collections = []string{"k1", "k2", "k3"}
		rec := chatOf(save(a))
		sc := svc.scopeOf(ctx, rec)
		Expect(sc.collections).To(Equal(map[string]bool{"k1": true, "k2": true}), "k3 is outside the project")
		// The chat's own restriction narrows what is left (collections.go:
		// a name must be in rec.Collections as well as in the scope).
		rec.Collections = []string{"k2", "k3"}
		visible := []string{}
		for _, n := range rec.Collections {
			if sc.collections[n] {
				visible = append(visible, n)
			}
		}
		Expect(visible).To(Equal([]string{"k2"}))
	})

	It("narrows at once when the project is edited between turns", func() {
		rec := chatOf(save(inProject()))
		Expect(svc.scopeOf(ctx, rec).skillNames).To(Equal([]string{"a", "b"}))
		ceiling(projects.Defaults{Skills: []string{"b"}})
		Expect(svc.scopeOf(ctx, rec).skillNames).To(Equal([]string{"b"}))
		ceiling(projects.Defaults{})
		Expect(svc.scopeOf(ctx, rec).skillNames).To(Equal([]string{"a", "b"}))
	})

	It("leaves an agent of a project with no lists unchanged", func() {
		rec := chatOf(save(inProject()))
		sc := svc.scopeOf(ctx, rec)
		Expect(sc.skillNames).To(Equal(wide.Skills))
		Expect(sc.addonNames).To(Equal(wide.Addons))
		Expect(sc.collNames).To(Equal(wide.Collections))
		Expect(sc.mcp).To(Equal(wide.MCPServers))
	})

	It("does not cap connections or resources, and a plain agent is unchanged", func() {
		ceiling(projects.Defaults{Addons: []string{"y"}})
		got, err := svc.chatAgent(ctx, "alice", save(inProject()).ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Resources).To(Equal(wide.Resources))
		Expect(got.Builtins).To(Equal(wide.Builtins))
		plain, err := svc.SaveAgent(ctx, "alice", agents.Agent{Name: "Plain", Model: "m", Skills: wide.Skills, Addons: wide.Addons, Collections: wide.Collections, MCPServers: wide.MCPServers})
		Expect(err).NotTo(HaveOccurred())
		pg, err := svc.chatAgent(ctx, "alice", plain.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(pg).To(Equal(plain))
	})

	It("refuses, never falls back to the uncapped lists, when the project is gone", func() {
		a := save(inProject())
		ceiling(projects.Defaults{Skills: []string{"a"}})
		_, err := ps.Archive(ctx, "alice", atlas.ID)
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.chatAgent(ctx, "alice", a.ID)
		Expect(err).To(MatchError(ErrProjectUnavailable))
	})

	It("keeps the stored lists in the editor and the export", func() {
		ceiling(projects.Defaults{Skills: []string{"a"}, Addons: []string{"y"}, MCPServers: []string{"git"}})
		a := save(inProject())
		got, err := svc.Agent(ctx, "alice", a.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Skills).To(Equal(wide.Skills))
		Expect(got.Addons).To(Equal(wide.Addons))
		Expect(got.MCPServers).To(Equal(wide.MCPServers))
		list, err := svc.Agents(ctx, "alice")
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(1))
		Expect(list[0].Skills).To(Equal(wide.Skills))
		stored, err := svc.storedChatAgent(ctx, "alice", a.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.Skills).To(Equal(wide.Skills))
	})

	It("keeps an agent of an unavailable project a project agent, never a plain chat", func() {
		a := save(inProject())
		rec := chatOf(a)
		ceiling(projects.Defaults{Skills: []string{"a"}})
		_, err := ps.Archive(ctx, "alice", atlas.ID)
		Expect(err).NotTo(HaveOccurred())
		got, ok, err := svc.projectAgentOf(ctx, rec)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue(), "the stored record decides whether it is a project agent")
		Expect(got.ID).To(Equal(a.ID))
		Expect(got.Skills).To(Equal(wide.Skills), "the check reads the stored agent")
		fs, err := svc.agentFolderScope(ctx, rec, false)
		Expect(fs).To(BeNil())
		Expect(err).To(MatchError(ErrProjectUnavailable), "refused, not an unfenced chat")
		Expect(err.Error()).NotTo(ContainSubstring("read the chat's agent"),
			"the project check refuses, the stored agent was read: not the failed read of the capped agent")
	})

	It("gives the most restrictive memory choice to an agent that cannot run, and keeps it for a missing agent", func() {
		a := save(inProject())
		Expect(svc.agentMemory(ctx, "alice", a.ID)).To(Equal(""), "an agent that is available keeps its own choice")
		_, err := ps.Archive(ctx, "alice", atlas.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(svc.agentMemory(ctx, "alice", a.ID)).To(Equal(agents.MemoryNone), "an unavailable project: read and save nothing")
		Expect(svc.agentMemory(ctx, "alice", "missing")).To(Equal(""), "a missing agent is as before")
	})

	Describe("a saved agent run as a sub-agent inside a project", func() {
		plainWide := func(name string) agents.Agent {
			GinkgoHelper()
			a, err := svc.SaveAgent(ctx, "alice", agents.Agent{Name: name, Model: "m", Skills: wide.Skills, Addons: wide.Addons,
				Collections: wide.Collections, MCPServers: wide.MCPServers})
			Expect(err).NotTo(HaveOccurred())
			return a
		}
		kidOf := func(parent session.ChatRecord, id, agentID string) session.ChatRecord {
			GinkgoHelper()
			kid := session.ChatRecord{ID: id, Principal: "alice", Title: "Sub", Kind: session.KindSubAgent, ParentID: parent.ID, Model: "m", AgentID: agentID}
			Expect(st.CreateChat(ctx, kid)).To(Succeed())
			return kid
		}

		It("gets none of the tools outside the project ceiling", func() {
			ceiling(projects.Defaults{Skills: []string{"a"}, Addons: []string{"y"}, Collections: []string{"k2"},
				MCPServers: []string{"git"}, Connectors: []string{"c1"}})
			parent := chatOf(save(inProject()))
			kid := kidOf(parent, "kid-wide", plainWide("Plain").ID)
			sc := svc.scopeOf(ctx, kid)
			Expect(sc.deleted).To(BeFalse())
			Expect(sc.skillNames).To(Equal([]string{"a"}))
			Expect(sc.addonNames).To(Equal([]string{"y"}))
			Expect(sc.collNames).To(Equal([]string{"k2"}))
			Expect(sc.mcp).To(Equal([]string{"git", "connector:c1"}))
		})

		It("keeps what is inside the ceiling", func() {
			ceiling(projects.Defaults{Skills: []string{"a", "b"}, Addons: []string{"x", "y"}})
			parent := chatOf(save(inProject()))
			kid := kidOf(parent, "kid-in", plainWide("Plain").ID)
			sc := svc.scopeOf(ctx, kid)
			Expect(sc.skillNames).To(Equal(wide.Skills))
			Expect(sc.addonNames).To(Equal(wide.Addons))
			Expect(sc.collNames).To(Equal(wide.Collections), "no collection ceiling")
		})

		It("leaves a sub-agent with no saved agent as it is", func() {
			ceiling(projects.Defaults{Skills: []string{"a"}})
			parent := chatOf(save(inProject()))
			kid := kidOf(parent, "kid-none", "")
			Expect(svc.scopeOf(ctx, kid)).To(BeNil(), "a plain subset of the parent has no scope of its own")
		})

		It("caps the agent of the execution binding of the child", func() {
			ceiling(projects.Defaults{Addons: []string{"y"}})
			parent := chatOf(save(inProject()))
			plain := plainWide("Plain")
			kid := kidOf(parent, "kid-bind", plain.ID)
			got, err := svc.CappedAgentForChat(ctx, kid, plain)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Addons).To(Equal([]string{"y"}))
			top := chatOf(plainWide("Top"))
			got, err = svc.CappedAgentForChat(ctx, top, plain)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Addons).To(Equal(wide.Addons), "a chat outside any project is unchanged")
		})

		It("fails closed, in the chat and in the execution binding, when a store read fails", func() {
			ceiling(projects.Defaults{Addons: []string{"y"}})
			parent := chatOf(save(inProject()))
			plain := plainWide("Plain")
			kid := kidOf(parent, "kid-err", plain.ID)
			_, err := svc.CappedAgentForChat(ctx, kid, plain)
			Expect(err).NotTo(HaveOccurred(), "the healthy case is capped")

			realAgents, realChats := svc.d.Agents, svc.d.Store
			DeferCleanup(func() { svc.d.Agents, svc.d.Store = realAgents, realChats })
			cases := map[string]func(){
				"the child's agent":  func() { svc.d.Agents = failingAgents{Store: realAgents, bad: map[string]bool{plain.ID: true}} },
				"the parent chat":    func() { svc.d.Store = failingChats{Store: realChats, bad: map[string]bool{parent.ID: true}} },
				"the parent's agent": func() { svc.d.Agents = failingAgents{Store: realAgents, bad: map[string]bool{parent.AgentID: true}} },
			}
			for name, fail := range cases {
				svc.d.Agents, svc.d.Store = realAgents, realChats
				fail()
				got, err := svc.CappedAgentForChat(ctx, kid, plain)
				Expect(err).To(HaveOccurred(), "%s: the plain agent came back uncapped: %v", name, got.Addons)
				sc := svc.scopeOf(ctx, kid)
				Expect(sc.deleted).To(BeTrue(), "%s: the chat runs only with a scope that refuses", name)
			}
		})

		It("keeps the plain agent when the parent has no project agent and the reads say not found", func() {
			top := chatOf(plainWide("Top"))
			plain := plainWide("Plain")
			kid := kidOf(top, "kid-top", plain.ID)
			got, err := svc.CappedAgentForChat(ctx, kid, plain)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Addons).To(Equal(wide.Addons))
		})

		It("fails closed when the project is gone", func() {
			parent := chatOf(save(inProject()))
			kid := kidOf(parent, "kid-gone", plainWide("Plain").ID)
			_, err := ps.Archive(ctx, "alice", atlas.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.scopeOf(ctx, kid).deleted).To(BeTrue())
			_, err = svc.CappedAgentForChat(ctx, kid, agents.Agent{ID: "x"})
			Expect(err).To(MatchError(ErrProjectUnavailable))
		})
	})
})
