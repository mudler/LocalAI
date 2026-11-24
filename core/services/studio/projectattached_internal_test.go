package studio

import (
	"context"
	"path/filepath"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

var _ = Describe("attached tools of a project", func() {
	var (
		ctx   context.Context
		svc   *Service
		ps    *projects.Service
		st    store.Store
		atlas projects.Project
	)
	attached := projects.Defaults{Skills: []string{"sk"}, Addons: []string{"ad"}, Connectors: []string{"cn"}, MCPServers: []string{"srv"}, Collections: []string{"col"}}
	attach := func(a projects.Defaults) {
		GinkgoHelper()
		_, err := ps.Update(ctx, "alice", atlas.ID, projects.UpdateInput{Attached: &a})
		Expect(err).NotTo(HaveOccurred())
	}
	limit := func(d projects.Defaults) {
		GinkgoHelper()
		_, err := ps.Update(ctx, "alice", atlas.ID, projects.UpdateInput{Defaults: &d})
		Expect(err).NotTo(HaveOccurred())
	}
	own := func(a agents.Agent) agents.Agent {
		GinkgoHelper()
		a.Name, a.Model, a.ProjectID = "Helper", "m", atlas.ID
		out, err := svc.SaveAgent(ctx, "alice", a)
		Expect(err).NotTo(HaveOccurred())
		return out
	}

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

	Describe("an agent of the project", func() {
		It("has the attached tools of every kind when the owner runs it, read live on each turn", func() {
			a := own(agents.Agent{})
			sc := func() *scope {
				rec, err := svc.CreateAs(ctx, "alice", CreateInput{AgentID: a.ID}, Origin{})
				Expect(err).NotTo(HaveOccurred())
				return svc.scopeOf(ctx, rec)
			}
			Expect(sc().addonNames).To(BeEmpty())
			attach(attached)
			got := sc()
			Expect(got.skillNames).To(Equal([]string{"sk"}))
			Expect(got.addonNames).To(Equal([]string{"ad"}))
			Expect(got.collNames).To(Equal([]string{"col"}))
			Expect(got.mcp).To(ConsistOf("srv", "connector:cn"))
			attach(projects.Defaults{})
			Expect(sc().addonNames).To(BeEmpty(), "a detach reaches the next turn")
		})

		It("keeps its own tools and adds the attached ones without a repeat", func() {
			attach(attached)
			a := own(agents.Agent{Addons: []string{"mine", "ad"}, MCPServers: []string{"other"}})
			got, err := svc.chatAgent(ctx, "alice", a.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Addons).To(Equal([]string{"mine", "ad"}))
			Expect(got.MCPServers).To(Equal([]string{"other", "srv", "connector:cn"}))
			stored, err := svc.storedChatAgent(ctx, "alice", a.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(stored.Addons).To(Equal([]string{"mine", "ad"}), "the stored agent is not changed")
		})

		It("is cut to a non-empty limit, which still removes what is outside it", func() {
			attach(attached)
			limit(projects.Defaults{Addons: []string{"ad"}, MCPServers: []string{"srv"}, Connectors: []string{"cn"}})
			a := own(agents.Agent{Addons: []string{"mine", "ad"}, MCPServers: []string{"other", "connector:zz"}})
			got, err := svc.chatAgent(ctx, "alice", a.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Addons).To(Equal([]string{"ad"}))
			Expect(got.MCPServers).To(ConsistOf("srv", "connector:cn"))
			Expect(got.Collections).To(Equal([]string{"col"}), "no collection limit")
		})

		// One case per kind: a person the agent is shared with is not the owner.
		DescribeTable("never gets an attached tool when a grantee is the actor",
			func(kind string, in projects.Defaults, has func(agents.Agent) []string, want string) {
				attach(in)
				a := own(agents.Agent{})
				stored, err := svc.storedChatAgent(ctx, "alice", a.ID)
				Expect(err).NotTo(HaveOccurred())

				as, err := svc.runtimeAgent(ctx, "bob", stored)
				Expect(err).NotTo(HaveOccurred())
				Expect(has(as)).NotTo(ContainElement(want), kind+" reached a grantee")

				rec := session.ChatRecord{ID: "g-" + kind, Principal: "bob", Title: "t", Model: "m", AgentID: a.ID}
				capped, err := svc.cappedForChat(ctx, rec, stored)
				Expect(err).NotTo(HaveOccurred())
				Expect(has(capped)).NotTo(ContainElement(want), kind+" reached a grantee chat")

				owner, err := svc.runtimeAgent(ctx, "alice", stored)
				Expect(err).NotTo(HaveOccurred())
				Expect(has(owner)).To(ContainElement(want), kind+" is missing for the owner")
			},
			Entry("add-on", "add-on", projects.Defaults{Addons: []string{"ad"}}, func(a agents.Agent) []string { return a.Addons }, "ad"),
			Entry("connector", "connector", projects.Defaults{Connectors: []string{"cn"}}, func(a agents.Agent) []string { return a.MCPServers }, "connector:cn"),
			Entry("MCP server", "mcp", projects.Defaults{MCPServers: []string{"srv"}}, func(a agents.Agent) []string { return a.MCPServers }, "srv"),
			Entry("collection", "collection", projects.Defaults{Collections: []string{"col"}}, func(a agents.Agent) []string { return a.Collections }, "col"),
			Entry("skill", "skill", projects.Defaults{Skills: []string{"sk"}}, func(a agents.Agent) []string { return a.Skills }, "sk"),
		)

		It("gets them in a run that a trigger or a connector starts, which reads the same scope", func() {
			attach(attached)
			a := own(agents.Agent{})
			rec, err := svc.CreateAs(ctx, "alice", CreateInput{AgentID: a.ID}, Origin{Kind: session.OriginTrigger, ID: "x"})
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.scopeOf(ctx, rec).addonNames).To(Equal([]string{"ad"}))
			// A messaging connection gets no add-on of the project (see the
			// specs of the messaging connection).
		})

		It("gives a saved agent that runs as a sub-agent in the project the attached tools, for the owner only", func() {
			attach(attached)
			parent, err := svc.CreateAs(ctx, "alice", CreateInput{AgentID: own(agents.Agent{}).ID}, Origin{})
			Expect(err).NotTo(HaveOccurred())
			plain, err := svc.SaveAgent(ctx, "alice", agents.Agent{Name: "Plain", Model: "m"})
			Expect(err).NotTo(HaveOccurred())
			kid := session.ChatRecord{ID: "kid", Principal: "alice", Title: "Sub", Kind: session.KindSubAgent, ParentID: parent.ID, Model: "m", AgentID: plain.ID}
			Expect(st.CreateChat(ctx, kid)).To(Succeed())
			Expect(svc.scopeOf(ctx, kid).addonNames).To(Equal([]string{"ad"}))
		})
	})

	Describe("a chat of the project", func() {
		mk := func(principal, id string, rec session.ChatRecord) session.ChatRecord {
			GinkgoHelper()
			rec.ID, rec.Principal, rec.Title, rec.Model, rec.ProjectID = id, principal, "t", "m", atlas.ID
			Expect(st.CreateChat(ctx, rec)).To(Succeed())
			return rec
		}

		It("has the attached tools on in an old chat that switched them off, on its next turn", func() {
			old := mk("alice", "old", session.ChatRecord{
				SkillsOff: []string{"sk", "keep-off"}, AddonsOff: []string{"ad", "keep-off"},
				McpOff: []string{"srv", "connector:cn", "connector:other"}, Collections: []string{"mine"}})
			attach(attached)
			got, err := svc.getChatEffective(ctx, "alice", old.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.SkillsOff).To(Equal([]string{"keep-off"}))
			Expect(got.AddonsOff).To(Equal([]string{"keep-off"}))
			Expect(got.McpOff).To(Equal([]string{"connector:other"}))
			Expect(got.Collections).To(Equal([]string{"mine", "col"}), "attached collections become searchable")
			raw, err := st.GetChat(ctx, "alice", old.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(raw.AddonsOff).To(Equal([]string{"ad", "keep-off"}), "the stored record is not changed")
			attach(projects.Defaults{})
			got, _ = svc.getChatEffective(ctx, "alice", old.ID)
			Expect(got.AddonsOff).To(Equal([]string{"ad", "keep-off"}), "a detach restores the chat's own choice")
		})

		It("leaves a chat with no collection restriction searching all of them", func() {
			rec := mk("alice", "free", session.ChatRecord{})
			attach(attached)
			got, err := svc.getChatEffective(ctx, "alice", rec.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Collections).To(BeEmpty())
		})

		It("is not changed for a person who is not the owner of the project", func() {
			rec := mk("bob", "bobs", session.ChatRecord{AddonsOff: []string{"ad"}, McpOff: []string{"srv", "connector:cn"}, Collections: []string{"x"}})
			attach(attached)
			got, err := svc.getChatEffective(ctx, "bob", rec.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.AddonsOff).To(Equal([]string{"ad"}))
			Expect(got.McpOff).To(Equal([]string{"srv", "connector:cn"}))
			Expect(got.Collections).To(Equal([]string{"x"}))
		})

		It("is not changed by an archived project or one that is gone", func() {
			rec := mk("alice", "arch", session.ChatRecord{AddonsOff: []string{"ad"}})
			attach(attached)
			_, err := ps.Archive(ctx, "alice", atlas.ID)
			Expect(err).NotTo(HaveOccurred())
			got, err := svc.getChatEffective(ctx, "alice", rec.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.AddonsOff).To(Equal([]string{"ad"}))
		})
	})
})
