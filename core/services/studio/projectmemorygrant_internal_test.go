package studio

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

var _ = Describe("the project.memory.write grant", func() {
	var (
		ctx   context.Context
		svc   *Service
		f     *files.Service
		ps    *projects.Service
		st    store.Store
		atlas projects.Project
	)
	const open = "To save a note, propose a change in the project scope; the user confirms it."
	const closed = "you cannot save notes in the project"
	home := session.HomePath("alice")

	agentWith := func(memory, memorySave string, grants ...string) agents.Agent {
		GinkgoHelper()
		a, err := svc.SaveAgent(ctx, "alice", agents.Agent{Name: "Helper " + memory + strings.Join(grants, ","), Model: "m", ProjectID: atlas.ID,
			Memory: memory, MemorySave: memorySave, Builtins: append([]string{"act.files"}, grants...)})
		Expect(err).NotTo(HaveOccurred())
		return a
	}
	chatOf := func(a agents.Agent) session.ChatRecord {
		GinkgoHelper()
		rec, err := svc.CreateAs(ctx, "alice", CreateInput{AgentID: a.ID}, Origin{})
		Expect(err).NotTo(HaveOccurred())
		return rec
	}
	turn := func(rec session.ChatRecord) (session.TurnTools, session.Access) {
		GinkgoHelper()
		x, err := svc.ChatAccess(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		tt, err := svc.chatTools(ctx, x)
		Expect(err).NotTo(HaveOccurred())
		return tt, x
	}
	propose := func(tt session.TurnTools) *session.Tool {
		for i := range tt.Tools {
			if tt.Tools[i].Name == "propose_change" {
				return &tt.Tools[i]
			}
		}
		return nil
	}
	scopesOf := func(t *session.Tool) []string {
		GinkgoHelper()
		var sch struct {
			Properties struct {
				Scope struct {
					Enum []string `json:"enum"`
				} `json:"scope"`
			} `json:"properties"`
		}
		Expect(json.Unmarshal(t.Schema, &sch)).To(Succeed())
		return sch.Properties.Scope.Enum
	}
	layoutOf := func(x session.Access) string { return session.LayoutPrompt(x.View(), session.PromptScope(x.View())) }
	call := func(tool *session.Tool, args map[string]any) (string, error) {
		GinkgoHelper()
		raw, _ := json.Marshal(args)
		return tool.Run(ctx, raw)
	}
	note := map[string]any{"kind": "memory", "scope": "project", "target": "goal.md", "content": "- ship it\n", "summary": "Goal"}

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
		atlas, err = ps.Create(ctx, "alice", projects.CreateInput{Name: "Atlas"})
		Expect(err).NotTo(HaveOccurred())
	})

	It("is on the closed list, plain and not sensitive, and usable on a server with files", func() {
		g, ok := builtins.LookupGrant("project.memory.write")
		Expect(ok).To(BeTrue())
		Expect(g.Name).To(Equal(builtins.GrantProjectMemoryWrite))
		Expect(g.Title).To(Equal("Remember things in the project"))
		Expect(g.Description).NotTo(BeEmpty())
		Expect(g.Sensitive).To(BeFalse())
		Expect(svc.grantUsable(ctx, "alice", builtins.GrantProjectMemoryWrite)).To(BeTrue())
	})

	It("is kept by an import and dropped by one that does not know it", func() {
		got, err := cleanGrants([]string{"project.memory.write", "future.grant"}, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal([]string{"project.memory.write"}))
		_, err = cleanGrants([]string{"future.grant"}, false)
		Expect(err).To(MatchError(ErrUnknownGrant))
	})

	Describe("a project agent without the grant", func() {
		It("has no project scope, a refusal for a direct call and the closed wording", func() {
			rec := chatOf(agentWith("", "", "memory.write"))
			tt, x := turn(rec)
			t := propose(tt)
			Expect(t).NotTo(BeNil())
			Expect(scopesOf(t)).NotTo(ContainElement("project"))
			_, err := call(t, note)
			Expect(err).To(HaveOccurred())
			Expect(layoutOf(x)).To(ContainSubstring(closed))
			Expect(layoutOf(x)).NotTo(ContainSubstring("propose a change"))
			Expect(svc.Changes().List(ctx, "alice")).To(BeEmpty())
		})
	})

	Describe("a project agent with the grant", func() {
		for _, extra := range [][]string{{"project.memory.write"}, {"memory.write", "project.memory.write"}} {
			extra := extra
			It("offers the project scope and keeps a note as a pending proposal ("+strings.Join(extra, ",")+")", func() {
				rec := chatOf(agentWith("", "", extra...))
				tt, x := turn(rec)
				t := propose(tt)
				Expect(t).NotTo(BeNil())
				Expect(scopesOf(t)).To(ContainElement("project"))
				Expect(layoutOf(x)).To(ContainSubstring(open))
				Expect(layoutOf(x)).NotTo(ContainSubstring(closed))
				out, err := call(t, note)
				Expect(err).NotTo(HaveOccurred())
				Expect(out).To(ContainSubstring("Nothing is saved yet"))
				list, err := svc.Changes().List(ctx, "alice")
				Expect(err).NotTo(HaveOccurred())
				Expect(list).To(HaveLen(1))
				Expect(list[0].Status).To(Equal(contract.ProposalPending))
				Expect(list[0].Scope).To(Equal(contract.ScopeProject))
				Expect(list[0].Target).To(Equal("Projects/atlas/memory/goal.md"))
				_, ok, err := f.ReadHome(ctx, "alice", "Projects/atlas/memory/goal.md", 1<<20)
				Expect(err).NotTo(HaveOccurred())
				Expect(ok).To(BeFalse(), "nothing is written before the owner confirms")
				done, err := svc.Changes().Confirm(ctx, "alice", list[0].ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(done.Status).To(Equal(contract.ProposalApplied))
				text, ok, err := f.ReadHome(ctx, "alice", "Projects/atlas/memory/goal.md", 1<<20)
				Expect(err).NotTo(HaveOccurred())
				Expect(ok).To(BeTrue())
				Expect(text).To(ContainSubstring("ship it"))
				_, err = svc.Changes().Revert(ctx, "alice", done.ID)
				Expect(err).NotTo(HaveOccurred(), "the write is a change set the owner can undo")
			})
		}

		It("is never applied at once, even when saving at once is on", func() {
			tt, _ := turn(chatOf(agentWith("", agents.MemorySaveAuto, "project.memory.write")))
			_, err := call(propose(tt), note)
			Expect(err).NotTo(HaveOccurred())
			list, _ := svc.Changes().List(ctx, "alice")
			Expect(list).To(HaveLen(1))
			Expect(list[0].Status).To(Equal(contract.ProposalPending))
		})

		It("is refused when the agent keeps no notes", func() {
			tt, x := turn(chatOf(agentWith(agents.MemoryNone, "", "memory.write", "project.memory.write")))
			if t := propose(tt); t != nil {
				Expect(scopesOf(t)).NotTo(ContainElement("project"))
				_, err := call(t, note)
				Expect(err).To(HaveOccurred())
			}
			Expect(layoutOf(x)).To(ContainSubstring(closed))
			Expect(svc.Changes().List(ctx, "alice")).To(BeEmpty())
		})

		It("sees the closed wording when saving is turned off for the agent", func() {
			tt, x := turn(chatOf(agentWith("", agents.MemorySaveNever, "memory.write", "project.memory.write")))
			if t := propose(tt); t != nil {
				Expect(scopesOf(t)).NotTo(ContainElement("project"))
			}
			Expect(layoutOf(x)).To(ContainSubstring(closed))
		})

		It("writes only its own project: an argument that names another is refused", func() {
			beta, err := ps.Create(ctx, "alice", projects.CreateInput{Name: "Beta"})
			Expect(err).NotTo(HaveOccurred())
			tt, _ := turn(chatOf(agentWith("", "", "project.memory.write")))
			args := map[string]any{"project": beta.ID}
			for k, v := range note {
				args[k] = v
			}
			_, err = call(propose(tt), args)
			Expect(err).To(HaveOccurred(), "a project argument is only for a project shared with the person")
			args = map[string]any{"project_id": beta.ID, "project_dir": "beta"}
			for k, v := range note {
				args[k] = v
			}
			_, err = call(propose(tt), args)
			Expect(err).NotTo(HaveOccurred(), "other unknown arguments change nothing")
			list, _ := svc.Changes().List(ctx, "alice")
			Expect(list).To(HaveLen(1))
			Expect(list[0].Target).To(Equal("Projects/atlas/memory/goal.md"))
			_, err = call(propose(tt), map[string]any{"kind": "memory", "scope": "project", "target": "../beta/memory/x.md", "content": "x", "summary": "s"})
			Expect(err).To(HaveOccurred())
		})

		It("leaves a sub-agent of it with the closed wording and no scope", func() {
			parent := chatOf(agentWith("", "", "memory.write", "project.memory.write"))
			kid := session.ChatRecord{ID: parent.ID + "-sub", Principal: "alice", Title: "Sub", Kind: session.KindSubAgent, ParentID: parent.ID, Model: "m", ScopedAgent: true}
			Expect(st.CreateChat(ctx, kid)).To(Succeed())
			c := svc.builtinChat(ctx, userChatActor("alice", kid.ID), kid, true)
			Expect(svc.projectScopeOpen(ctx, c, svc.memoryChatScopes(ctx, c))).To(BeFalse())
			Expect(svc.chatProjectScopeOpen(ctx, kid)).To(BeFalse())
			x, err := svc.ChatAccess(ctx, "alice", kid.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(layoutOf(x)).To(ContainSubstring(closed))
			Expect(layoutOf(x)).NotTo(ContainSubstring("propose a change"))
		})

		It("gives a messaging chat of an unpaired sender no project scope", func() {
			rec := chatOf(agentWith("", "", "memory.write", "project.memory.write"))
			Expect(svc.chatProjectScopeOpen(ctx, rec)).To(BeTrue())
			c := svc.builtinChat(ctx, userChatActor("alice", rec.ID), rec, false)
			c.Origin = session.OriginConnector
			Expect(svc.projectScopeOpen(ctx, c, svc.memoryChatScopes(ctx, c))).To(BeFalse())
			c.Origin = session.OriginTrigger
			Expect(svc.projectScopeOpen(ctx, c, svc.memoryChatScopes(ctx, c))).To(BeFalse())
		})
	})

	Describe("an agent that holds only the project grant", func() {
		descOf := func(tt session.TurnTools) string {
			for _, t := range tt.Tools {
				if t.Name == "propose_change" {
					return t.Description
				}
			}
			return ""
		}

		It("is offered the scope project and no other scope, in the schema and in the words", func() {
			tt, _ := turn(chatOf(agentWith("", "", "project.memory.write")))
			t := propose(tt)
			Expect(t).NotTo(BeNil())
			Expect(scopesOf(t)).To(Equal([]string{"project"}))
			Expect(descOf(tt)).NotTo(ContainSubstring("every chat of the person"))
			Expect(descOf(tt)).NotTo(ContainSubstring("this conversation only"))
			Expect(descOf(tt)).To(ContainSubstring("scope project"))
		})

		DescribeTable("refuses a note for another scope as not granted and keeps no proposal",
			func(scope, save string) {
				tt, _ := turn(chatOf(agentWith("", save, "project.memory.write")))
				_, err := call(propose(tt), map[string]any{"kind": "memory", "scope": scope, "target": "x.md", "content": "x\n", "summary": "s"})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("not given the right"))
				Expect(err.Error()).To(ContainSubstring("memory.write"))
				Expect(svc.Changes().List(ctx, "alice")).To(BeEmpty())
			},
			Entry("user", "user", ""),
			Entry("agent", "agent", ""),
			Entry("channel", "channel", ""),
			Entry("user, saving at once on", "user", agents.MemorySaveAuto),
			Entry("agent, saving at once on", "agent", agents.MemorySaveAuto),
			Entry("channel, saving at once on", "channel", agents.MemorySaveAuto),
		)
	})

	It("keeps the usual scopes for an agent with memory.write, and adds project when it has both grants", func() {
		tt, _ := turn(chatOf(agentWith("", "", "memory.write")))
		Expect(scopesOf(propose(tt))).To(Equal([]string{"user", "agent", "channel"}))
		tt, _ = turn(chatOf(agentWith("", "", "memory.write", "project.memory.write")))
		Expect(scopesOf(propose(tt))).To(Equal([]string{"user", "agent", "channel", "project"}))
	})

	It("gives a plain fenced agent with the grant listed but no project no project scope", func() {
		_, err := f.Store().Mkdir(ctx, f.Actor("alice"), home+"/Work")
		Expect(err).NotTo(HaveOccurred())
		a, err := svc.SaveAgentWithFolder(ctx, "alice", agents.Agent{Name: "Scoped", Model: "m", Builtins: []string{"memory.write", "project.memory.write"}},
			&AgentFolderWrite{Path: "/Work", Mode: agents.FolderWrite})
		Expect(err).NotTo(HaveOccurred())
		rec := chatOf(a)
		Expect(st.SetProject(ctx, "alice", rec.ID, atlas.ID)).To(Succeed()) // a stale record
		tt, _ := turn(rec)
		if t := propose(tt); t != nil {
			Expect(scopesOf(t)).NotTo(ContainElement("project"))
		}
	})

	It("leaves the unscoped project chat as it was: no grant needed", func() {
		rec, err := svc.CreateAs(ctx, "alice", CreateInput{Model: "m", ProjectID: atlas.ID}, Origin{})
		Expect(err).NotTo(HaveOccurred())
		tt, _ := turn(rec)
		t := propose(tt)
		Expect(t).NotTo(BeNil())
		Expect(scopesOf(t)).To(ContainElement("project"))
	})
})
