package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/contextfiles"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

var _ = Describe("project memory", func() {
	var (
		ctx   context.Context
		svc   *Service
		f     *files.Service
		ps    *projects.Service
		st    store.Store
		cs    connectors.Store
		atlas projects.Project
	)
	home := session.HomePath("alice")
	put := func(rel, text string) {
		GinkgoHelper()
		_, err := f.Store().Put(ctx, f.Actor("alice"), home+"/"+rel, strings.NewReader(text), workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
	}
	mk := func(name string) projects.Project {
		GinkgoHelper()
		p, err := ps.Create(ctx, "alice", projects.CreateInput{Name: name})
		Expect(err).NotTo(HaveOccurred())
		return p
	}
	turn := func(in CreateInput, origin Origin) (session.TurnTools, session.ChatRecord) {
		GinkgoHelper()
		rec, err := svc.CreateAs(ctx, "alice", in, origin)
		Expect(err).NotTo(HaveOccurred())
		x, err := svc.ChatAccess(ctx, "alice", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		tt, err := svc.chatTools(ctx, x)
		Expect(err).NotTo(HaveOccurred())
		return tt, rec
	}
	tool := func(tt session.TurnTools, name string) *session.Tool {
		for i := range tt.Tools {
			if tt.Tools[i].Name == name {
				return &tt.Tools[i]
			}
		}
		return nil
	}
	propose := func(tt session.TurnTools, args map[string]string) (string, error) {
		GinkgoHelper()
		pc := tool(tt, "propose_change")
		Expect(pc).NotTo(BeNil(), "the chat has propose_change")
		raw, _ := json.Marshal(args)
		return pc.Run(ctx, raw)
	}
	note := func(target, content string) map[string]string {
		return map[string]string{"kind": "memory", "scope": "project", "target": target, "content": content, "summary": "Project note"}
	}
	read := func(rel string) (string, bool) {
		GinkgoHelper()
		text, ok, err := f.ReadHome(ctx, "alice", rel, 1<<20)
		Expect(err).NotTo(HaveOccurred())
		return text, ok
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
		cs, err = connectors.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		c, err := NewConnectors(svc, ConnectorOptions{Store: cs, Transport: titleTransport{}})
		Expect(err).NotTo(HaveOccurred())
		svc.SetConnectors(c)
		atlas = mk("Atlas")
		put("Memory/index.md", "- home: lives in Rome -> home.md\n")
	})

	Describe("loading", func() {
		It("puts the project index after the user index and offers the project scope to context_read", func() {
			put("Projects/atlas/memory/index.md", "- goal: ship the atlas -> goal.md\n")
			put("Projects/atlas/memory/goal.md", "Ship by spring.")
			tt, _ := turn(CreateInput{Model: "m", ProjectID: atlas.ID}, Origin{})
			u, p := strings.Index(tt.Prompt, "[user notes"), strings.Index(tt.Prompt, "[project notes")
			Expect(u).To(BeNumerically(">=", 0))
			Expect(p).To(BeNumerically(">", u))
			Expect(tt.Prompt).To(ContainSubstring("ship the atlas"))
			mr := tool(tt, contextfiles.ToolName)
			Expect(mr).NotTo(BeNil())
			Expect(string(mr.Schema)).To(ContainSubstring(`"enum":["user","project","agent","channel"]`))
			out, err := mr.Run(ctx, json.RawMessage(`{"topic":"goal","scope":"project"}`))
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("Ship by spring."))
		})

		It("leaves the prompt of a chat without a project as it was", func() {
			put("Projects/atlas/memory/index.md", "- goal: ship the atlas -> goal.md\n")
			tt, _ := turn(CreateInput{Model: "m"}, Origin{})
			Expect(tt.Prompt).To(ContainSubstring(contextfiles.Header + " Save new facts with propose_change.\n\n[user notes: memory]\n- home: lives in Rome -> home.md"))
			Expect(tt.Prompt).NotTo(ContainSubstring("project notes"))
			Expect(tt.Prompt).NotTo(ContainSubstring("ship the atlas"))
			mr := tool(tt, contextfiles.ToolName)
			Expect(string(mr.Schema)).To(ContainSubstring(`"enum":["user","agent","channel"]`))
		})

		It("does not put the notes of a pulled project in the prompt", func() {
			beta := mk("Beta")
			put("Projects/beta/memory/index.md", "- secret: beta only\n")
			put("Projects/atlas/memory/index.md", "- goal: ship the atlas\n")
			_, rec := turn(CreateInput{Model: "m", ProjectID: atlas.ID}, Origin{})
			Expect(svc.SetChatPulled(ctx, "alice", rec.ID, []string{beta.ID})).To(Succeed())
			x, err := svc.ChatAccess(ctx, "alice", rec.ID)
			Expect(err).NotTo(HaveOccurred())
			tt, err := svc.chatTools(ctx, x)
			Expect(err).NotTo(HaveOccurred())
			Expect(tt.Prompt).To(ContainSubstring("ship the atlas"))
			Expect(tt.Prompt).NotTo(ContainSubstring("beta only"))
		})

		It("reads the project each turn: a deleted project leaves no notes and no error", func() {
			put("Projects/atlas/memory/index.md", "- goal: ship the atlas\n")
			_, rec := turn(CreateInput{Model: "m", ProjectID: atlas.ID}, Origin{})
			Expect(ps.Delete(ctx, "alice", atlas.ID)).To(Succeed())
			x, err := svc.ChatAccess(ctx, "alice", rec.ID)
			Expect(err).NotTo(HaveOccurred())
			tt, err := svc.chatTools(ctx, x)
			Expect(err).NotTo(HaveOccurred())
			Expect(tt.Prompt).NotTo(ContainSubstring("project notes"))
		})

		It("holds the project notes back from a messaging sender who is not paired", func() {
			put("Projects/atlas/memory/index.md", "- goal: ship the atlas\n")
			Expect(cs.PutBinding(ctx, connectors.Binding{ID: "b-tg", Owner: "alice", Kind: "tg", AddonID: "tg-addon"})).To(Succeed())
			tt, _ := turn(CreateInput{Model: "m", ProjectID: atlas.ID}, Origin{Kind: session.OriginConnector, ID: "b-tg"})
			Expect(tt.Prompt).NotTo(ContainSubstring("ship the atlas"))
		})
	})

	Describe("a chat that is fenced to the folder of its agent", func() {
		It("gets no project index and no project scope even when its record names a project", func() {
			Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
			_, err := f.Store().Mkdir(ctx, f.Actor("alice"), home+"/Work")
			Expect(err).NotTo(HaveOccurred())
			a, err := svc.SaveAgentWithFolder(ctx, "alice", agents.Agent{Name: "Scoped", Model: "m"}, &AgentFolderWrite{Path: "/Work", Mode: agents.FolderWrite})
			Expect(err).NotTo(HaveOccurred())
			rec, err := svc.CreateAs(ctx, "alice", CreateInput{AgentID: a.ID}, Origin{})
			Expect(err).NotTo(HaveOccurred())
			Expect(st.SetProject(ctx, "alice", rec.ID, atlas.ID)).To(Succeed()) // a stale record
			rec, err = st.GetChat(ctx, "alice", rec.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(svc.projectSourcesFor(ctx, rec, "")).To(BeEmpty())
			x, err := svc.ChatAccess(ctx, "alice", rec.ID)
			Expect(err).NotTo(HaveOccurred())
			tt, err := svc.chatTools(ctx, x)
			Expect(err).NotTo(HaveOccurred())
			if pc := tool(tt, "propose_change"); pc != nil {
				Expect(string(pc.Schema)).NotTo(ContainSubstring(`"project"`))
				_, err := propose(tt, note("goal.md", "x\n"))
				Expect(err).To(HaveOccurred())
			}
			list, _ := svc.Changes().List(ctx, "alice")
			Expect(list).To(BeEmpty())
		})
	})

	Describe("proposing", func() {
		It("targets the home project and lands under its memory folder", func() {
			tt, rec := turn(CreateInput{Model: "m", ProjectID: atlas.ID}, Origin{})
			out, err := propose(tt, note("goal.md", "Ship by spring.\n"))
			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("Nothing is saved yet"))
			list, err := svc.Changes().List(ctx, "alice")
			Expect(err).NotTo(HaveOccurred())
			Expect(list).To(HaveLen(1))
			Expect(list[0].Target).To(Equal("Projects/atlas/memory/goal.md"))
			Expect(list[0].Scope).To(Equal(contract.ScopeProject))
			Expect(list[0].Source.Chat).To(Equal(rec.ID))
			_, ok := read("Projects/atlas/memory/goal.md")
			Expect(ok).To(BeFalse(), "a proposal writes nothing")
		})

		It("needs a target for a project note, names the topic file in the error, and writes nothing", func() {
			put("Projects/atlas/memory/index.md", "# Atlas\nCodename: BLUE.\n")
			tt, _ := turn(CreateInput{Model: "m", ProjectID: atlas.ID}, Origin{})
			pc := tool(tt, "propose_change")
			for _, args := range []string{
				`{"kind":"memory","scope":"project","content":"- a new fact\n","summary":"fact"}`,
				`{"kind":"memory","scope":"project","target":"  ","content":"- a new fact\n","summary":"fact"}`,
			} {
				_, err := pc.Run(ctx, json.RawMessage(args))
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("target is required"))
				Expect(err.Error()).To(ContainSubstring("topic file"))
				Expect(err.Error()).To(ContainSubstring("replaces the whole file"))
			}
			list, _ := svc.Changes().List(ctx, "alice")
			Expect(list).To(BeEmpty(), "no proposal was stored")
			text, _ := read("Projects/atlas/memory/index.md")
			Expect(text).To(ContainSubstring("BLUE"), "the index is untouched")
		})

		It("keeps index.md allowed when the model names it, and tells the model a proposal replaces the file", func() {
			put("Projects/atlas/memory/index.md", "# Atlas\n")
			tt, _ := turn(CreateInput{Model: "m", ProjectID: atlas.ID}, Origin{})
			_, err := propose(tt, note("index.md", "# Atlas\nCodename: BLUE.\n"))
			Expect(err).NotTo(HaveOccurred())
			pc := tool(tt, "propose_change")
			Expect(pc.Description).To(ContainSubstring("replaces the whole file"))
			Expect(pc.Description).To(ContainSubstring("target is required"))
		})

		It("writes a change set on confirm and Revert restores the file", func() {
			put("Projects/atlas/memory/goal.md", "old goal\n")
			tt, _ := turn(CreateInput{Model: "m", ProjectID: atlas.ID}, Origin{})
			_, err := propose(tt, note("goal.md", "new goal\n"))
			Expect(err).NotTo(HaveOccurred())
			list, _ := svc.Changes().List(ctx, "alice")
			done, err := svc.Changes().Confirm(ctx, "alice", list[0].ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(done.Rev).NotTo(BeZero(), "the write is one change set")
			text, _ := read("Projects/atlas/memory/goal.md")
			Expect(text).To(Equal("new goal\n"))
			_, err = svc.Changes().Revert(ctx, "alice", list[0].ID)
			Expect(err).NotTo(HaveOccurred())
			text, _ = read("Projects/atlas/memory/goal.md")
			Expect(text).To(Equal("old goal\n"))
		})

		It("never saves a project note at once, even when the person chose auto", func() {
			_, err := svc.Changes().Propose(ctx, ProposeInput{User: "alice", Project: atlas.ID, Kind: "memory", Scope: "project", Target: "goal.md", Content: "x\n", Summary: "s", Auto: true})
			Expect(err).To(MatchError(ErrBadChange))
		})

		It("refuses the project scope in a chat with no project", func() {
			tt, _ := turn(CreateInput{Model: "m"}, Origin{})
			_, err := propose(tt, note("goal.md", "x\n"))
			Expect(err).To(MatchError(ContainSubstring("project")))
			t := tool(tt, "propose_change")
			Expect(string(t.Schema)).NotTo(ContainSubstring(`"project"`))
		})

		It("refuses it in a chat that only pulled a project", func() {
			beta := mk("Beta")
			_, rec := turn(CreateInput{Model: "m"}, Origin{})
			Expect(svc.SetChatPulled(ctx, "alice", rec.ID, []string{beta.ID})).To(Succeed())
			x, err := svc.ChatAccess(ctx, "alice", rec.ID)
			Expect(err).NotTo(HaveOccurred())
			tt, err := svc.chatTools(ctx, x)
			Expect(err).NotTo(HaveOccurred())
			_, err = propose(tt, note("goal.md", "x\n"))
			Expect(err).To(MatchError(ContainSubstring("project")))
			_, ok := read("Projects/beta/memory/goal.md")
			Expect(ok).To(BeFalse())
		})

		It("refuses it when the project of the chat is gone", func() {
			tt, _ := turn(CreateInput{Model: "m", ProjectID: atlas.ID}, Origin{})
			Expect(ps.Delete(ctx, "alice", atlas.ID)).To(Succeed())
			_, err := propose(tt, note("goal.md", "x\n"))
			Expect(err).To(MatchError(ContainSubstring("project")))
		})

		It("lets the model name no own project: a project argument is refused and writes nothing", func() {
			beta := mk("Beta")
			tt, _ := turn(CreateInput{Model: "m", ProjectID: atlas.ID}, Origin{})
			args := note("goal.md", "x\n")
			args["project"] = beta.ID
			_, err := propose(tt, args)
			Expect(err).To(MatchError(ContainSubstring("shared with you")))
			list, _ := svc.Changes().List(ctx, "alice")
			Expect(list).To(BeEmpty())
			_, err = propose(tt, note("goal.md", "x\n"))
			Expect(err).NotTo(HaveOccurred(), "the home project stays the default")
			list, _ = svc.Changes().List(ctx, "alice")
			Expect(list).To(HaveLen(1))
			Expect(list[0].Target).To(Equal("Projects/atlas/memory/goal.md"))
		})

		DescribeTable("refuses a target that is not a plain topic file",
			func(target string) {
				tt, _ := turn(CreateInput{Model: "m", ProjectID: atlas.ID}, Origin{})
				_, err := propose(tt, note(target, "x\n"))
				Expect(err).To(HaveOccurred())
				list, _ := svc.Changes().List(ctx, "alice")
				Expect(list).To(BeEmpty())
			},
			Entry("a parent", "../x.md"),
			Entry("a nested path", "a/b.md"),
			Entry("upper case", "Goal.md"),
			Entry("no md", "goal.txt"),
			Entry("empty", ""),
		)

		It("refuses an empty or blank memory note, in every scope, and writes nothing", func() {
			tt, _ := turn(CreateInput{Model: "m", ProjectID: atlas.ID}, Origin{})
			before, _ := read("Projects/atlas/memory/index.md")
			for _, scope := range []string{"project", "user", "channel"} {
				for _, content := range []string{"", "  \n\t "} {
					args := note("index.md", content)
					args["scope"] = scope
					_, err := propose(tt, args)
					Expect(err).To(MatchError(ContainSubstring("content")), "scope %s %q", scope, content)
					Expect(err).To(MatchError(ContainSubstring("full new text")))
				}
			}
			args := note("index.md", "")
			delete(args, "content")
			_, err := propose(tt, args)
			Expect(err).To(MatchError(ContainSubstring("content")), "content left out")
			list, _ := svc.Changes().List(ctx, "alice")
			Expect(list).To(BeEmpty())
			text, _ := read("Projects/atlas/memory/index.md")
			Expect(text).To(Equal(before), "the index is as it was")
		})

		It("marks content required in the schema of a chat that can only propose notes", func() {
			Expect(proposeChangeSchemaFor([]contract.ChangeKind{contract.ChangeMemory}, nil, true)).To(ContainSubstring(`"required":["kind","scope","content","summary"]`))
			Expect(proposeChangeSchemaFor([]contract.ChangeKind{contract.ChangeMemory, contract.ChangePage}, nil, true)).NotTo(ContainSubstring(`"content","summary"`), "a page has no content")
		})

		It("refuses the project scope from Propose without a project", func() {
			_, err := svc.Changes().Propose(ctx, ProposeInput{User: "alice", Kind: "memory", Scope: "project", Target: "goal.md", Content: "x\n", Summary: "s"})
			Expect(err).To(MatchError(ErrBadChange))
		})

		It("offers no propose_change to a sub-agent, and the same gate holds for an unpaired messaging sender", func() {
			_, rec := turn(CreateInput{Model: "m", ProjectID: atlas.ID}, Origin{})
			kid := session.ChatRecord{ID: rec.ID + "-sub", Principal: "alice", Title: "Sub", Kind: session.KindSubAgent, ParentID: rec.ID, Model: "m", ProjectID: atlas.ID}
			Expect(st.CreateChat(ctx, kid)).To(Succeed())
			x, err := svc.ChatAccess(ctx, "alice", kid.ID)
			Expect(err).NotTo(HaveOccurred())
			tt, err := svc.chatTools(ctx, x)
			Expect(err).NotTo(HaveOccurred())
			Expect(tool(tt, "propose_change")).To(BeNil())

			Expect(cs.PutBinding(ctx, connectors.Binding{ID: "b-tg", Owner: "alice", Kind: "tg", AddonID: "tg-addon"})).To(Succeed())
			ct, _ := turn(CreateInput{Model: "m", ProjectID: atlas.ID}, Origin{Kind: session.OriginConnector, ID: "b-tg"})
			if t := tool(ct, "propose_change"); t != nil {
				Expect(string(t.Schema)).NotTo(ContainSubstring(`"project"`))
				_, err := propose(ct, note("goal.md", "x\n"))
				Expect(err).To(HaveOccurred())
			}
			list, _ := svc.Changes().List(ctx, "alice")
			Expect(list).To(BeEmpty(), fmt.Sprint(list))
		})
	})
})
