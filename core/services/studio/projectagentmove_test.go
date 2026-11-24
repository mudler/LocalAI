package studio_test

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var _ = Describe("moving a saved agent to another project", func() {
	var (
		r    *projRig
		acme projects.Project
		beta projects.Project
	)
	BeforeEach(func() {
		r = newProjRig()
		acme, beta = r.project("Acme"), r.project("Beta")
		r.put("/Projects/beta/files/b.txt", "beta-secret")
	})

	move := func(id string, in studio.AgentMove) (studio.AgentMoveReport, error) {
		return r.svc.MoveAgentProject(r.ctx, alice, id, in)
	}
	stored := func(id string) agents.Agent {
		GinkgoHelper()
		a, err := r.svc.Agent(r.ctx, alice, id)
		Expect(err).NotTo(HaveOccurred())
		return a
	}
	plain := func(name string) agents.Agent {
		GinkgoHelper()
		a, err := r.svc.SaveAgent(r.ctx, alice, agents.Agent{Name: name, Model: "m", Builtins: []string{"act.files", builtins.GrantProjectMemoryWrite}})
		Expect(err).NotTo(HaveOccurred())
		return a
	}

	It("moves a plain agent into a project: the project's two folders, no memory grant, old chats stop, new chats work", func() {
		a := plain("Helper")
		old := r.chat(a.ID)
		rep, err := move(a.ID, studio.AgentMove{ProjectID: beta.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.Moved).To(BeTrue())
		Expect(rep.Chats).To(Equal(1))
		Expect(rep.MemoryGrantDropped).To(BeTrue())
		got := stored(a.ID)
		files, memory := r.fence(beta)
		Expect(got.ProjectID).To(Equal(beta.ID))
		Expect(got.Folders).To(Equal([]agents.AgentFolder{{Node: files.Node, Mode: agents.FolderWrite}, {Node: memory.Node, Mode: agents.FolderRead}}))
		Expect(got.Builtins).To(Equal([]string{"act.files"}))

		_, err = r.svc.Send(r.ctx, alice, old.ID, "again")
		Expect(err).To(MatchError(studio.ErrAgentMoved))
		rec, err := r.svc.Get(r.ctx, alice, old.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.ProjectID).To(BeEmpty(), "an old chat keeps its record")

		fresh := r.chat(a.ID)
		Expect(fresh.ProjectID).To(Equal(beta.ID))
		res := r.run(fresh.ID, "read", call("read_file", `{"path":"/b.txt"}`))
		Expect(res[0].Err).NotTo(HaveOccurred(), outOf(res[0]))
		Expect(res[0].Out).To(ContainSubstring("beta-secret"))
	})

	It("moves an agent between projects and keeps the old project's files out of reach", func() {
		a := r.agentOf(acme, "Helper")
		old := r.chat(a.ID)
		rep, err := move(a.ID, studio.AgentMove{ProjectID: beta.ID, FilesMode: agents.FolderRead})
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.Chats).To(Equal(1))
		got := stored(a.ID)
		files, _ := r.fence(beta)
		Expect(got.Folders[0]).To(Equal(agents.AgentFolder{Node: files.Node, Mode: agents.FolderRead}))
		_, err = r.svc.Send(r.ctx, alice, old.ID, "again")
		Expect(err).To(MatchError(studio.ErrAgentMoved))
		act := r.actor(r.chat(a.ID).ID)
		Expect(r.stat(act, "/Projects/beta/files/b.txt")).To(Succeed())
		Expect(r.stat(act, "/Projects/acme/files/a.txt")).To(HaveOccurred(), "the old project is outside the new fence")
		Expect(r.write(act, "/Projects/beta/files/new.txt")).To(HaveOccurred(), "files_mode read")
	})

	It("takes an agent out of its project only with named folders or an explicit whole home", func() {
		a := r.agentOf(acme, "Helper")
		old := r.chat(a.ID)
		for name, in := range map[string]studio.AgentMove{
			"nothing":       {},
			"both":          {Folders: []studio.AgentFolderWrite{{Path: "/Projects/acme/files", Mode: agents.FolderRead}}, WholeHome: true},
			"bad folder":    {Folders: []studio.AgentFolderWrite{{Path: "/Nope", Mode: agents.FolderRead}}},
			"folder nomode": {Folders: []studio.AgentFolderWrite{{Path: "/Projects/acme/files"}}},
		} {
			_, err := move(a.ID, in)
			Expect(err).To(HaveOccurred(), name)
			Expect(stored(a.ID).ProjectID).To(Equal(acme.ID), "%s changed nothing", name)
		}
		rep, err := move(a.ID, studio.AgentMove{Folders: []studio.AgentFolderWrite{{Path: "/Projects/acme/files", Mode: agents.FolderRead}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.Chats).To(Equal(1))
		got := stored(a.ID)
		Expect(got.ProjectID).To(BeEmpty())
		Expect(got.Folders).To(HaveLen(1))
		_, err = r.svc.Send(r.ctx, alice, old.ID, "again")
		Expect(err).To(MatchError(studio.ErrAgentMoved))
		// Once out, an agent with a folder is fenced like any other.
		Expect(r.stat(r.actor(r.chat(a.ID).ID), "/Other/secret.txt")).To(HaveOccurred())

		whole := r.agentOf(acme, "Whole")
		_, err = move(whole.ID, studio.AgentMove{WholeHome: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(stored(whole.ID).FolderList()).To(BeEmpty())
	})

	It("refuses a bad target and changes nothing", func() {
		a := plain("Helper")
		_, err := r.proj.Archive(r.ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		foreign, err := r.proj.Create(r.ctx, bob, projects.CreateInput{Name: "Theirs"})
		Expect(err).NotTo(HaveOccurred())
		for name, c := range map[string]struct {
			in   studio.AgentMove
			want error
		}{
			"archived":     {studio.AgentMove{ProjectID: acme.ID}, agents.ErrInvalid},
			"unknown":      {studio.AgentMove{ProjectID: "nope"}, projects.ErrNotFound},
			"another user": {studio.AgentMove{ProjectID: foreign.ID}, projects.ErrNotFound},
			"shared ref":   {studio.AgentMove{ProjectID: "s:12"}, agents.ErrInvalid},
			"bad mode":     {studio.AgentMove{ProjectID: beta.ID, FilesMode: "admin"}, agents.ErrInvalid},
			"same project": {studio.AgentMove{}, agents.ErrInvalid},
		} {
			_, err := move(a.ID, c.in)
			Expect(err).To(MatchError(c.want), name)
			Expect(stored(a.ID).ProjectID).To(BeEmpty(), name)
		}
		_, err = r.svc.MoveAgentProject(r.ctx, bob, a.ID, studio.AgentMove{ProjectID: beta.ID})
		Expect(err).To(HaveOccurred(), "another user's agent")
		_, err = move("missing", studio.AgentMove{ProjectID: beta.ID})
		Expect(err).To(MatchError(agents.ErrNotFound))
	})

	It("keeps refusing a changed project on a plain save", func() {
		a := r.agentOf(acme, "Helper")
		_, err := r.svc.SaveAgent(r.ctx, alice, agents.Agent{ID: a.ID, Name: "Helper", Model: "m", ProjectID: beta.ID})
		Expect(err).To(MatchError(agents.ErrInvalid))
		_, err = r.svc.SaveAgent(r.ctx, alice, agents.Agent{ID: a.ID, Name: "Helper", Model: "m"})
		Expect(err).NotTo(HaveOccurred())
		Expect(stored(a.ID).ProjectID).To(Equal(acme.ID))
		p := plain("Plain")
		_, err = r.svc.SaveAgent(r.ctx, alice, agents.Agent{ID: p.ID, Name: "Plain", Model: "m", ProjectID: acme.ID})
		Expect(err).To(MatchError(agents.ErrInvalid))
	})

	It("reports what a move does on a dry run and writes nothing", func() {
		a := plain("Helper")
		r.chat(a.ID)
		r.chat(a.ID)
		r.put("/Memory/agents/"+a.ID+"/index.md", "my notes")
		tr := r.schedule(studio.TriggerInput{AgentID: a.ID})
		before := stored(a.ID)
		rep, err := move(a.ID, studio.AgentMove{ProjectID: beta.ID, DryRun: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.DryRun).To(BeTrue())
		Expect(rep.Moved).To(BeFalse())
		Expect(rep.Chats).To(Equal(2))
		Expect(rep.MemoryGrantDropped).To(BeTrue())
		Expect(stored(a.ID)).To(Equal(before))
		Expect(r.exists("/Memory/agents/" + a.ID + "/index.md")).To(BeTrue())
		got, err := r.host.GetTrigger(r.ctx, alice, tr.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Enabled).To(BeTrue())
	})

	It("lists the tools the limits of the new project take away", func() {
		_, err := r.proj.Update(r.ctx, alice, beta.ID, projects.UpdateInput{Defaults: &projects.Defaults{Skills: []string{"keep"}, Collections: []string{"docs"}}})
		Expect(err).NotTo(HaveOccurred())
		a, err := r.svc.SaveAgent(r.ctx, alice, agents.Agent{Name: "Helper", Model: "m", Skills: []string{"keep", "drop"}, Collections: []string{"docs", "other"}})
		Expect(err).NotTo(HaveOccurred())
		rep, err := move(a.ID, studio.AgentMove{ProjectID: beta.ID, DryRun: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.DroppedTools.Skills).To(Equal([]string{"drop"}))
		Expect(rep.DroppedTools.Collections).To(Equal([]string{"other"}))
		Expect(rep.DroppedTools.Addons).To(BeEmpty())
	})

	skill := "---\nname: plan\ndescription: A plan.\n---\nBody.\n"

	It("lists what the new project attaches and what the old one attached", func() {
		r.put("/Projects/"+beta.Slug+"/skills/plan/SKILL.md", skill)
		_, err := r.proj.Update(r.ctx, alice, acme.ID, projects.UpdateInput{Attached: &projects.Defaults{
			Connectors: []string{"mail"}, Addons: []string{"shared-addon", "old-addon"}}})
		Expect(err).NotTo(HaveOccurred())
		_, err = r.proj.Update(r.ctx, alice, beta.ID, projects.UpdateInput{Attached: &projects.Defaults{
			Skills: []string{"s1"}, Addons: []string{"shared-addon"}, MCPServers: []string{"srv"}}})
		Expect(err).NotTo(HaveOccurred())
		a := r.agentOf(acme, "Helper")
		check := func(rep studio.AgentMoveReport) {
			Expect(rep.AttachedNew.Skills).To(Equal([]string{"s1"}))
			Expect(rep.AttachedNew.Addons).To(Equal([]string{"shared-addon"}))
			Expect(rep.AttachedNew.MCPServers).To(Equal([]string{"srv"}))
			Expect(rep.AttachedNew.Connectors).To(BeEmpty())
			// A tool both projects attach keeps applying: it is not listed as stopped.
			Expect(rep.AttachedOld.Addons).To(Equal([]string{"old-addon"}))
			Expect(rep.AttachedOld.Connectors).To(Equal([]string{"mail"}))
			Expect(rep.AttachedOld.Skills).To(BeEmpty())
			Expect(rep.ProjectSkillsSwitch).To(BeTrue())
		}
		dry, err := move(a.ID, studio.AgentMove{ProjectID: beta.ID, DryRun: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(dry.Moved).To(BeFalse())
		check(dry)
		Expect(stored(a.ID).ProjectID).To(Equal(acme.ID))
		rep, err := move(a.ID, studio.AgentMove{ProjectID: beta.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.Moved).To(BeTrue())
		check(rep)
	})

	It("reports no attached tools for a move that has none, and the skill switch out of a project", func() {
		a := r.agentOf(acme, "Helper")
		rep, err := move(a.ID, studio.AgentMove{ProjectID: "", WholeHome: true, DryRun: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.AttachedNew.Skills).NotTo(BeNil())
		Expect(rep.AttachedNew.Skills).To(BeEmpty())
		Expect(rep.AttachedOld.Connectors).To(BeEmpty())
		Expect(rep.ProjectSkillsSwitch).To(BeFalse(), "neither project has a skill of its own")
		r.put("/Projects/"+acme.Slug+"/skills/plan/SKILL.md", skill)
		rep, err = move(a.ID, studio.AgentMove{ProjectID: "", WholeHome: true, DryRun: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.ProjectSkillsSwitch).To(BeTrue(), "the old project's skills stop")
		p := plain("Plain")
		rep, err = move(p.ID, studio.AgentMove{ProjectID: beta.ID, DryRun: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.ProjectSkillsSwitch).To(BeFalse())
		r.put("/Projects/"+beta.Slug+"/skills/plan/SKILL.md", skill)
		rep, err = move(p.ID, studio.AgentMove{ProjectID: beta.ID, DryRun: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(rep.ProjectSkillsSwitch).To(BeTrue(), "the new project's skills start")
	})

	It("starts the agent's own notes empty and keeps the old ones in the home", func() {
		a := plain("Helper")
		r.put("/Memory/agents/"+a.ID+"/index.md", "old facts")
		_, err := move(a.ID, studio.AgentMove{ProjectID: beta.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.exists("/Memory/agents/" + a.ID)).To(BeFalse())
		entries, err := r.sh.files.Store().List(r.ctx, r.user, r.home+"/Memory/Moved agent notes")
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(1))
		text, ok, err := r.sh.files.ReadHome(r.ctx, alice, "Memory/Moved agent notes/"+entries[0].Name+"/index.md", 1<<20)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(text).To(Equal("old facts"))
		// An agent without notes moves too.
		b := plain("Other")
		_, err = move(b.ID, studio.AgentMove{ProjectID: beta.ID})
		Expect(err).NotTo(HaveOccurred())
	})

	It("voids the waiting project notes of the agent and no others", func() {
		a := r.agentOf(acme, "Helper")
		a, err := r.svc.SaveAgent(r.ctx, alice, agents.Agent{ID: a.ID, Name: "Helper", Model: "m", Builtins: []string{builtins.GrantProjectMemoryWrite}})
		Expect(err).NotTo(HaveOccurred())
		sibling := r.agentOf(acme, "Sibling")
		sibling, err = r.svc.SaveAgent(r.ctx, alice, agents.Agent{ID: sibling.ID, Name: "Sibling", Model: "m", Builtins: []string{builtins.GrantProjectMemoryWrite}})
		Expect(err).NotTo(HaveOccurred())
		propose := func(agent string) string {
			p, err := r.svc.Changes().Propose(r.ctx, studio.ProposeInput{User: alice, Agent: agent, Kind: "memory", Scope: "project", Project: acme.ID,
				Target: "n.md", Content: "x", Summary: "a note"})
			Expect(err).NotTo(HaveOccurred())
			return p.ID
		}
		mine, others := propose(a.ID), propose(sibling.ID)
		dry, err := move(a.ID, studio.AgentMove{ProjectID: beta.ID, DryRun: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(dry.Proposals).To(Equal(1))
		_, err = move(a.ID, studio.AgentMove{ProjectID: beta.ID})
		Expect(err).NotTo(HaveOccurred())
		status := func(id string) contract.ProposalStatus {
			p, err := r.svc.Changes().Get(r.ctx, alice, id)
			Expect(err).NotTo(HaveOccurred())
			return p.Status
		}
		Expect(status(mine)).To(Equal(contract.ProposalStale))
		Expect(status(others)).To(Equal(contract.ProposalPending))
	})

	It("blocks the move while a chat of the agent runs a turn", func() {
		a := plain("Helper")
		rec := r.chat(a.ID)
		Expect(r.sh.chats.ClaimTurn(r.ctx, alice, rec.ID, "run-1")).To(Succeed())
		dry, err := move(a.ID, studio.AgentMove{ProjectID: beta.ID, DryRun: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(dry.Blockers).To(ConsistOf(studio.MoveBlockedBusy))
		_, err = move(a.ID, studio.AgentMove{ProjectID: beta.ID})
		Expect(err).To(MatchError(studio.ErrMoveBlocked))
		Expect(stored(a.ID).ProjectID).To(BeEmpty())
	})

	Describe("file triggers", func() {
		var a agents.Agent
		var tr triggers.Trigger
		BeforeEach(func() {
			a = r.agentOf(acme, "Helper")
			r.mkdirs("/Projects/acme/files/in")
			var err error
			tr, err = r.host.CreateTrigger(r.ctx, alice, studio.TriggerInput{Name: "Arrivals", Prompt: "sort", Kind: triggers.KindFile,
				Folder: "/Projects/acme/files/in", Quiet: time.Second, Model: "m", AgentID: a.ID, Enabled: true})
			Expect(err).NotTo(HaveOccurred())
		})

		It("refuses the move when a file trigger would watch outside the new folders", func() {
			dry, err := move(a.ID, studio.AgentMove{ProjectID: beta.ID, DryRun: true})
			Expect(err).NotTo(HaveOccurred())
			Expect(dry.TriggersOutside).To(Equal([]studio.MovedTrigger{{ID: tr.ID, Name: "Arrivals"}}))
			Expect(dry.Blockers).To(ConsistOf(studio.MoveBlockedTriggers))
			_, err = move(a.ID, studio.AgentMove{ProjectID: beta.ID})
			Expect(err).To(MatchError(studio.ErrMoveBlocked))
			Expect(stored(a.ID).ProjectID).To(Equal(acme.ID))
		})

		It("switches the trigger off when the person chose to", func() {
			_, err := move(a.ID, studio.AgentMove{ProjectID: beta.ID, DisableTriggers: true})
			Expect(err).NotTo(HaveOccurred())
			got, err := r.host.GetTrigger(r.ctx, alice, tr.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Enabled).To(BeFalse())
		})

		It("accepts the move when the trigger folder is inside the folders the agent keeps", func() {
			rep, err := move(a.ID, studio.AgentMove{Folders: []studio.AgentFolderWrite{{Path: "/Projects/acme/files", Mode: agents.FolderRead}}})
			Expect(err).NotTo(HaveOccurred())
			Expect(rep.TriggersOutside).To(BeEmpty())
			got, err := r.host.GetTrigger(r.ctx, alice, tr.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Enabled).To(BeTrue())
		})
	})

	It("ends the loops of the stale chats when they come due, and not the loops of a new chat", func() {
		a := r.agentOf(acme, "Helper")
		old := r.chat(a.ID)
		ls, err := loops.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		host, err := studio.NewLoops(r.svc, studio.LoopOptions{Store: ls, Clock: r.sh.clock.Now, Tick: time.Hour})
		Expect(err).NotTo(HaveOccurred())
		r.svc.SetLoops(host)
		DeferCleanup(host.Close)
		l, err := host.Start(r.ctx, alice, old.ID, studio.LoopInput{Interval: "5m", Prompt: "Check."})
		Expect(err).NotTo(HaveOccurred())
		if l.State == loops.StatePending {
			_, err = host.SetState(r.ctx, alice, old.ID, l.ID, loops.StateActive)
			Expect(err).NotTo(HaveOccurred())
		}
		dry, err := move(a.ID, studio.AgentMove{ProjectID: beta.ID, DryRun: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(dry.Loops).To(Equal(1))
		_, err = move(a.ID, studio.AgentMove{ProjectID: beta.ID})
		Expect(err).NotTo(HaveOccurred())
		r.sh.clock.Advance(6 * time.Minute)
		Expect(host.Pass(r.ctx)).To(Succeed())
		got, err := ls.Get(r.ctx, l.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(loops.StateEnded))
		Expect(got.Reason).To(ContainSubstring("moved"))
		Expect(got.Runs).To(BeZero(), "no run was spent")
	})

	It("gives a messaging sender a new chat after the move", func() {
		a := r.agentOf(acme, "Helper")
		r.connRig.agent = a
		r.bind(studio.ConnectorInput{Allow: []string{"11"}})
		r.bot.say(1, 11, "Ada", "ada", "hi")
		waitFor("the reply", func() bool { return len(r.bot.answers()) >= 1 })
		_, err := move(a.ID, studio.AgentMove{ProjectID: beta.ID})
		Expect(err).NotTo(HaveOccurred())
		r.bot.say(2, 11, "Ada", "ada", "again")
		waitFor("the second reply", func() bool { return len(r.bot.answers()) >= 2 })
		Expect(r.bot.answers()[1]).To(Equal("ok"))
		chats, err := r.svc.List(r.ctx, alice, 100)
		Expect(err).NotTo(HaveOccurred())
		var projects []string
		for _, c := range chats {
			if c.Origin == session.OriginConnector {
				projects = append(projects, c.ProjectID)
			}
		}
		Expect(projects).To(ConsistOf(acme.ID, beta.ID), fmt.Sprint(projects))
	})
})
