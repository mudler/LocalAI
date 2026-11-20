package studio

import (
	"context"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/skills"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

// One project that owns a tool of every kind and a skill of its own, followed
// through the whole feature: a new chat, an old chat, an agent run, a trigger
// run, a person the project is shared with, and a move of the agent.
var _ = Describe("a project that owns its tools, end to end", func() {
	var (
		ctx   context.Context
		svc   *Service
		f     *files.Service
		ps    *projects.Service
		st    store.Store
		atlas projects.Project
		beta  projects.Project
	)
	// The project owns one tool of each kind. The skill list holds a skill
	// that lives in the personal layer; the project skill is a file.
	owned := projects.Defaults{Skills: []string{"style"}, Addons: []string{"ad"}, Connectors: []string{"cn"}, MCPServers: []string{"srv"}, Collections: []string{"col"}}
	home := session.HomePath("alice")
	put := func(rel, name, desc string) {
		GinkgoHelper()
		_, err := f.Store().Put(ctx, f.Actor("alice"), home+"/"+rel+"/SKILL.md", strings.NewReader(string(skillMD(name, desc, desc+" body."))), workspace.PutOptions{Overwrite: true})
		Expect(err).NotTo(HaveOccurred())
	}
	// skillsOf returns the skills a turn of rec has on.
	skillsOf := func(rec session.ChatRecord) map[string]skills.Skill {
		GinkgoHelper()
		got, err := svc.getChatEffective(ctx, rec.Principal, rec.ID)
		Expect(err).NotTo(HaveOccurred())
		sc := svc.scopeOf(ctx, got)
		list, _, _, err := svc.chatSkillSet(ctx, "alice", got, svc.skillsOffOf(ctx, got, sc), got.AddonsOff, sc, Commands{})
		Expect(err).NotTo(HaveOccurred())
		out := map[string]skills.Skill{}
		for _, sk := range list {
			if sk.Invalid == "" && sk.Shadowed == "" && !sk.Off {
				out[sk.Name] = sk
			}
		}
		return out
	}
	// offOf is what a turn of a chat with no agent has switched off: the
	// record as the turn reads it, with the attached tools taken out.
	offOf := func(rec session.ChatRecord) session.ChatRecord {
		GinkgoHelper()
		got, err := svc.getChatEffective(ctx, rec.Principal, rec.ID)
		Expect(err).NotTo(HaveOccurred())
		return got
	}
	// toolsOf is what the scope of a turn of an agent grants.
	type tools struct{ addons, mcp, collections []string }
	toolsOf := func(rec session.ChatRecord) tools {
		GinkgoHelper()
		got := offOf(rec)
		sc := svc.scopeOf(ctx, got)
		Expect(sc).NotTo(BeNil())
		return tools{addons: sc.addonNames, mcp: sc.mcp, collections: sc.collNames}
	}
	attach := func(p projects.Project, a projects.Defaults) {
		GinkgoHelper()
		_, err := ps.Update(ctx, "alice", p.ID, projects.UpdateInput{Attached: &a})
		Expect(err).NotTo(HaveOccurred())
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
		beta, err = ps.Create(ctx, "alice", projects.CreateInput{Name: "Beta"})
		Expect(err).NotTo(HaveOccurred())
		put("Projects/atlas/skills/plan", "plan", "Atlas plan.")
		put(".skills/style", "style", "Personal style.")
	})

	It("gives the project's tools to every chat and agent of it, never to a grantee, and reports them on a move", func() {
		// An old chat starts before anything is attached, with the tools off.
		old, err := svc.CreateAs(ctx, "alice", CreateInput{Model: "m", ProjectID: atlas.ID, Switches: Switches{SkillsOff: []string{"style"}, AddonsOff: []string{"ad"}, McpOff: []string{"srv", "connector:cn"}}}, Origin{})
		Expect(err).NotTo(HaveOccurred())
		Expect(offOf(old).AddonsOff).To(ConsistOf("ad"))

		// Attach one tool of each kind. No step is needed in any chat.
		attach(atlas, owned)

		// The old chat has them on its next turn, and the project skill.
		on := offOf(old)
		Expect(on.AddonsOff).To(BeEmpty())
		Expect(on.McpOff).To(BeEmpty())
		Expect(on.SkillsOff).To(BeEmpty())
		Expect(skillsOf(old)).To(And(HaveKey("plan"), HaveKey("style")))

		// A new chat has them too.
		fresh, err := svc.CreateAs(ctx, "alice", CreateInput{Model: "m", ProjectID: atlas.ID}, Origin{})
		Expect(err).NotTo(HaveOccurred())
		on = offOf(fresh)
		Expect(on.AddonsOff).To(BeEmpty())
		Expect(on.McpOff).To(BeEmpty())
		Expect(skillsOf(fresh)).To(HaveKey("plan"))

		// A chat outside the project has none of it.
		plain, err := svc.CreateAs(ctx, "alice", CreateInput{Model: "m"}, Origin{})
		Expect(err).NotTo(HaveOccurred())
		Expect(skillsOf(plain)).NotTo(HaveKey("plan"))

		// An agent of the project has all five kinds on every run, and the
		// project skill, with nothing in its own record.
		helper, err := svc.SaveAgent(ctx, "alice", agents.Agent{Name: "Helper", Model: "m", ProjectID: atlas.ID})
		Expect(err).NotTo(HaveOccurred())
		stored, err := svc.storedChatAgent(ctx, "alice", helper.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.Addons).To(BeEmpty())
		run, err := svc.CreateAs(ctx, "alice", CreateInput{AgentID: helper.ID}, Origin{})
		Expect(err).NotTo(HaveOccurred())
		got := toolsOf(run)
		Expect(got.addons).To(ConsistOf("ad"))
		Expect(got.mcp).To(ConsistOf("srv", "connector:cn"))
		Expect(got.collections).To(ConsistOf("col"))
		Expect(skillsOf(run)).To(And(HaveKey("plan"), HaveKey("style")))

		// A run that a trigger starts reads the same scope.
		fired, err := svc.CreateAs(ctx, "alice", CreateInput{AgentID: helper.ID}, Origin{Kind: session.OriginTrigger, ID: "t1"})
		Expect(err).NotTo(HaveOccurred())
		got = toolsOf(fired)
		Expect(got.addons).To(ConsistOf("ad"))
		Expect(got.mcp).To(ConsistOf("srv", "connector:cn"))
		Expect(skillsOf(fired)).To(HaveKey("plan"))

		// A person the agent is shared with gets none of the credentialed
		// tools, in a run as that person and in a chat they own.
		as, err := svc.runtimeAgent(ctx, "bob", stored)
		Expect(err).NotTo(HaveOccurred())
		Expect(as.Addons).NotTo(ContainElement("ad"))
		Expect(as.MCPServers).NotTo(ContainElements("srv", "connector:cn"))
		Expect(as.Collections).NotTo(ContainElement("col"))
		rec := session.ChatRecord{ID: "bobs", Principal: "bob", Title: "t", Model: "m", AgentID: helper.ID, ProjectID: atlas.ID, AddonsOff: []string{"ad"}, McpOff: []string{"srv", "connector:cn"}}
		Expect(st.CreateChat(ctx, rec)).To(Succeed())
		capped, err := svc.cappedForChat(ctx, rec, stored)
		Expect(err).NotTo(HaveOccurred())
		Expect(capped.Addons).NotTo(ContainElement("ad"))
		Expect(capped.MCPServers).NotTo(ContainElements("srv", "connector:cn"))
		bobsChat, err := svc.getChatEffective(ctx, "bob", rec.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(bobsChat.AddonsOff).To(Equal([]string{"ad"}), "the chat of a person who is not the owner keeps its own switches")
		Expect(bobsChat.McpOff).To(Equal([]string{"srv", "connector:cn"}))

		// Moving the agent to a project that attaches another add-on reports
		// what stops, what starts, and the switch of the project skills.
		attach(beta, projects.Defaults{Addons: []string{"other-ad"}})
		dry, err := svc.MoveAgentProject(ctx, "alice", helper.ID, AgentMove{ProjectID: beta.ID, DryRun: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(dry.AttachedOld.Addons).To(Equal([]string{"ad"}))
		Expect(dry.AttachedOld.Connectors).To(Equal([]string{"cn"}))
		Expect(dry.AttachedOld.MCPServers).To(Equal([]string{"srv"}))
		Expect(dry.AttachedOld.Collections).To(Equal([]string{"col"}))
		Expect(dry.AttachedOld.Skills).To(Equal([]string{"style"}))
		Expect(dry.AttachedNew.Addons).To(Equal([]string{"other-ad"}))
		Expect(dry.AttachedNew.Connectors).To(BeEmpty())
		Expect(dry.ProjectSkillsSwitch).To(BeTrue())
		moved, err := svc.MoveAgentProject(ctx, "alice", helper.ID, AgentMove{ProjectID: beta.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(moved.Moved).To(BeTrue())
		next, err := svc.CreateAs(ctx, "alice", CreateInput{AgentID: helper.ID}, Origin{})
		Expect(err).NotTo(HaveOccurred())
		got = toolsOf(next)
		Expect(got.addons).To(ConsistOf("other-ad"), "the old project's tools stop and the new one's start")
		Expect(got.mcp).To(BeEmpty())
		Expect(skillsOf(next)).NotTo(HaveKey("plan"), "the old project's skill stops")

		// Detaching reaches the next turn of an old chat, with no step.
		attach(atlas, projects.Defaults{})
		Expect(offOf(old).AddonsOff).To(ConsistOf("ad"), "the chat's own choice comes back")
	})

	It("refuses to attach outside a limit, naming the tool and the limit", func() {
		_, err := ps.Update(ctx, "alice", atlas.ID, projects.UpdateInput{Defaults: &projects.Defaults{Addons: []string{"ok"}}})
		Expect(err).NotTo(HaveOccurred())
		_, err = ps.Update(ctx, "alice", atlas.ID, projects.UpdateInput{Attached: &projects.Defaults{Addons: []string{"ad"}}})
		Expect(err).To(MatchError(And(ContainSubstring(`"ad"`), ContainSubstring("defaults.addons"))))
	})
})
