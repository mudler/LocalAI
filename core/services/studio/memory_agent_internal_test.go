package studio

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtinskills"
	"github.com/mudler/LocalAI/core/services/studio/contextfiles"
	"github.com/mudler/LocalAI/core/services/studio/files"
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

// The memory choice of a saved agent: what the chat reads, where a note may go,
// what the model is offered, and what happens to the notes with the agent.
var _ = Describe("the memory choice of an agent", func() {
	var (
		ctx    context.Context
		svc    *Service
		f      *files.Service
		br     *memBroker
		stored map[string]string
		smu    sync.Mutex
	)
	home := session.HomePath("alice")
	put := func(rel, text string) {
		GinkgoHelper()
		_, err := f.Store().Put(ctx, f.Actor("alice"), home+"/"+rel, strings.NewReader(text), workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
	}
	setting := func(addon, key, value string) {
		smu.Lock()
		defer smu.Unlock()
		stored[addon+"/"+key] = value
	}
	turnOf := func(recID string) session.TurnTools {
		GinkgoHelper()
		x, err := svc.ChatAccess(ctx, "alice", recID)
		Expect(err).NotTo(HaveOccurred())
		tt, err := svc.chatTools(ctx, x)
		Expect(err).NotTo(HaveOccurred())
		return tt
	}
	turn := func(in CreateInput, origin ...Origin) session.TurnTools {
		GinkgoHelper()
		o := Origin{}
		if len(origin) > 0 {
			o = origin[0]
		}
		rec, err := svc.CreateAs(ctx, "alice", in, o)
		Expect(err).NotTo(HaveOccurred())
		return turnOf(rec.ID)
	}
	toolOf := func(tt session.TurnTools, name string) (session.Tool, bool) {
		for _, t := range tt.Tools {
			if t.Name == name {
				return t, true
			}
		}
		return session.Tool{}, false
	}
	// agent saves an agent with a choice of notes. It names the grant that lets
	// it save, unless noGrant.
	agent := func(name, choice string, noGrant bool, addons ...string) agents.Agent {
		GinkgoHelper()
		a := agents.Agent{Name: name, Model: "m", Memory: choice, Addons: addons}
		if !noGrant {
			a.Builtins = []string{contract.CapMemoryWrite}
		}
		saved, err := svc.SaveAgentWithFolder(ctx, "alice", a, nil)
		Expect(err).NotTo(HaveOccurred())
		put("Memory/agents/"+saved.ID+"/index.md", "- style: keeps answers terse -> style.md\n")
		return saved
	}
	const userNote, agentNote = "lives in Rome", "keeps answers terse"
	propose := func(tt session.TurnTools, args map[string]any) (string, error) {
		GinkgoHelper()
		t, ok := toolOf(tt, "propose_change")
		Expect(ok).To(BeTrue(), "the chat has propose_change")
		raw, err := json.Marshal(args)
		Expect(err).NotTo(HaveOccurred())
		return t.Run(ctx, raw)
	}
	note := func(scope string) map[string]any {
		m := map[string]any{"kind": "memory", "target": "food.md", "content": "- likes tea\n", "summary": "Likes tea"}
		if scope != "" {
			m["scope"] = scope
		}
		return m
	}
	pendingScopes := func() []string {
		GinkgoHelper()
		list, err := svc.Changes().List(ctx, "alice", contract.ProposalPending)
		Expect(err).NotTo(HaveOccurred())
		var out []string
		for _, p := range list {
			out = append(out, string(p.Scope))
		}
		return out
	}

	BeforeEach(func() {
		ctx = context.Background()
		stored = map[string]string{}
		dsn := "file:" + filepath.Join(GinkgoT().TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
		db, err := sqlitedb.Open(dsn, &gorm.Config{})
		Expect(err).NotTo(HaveOccurred())
		blobs, _ := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
		ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
		Expect(err).NotTo(HaveOccurred())
		f = files.New(ws)
		Expect(f.SyncBuiltin(ctx, builtinskills.FS())).To(Succeed())
		st, _ := store.NewJSON("")
		ag, _ := agents.NewJSON("")
		br = &memBroker{}
		svc, err = New(Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Files: f, Agents: ag, AddonTools: br,
			SetUserSetting: func(_ context.Context, _, addon, key, value string) error {
				smu.Lock()
				defer smu.Unlock()
				stored[addon+"/"+key] = value
				return nil
			},
			UserSetting: func(_ context.Context, _, addon, key string) (string, bool, error) {
				smu.Lock()
				defer smu.Unlock()
				v, ok := stored[addon+"/"+key]
				return v, ok, nil
			},
			ProposalGate: proposals.GateFunc(func(context.Context, proposals.Request) error { return nil })})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = svc.Close(context.Background())
			if raw, err := db.DB(); err == nil {
				_ = raw.Close()
			}
		})
		Expect(f.EnsureHome(ctx, "alice")).To(Succeed())
		put("Memory/index.md", "- home: lives in Rome -> home.md\n")
	})

	// What each choice reads. The last two are agents saved before the choice.
	type reads struct{ user, agent bool }
	choices := []struct {
		name, choice string
		addons       []string
		reads        reads
	}{
		{"own", agents.MemoryOwn, nil, reads{false, true}},
		{"mine_and_own", agents.MemoryMineAndOwn, nil, reads{true, true}},
		{"mine", agents.MemoryMine, nil, reads{true, false}},
		{"none", agents.MemoryNone, nil, reads{false, false}},
		{"as before, with the Memory add-on", "", []string{"memory"}, reads{true, true}},
		{"as before, without it", "", nil, reads{false, false}},
	}

	Describe("what a chat reads", func() {
		for _, c := range choices {
			It("follows the choice: "+c.name, func() {
				a := agent("A", c.choice, false, c.addons...)
				tt := turn(CreateInput{Model: "m", AgentID: a.ID})
				Expect(strings.Contains(tt.Prompt, userNote)).To(Equal(c.reads.user), "the notes of the user")
				Expect(strings.Contains(tt.Prompt, agentNote)).To(Equal(c.reads.agent), "the notes of the agent")
				_, has := toolOf(tt, contextfiles.ToolName)
				Expect(has).To(Equal(c.reads.user || c.reads.agent))
				if c.choice == agents.MemoryNone {
					Expect(tt.Prompt).To(ContainSubstring("Memory is off for this agent"))
				}

				// A private chat reads nothing, whatever the agent chose.
				pt := turn(CreateInput{Model: "m", AgentID: a.ID, Switches: Switches{BuiltinsOff: []string{"memory"}}})
				Expect(pt.Prompt).NotTo(ContainSubstring(userNote))
				Expect(pt.Prompt).NotTo(ContainSubstring(agentNote))

				// A run that started by itself honours the same scopes.
				rt := turn(CreateInput{Model: "m", AgentID: a.ID}, Origin{Kind: session.OriginTrigger, ID: "t1"})
				Expect(strings.Contains(rt.Prompt, userNote)).To(Equal(c.reads.user))
				Expect(strings.Contains(rt.Prompt, agentNote)).To(Equal(c.reads.agent))
				_, saves := toolOf(rt, "propose_change")
				Expect(saves).To(BeFalse(), "an unattended run cannot save")

				// The switch of the person beats the agent.
				setting(HostSettingsID, MemoryEnabledKey, MemoryOff)
				ot := turn(CreateInput{Model: "m", AgentID: a.ID})
				Expect(ot.Prompt).NotTo(ContainSubstring(userNote))
				Expect(ot.Prompt).NotTo(ContainSubstring(agentNote))
				_, has = toolOf(ot, contextfiles.ToolName)
				Expect(has).To(BeFalse())
			})
		}

		It("lets context_read name only the scopes the chat reads (a conversation stays)", func() {
			a := agent("A", agents.MemoryOwn, false)
			t, ok := toolOf(turn(CreateInput{Model: "m", AgentID: a.ID}), contextfiles.ToolName)
			Expect(ok).To(BeTrue())
			Expect(string(t.Schema)).To(ContainSubstring(`"enum":["agent","channel"]`))
			b := agent("B", agents.MemoryMineAndOwn, false)
			t, _ = toolOf(turn(CreateInput{Model: "m", AgentID: b.ID}), contextfiles.ToolName)
			Expect(string(t.Schema)).To(ContainSubstring(`"enum":["user","agent","channel"]`))
		})

		It("keeps a plain chat on the notes of the person", func() {
			tt := turn(CreateInput{Model: "m"})
			Expect(tt.Prompt).To(ContainSubstring(userNote))
			_, has := toolOf(tt, contextfiles.ToolName)
			Expect(has).To(BeTrue())
		})

		It("gives a sub-agent no notes and no save tool, whatever the choice", func() {
			a := agent("A", agents.MemoryMineAndOwn, false)
			parent, err := svc.CreateAs(ctx, "alice", CreateInput{Model: "m", AgentID: a.ID}, Origin{})
			Expect(err).NotTo(HaveOccurred())
			child := session.ChatRecord{ID: "child1", Principal: "alice", Title: "sub", Kind: session.KindSubAgent, ParentID: parent.ID, Model: "m", AgentID: a.ID,
				Created: parent.Updated, Updated: parent.Updated}
			Expect(svc.d.Store.CreateChat(ctx, child)).To(Succeed())
			tt := turnOf(child.ID)
			Expect(tt.Prompt).NotTo(ContainSubstring(userNote))
			Expect(tt.Prompt).NotTo(ContainSubstring(agentNote))
			_, has := toolOf(tt, "propose_change")
			Expect(has).To(BeFalse())
		})
	})

	Describe("where a note may go", func() {
		schemaOf := func(tt session.TurnTools) string {
			GinkgoHelper()
			t, ok := toolOf(tt, "propose_change")
			Expect(ok).To(BeTrue())
			return string(t.Schema)
		}

		It("fixes Mine and its own and Its own only to the agent", func() {
			for _, choice := range []string{agents.MemoryMineAndOwn, agents.MemoryOwn} {
				a := agent("A-"+choice, choice, false)
				tt := turn(CreateInput{Model: "m", AgentID: a.ID})
				Expect(schemaOf(tt)).To(ContainSubstring(`"scope":{"type":"string","enum":["agent"]}`), choice)
				Expect(schemaOf(tt)).To(ContainSubstring(`"required":["kind","summary"]`), choice)
				t, _ := toolOf(tt, "propose_change")
				Expect(t.Description).To(ContainSubstring("saved to this agent's own memory"))
				Expect(t.Description).NotTo(ContainSubstring("or channel"))

				_, err := propose(tt, note("user"))
				Expect(err).To(MatchError(ContainSubstring("saves notes to its own memory only")), choice)
				_, err = propose(tt, note("channel"))
				Expect(err).To(HaveOccurred(), choice)
				Expect(pendingScopes()).To(BeEmpty(), "a refused scope proposes nothing")
				out, err := propose(tt, note(""))
				Expect(err).NotTo(HaveOccurred(), choice)
				Expect(out).To(ContainSubstring("stored"))
				_, err = propose(tt, note("agent"))
				Expect(err).NotTo(HaveOccurred(), choice)
				Expect(pendingScopes()).To(Equal([]string{"agent", "agent"}), choice)
				svcDismissAll(ctx, svc)
			}
		})

		It("fixes Mine to the notes of the person", func() {
			a := agent("A", agents.MemoryMine, false)
			tt := turn(CreateInput{Model: "m", AgentID: a.ID})
			Expect(schemaOf(tt)).To(ContainSubstring(`"scope":{"type":"string","enum":["user"]}`))
			_, err := propose(tt, note("agent"))
			Expect(err).To(MatchError(ContainSubstring("the user's memory only")))
			_, err = propose(tt, note(""))
			Expect(err).NotTo(HaveOccurred())
			Expect(pendingScopes()).To(Equal([]string{"user"}))
		})

		It("refuses every note for None, and offers no save tool", func() {
			a := agent("A", agents.MemoryNone, false)
			tt := turn(CreateInput{Model: "m", AgentID: a.ID})
			_, has := toolOf(tt, "propose_change")
			Expect(has).To(BeFalse())
		})

		It("keeps the other kinds and their scopes when the agent also holds them", func() {
			a, err := svc.SaveAgentWithFolder(ctx, "alice", agents.Agent{Name: "Rich", Model: "m", Memory: agents.MemoryOwn,
				Builtins: []string{contract.CapMemoryWrite, contract.CapProfileWrite, contract.CapSkillsWrite}}, nil)
			Expect(err).NotTo(HaveOccurred())
			s := schemaOf(turn(CreateInput{Model: "m", AgentID: a.ID}))
			Expect(s).To(ContainSubstring(`"enum":["user","agent"]`), "a skill can still go to the user")
			Expect(s).NotTo(ContainSubstring("channel"))
		})

		It("leaves the model to choose for an agent saved before the choice, and for a plain chat", func() {
			a := agent("Old", "", false, "memory")
			tt := turn(CreateInput{Model: "m", AgentID: a.ID})
			Expect(schemaOf(tt)).To(ContainSubstring(`"scope":{"type":"string","enum":["user","agent","channel"]}`))
			Expect(schemaOf(tt)).To(ContainSubstring(`"required":["kind","scope","summary"]`))
			t, _ := toolOf(tt, "propose_change")
			Expect(t.Description).To(Equal(proposeChangeDescription))
			for _, scope := range []string{"user", "agent"} {
				_, err := propose(tt, note(scope))
				Expect(err).NotTo(HaveOccurred(), scope)
			}
			Expect(pendingScopes()).To(ConsistOf("user", "agent"))

			// A plain chat is unchanged too.
			plain := turn(CreateInput{Model: "m"})
			Expect(schemaOf(plain)).To(ContainSubstring(`"enum":["user","agent","channel"]`))
		})

		It("does not widen the person's choice: Off removes the memory kind for every choice", func() {
			setting(HostSettingsID, MemoryEnabledKey, MemoryOff)
			for _, choice := range []string{agents.MemoryOwn, agents.MemoryMineAndOwn, agents.MemoryMine, ""} {
				a := agent("A-"+choice, choice, false, "memory")
				tt := turn(CreateInput{Model: "m", AgentID: a.ID})
				_, has := toolOf(tt, "propose_change")
				Expect(has).To(BeFalse(), "choice %q", choice)
			}
		})

		It("saves nothing from a private chat", func() {
			a := agent("A", agents.MemoryMineAndOwn, false)
			tt := turn(CreateInput{Model: "m", AgentID: a.ID, Switches: Switches{BuiltinsOff: []string{"memory"}}})
			_, has := toolOf(tt, "propose_change")
			Expect(has).To(BeFalse())
		})

		It("does not save without the grant, whatever the choice", func() {
			a := agent("A", agents.MemoryMineAndOwn, true)
			tt := turn(CreateInput{Model: "m", AgentID: a.ID})
			_, has := toolOf(tt, "propose_change")
			Expect(has).To(BeFalse())
			Expect(tt.Prompt).To(ContainSubstring(agentNote), "it still reads")
		})

		It("reads, keeps the memory skills and refuses a memory note when the agent holds another change grant but not memory.write", func() {
			a := agents.Agent{Name: "B", Model: "m", Memory: agents.MemoryMineAndOwn, Builtins: []string{contract.CapPagesWrite}}
			saved, err := svc.SaveAgentWithFolder(ctx, "alice", a, nil)
			Expect(err).NotTo(HaveOccurred())
			put("Memory/agents/"+saved.ID+"/index.md", "- style: keeps answers terse -> style.md\n")
			tt := turn(CreateInput{Model: "m", AgentID: saved.ID})
			Expect(tt.Prompt).To(ContainSubstring(agentNote), "it still reads")
			// The host keeps granting the memory skills: reading needs them, and
			// the saving part of the skill tells the model what to say when the
			// tool has no memory kind.
			Expect(tt.Prompt).To(ContainSubstring("- memory:"))
			t, ok := toolOf(tt, "propose_change")
			Expect(ok).To(BeTrue())
			Expect(string(t.Schema)).NotTo(ContainSubstring(`"memory"`), "the kind is not offered")
			_, err = propose(tt, note(""))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("not given the right"))
			Expect(pendingScopes()).To(BeEmpty())
		})
	})

	Describe("reporting that memory is off", func() {
		It("names the choice None as the level that is off", func() {
			a := agent("A", agents.MemoryNone, false)
			rec, err := svc.CreateAs(ctx, "alice", CreateInput{Model: "m", AgentID: a.ID}, Origin{})
			Expect(err).NotTo(HaveOccurred())
			r := svc.ChatMemory(ctx, rec)
			Expect(r.On).To(BeFalse())
			Expect(r.Code).To(Equal(MemoryOffAgent))
			Expect(r.Fix).To(Equal(&Fix{Kind: FixOpenAgent, Target: a.ID, Field: "memory"}))
			Expect(svc.ChatMemory(ctx, mustChat(ctx, svc, a.ID, "alice", "m", Switches{BuiltinsOff: []string{"memory"}})).Code).To(Equal(MemoryOffChat), "the narrowest level is named")
		})
	})

	Describe("saving an agent", func() {
		It("keeps the stored choice when a write does not mention it", func() {
			a := agent("A", agents.MemoryOwn, false)
			a.Memory = ""
			a.Name = "Renamed"
			got, err := svc.SaveAgentWithFolder(ctx, "alice", a, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Memory).To(Equal(agents.MemoryOwn))
			a.Memory = agents.MemoryMine
			got, err = svc.SaveAgentWithFolder(ctx, "alice", a, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Memory).To(Equal(agents.MemoryMine))
		})

		It("refuses a choice that does not exist", func() {
			_, err := svc.SaveAgentWithFolder(ctx, "alice", agents.Agent{Name: "A", Memory: "everything"}, nil)
			Expect(err).To(MatchError(agents.ErrInvalid))
		})

		It("starts a starter agent with Mine and its own", func() {
			made, err := svc.CreateAgentFromTemplate(ctx, "alice", agents.Templates()[0].ID, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(made.Memory).To(Equal(agents.MemoryMineAndOwn))
		})

		It("does not copy the notes when an agent is imported as a new one", func() {
			a := agent("Source", agents.MemoryOwn, false)
			copyOf, err := svc.ImportAgentFolders(ctx, "alice", agents.Agent{Name: "Source", Memory: a.Memory, Builtins: a.Builtins}, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(copyOf.ID).NotTo(Equal(a.ID))
			Expect(copyOf.Memory).To(Equal(agents.MemoryOwn))
			tt := turn(CreateInput{Model: "m", AgentID: copyOf.ID})
			Expect(tt.Prompt).NotTo(ContainSubstring(agentNote), "the copy starts with no notes of its own")
		})
	})

	Describe("deleting an agent", func() {
		listAgentGroups := func() []ContextGroup {
			GinkgoHelper()
			list, err := svc.ListContextFiles(ctx, "alice")
			Expect(err).NotTo(HaveOccurred())
			var out []ContextGroup
			for _, g := range list.Groups {
				if g.Source == MemoryAddonID && g.Scope == contextfiles.ScopeAgent {
					out = append(out, g)
				}
			}
			return out
		}

		It("names the agent of its notes, and lists them as deleted once it is gone: keep is the default", func() {
			a := agent("Helper", agents.MemoryOwn, false)
			groups := listAgentGroups()
			Expect(groups).To(HaveLen(1))
			Expect(groups[0].Label).To(Equal("Helper"))
			Expect(groups[0].Deleted).To(BeFalse())

			zipped, err := svc.DeleteAgentWithNotes(ctx, "alice", a.ID, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(zipped).To(BeNil())
			groups = listAgentGroups()
			Expect(groups).To(HaveLen(1), "the notes were kept")
			Expect(groups[0].Deleted).To(BeTrue())
			Expect(groups[0].Label).To(BeEmpty())
			_, err = svc.Agent(ctx, "alice", a.ID)
			Expect(err).To(MatchError(agents.ErrNotFound))
		})

		It("moves the notes to the Trash with delete", func() {
			a := agent("Helper", agents.MemoryOwn, false)
			_, err := svc.DeleteAgentWithNotes(ctx, "alice", a.ID, AgentNotesDelete)
			Expect(err).NotTo(HaveOccurred())
			Expect(listAgentGroups()).To(BeEmpty())
			trash, err := f.Trash(ctx, "alice", 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(trash).NotTo(BeEmpty(), "the notes can be restored")
			// The notes of the person stay.
			list, err := svc.ListContextFiles(ctx, "alice")
			Expect(err).NotTo(HaveOccurred())
			Expect(list.Groups).NotTo(BeEmpty())
		})

		It("returns the notes as a zip and then removes them with export", func() {
			a := agent("Helper", agents.MemoryOwn, false)
			zipped, err := svc.DeleteAgentWithNotes(ctx, "alice", a.ID, AgentNotesExport)
			Expect(err).NotTo(HaveOccurred())
			zr, err := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
			Expect(err).NotTo(HaveOccurred())
			var names []string
			for _, zf := range zr.File {
				names = append(names, zf.Name)
			}
			Expect(names).To(ConsistOf("Memory/agents/" + a.ID + "/index.md"))
			Expect(listAgentGroups()).To(BeEmpty())
		})

		It("answers an agent without notes with no zip", func() {
			a, err := svc.SaveAgentWithFolder(ctx, "alice", agents.Agent{Name: "Quiet", Model: "m"}, nil)
			Expect(err).NotTo(HaveOccurred())
			zipped, err := svc.DeleteAgentWithNotes(ctx, "alice", a.ID, AgentNotesExport)
			Expect(err).NotTo(HaveOccurred())
			Expect(zipped).To(BeNil())
		})

		It("refuses a value that is not keep, export or delete, and deletes nothing", func() {
			a := agent("Helper", agents.MemoryOwn, false)
			_, err := svc.DeleteAgentWithNotes(ctx, "alice", a.ID, "burn")
			Expect(err).To(MatchError(ErrInvalidSettings))
			_, err = svc.Agent(ctx, "alice", a.ID)
			Expect(err).NotTo(HaveOccurred())
		})

		It("is not another user's to delete", func() {
			a := agent("Helper", agents.MemoryOwn, false)
			_, err := svc.DeleteAgentWithNotes(ctx, "bob", a.ID, AgentNotesDelete)
			Expect(err).To(MatchError(agents.ErrNotFound))
			Expect(listAgentGroups()).To(HaveLen(1))
		})
	})
})

// svcDismissAll dismisses every pending proposal of alice, so a spec can count
// again.
func svcDismissAll(ctx context.Context, svc *Service) {
	GinkgoHelper()
	list, err := svc.Changes().List(ctx, "alice", contract.ProposalPending)
	Expect(err).NotTo(HaveOccurred())
	for _, p := range list {
		_, err := svc.Changes().Dismiss(ctx, "alice", p.ID)
		Expect(err).NotTo(HaveOccurred())
	}
}

// mustChat creates a chat of an agent with switches and returns its record.
func mustChat(ctx context.Context, svc *Service, agentID, user, model string, sw Switches) session.ChatRecord {
	GinkgoHelper()
	rec, err := svc.CreateAs(ctx, user, CreateInput{Model: model, AgentID: agentID, Switches: sw}, Origin{})
	Expect(err).NotTo(HaveOccurred())
	return rec
}
