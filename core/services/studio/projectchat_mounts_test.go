package studio_test

import (
	"context"
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

var _ = Describe("the mounts of a chat in a project", func() {
	ctx := context.Background()
	const bob = "bob"

	type rig struct {
		svc  *studio.Service
		proj *projects.Service
		st   store.Store
		f    *files.Service
		h    *studiotest.GateHarness
	}
	newRig := func() rig {
		f := newFiles()
		ps, err := projects.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		proj := projects.NewService(projects.Options{Store: ps, Files: f})
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		ag, err := agents.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		h := studiotest.NewGateHarness()
		svc, err := studio.New(studio.Deps{Store: st, Agents: ag, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h, Files: f, Projects: proj})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(context.Background()) })
		return rig{svc, proj, st, f, h}
	}
	mk := func(r rig, owner, name string) projects.Project {
		p, err := r.proj.Create(ctx, owner, projects.CreateInput{Name: name})
		Expect(err).NotTo(HaveOccurred())
		return p
	}
	mount := func(v workspace.View, at string) (workspace.Mount, bool) {
		for _, m := range v.Mounts {
			if m.At == at {
				return m, true
			}
		}
		return workspace.Mount{}, false
	}

	It("mounts the project and its memory, and a pulled project read only", func() {
		r := newRig()
		p, q := mk(r, alice, "Acme"), mk(r, alice, "Beta")
		rec, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "m", ProjectID: p.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.svc.SetChatPulled(ctx, alice, rec.ID, []string{q.ID})).To(Succeed())
		x, err := r.svc.ChatAccess(ctx, alice, rec.ID)
		Expect(err).NotTo(HaveOccurred())
		v := x.View()
		pm, ok := mount(v, "project")
		Expect(ok).To(BeTrue(), "%+v", v.Mounts)
		Expect(pm.Mode).To(Equal(workspace.ModeWrite))
		Expect(pm.Path).To(HaveSuffix("/files"))
		mm, ok := mount(v, "project-memory")
		Expect(ok).To(BeTrue())
		Expect(mm.Path).To(HaveSuffix("/memory"))
		Expect(mm.Mode).To(Equal(workspace.ModeRead), "project memory changes only by a confirmed proposal")
		pq, ok := mount(v, "pulled-"+q.Slug)
		Expect(ok).To(BeTrue())
		Expect(pq.Mode).To(Equal(workspace.ModeRead))
		_, ok = mount(v, "pulled-"+q.Slug+"-memory")
		Expect(ok).To(BeTrue())
		t, err := x.Place().Resolve("/workspace/pulled-" + q.Slug + "/a.md")
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Write).To(BeFalse())
		Expect(session.LayoutPrompt(v, session.PromptScope(v))).To(ContainSubstring("/workspace/project: "))
		Expect(v.Notes).To(ConsistOf(`This chat belongs to the project "Acme".`))
	})

	It("starts a chat whose project is gone without project mounts", func() {
		r := newRig()
		p := mk(r, alice, "Acme")
		rec, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "m", ProjectID: p.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.proj.Delete(ctx, alice, p.ID)).To(Succeed())
		x, err := r.svc.ChatAccess(ctx, alice, rec.ID)
		Expect(err).NotTo(HaveOccurred())
		_, ok := mount(x.View(), "project")
		Expect(ok).To(BeFalse())
		Expect(x.View().Notes).To(BeEmpty())
	})

	It("drops archived, foreign and missing pulled projects with one notice and keeps the stored list", func() {
		r := newRig()
		home, good, arch := mk(r, alice, "Home"), mk(r, alice, "Good"), mk(r, alice, "Old")
		theirs := mk(r, bob, "Theirs")
		rec, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "m", ProjectID: home.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.svc.SetChatPulled(ctx, alice, rec.ID, []string{good.ID, arch.ID})).To(Succeed())
		_, err = r.proj.Archive(ctx, alice, arch.ID)
		Expect(err).NotTo(HaveOccurred())
		// The store holds ids the setter would refuse.
		st := r.st
		Expect(st.SetPulledProjects(ctx, alice, rec.ID, []string{good.ID, arch.ID, theirs.ID, "gone"})).To(Succeed())

		x, err := r.svc.ChatAccess(ctx, alice, rec.ID)
		Expect(err).NotTo(HaveOccurred())
		v := x.View()
		_, ok := mount(v, "pulled-"+good.Slug)
		Expect(ok).To(BeTrue())
		for _, s := range []string{arch.Slug, theirs.Slug} {
			_, ok = mount(v, "pulled-"+s)
			Expect(ok).To(BeFalse(), s)
		}
		Expect(v.Notes).To(HaveLen(2))
		Expect(v.Notes[0]).To(HavePrefix("This chat belongs to the project"))
		Expect(v.Notes[1]).To(ContainSubstring("3 pulled projects"))
		got, err := r.svc.Get(ctx, alice, rec.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.PulledProjects).To(HaveLen(4), "the stored list is not rewritten")
	})

	It("gives a saved-agent chat with a folder no project mounts", func() {
		r := newRig()
		p, q := mk(r, alice, "Acme"), mk(r, alice, "Beta")
		Expect(r.f.EnsureHome(ctx, alice)).To(Succeed())
		_, err := r.f.Store().Mkdir(ctx, r.f.Actor(alice), session.HomePath(alice)+"/Work")
		Expect(err).NotTo(HaveOccurred())
		a, err := r.svc.SaveAgentWithFolder(ctx, alice, agents.Agent{Name: "Scoped", Model: "m"}, &studio.AgentFolderWrite{Path: "/Work", Mode: agents.FolderWrite})
		Expect(err).NotTo(HaveOccurred())
		rec, err := r.svc.Create(ctx, alice, studio.CreateInput{AgentID: a.ID})
		Expect(err).NotTo(HaveOccurred())
		st := r.st
		Expect(st.SetProject(ctx, alice, rec.ID, p.ID)).To(Succeed())
		Expect(st.SetPulledProjects(ctx, alice, rec.ID, []string{q.ID})).To(Succeed())
		x, err := r.svc.ChatAccess(ctx, alice, rec.ID)
		Expect(err).NotTo(HaveOccurred())
		for _, m := range x.View().Mounts {
			Expect(m.At).NotTo(Or(Equal("project"), Equal("project-memory"), HavePrefix("pulled-")))
		}
		Expect(x.View().Notes).To(BeEmpty())
	})

	// write runs the file tool write_file of a chat, as the model would.
	write := func(r rig, x session.Access, p string) error {
		GinkgoHelper()
		for _, tool := range r.f.Tools(x) {
			if tool.Name == "write_file" {
				_, err := tool.Run(ctx, []byte(`{"path":"`+p+`","content":"hello"}`))
				return err
			}
		}
		Fail("no write_file tool")
		return nil
	}

	It("refuses a write to a pulled mount and to the project memory mount, and allows the project files", func() {
		r := newRig()
		p, q := mk(r, alice, "Acme"), mk(r, alice, "Beta")
		rec, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "m", ProjectID: p.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.svc.SetChatPulled(ctx, alice, rec.ID, []string{q.ID})).To(Succeed())
		x, err := r.svc.ChatAccess(ctx, alice, rec.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(write(r, x, "/workspace/pulled-"+q.Slug+"/a.md")).To(MatchError(ContainSubstring("read-only")))
		Expect(write(r, x, "/workspace/pulled-"+q.Slug+"-memory/a.md")).To(MatchError(ContainSubstring("read-only")))
		Expect(write(r, x, "/workspace/project-memory/index.md")).To(MatchError(ContainSubstring("read-only")))
		Expect(write(r, x, "/workspace/project/a.md")).To(Succeed())
		got, ok, err := r.f.ReadHome(ctx, alice, "Projects/"+p.Slug+"/files/a.md", 1<<10)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(got).To(Equal("hello"))
		before, _, _ := r.f.ReadHome(ctx, alice, "Projects/"+p.Slug+"/memory/index.md", 1<<10)
		Expect(write(r, x, "/workspace/project-memory/index.md")).To(HaveOccurred())
		after, _, _ := r.f.ReadHome(ctx, alice, "Projects/"+p.Slug+"/memory/index.md", 1<<10)
		Expect(after).To(Equal(before), "nothing reached the project memory")
		Expect(after).NotTo(Equal("hello"))
	})

	It("names the project in the prompt, on one line with the description cut at 200 characters", func() {
		r := newRig()
		long := "Line one\nline two " + strings.Repeat("x", 400)
		p, err := r.proj.Create(ctx, alice, projects.CreateInput{Name: "Acme", Description: long})
		Expect(err).NotTo(HaveOccurred())
		rec, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "m", ProjectID: p.ID})
		Expect(err).NotTo(HaveOccurred())
		x, err := r.svc.ChatAccess(ctx, alice, rec.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(x.View().Notes).To(HaveLen(1))
		line := x.View().Notes[0]
		Expect(line).To(HavePrefix(`This chat belongs to the project "Acme". Project description: Line one line two xxx`))
		Expect(line).NotTo(ContainSubstring("\n"))
		Expect(line).To(HaveSuffix("…"))
		Expect([]rune(strings.TrimPrefix(line, `This chat belongs to the project "Acme". Project description: `))).To(HaveLen(200))
		Expect(session.LayoutPrompt(x.View(), session.PromptScope(x.View()))).To(ContainSubstring(line))
		// A chat with no project hears nothing of the sort.
		plain, _ := r.svc.Create(ctx, alice, studio.CreateInput{Model: "m"})
		px, err := r.svc.ChatAccess(ctx, alice, plain.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(px.View().Notes).To(BeEmpty())
	})

	Describe("a chat of a saved agent that has a folder", func() {
		scopedAgent := func(r rig) agents.Agent {
			GinkgoHelper()
			Expect(r.f.EnsureHome(ctx, alice)).To(Succeed())
			_, err := r.f.Store().Mkdir(ctx, r.f.Actor(alice), session.HomePath(alice)+"/Work")
			Expect(err).NotTo(HaveOccurred())
			a, err := r.svc.SaveAgentWithFolder(ctx, alice, agents.Agent{Name: "Scoped", Model: "m"}, &studio.AgentFolderWrite{Path: "/Work", Mode: agents.FolderWrite})
			Expect(err).NotTo(HaveOccurred())
			return a
		}

		It("cannot start in a project, join one or pull one", func() {
			r := newRig()
			p, q := mk(r, alice, "Acme"), mk(r, alice, "Beta")
			a := scopedAgent(r)
			_, err := r.svc.Create(ctx, alice, studio.CreateInput{AgentID: a.ID, ProjectID: p.ID})
			Expect(errors.Is(err, session.ErrInvalid)).To(BeTrue(), "%v", err)
			rec, err := r.svc.Create(ctx, alice, studio.CreateInput{AgentID: a.ID})
			Expect(err).NotTo(HaveOccurred())
			Expect(errors.Is(r.svc.SetChatProject(ctx, alice, rec.ID, p.ID), session.ErrInvalid)).To(BeTrue())
			Expect(errors.Is(r.svc.SetChatPulled(ctx, alice, rec.ID, []string{q.ID}), session.ErrInvalid)).To(BeTrue())
			Expect(r.svc.SetChatProject(ctx, alice, rec.ID, "")).To(Succeed(), "leaving is always allowed")
			Expect(r.svc.SetChatPulled(ctx, alice, rec.ID, nil)).To(Succeed(), "so is clearing")
			got, _ := r.svc.Get(ctx, alice, rec.ID)
			Expect(got.ProjectID).To(BeEmpty())
			Expect(got.PulledProjects).To(BeEmpty())
		})

		It("is not a project chat for a sub-agent either", func() {
			r := newRig()
			p := mk(r, alice, "Acme")
			a := scopedAgent(r)
			parent, err := r.svc.Create(ctx, alice, studio.CreateInput{AgentID: a.ID})
			Expect(err).NotTo(HaveOccurred())
			Expect(r.st.SetProject(ctx, alice, parent.ID, p.ID)).To(Succeed()) // a stale record
			kid := session.ChatRecord{ID: "kid-fenced", Principal: alice, Title: "Sub", Kind: session.KindSubAgent, ParentID: parent.ID, Model: "m", ScopedAgent: true}
			Expect(r.st.CreateChat(ctx, kid)).To(Succeed())
			x, err := r.svc.ChatAccess(ctx, alice, kid.ID)
			Expect(err).NotTo(HaveOccurred())
			for _, m := range x.View().Mounts {
				Expect(m.At).NotTo(Or(Equal("project"), Equal("project-memory"), HavePrefix("pulled-")))
			}
		})
	})

	It("keeps the project and the pulled list in a fork", func() {
		r := newRig()
		p, q := mk(r, alice, "Acme"), mk(r, alice, "Beta")
		rec, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "m", ProjectID: p.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.svc.SetChatPulled(ctx, alice, rec.ID, []string{q.ID})).To(Succeed())
		fork, err := r.svc.Fork(ctx, alice, rec.ID, 0, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(fork.ProjectID).To(Equal(p.ID))
		Expect(fork.PulledProjects).To(Equal([]string{q.ID}))
		got, err := r.svc.Get(ctx, alice, fork.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ProjectID).To(Equal(p.ID))
		Expect(got.PulledProjects).To(Equal([]string{q.ID}))
	})

	It("gives a sub-agent the project of its parent: files read and write, a pulled project read only", func() {
		r := newRig()
		p, q := mk(r, alice, "Acme"), mk(r, alice, "Beta")
		parent, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "m", ProjectID: p.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.svc.SetChatPulled(ctx, alice, parent.ID, []string{q.ID})).To(Succeed())
		kid := session.ChatRecord{ID: "kid-1", Principal: alice, Title: "Sub", Kind: session.KindSubAgent, ParentID: parent.ID, Model: "m"}
		Expect(r.st.CreateChat(ctx, kid)).To(Succeed())
		x, err := r.svc.ChatAccess(ctx, alice, kid.ID)
		Expect(err).NotTo(HaveOccurred())
		v := x.View()
		pm, ok := mount(v, "project")
		Expect(ok).To(BeTrue(), "%+v", v.Mounts)
		Expect(pm.Mode).To(Equal(workspace.ModeWrite))
		mm, ok := mount(v, "project-memory")
		Expect(ok).To(BeTrue())
		Expect(mm.Mode).To(Equal(workspace.ModeRead))
		pq, ok := mount(v, "pulled-"+q.Slug)
		Expect(ok).To(BeTrue())
		Expect(pq.Mode).To(Equal(workspace.ModeRead))
		Expect(write(r, x, "/workspace/project/sub.md")).To(Succeed())
		Expect(write(r, x, "/workspace/pulled-"+q.Slug+"/sub.md")).To(MatchError(ContainSubstring("read-only")))

		// The parent is read each turn: when it leaves the project, so does the child.
		Expect(r.svc.SetChatProject(ctx, alice, parent.ID, "")).To(Succeed())
		x, err = r.svc.ChatAccess(ctx, alice, kid.ID)
		Expect(err).NotTo(HaveOccurred())
		_, ok = mount(x.View(), "project")
		Expect(ok).To(BeFalse())
	})

	It("refuses SetChatPulled on a sub-agent and while a turn runs, as SetChatProject does", func() {
		r := newRig()
		p, q := mk(r, alice, "Acme"), mk(r, alice, "Beta")
		parent, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "m", ProjectID: p.ID})
		Expect(err).NotTo(HaveOccurred())
		kid := session.ChatRecord{ID: "kid-2", Principal: alice, Title: "Sub", Kind: session.KindSubAgent, ParentID: parent.ID, Model: "m"}
		Expect(r.st.CreateChat(ctx, kid)).To(Succeed())
		Expect(r.svc.SetChatPulled(ctx, alice, kid.ID, []string{q.ID})).To(MatchError(studio.ErrSubAgentReadOnly))

		id := create(r.svc, alice)
		sent(r.svc, alice, id, "hi")
		until("the turn to start", func() bool { return len(r.h.Sent()) == 1 })
		Expect(r.svc.SetChatPulled(ctx, alice, id, []string{q.ID})).To(MatchError(studio.ErrChatBusy))
		r.h.Gate <- struct{}{}
		idle(r.svc, alice, id)
		Expect(r.svc.SetChatPulled(ctx, alice, id, []string{q.ID})).To(Succeed())
	})
})
