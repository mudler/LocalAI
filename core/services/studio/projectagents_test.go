package studio_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/projects"
)

var _ = Describe("saved agents of a project", func() {
	ctx := context.Background()

	newRig := func() (*shareRig, *projects.Service) {
		var proj *projects.Service
		r := newShareRig(sayOK, func(d *studio.Deps) {
			ps, err := projects.NewJSON("")
			Expect(err).NotTo(HaveOccurred())
			proj = projects.NewService(projects.Options{Store: ps, Files: d.Files})
			d.Projects = proj
		})
		return r, proj
	}
	mk := func(proj *projects.Service, owner, name string) projects.Project {
		p, err := proj.Create(ctx, owner, projects.CreateInput{Name: name})
		Expect(err).NotTo(HaveOccurred())
		return p
	}

	It("sets the project at creation and keeps it when a write omits it", func() {
		r, proj := newRig()
		p := mk(proj, alice, "Acme")
		ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Helper", ProjectID: p.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(ag.ProjectID).To(Equal(p.ID))
		got, err := r.svc.SaveAgent(ctx, alice, agents.Agent{ID: ag.ID, Name: "Helper 2"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got.ProjectID).To(Equal(p.ID), "a write without the field keeps it")
		same, err := r.svc.SaveAgent(ctx, alice, agents.Agent{ID: ag.ID, Name: "Helper 3", ProjectID: p.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(same.ProjectID).To(Equal(p.ID), "the same value is not a change")
	})

	It("refuses to change or add a project id on an existing agent", func() {
		r, proj := newRig()
		p1, p2 := mk(proj, alice, "One"), mk(proj, alice, "Two")
		in, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "In", ProjectID: p1.ID})
		Expect(err).NotTo(HaveOccurred())
		plain, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Plain"})
		Expect(err).NotTo(HaveOccurred())
		_, err = r.svc.SaveAgent(ctx, alice, agents.Agent{ID: in.ID, Name: "In", ProjectID: p2.ID})
		Expect(errors.Is(err, agents.ErrInvalid)).To(BeTrue(), "%v", err)
		_, err = r.svc.SaveAgent(ctx, alice, agents.Agent{ID: plain.ID, Name: "Plain", ProjectID: p1.ID})
		Expect(errors.Is(err, agents.ErrInvalid)).To(BeTrue(), "%v", err)
		got, _ := r.svc.Agent(ctx, alice, in.ID)
		Expect(got.ProjectID).To(Equal(p1.ID))
	})

	It("takes only the caller's own active project at creation", func() {
		r, proj := newRig()
		theirs := mk(proj, bob, "Theirs")
		_, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "A", ProjectID: theirs.ID})
		Expect(err).To(MatchError(projects.ErrNotFound))
		_, err = r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "B", ProjectID: "nope"})
		Expect(err).To(MatchError(projects.ErrNotFound))
		mine := mk(proj, alice, "Mine")
		_, err = proj.Archive(ctx, alice, mine.ID)
		Expect(err).NotTo(HaveOccurred())
		_, err = r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "C", ProjectID: mine.ID})
		Expect(errors.Is(err, agents.ErrInvalid)).To(BeTrue(), "%v", err)
	})

	It("refuses to share a project agent", func() {
		r, proj := newRig()
		p := mk(proj, alice, "Acme")
		ag, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Helper", ProjectID: p.ID})
		Expect(err).NotTo(HaveOccurred())
		_, err = r.svc.AgentShareRef(ctx, alice, ag.ID)
		Expect(errors.Is(err, agents.ErrInvalid)).To(BeTrue(), "%v", err)
		Expect(err.Error()).To(ContainSubstring("project agents cannot be shared"))
		plain, err := r.svc.SaveAgent(ctx, alice, agents.Agent{Name: "Plain"})
		Expect(err).NotTo(HaveOccurred())
		_, err = r.svc.AgentShareRef(ctx, alice, plain.ID)
		Expect(err).NotTo(HaveOccurred())
	})

	It("lists the agents of a project of the caller, sorted by name", func() {
		r, proj := newRig()
		p, q := mk(proj, alice, "One"), mk(proj, alice, "Two")
		for _, a := range []agents.Agent{{Name: "zeta", ProjectID: p.ID}, {Name: "Alpha", ProjectID: p.ID}, {Name: "other", ProjectID: q.ID}, {Name: "plain"}} {
			_, err := r.svc.SaveAgent(ctx, alice, a)
			Expect(err).NotTo(HaveOccurred())
		}
		list, err := r.svc.AgentsOfProject(ctx, alice, p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(list).To(HaveLen(2))
		Expect(list[0].Name).To(Equal("Alpha"))
		Expect(list[1].Name).To(Equal("zeta"))
		none, err := r.svc.AgentsOfProject(ctx, bob, p.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(none).To(BeEmpty(), "another owner sees none")
		_, err = r.svc.AgentsOfProject(ctx, alice, "")
		Expect(errors.Is(err, agents.ErrInvalid)).To(BeTrue())
	})
})
