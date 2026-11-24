package studio_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/projects"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
)

var _ = Describe("chats in a project", func() {
	ctx := context.Background()
	const bob = "bob"

	type rig struct {
		svc  *studio.Service
		proj *projects.Service
		h    *studiotest.GateHarness
	}
	newRig := func() rig {
		f := newFiles()
		ps, err := projects.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		proj := projects.NewService(projects.Options{Store: ps, Files: f})
		st, err := store.NewJSON("")
		Expect(err).NotTo(HaveOccurred())
		h := studiotest.NewGateHarness()
		svc, err := studio.New(studio.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: h, Files: f, Projects: proj,
			MCPServers: func(context.Context, string, string) []studio.ModelMCPServer {
				return []studio.ModelMCPServer{{Name: "crm", Auto: true}, {Name: "mail", Auto: true}}
			}})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = svc.Close(context.Background()) })
		return rig{svc, proj, h}
	}
	mk := func(r rig, owner, name string, d projects.Defaults) projects.Project {
		p, err := r.proj.Create(ctx, owner, projects.CreateInput{Name: name, Defaults: d})
		Expect(err).NotTo(HaveOccurred())
		return p
	}

	It("starts a chat in a project with its defaults, and an explicit switch wins", func() {
		r := newRig()
		p := mk(r, alice, "Acme", projects.Defaults{MCPServers: []string{"crm"}})
		rec, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "m", ProjectID: p.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.ProjectID).To(Equal(p.ID))
		Expect(rec.McpOff).To(Equal([]string{"mail"}), "only crm stays on")
		got, err := r.svc.Get(ctx, alice, rec.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ProjectID).To(Equal(p.ID))

		rec, err = r.svc.Create(ctx, alice, studio.CreateInput{Model: "m", ProjectID: p.ID, Switches: studio.Switches{McpOff: []string{"crm"}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.McpOff).To(Equal([]string{"crm"}), "the request wins")
	})

	It("skips a default that names nothing installed", func() {
		r := newRig()
		p := mk(r, alice, "Acme", projects.Defaults{MCPServers: []string{"gone"}, Addons: []string{"nope"}, Skills: []string{"missing"}})
		rec, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "m", ProjectID: p.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(rec.McpOff).To(BeEmpty())
		Expect(rec.AddonsOff).To(BeEmpty())
		Expect(rec.SkillsOff).To(BeEmpty())
	})

	It("refuses an unknown, foreign or archived project at create", func() {
		r := newRig()
		_, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "m", ProjectID: "nope"})
		Expect(err).To(MatchError(projects.ErrNotFound))
		theirs := mk(r, bob, "Theirs", projects.Defaults{})
		_, err = r.svc.Create(ctx, alice, studio.CreateInput{Model: "m", ProjectID: theirs.ID})
		Expect(err).To(MatchError(projects.ErrNotFound))
		mine := mk(r, alice, "Mine", projects.Defaults{})
		_, err = r.proj.Archive(ctx, alice, mine.ID)
		Expect(err).NotTo(HaveOccurred())
		_, err = r.svc.Create(ctx, alice, studio.CreateInput{Model: "m", ProjectID: mine.ID})
		Expect(errors.Is(err, session.ErrInvalid)).To(BeTrue(), "%v", err)
	})

	It("moves a chat into a project without touching its switches", func() {
		r := newRig()
		p := mk(r, alice, "Acme", projects.Defaults{MCPServers: []string{"crm"}})
		rec, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "m", Switches: studio.Switches{McpOff: []string{"mail"}, SkillsOff: []string{"s1"}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.svc.SetChatProject(ctx, alice, rec.ID, p.ID)).To(Succeed())
		got, _ := r.svc.Get(ctx, alice, rec.ID)
		Expect(got.ProjectID).To(Equal(p.ID))
		Expect(got.McpOff).To(Equal([]string{"mail"}))
		Expect(got.SkillsOff).To(Equal([]string{"s1"}))
		Expect(r.svc.SetChatProject(ctx, alice, rec.ID, "")).To(Succeed())
		got, _ = r.svc.Get(ctx, alice, rec.ID)
		Expect(got.ProjectID).To(BeEmpty())
	})

	It("refuses another owner's project and another owner's chat", func() {
		r := newRig()
		theirs := mk(r, bob, "Theirs", projects.Defaults{})
		id := create(r.svc, alice)
		Expect(r.svc.SetChatProject(ctx, alice, id, theirs.ID)).To(MatchError(projects.ErrNotFound))
		mine := mk(r, alice, "Mine", projects.Defaults{})
		Expect(r.svc.SetChatProject(ctx, bob, id, mine.ID)).To(MatchError(session.ErrNotFound))
	})

	It("refuses to move a chat while a turn runs", func() {
		r := newRig()
		p := mk(r, alice, "Acme", projects.Defaults{})
		id := create(r.svc, alice)
		sent(r.svc, alice, id, "hi")
		until("the turn to start", func() bool { return len(r.h.Sent()) == 1 })
		Expect(r.svc.SetChatProject(ctx, alice, id, p.ID)).To(MatchError(studio.ErrChatBusy))
		r.h.Gate <- struct{}{}
		idle(r.svc, alice, id)
		Expect(r.svc.SetChatProject(ctx, alice, id, p.ID)).To(Succeed())
	})

	It("keeps a chat usable after its project is deleted", func() {
		r := newRig()
		p := mk(r, alice, "Acme", projects.Defaults{})
		rec, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "m", ProjectID: p.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.proj.Delete(ctx, alice, p.ID)).To(Succeed())
		got, err := r.svc.Get(ctx, alice, rec.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ProjectID).To(Equal(p.ID), "the ID dangles")
		sent(r.svc, alice, rec.ID, "hi")
		until("the turn to start", func() bool { return len(r.h.Sent()) == 1 })
		r.h.Gate <- struct{}{}
		idle(r.svc, alice, rec.ID)
	})

	It("sets the pulled projects within the limits", func() {
		r := newRig()
		home := mk(r, alice, "Home", projects.Defaults{})
		a := mk(r, alice, "A", projects.Defaults{})
		b := mk(r, alice, "B", projects.Defaults{})
		arch := mk(r, alice, "Old", projects.Defaults{})
		_, err := r.proj.Archive(ctx, alice, arch.ID)
		Expect(err).NotTo(HaveOccurred())
		theirs := mk(r, bob, "Theirs", projects.Defaults{})
		rec, err := r.svc.Create(ctx, alice, studio.CreateInput{Model: "m", ProjectID: home.ID})
		Expect(err).NotTo(HaveOccurred())

		Expect(r.svc.SetChatPulled(ctx, alice, rec.ID, []string{a.ID, b.ID})).To(Succeed())
		got, _ := r.svc.Get(ctx, alice, rec.ID)
		Expect(got.PulledProjects).To(Equal([]string{a.ID, b.ID}))

		Expect(errors.Is(r.svc.SetChatPulled(ctx, alice, rec.ID, []string{home.ID}), session.ErrInvalid)).To(BeTrue(), "the home project")
		Expect(errors.Is(r.svc.SetChatPulled(ctx, alice, rec.ID, []string{a.ID, a.ID}), session.ErrInvalid)).To(BeTrue(), "a duplicate")
		Expect(errors.Is(r.svc.SetChatPulled(ctx, alice, rec.ID, []string{arch.ID}), session.ErrInvalid)).To(BeTrue(), "an archived project")
		Expect(r.svc.SetChatPulled(ctx, alice, rec.ID, []string{theirs.ID})).To(MatchError(projects.ErrNotFound))
		nine := make([]string, 9)
		for i := range nine {
			nine[i] = "p" + string(rune('a'+i))
		}
		Expect(errors.Is(r.svc.SetChatPulled(ctx, alice, rec.ID, nine), session.ErrInvalid)).To(BeTrue(), "a ninth entry")
		got, _ = r.svc.Get(ctx, alice, rec.ID)
		Expect(got.PulledProjects).To(Equal([]string{a.ID, b.ID}), "a refused call changes nothing")

		Expect(r.svc.SetChatPulled(ctx, alice, rec.ID, nil)).To(Succeed())
		got, _ = r.svc.Get(ctx, alice, rec.ID)
		Expect(got.PulledProjects).To(BeEmpty())
	})
})
