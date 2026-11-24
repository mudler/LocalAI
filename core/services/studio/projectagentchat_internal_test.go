package studio

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

var _ = Describe("project context in the chats of a project agent", func() {
	var (
		ctx   context.Context
		svc   *Service
		f     *files.Service
		ps    *projects.Service
		st    store.Store
		atlas projects.Project
	)
	home := session.HomePath("alice")
	put := func(rel, text string) {
		GinkgoHelper()
		_, err := f.Store().Put(ctx, f.Actor("alice"), home+"/"+rel, strings.NewReader(text), workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
	}
	agentOf := func(p projects.Project, memory string) agents.Agent {
		GinkgoHelper()
		a, err := svc.SaveAgent(ctx, "alice", agents.Agent{Name: "Helper " + memory, Model: "m", ProjectID: p.ID, Memory: memory, Builtins: []string{"act.files"}})
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
		atlas, err = ps.Create(ctx, "alice", projects.CreateInput{Name: "Atlas", Description: "Map the\n  world"})
		Expect(err).NotTo(HaveOccurred())
		put("Memory/index.md", "- home: lives in Rome -> home.md\n")
		put("Projects/atlas/memory/index.md", "- goal: ship the atlas -> goal.md\n")
	})

	It("records the project of the agent on the chat", func() {
		rec := chatOf(agentOf(atlas, ""))
		got, err := st.GetChat(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ProjectID).To(Equal(atlas.ID))
		Expect(got.ScopedAgent).To(BeTrue())
	})

	It("accepts the same project id on create and refuses another", func() {
		a := agentOf(atlas, "")
		_, err := svc.CreateAs(ctx, "alice", CreateInput{AgentID: a.ID, ProjectID: atlas.ID}, Origin{})
		Expect(err).NotTo(HaveOccurred())
		beta, err := ps.Create(ctx, "alice", projects.CreateInput{Name: "Beta"})
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.CreateAs(ctx, "alice", CreateInput{AgentID: a.ID, ProjectID: beta.ID}, Origin{})
		Expect(errors.Is(err, session.ErrInvalid)).To(BeTrue(), "%v", err)
	})

	It("puts the project index and the project line in the prompt, and names both roots", func() {
		rec := chatOf(agentOf(atlas, ""))
		tt, x := turn(rec)
		Expect(tt.Prompt).To(ContainSubstring("[project notes"))
		Expect(tt.Prompt).To(ContainSubstring("ship the atlas"))
		Expect(strings.Count(tt.Prompt, "ship the atlas")).To(Equal(1), "the index is loaded once")
		layout := session.LayoutPrompt(x.View(), session.PromptScope(x.View()))
		Expect(layout).To(ContainSubstring(`This chat belongs to the project "Atlas". Project description: Map the world`))
		Expect(layout).To(ContainSubstring("/workspace/home holds the files of the project"))
		Expect(layout).To(ContainSubstring("/workspace/folder-memory holds the memory of the project"))
		Expect(layout).To(ContainSubstring("read only"))
		Expect(layout).To(ContainSubstring("you cannot save notes in the project"))
		Expect(layout).NotTo(ContainSubstring("propose a change"), "the project scope is closed")
	})

	It("names the roots by node, and offers a change only when the scope is open", func() {
		a := agentOf(atlas, "")
		rec := chatOf(a)
		scopes, _, err := svc.chatScopes(ctx, rec, false)
		Expect(err).NotTo(HaveOccurred())
		work := scopes[len(scopes)-1]
		fence, err := svc.fenceOfAgent(ctx, a)
		Expect(err).NotTo(HaveOccurred())
		Expect(work.folders[0].node).To(Equal(fence.files.Node), "the first folder is the files folder")
		Expect(work.folders[1].node).To(Equal(fence.memory.Node), "the second is the memory folder")
		open := strings.Join(svc.projectNotes(ctx, rec, work, func() bool { return true }), "\n")
		Expect(open).To(ContainSubstring("To save a note, propose a change in the project scope; the user confirms it."))
		Expect(open).To(ContainSubstring("/workspace/folder-memory holds the memory of the project"))
		closed := strings.Join(svc.projectNotes(ctx, rec, work, func() bool { return false }), "\n")
		Expect(closed).To(ContainSubstring("you cannot save notes in the project"))
		Expect(closed).NotTo(ContainSubstring("propose a change"))
	})

	It("reads the project index even when the agent keeps no notes of its own", func() {
		tt, _ := turn(chatOf(agentOf(atlas, agents.MemoryNone)))
		Expect(tt.Prompt).To(ContainSubstring("ship the atlas"))
		Expect(tt.Prompt).NotTo(ContainSubstring("lives in Rome"), "the personal notes stay off")
	})

	It("refuses SetChatProject and SetChatPulled on such a chat", func() {
		rec := chatOf(agentOf(atlas, ""))
		beta, err := ps.Create(ctx, "alice", projects.CreateInput{Name: "Beta"})
		Expect(err).NotTo(HaveOccurred())
		Expect(errors.Is(svc.SetChatProject(ctx, "alice", rec.ID, beta.ID), session.ErrInvalid)).To(BeTrue())
		Expect(errors.Is(svc.SetChatProject(ctx, "alice", rec.ID, ""), session.ErrInvalid)).To(BeTrue())
		Expect(errors.Is(svc.SetChatPulled(ctx, "alice", rec.ID, []string{beta.ID}), session.ErrInvalid)).To(BeTrue())
		got, _ := st.GetChat(ctx, "alice", rec.ID)
		Expect(got.ProjectID).To(Equal(atlas.ID))
		Expect(got.PulledProjects).To(BeEmpty())
	})

	It("gives a plain fenced agent no project context", func() {
		_, err := f.Store().Mkdir(ctx, f.Actor("alice"), home+"/Work")
		Expect(err).NotTo(HaveOccurred())
		a, err := svc.SaveAgentWithFolder(ctx, "alice", agents.Agent{Name: "Scoped", Model: "m"}, &AgentFolderWrite{Path: "/Work", Mode: agents.FolderWrite})
		Expect(err).NotTo(HaveOccurred())
		rec := chatOf(a)
		Expect(rec.ProjectID).To(BeEmpty())
		Expect(st.SetProject(ctx, "alice", rec.ID, atlas.ID)).To(Succeed()) // a stale record
		rec, _ = st.GetChat(ctx, "alice", rec.ID)
		Expect(svc.projectSourcesFor(ctx, rec, "")).To(BeEmpty())
		tt, x := turn(rec)
		Expect(tt.Prompt).NotTo(ContainSubstring("project notes"))
		Expect(x.View().Notes).To(BeEmpty())
	})

	It("leaves the project scope of propose_change closed for a project agent", func() {
		tt, _ := turn(chatOf(agentOf(atlas, "")))
		for _, t := range tt.Tools {
			if t.Name == "propose_change" {
				Expect(string(t.Schema)).NotTo(ContainSubstring(`"project"`))
			}
		}
	})

	It("checks the project scope once for a project agent chat and never for a chat without a project", func() {
		checks := 0
		svc.scopeChecks = func() { checks++ }
		_, err := f.Store().Mkdir(ctx, f.Actor("alice"), home+"/Work")
		Expect(err).NotTo(HaveOccurred())
		plain, err := svc.SaveAgentWithFolder(ctx, "alice", agents.Agent{Name: "Scoped", Model: "m"}, &AgentFolderWrite{Path: "/Work", Mode: agents.FolderWrite})
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.ChatAccess(ctx, "alice", chatOf(plain).ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(checks).To(BeZero(), "a plain fenced agent")
		chatOnly, err := svc.SaveAgent(ctx, "alice", agents.Agent{Name: "NoFiles", Model: "m", BuiltinsOff: []string{"act.files"}})
		Expect(err).NotTo(HaveOccurred())
		_, err = svc.ChatAccess(ctx, "alice", chatOf(chatOnly).ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(checks).To(BeZero(), "a fenced chat with no project")
		rec := chatOf(agentOf(atlas, ""))
		_, err = svc.ChatAccess(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(checks).To(Equal(1))
	})

	It("names the own project of a chat: its agent's project, the sub-agent's parent's, and none for a plain chat", func() {
		rec := chatOf(agentOf(atlas, ""))
		Expect(svc.ownProjectID(ctx, rec)).To(Equal(atlas.ID))
		kid := session.ChatRecord{ID: rec.ID + "-own", Principal: "alice", Title: "Sub", Kind: session.KindSubAgent, ParentID: rec.ID, Model: "m", ScopedAgent: true}
		Expect(st.CreateChat(ctx, kid)).To(Succeed())
		Expect(svc.ownProjectID(ctx, kid)).To(Equal(atlas.ID))
		Expect(svc.ownProjectID(ctx, session.ChatRecord{ID: "free", Principal: "alice"})).To(BeEmpty())
		_, err := ps.Archive(ctx, "alice", atlas.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(svc.ownProjectID(ctx, rec)).To(Equal(atlas.ID), "an archived project is still the chat's own")
	})

	It("gives a sub-agent the project through its parent, still fenced", func() {
		parent := chatOf(agentOf(atlas, ""))
		kid := session.ChatRecord{ID: parent.ID + "-sub", Principal: "alice", Title: "Sub", Kind: session.KindSubAgent, ParentID: parent.ID, Model: "m", ScopedAgent: true}
		Expect(st.CreateChat(ctx, kid)).To(Succeed())
		r, ok := svc.activeHome(ctx, kid)
		Expect(ok).To(BeTrue())
		Expect(r.ID).To(Equal(atlas.ID))
		x, err := svc.ChatAccess(ctx, "alice", kid.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(x.View().Notes).NotTo(BeEmpty())
		Expect(x.View().Notes[0]).To(HavePrefix(`This chat belongs to the project "Atlas"`))
		for _, m := range x.View().Mounts {
			Expect(m.At).NotTo(Or(Equal("project"), Equal("project-memory")))
		}
		_, fenced := x.Actor().Fence()
		Expect(fenced).To(BeTrue())
	})

	It("gives no context and no new chat while the project is archived, and works again when restored", func() {
		a := agentOf(atlas, "")
		rec := chatOf(a)
		_, err := ps.Archive(ctx, "alice", atlas.ID)
		Expect(err).NotTo(HaveOccurred())
		_, ok := svc.activeHome(ctx, rec)
		Expect(ok).To(BeFalse())
		Expect(svc.projectSourcesFor(ctx, rec, "")).To(BeEmpty())
		_, err = svc.CreateAs(ctx, "alice", CreateInput{AgentID: a.ID}, Origin{})
		Expect(errors.Is(err, ErrProjectUnavailable)).To(BeTrue(), "%v", err)
		_, err = ps.Restore(ctx, "alice", atlas.ID)
		Expect(err).NotTo(HaveOccurred())
		tt, _ := turn(rec)
		Expect(tt.Prompt).To(ContainSubstring("ship the atlas"))
	})
})
