package studio_test

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

var _ = Describe("triggers and loops of a project agent", func() {
	var (
		r    *projRig
		acme projects.Project
		beta projects.Project
		ag   agents.Agent
	)
	BeforeEach(func() {
		r = newProjRig()
		acme, beta = r.project("Acme"), r.project("Beta")
		ag = r.agentOf(acme, "Helper")
		r.connRig.agent = ag
		r.mkdirs("/Projects/acme/files/inbox")
		r.mkdirs("/Projects/beta/files/inbox")
	})
	fileTrigger := func(folder, agentID string) studio.TriggerInput {
		return studio.TriggerInput{Name: "Arrivals", Prompt: "Sort the new file.", Kind: triggers.KindFile, Folder: folder,
			Model: "m", Quiet: time.Second, AgentID: agentID, Enabled: true}
	}
	// arrive drops a file in a folder and lets the watch see it settle.
	arrive := func(rel string) {
		r.put(rel, "hello")
		r.pass()
		r.sh.clock.Advance(2 * time.Second)
		r.pass()
	}

	It("accepts a folder inside the files of the project, and runs when a file arrives", func() {
		tr, err := r.host.CreateTrigger(r.ctx, alice, fileTrigger("/Projects/acme/files/inbox", ag.ID))
		Expect(err).NotTo(HaveOccurred())
		r.pass()
		arrive("/Projects/acme/files/inbox/a.txt")
		run := r.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusDone), "run = %+v", run)
		chat := r.chatOf(run)
		Expect(chat.ProjectID).To(Equal(acme.ID), "the run is a chat of the project")
	})

	It("accepts the memory folder of the project, which the agent can read", func() {
		_, err := r.host.CreateTrigger(r.ctx, alice, fileTrigger("/Projects/acme/memory", ag.ID))
		Expect(err).NotTo(HaveOccurred())
	})

	It("refuses a folder outside the fence, at creation and at update", func() {
		for _, out := range []string{"/Other", "/", "/Projects/beta/files/inbox", "/Projects/acme", "/Projects"} {
			_, err := r.host.CreateTrigger(r.ctx, alice, fileTrigger(out, ag.ID))
			Expect(err).To(MatchError(studio.ErrBadTrigger), "folder %s", out)
		}
		tr, err := r.host.CreateTrigger(r.ctx, alice, fileTrigger("/Projects/acme/files/inbox", ag.ID))
		Expect(err).NotTo(HaveOccurred())
		_, err = r.host.UpdateTrigger(r.ctx, alice, tr.ID, fileTrigger("/Other", ag.ID))
		Expect(err).To(MatchError(studio.ErrBadTrigger))
		got, err := r.host.GetTrigger(r.ctx, alice, tr.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Folder).To(HaveSuffix("/Projects/acme/files/inbox"))
	})

	It("refuses to move a trigger to an agent whose fence does not hold its folder", func() {
		other := r.agentOf(beta, "Other helper")
		tr, err := r.host.CreateTrigger(r.ctx, alice, fileTrigger("/Projects/acme/files/inbox", ag.ID))
		Expect(err).NotTo(HaveOccurred())
		_, err = r.host.UpdateTrigger(r.ctx, alice, tr.ID, fileTrigger("/Projects/acme/files/inbox", other.ID))
		Expect(err).To(MatchError(studio.ErrBadTrigger), "an agent of another project")
		plain, err := r.svc.SaveAgent(r.ctx, alice, agents.Agent{Name: "Plain", Model: "m", Folders: []agents.AgentFolder{{Node: r.nodeOf("/Other"), Mode: agents.FolderRead}}})
		Expect(err).NotTo(HaveOccurred())
		_, err = r.host.UpdateTrigger(r.ctx, alice, tr.ID, fileTrigger("/Projects/acme/files/inbox", plain.ID))
		Expect(err).To(MatchError(studio.ErrBadTrigger), "an agent with another folder")
		got, err := r.host.GetTrigger(r.ctx, alice, tr.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.AgentID).To(Equal(ag.ID))
	})

	It("refuses a new file trigger while the project is archived, and an enable of an old one", func() {
		tr, err := r.host.CreateTrigger(r.ctx, alice, fileTrigger("/Projects/acme/files/inbox", ag.ID))
		Expect(err).NotTo(HaveOccurred())
		_, err = r.host.SetTriggerEnabled(r.ctx, alice, tr.ID, false)
		Expect(err).NotTo(HaveOccurred())
		_, err = r.proj.Archive(r.ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		_, err = r.host.CreateTrigger(r.ctx, alice, fileTrigger("/Projects/acme/files/inbox", ag.ID))
		Expect(err).To(MatchError(studio.ErrBadTrigger))
		Expect(err).To(MatchError(ContainSubstring(studio.ProjectUnavailableText)))
		_, err = r.host.SetTriggerEnabled(r.ctx, alice, tr.ID, true)
		Expect(err).To(MatchError(studio.ErrBadTrigger), "a trigger of an archived project is not switched on")
		Expect(err).To(MatchError(ContainSubstring(studio.ProjectUnavailableText)))
		got, err := r.host.GetTrigger(r.ctx, alice, tr.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Enabled).To(BeFalse())
		_, err = r.proj.Restore(r.ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		on, err := r.host.SetTriggerEnabled(r.ctx, alice, tr.ID, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(on.Enabled).To(BeTrue())
	})

	It("refuses to switch a trigger on while the agent cannot be read, and switches it on once it can", func() {
		tr := r.schedule(studio.TriggerInput{AgentID: ag.ID})
		_, err := r.host.SetTriggerEnabled(r.ctx, alice, tr.ID, false)
		Expect(err).NotTo(HaveOccurred())
		r.flaky.down.Store(true)
		_, err = r.host.SetTriggerEnabled(r.ctx, alice, tr.ID, true)
		Expect(err).To(MatchError(studio.ErrBadTrigger))
		Expect(err).To(MatchError(ContainSubstring("Try again")))
		r.flaky.down.Store(false)
		got, err := r.host.GetTrigger(r.ctx, alice, tr.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Enabled).To(BeFalse())
		on, err := r.host.SetTriggerEnabled(r.ctx, alice, tr.ID, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(on.Enabled).To(BeTrue())
	})

	It("still switches on a trigger whose agent was deleted: the run says what is wrong", func() {
		tr := r.schedule(studio.TriggerInput{AgentID: ag.ID})
		_, err := r.host.SetTriggerEnabled(r.ctx, alice, tr.ID, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.svc.DeleteAgent(r.ctx, alice, ag.ID)).To(Succeed())
		on, err := r.host.SetTriggerEnabled(r.ctx, alice, tr.ID, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(on.Enabled).To(BeTrue())
	})

	It("refuses to switch a trigger on while the files folder of its project was replaced", func() {
		tr := r.schedule(studio.TriggerInput{AgentID: ag.ID})
		_, err := r.host.SetTriggerEnabled(r.ctx, alice, tr.ID, false)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.sh.files.Store().Delete(r.ctx, r.user, r.home+"/Projects/acme/files")).To(Succeed())
		r.put("/Projects/acme/files/a.txt", "again")
		_, err = r.host.SetTriggerEnabled(r.ctx, alice, tr.ID, true)
		Expect(err).To(MatchError(studio.ErrBadTrigger))
		Expect(err).To(MatchError(ContainSubstring(studio.ProjectFoldersChangedText)))
		got, err := r.host.GetTrigger(r.ctx, alice, tr.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Enabled).To(BeFalse())
	})

	Describe("switching on through a change from a chat", func() {
		var chat string
		BeforeEach(func() { chat = r.chat(ag.ID).ID })
		off, on := false, true
		archive := func() {
			_, err := r.proj.Archive(r.ctx, alice, acme.ID)
			Expect(err).NotTo(HaveOccurred())
		}
		restore := func() {
			_, err := r.proj.Restore(r.ctx, alice, acme.ID)
			Expect(err).NotTo(HaveOccurred())
		}
		enabled := func(id string) bool {
			got, err := r.host.GetTrigger(r.ctx, alice, id)
			Expect(err).NotTo(HaveOccurred())
			return got.Enabled
		}
		It("refuses an agent-driven resume while archived, and resumes after restore", func() {
			tr := r.schedule(studio.TriggerInput{AgentID: ag.ID})
			_, err := r.host.SetTriggerEnabled(r.ctx, alice, tr.ID, false)
			Expect(err).NotTo(HaveOccurred())
			off1, err := r.host.GetTrigger(r.ctx, alice, tr.ID)
			Expect(err).NotTo(HaveOccurred())
			archive()
			_, _, err = r.host.ProposeChange(r.ctx, alice, chat, off1, studio.TriggerPatch{Enabled: &on})
			Expect(err).To(MatchError(studio.ErrBadTrigger))
			Expect(err).To(MatchError(ContainSubstring(studio.ProjectUnavailableText)))
			Expect(enabled(tr.ID)).To(BeFalse())
			restore()
			_, applied, err := r.host.ProposeChange(r.ctx, alice, chat, off1, studio.TriggerPatch{Enabled: &on})
			Expect(err).NotTo(HaveOccurred())
			Expect(applied).To(BeTrue())
			Expect(enabled(tr.ID)).To(BeTrue())
		})
		It("refuses to undo a pause while archived, and undoes it after restore", func() {
			tr := r.schedule(studio.TriggerInput{AgentID: ag.ID})
			rec, applied, err := r.host.ProposeChange(r.ctx, alice, chat, tr, studio.TriggerPatch{Enabled: &off})
			Expect(err).NotTo(HaveOccurred())
			Expect(applied).To(BeTrue())
			Expect(enabled(tr.ID)).To(BeFalse())
			archive()
			_, err = r.host.UndoChange(r.ctx, alice, rec.ID)
			Expect(err).To(MatchError(studio.ErrBadTrigger))
			Expect(err).To(MatchError(ContainSubstring(studio.ProjectUnavailableText)))
			Expect(enabled(tr.ID)).To(BeFalse())
			restore()
			_, err = r.host.UndoChange(r.ctx, alice, rec.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(enabled(tr.ID)).To(BeTrue())
		})
		It("refuses to undo the delete of an enabled trigger while archived, and undoes it after restore", func() {
			tr := r.schedule(studio.TriggerInput{AgentID: ag.ID})
			card, err := r.host.ProposeDelete(r.ctx, alice, chat, tr)
			Expect(err).NotTo(HaveOccurred())
			_, err = r.host.ApplyChange(r.ctx, alice, card.ID)
			Expect(err).NotTo(HaveOccurred())
			_, err = r.host.GetTrigger(r.ctx, alice, tr.ID)
			Expect(err).To(HaveOccurred(), "deleted")
			archive()
			_, err = r.host.UndoChange(r.ctx, alice, card.ID)
			Expect(err).To(MatchError(studio.ErrBadTrigger))
			Expect(err).To(MatchError(ContainSubstring(studio.ProjectUnavailableText)))
			_, err = r.host.GetTrigger(r.ctx, alice, tr.ID)
			Expect(err).To(HaveOccurred(), "still deleted")
			restore()
			_, err = r.host.UndoChange(r.ctx, alice, card.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(enabled(tr.ID)).To(BeTrue())
		})
		It("refuses to confirm a proposal of a project agent while archived", func() {
			p, err := r.host.ProposeTrigger(r.ctx, alice, studio.TriggerInput{Name: "P", Prompt: "p", Kind: triggers.KindSchedule, Schedule: "daily 08:00", TZ: "UTC", AgentID: ag.ID, Model: "m", SourceChatID: chat})
			Expect(err).NotTo(HaveOccurred())
			archive()
			_, err = r.host.ConfirmProposal(r.ctx, alice, p.ID, "UTC")
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(ContainSubstring(studio.ProjectUnavailableText)))
		})
	})

	It("skips a file trigger while the project is archived and runs on the files that came, after restore", func() {
		tr, err := r.host.CreateTrigger(r.ctx, alice, fileTrigger("/Projects/acme/files/inbox", ag.ID))
		Expect(err).NotTo(HaveOccurred())
		r.pass()
		before, err := r.host.GetTrigger(r.ctx, alice, tr.ID)
		Expect(err).NotTo(HaveOccurred())
		_, err = r.proj.Archive(r.ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		arrive("/Projects/acme/files/inbox/while-archived.txt")
		run := r.waitEnded(tr.ID)
		Expect(run.Status).To(Equal(triggers.StatusSkipped), "run = %+v", run)
		Expect(run.ChatID).To(BeEmpty())
		got, err := r.host.GetTrigger(r.ctx, alice, tr.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Enabled).To(BeTrue())
		Expect(got.Since).To(Equal(before.Since))
		Expect(r.notes()).To(BeEmpty())

		_, err = r.proj.Restore(r.ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		arrive("/Projects/acme/files/inbox/after.txt")
		until("a run after the restore", func() bool {
			runs, _ := r.host.TriggerRuns(r.ctx, alice, tr.ID, 5)
			for _, x := range runs {
				if x.Status == triggers.StatusDone {
					return true
				}
			}
			r.sh.clock.Advance(2 * time.Second)
			r.pass()
			return false
		})
	})

	It("keeps a file trigger the user switched off disabled through archive and restore", func() {
		tr, err := r.host.CreateTrigger(r.ctx, alice, fileTrigger("/Projects/acme/files/inbox", ag.ID))
		Expect(err).NotTo(HaveOccurred())
		_, err = r.host.SetTriggerEnabled(r.ctx, alice, tr.ID, false)
		Expect(err).NotTo(HaveOccurred())
		_, err = r.proj.Archive(r.ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		arrive("/Projects/acme/files/inbox/x.txt")
		_, err = r.proj.Restore(r.ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		arrive("/Projects/acme/files/inbox/y.txt")
		got, err := r.host.GetTrigger(r.ctx, alice, tr.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Enabled).To(BeFalse())
		runs, _ := r.host.TriggerRuns(r.ctx, alice, tr.ID, 5)
		Expect(runs).To(BeEmpty())
	})

	for _, name := range []string{"files", "memory"} {
		It(fmt.Sprintf("skips a schedule run, tells nobody and keeps the trigger on while the %s folder was replaced", name), func() {
			tr := r.schedule(studio.TriggerInput{AgentID: ag.ID})
			before, err := r.host.GetTrigger(r.ctx, alice, tr.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(r.sh.files.Store().Delete(r.ctx, r.user, r.home+"/Projects/acme/"+name)).To(Succeed())
			r.put("/Projects/acme/"+name+"/a.txt", "again")
			r.due()
			run := r.waitEnded(tr.ID)
			Expect(run.Status).To(Equal(triggers.StatusSkipped), "run = %+v", run)
			got, err := r.host.GetTrigger(r.ctx, alice, tr.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Enabled).To(BeTrue())
			Expect(got.Since).To(Equal(before.Since))
			Expect(r.notes()).To(BeEmpty())
		})
	}

	It("runs a trigger inside the fence: its tools cannot read a file of the home", func() {
		tr, err := r.host.CreateTrigger(r.ctx, alice, studio.TriggerInput{Name: "Fenced", Prompt: "Look around. k:fenced", Kind: triggers.KindFile,
			Folder: "/Projects/acme/files/inbox", Model: "m", Quiet: time.Second, AgentID: ag.ID, Enabled: true})
		Expect(err).NotTo(HaveOccurred())
		r.mu.Lock()
		r.steps["k:fenced"] = []studiotest.ToolStep{call("read_file", `{"path":"/AGENTS.md"}`), call("read_file", `{"path":"/Other/secret.txt"}`)}
		r.mu.Unlock()
		r.pass()
		arrive("/Projects/acme/files/inbox/a.txt")
		run := r.waitEnded(tr.ID)
		Expect(run.ChatID).NotTo(BeEmpty(), "run = %+v", run)
		for _, res := range r.h.Results() {
			Expect(outOf(res)).NotTo(ContainSubstring(outsideMark))
		}
		Expect(r.stat(r.actor(run.ChatID), "/AGENTS.md")).To(HaveOccurred())
		Expect(r.stat(r.actor(run.ChatID), "/Projects/acme/files/a.txt")).To(Succeed())
	})

	It("keeps a loop of a project agent in the project listing, active, while the project is archived", func() {
		conv := r.chat(ag.ID).ID
		ls, err := loops.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		host, err := studio.NewLoops(r.svc, studio.LoopOptions{Store: ls, Clock: r.sh.clock.Now, Tick: time.Hour})
		Expect(err).NotTo(HaveOccurred())
		r.svc.SetLoops(host)
		DeferCleanup(host.Close)
		l, err := host.Start(r.ctx, alice, conv, studio.LoopInput{Interval: "5m", Prompt: "Check the build."})
		Expect(err).NotTo(HaveOccurred())
		if l.State == loops.StatePending {
			_, err = host.SetState(r.ctx, alice, conv, l.ID, loops.StateActive)
			Expect(err).NotTo(HaveOccurred())
		}
		_, err = r.proj.Archive(r.ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		for range 3 {
			r.sh.clock.Advance(6 * time.Minute)
			Expect(host.Pass(r.ctx)).To(Succeed())
		}
		links, err := r.svc.ProjectLoops(r.ctx, alice, acme.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(links).To(HaveLen(1))
		Expect(links[0].State).To(Equal(string(loops.StateActive)))
	})
})
