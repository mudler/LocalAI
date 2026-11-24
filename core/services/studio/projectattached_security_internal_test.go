package studio

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

// The attached tools are the owner's. A person who can write the project
// folder, a chat that starts from a messaging connection and a copy of a chat
// must not widen or change what the owner chose.
var _ = Describe("attached tools of a project: who may change them", func() {
	var (
		ctx   context.Context
		svc   *Service
		f     *files.Service
		ps    *projects.Service
		st    store.Store
		atlas projects.Project
	)
	admin := workspace.Actor{Principal: files.AdminPrincipal}
	home := session.HomePath("alice")
	attached := projects.Defaults{Skills: []string{"sk"}, Addons: []string{"ad"}, Connectors: []string{"cn"}, MCPServers: []string{"srv"}, Collections: []string{"col"}}

	attach := func(a projects.Defaults) {
		GinkgoHelper()
		_, err := ps.Update(ctx, "alice", atlas.ID, projects.UpdateInput{Attached: &a})
		Expect(err).NotTo(HaveOccurred())
	}
	own := func(a agents.Agent) agents.Agent {
		GinkgoHelper()
		a.Name, a.Model, a.ProjectID = "Helper", "m", atlas.ID
		out, err := svc.SaveAgent(ctx, "alice", a)
		Expect(err).NotTo(HaveOccurred())
		return out
	}
	mk := func(id string, rec session.ChatRecord) session.ChatRecord {
		GinkgoHelper()
		rec.ID, rec.Principal, rec.Title, rec.Model, rec.ProjectID = id, "alice", "t", "m", atlas.ID
		Expect(st.CreateChat(ctx, rec)).To(Succeed())
		return rec
	}
	// manifestAs replaces project.json as principal, so a grantee's edit and a
	// hand edit of the owner can be told apart from a write through the API.
	manifestAs := func(principal, text string) {
		GinkgoHelper()
		_, err := f.Store().Put(ctx, f.Actor(principal), home+"/Projects/atlas/project.json", strings.NewReader(text), workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
	}
	manifest := func(extra string) string {
		return `{"version":1,"id":"` + atlas.ID + `","name":"Atlas"` + extra + `}`
	}

	BeforeEach(func() {
		ctx = context.Background()
		dsn := "file:" + filepath.Join(GinkgoT().TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
		db, err := sqlitedb.Open(dsn, &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		blobs, _ := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
		Expect(err).NotTo(HaveOccurred())
		f = files.New(ws)
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
		Expect(f.EnsureHome(ctx, "bob")).To(Succeed())
		atlas, err = ps.Create(ctx, "alice", projects.CreateInput{Name: "Atlas"})
		Expect(err).NotTo(HaveOccurred())
	})

	Describe("a person who may write the project folder", func() {
		const planted = `,"attached":{"mcpServers":["owner-mcp"],"connectors":["c1"],"addons":["owner-addon"],"skills":["s1"]}`

		It("cannot attach the owner's tools to the agent of the project by editing project.json", func() {
			a := own(agents.Agent{})
			Expect(f.Store().Grant(ctx, admin, "bob", home+"/Projects/atlas", workspace.ModeWrite)).To(Succeed())
			manifestAs("bob", manifest(planted))
			got, err := svc.runtimeAgent(ctx, "alice", a)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.MCPServers).NotTo(ContainElement("owner-mcp"))
			Expect(got.MCPServers).NotTo(ContainElement("connector:c1"))
			Expect(got.Addons).NotTo(ContainElement("owner-addon"))
			Expect(got.Skills).NotTo(ContainElement("s1"))
		})

		It("cannot switch on the owner's tools in a chat of the project, and the read says why", func() {
			rec := mk("c1", session.ChatRecord{AddonsOff: []string{"owner-addon"}, McpOff: []string{"owner-mcp", "connector:c1"}})
			Expect(f.Store().Grant(ctx, admin, "bob", home+"/Projects/atlas", workspace.ModeWrite)).To(Succeed())
			manifestAs("bob", manifest(planted))
			got, err := svc.getChatEffective(ctx, "alice", rec.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.AddonsOff).To(Equal([]string{"owner-addon"}))
			Expect(got.McpOff).To(Equal([]string{"owner-mcp", "connector:c1"}))
			r, err := ps.Get(ctx, "alice", atlas.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.AttachedBlocked).To(BeTrue())
			Expect(r.Manifest.Attached.Empty()).To(BeTrue())
		})

		It("blocks a grant on a folder above the project too, and a group such as everyone", func() {
			attach(attached)
			Expect(f.Store().Grant(ctx, admin, "bob", home+"/Projects", workspace.ModeWrite)).To(Succeed())
			r, err := ps.Get(ctx, "alice", atlas.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.AttachedBlocked).To(BeTrue(), "a write grant on Projects/ covers the manifest")
		})

		It("does not block the owner for a read grant, and the block lifts when the grant goes", func() {
			attach(attached)
			Expect(f.Store().Grant(ctx, admin, "bob", home+"/Projects/atlas", workspace.ModeRead)).To(Succeed())
			r, err := ps.Get(ctx, "alice", atlas.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.AttachedBlocked).To(BeFalse())
			Expect(r.Manifest.Attached.Addons).To(Equal([]string{"ad"}))

			Expect(f.Store().Grant(ctx, admin, "bob", home+"/Projects/atlas", workspace.ModeWrite)).To(Succeed())
			r, err = ps.Get(ctx, "alice", atlas.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.AttachedBlocked).To(BeTrue())
			Expect(r.Manifest.Attached.Empty()).To(BeTrue())

			Expect(f.Store().Revoke(ctx, admin, "bob", home+"/Projects/atlas")).To(Succeed())
			r, err = ps.Get(ctx, "alice", atlas.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.AttachedBlocked).To(BeFalse())
			Expect(r.Manifest.Attached.Addons).To(Equal([]string{"ad"}), "the owner's own block applies again")
		})
	})

	Describe("a manifest edited by hand", func() {
		It("ignores an attached entry outside a non-empty limit, in a chat as in an agent", func() {
			manifestAs("alice", manifest(`,"defaults":{"addons":["ad"],"mcpServers":["srv"]},"attached":{"addons":["ad","outside"],"mcpServers":["srv","other"],"skills":["sk"]}`))
			rec := mk("hand", session.ChatRecord{AddonsOff: []string{"ad", "outside"}, McpOff: []string{"srv", "other"}})
			got, err := svc.getChatEffective(ctx, "alice", rec.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.AddonsOff).To(Equal([]string{"outside"}), "the entry inside the limit is on, the one outside stays off")
			Expect(got.McpOff).To(Equal([]string{"other"}))

			a := own(agents.Agent{})
			ag, err := svc.runtimeAgent(ctx, "alice", a)
			Expect(err).NotTo(HaveOccurred())
			Expect(ag.Addons).To(Equal([]string{"ad"}))
			Expect(ag.MCPServers).To(Equal([]string{"srv"}))
			Expect(ag.Skills).To(Equal([]string{"sk"}))
		})

		It("ignores a connector listed as an MCP server, and entries of a bad shape", func() {
			manifestAs("alice", manifest(`,"attached":{"mcpServers":["connector:c9","good"],"addons":["","  pad ","ok"]}`))
			rec := mk("shape", session.ChatRecord{McpOff: []string{"connector:c9", "good"}, AddonsOff: []string{"ok", "  pad "}})
			got, err := svc.getChatEffective(ctx, "alice", rec.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.McpOff).To(Equal([]string{"connector:c9"}), "a connector is not an MCP server: the connector limit cannot be skipped")
			Expect(got.AddonsOff).To(Equal([]string{"  pad "}))
		})
	})

	Describe("a chat that starts from a messaging connection", func() {
		It("gets the attached skills and collections but no connector, add-on or MCP server", func() {
			attach(attached)
			rec := mk("bot", session.ChatRecord{Origin: session.OriginConnector, OriginID: "b1",
				SkillsOff: []string{"sk"}, AddonsOff: []string{"ad"}, McpOff: []string{"srv", "connector:cn"}, Collections: []string{"mine"}})
			got, err := svc.getChatEffective(ctx, "alice", rec.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.SkillsOff).To(BeEmpty())
			Expect(got.Collections).To(Equal([]string{"mine", "col"}))
			Expect(got.AddonsOff).To(Equal([]string{"ad"}))
			Expect(got.McpOff).To(Equal([]string{"srv", "connector:cn"}))
		})

		It("gives the agent of such a chat the attached skills and collections only, and what the agent lists itself", func() {
			attach(attached)
			a := own(agents.Agent{Addons: []string{"mine"}, MCPServers: []string{"own-srv"}})
			rec, err := svc.CreateAs(ctx, "alice", CreateInput{AgentID: a.ID}, Origin{Kind: session.OriginConnector, ID: "b1"})
			Expect(err).NotTo(HaveOccurred())
			sc := svc.scopeOf(ctx, rec)
			Expect(sc.addonNames).To(Equal([]string{"mine"}))
			Expect(sc.mcp).To(Equal([]string{"own-srv"}))
			Expect(sc.skillNames).To(Equal([]string{"sk"}))
			Expect(sc.collNames).To(Equal([]string{"col"}))

			bot, err := svc.runtimeAgentVia(ctx, "alice", a, true)
			Expect(err).NotTo(HaveOccurred())
			Expect(bot.Addons).To(Equal([]string{"mine"}))
			Expect(bot.MCPServers).To(Equal([]string{"own-srv"}))

			// The same agent in a chat of the person has all of it.
			mine, err := svc.CreateAs(ctx, "alice", CreateInput{AgentID: a.ID}, Origin{})
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.scopeOf(ctx, mine).addonNames).To(ConsistOf("mine", "ad"))
		})

		It("holds for a sub-agent of such a chat", func() {
			attach(attached)
			parent := mk("botparent", session.ChatRecord{Origin: session.OriginConnector, OriginID: "b1"})
			plain, err := svc.SaveAgent(ctx, "alice", agents.Agent{Name: "Plain", Model: "m"})
			Expect(err).NotTo(HaveOccurred())
			kid := session.ChatRecord{ID: "botkid", Principal: "alice", Title: "Sub", Kind: session.KindSubAgent, ParentID: parent.ID, Model: "m", AgentID: plain.ID}
			Expect(st.CreateChat(ctx, kid)).To(Succeed())
			// The sub-agent runs a saved agent of no project inside the project of
			// its parent only when the parent is an agent chat, so give it one.
			pagent := own(agents.Agent{})
			parent2 := mk("botparent2", session.ChatRecord{Origin: session.OriginConnector, OriginID: "b1", AgentID: pagent.ID})
			kid2 := session.ChatRecord{ID: "botkid2", Principal: "alice", Title: "Sub", Kind: session.KindSubAgent, ParentID: parent2.ID, Model: "m", AgentID: plain.ID}
			Expect(st.CreateChat(ctx, kid2)).To(Succeed())
			Expect(svc.viaConnector(ctx, kid2)).To(BeTrue())
			Expect(svc.scopeOf(ctx, kid2).addonNames).To(BeEmpty())
			Expect(svc.viaConnector(ctx, kid)).To(BeTrue())
		})
	})

	Describe("a copy of a chat", func() {
		It("takes the switches the chat chose, not the attached tools, in a trigger run", func() {
			rec := mk("src", session.ChatRecord{})
			Expect(st.SetMcpOff(ctx, "alice", rec.ID, []string{"connector:c1"})).To(Succeed())
			Expect(st.SetAddonsOff(ctx, "alice", rec.ID, []string{"ad", "mine"})).To(Succeed())
			Expect(st.SetSkillsOff(ctx, "alice", rec.ID, []string{"sk", "mine-skill"})).To(Succeed())
			attach(projects.Defaults{Connectors: []string{"c1"}, Addons: []string{"ad"}, Skills: []string{"sk"}})
			h := &Triggers{svc: svc}
			_, skillsOff, addonsOff, mcpOff, err := h.runSettings(ctx, triggers.Trigger{Owner: "alice", SourceChatID: rec.ID, Model: "m"})
			Expect(err).NotTo(HaveOccurred())
			Expect(mcpOff).To(ContainElement("connector:c1"), "the stored off entry carries to the chat that has no project")
			Expect(addonsOff).To(Equal([]string{"ad", "mine"}))
			Expect(skillsOff).To(Equal([]string{"sk", "mine-skill"}))
		})

		It("keeps the off entries and the collection restriction of the chat in a fork", func() {
			rec := mk("forksrc", session.ChatRecord{Collections: []string{"mine"}})
			Expect(st.SetMcpOff(ctx, "alice", rec.ID, []string{"connector:c1"})).To(Succeed())
			Expect(st.SetAddonsOff(ctx, "alice", rec.ID, []string{"ad"})).To(Succeed())
			attach(projects.Defaults{Connectors: []string{"c1"}, Addons: []string{"ad"}, Collections: []string{"col"}})
			_, err := st.AppendEntry(ctx, rec.ID, session.Entry{Turn: 1, Role: session.RoleUser, Text: "hello"})
			Expect(err).NotTo(HaveOccurred())
			fork, err := svc.Fork(ctx, "alice", rec.ID, 0, "")
			Expect(err).NotTo(HaveOccurred())
			stored, err := st.GetChat(ctx, "alice", fork.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(stored.McpOff).To(Equal([]string{"connector:c1"}))
			Expect(stored.AddonsOff).To(Equal([]string{"ad"}))
			Expect(stored.Collections).To(Equal([]string{"mine"}), "the attached collection is not baked in as a restriction")
			// The fork is in the same project, so it still gets the attached tools live.
			eff, err := svc.getChatEffective(ctx, "alice", fork.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(eff.AddonsOff).To(BeEmpty())
		})
	})

	// One case per kind: the chat of a person who is not the owner of the
	// project keeps its own switches, whatever the project attaches.
	DescribeTable("a chat of another person is not changed, for each kind",
		func(in projects.Defaults, rec session.ChatRecord, check func(session.ChatRecord)) {
			attach(in)
			rec.ID, rec.Principal, rec.Title, rec.Model, rec.ProjectID = "bobs-"+rec.ID, "bob", "t", "m", atlas.ID
			Expect(st.CreateChat(ctx, rec)).To(Succeed())
			got, err := svc.getChatEffective(ctx, "bob", rec.ID)
			Expect(err).NotTo(HaveOccurred())
			check(got)
		},
		Entry("add-on", projects.Defaults{Addons: []string{"ad"}}, session.ChatRecord{ID: "a", AddonsOff: []string{"ad"}},
			func(r session.ChatRecord) { Expect(r.AddonsOff).To(Equal([]string{"ad"})) }),
		Entry("connector", projects.Defaults{Connectors: []string{"cn"}}, session.ChatRecord{ID: "c", McpOff: []string{"connector:cn"}},
			func(r session.ChatRecord) { Expect(r.McpOff).To(Equal([]string{"connector:cn"})) }),
		Entry("MCP server", projects.Defaults{MCPServers: []string{"srv"}}, session.ChatRecord{ID: "m", McpOff: []string{"srv"}},
			func(r session.ChatRecord) { Expect(r.McpOff).To(Equal([]string{"srv"})) }),
		Entry("skill", projects.Defaults{Skills: []string{"sk"}}, session.ChatRecord{ID: "s", SkillsOff: []string{"sk"}},
			func(r session.ChatRecord) { Expect(r.SkillsOff).To(Equal([]string{"sk"})) }),
		Entry("collection", projects.Defaults{Collections: []string{"col"}}, session.ChatRecord{ID: "k", Collections: []string{"mine"}},
			func(r session.ChatRecord) { Expect(r.Collections).To(Equal([]string{"mine"})) }),
	)

	It("names connectors with the prefix the manifest check forbids in the MCP list", func() {
		Expect(projects.CheckAttached(projects.Defaults{}, projects.Defaults{MCPServers: []string{mcpconns.Prefix + "x"}})).To(HaveOccurred())
	})

	Describe("what a chat may switch off", func() {
		It("cannot switch off an attached skill, and an exact layer switch of it is dropped too", func() {
			rec := mk("skoff", session.ChatRecord{SkillsOff: []string{"sk", "personal:sk", "free"}})
			attach(projects.Defaults{Skills: []string{"sk"}})
			got, err := svc.getChatEffective(ctx, "alice", rec.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.SkillsOff).To(Equal([]string{"free"}))
		})

		It("never switches off a skill of the project layer, and a plain switch is kept for the one below", func() {
			off := skillOff([]string{"plan", "project:plan", "other"})
			Expect(off(skills.Skill{Name: "plan", Layer: skills.LayerProject})).To(BeFalse())
			Expect(off(skills.Skill{Name: "other", Layer: skills.LayerProject})).To(BeFalse())
			Expect(off(skills.Skill{Name: "plan", Layer: skills.LayerPersonal})).To(BeTrue(), "the plain switch still turns off the personal skill below")
			Expect(off(skills.Skill{Name: "other", Layer: skills.LayerPersonal})).To(BeTrue())
		})
	})

	Describe("reading the project for a chat", func() {
		It("reads the project once for repeated reads in a turn, and again after a write of this process", func() {
			rec := mk("cache", session.ChatRecord{AddonsOff: []string{"ad"}})
			attach(attached)
			before := svc.attached.loads.Load()
			for range 25 {
				got, err := svc.getChatEffective(ctx, "alice", rec.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(got.AddonsOff).To(BeEmpty())
			}
			Expect(svc.attached.loads.Load() - before).To(BeEquivalentTo(1))
			attach(projects.Defaults{})
			got, err := svc.getChatEffective(ctx, "alice", rec.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.AddonsOff).To(Equal([]string{"ad"}), "a detach through the service is seen at once")
			Expect(svc.attached.loads.Load() - before).To(BeEquivalentTo(2))
		})

		It("does not read the project for a chat that has none", func() {
			plain := session.ChatRecord{ID: "noproj", Principal: "alice", Title: "t", Model: "m"}
			Expect(st.CreateChat(ctx, plain)).To(Succeed())
			before := svc.attached.loads.Load()
			for range 5 {
				_, err := svc.getChatEffective(ctx, "alice", plain.ID)
				Expect(err).NotTo(HaveOccurred())
			}
			Expect(svc.attached.loads.Load()).To(Equal(before))
		})
	})

	// Every reader of a chat record goes through getChatRaw (copies into a
	// store write) or getChatEffective (decisions about tools). A read of the
	// store that skips both gives a decision that ignores the attached tools,
	// or a copy that bakes them in. The walk covers the whole core/ tree and
	// every receiver of the call.
	It("lets no code under core/ read a chat record outside the two readers", func() {
		rawRead := regexp.MustCompile(`\.GetChat\(`)
		allowed := map[string]bool{
			"projectattached.go": true, // getChatRaw: the one raw read of the studio service
		}
		// effectiveChat reads the raw store only when the application has no
		// studio service, where no chat has a project.
		const appFallback = "application/studio_service.go"
		// Reads that decide nothing about tools: the store implementations and
		// their conformance tests, and the session layer that loads transcripts
		// and state, never the switch lists.
		skipDir := []string{"/store/", "/session/"}
		root := filepath.Join("..", "..") // core/
		var offenders []string
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			slash := filepath.ToSlash(path)
			if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			for _, d := range skipDir {
				if strings.Contains(slash, "core/services/studio"+d) || strings.Contains(slash, "services/studio"+d) {
					return nil
				}
			}
			if allowed[filepath.Base(path)] && strings.Contains(slash, "services/studio/") {
				return nil
			}
			if strings.HasSuffix(slash, appFallback) {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if rawRead.Match(b) {
				offenders = append(offenders, slash)
			}
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(offenders).To(BeEmpty(), "read a chat through Service.getChatEffective, EffectiveChat or getChatRaw, never Store.GetChat")
	})
})
