// Package projectstest is the contract suite every projects.Store passes.
// Run registers its specs in the Ginkgo tree of the calling package.
package projectstest

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/projects"
)

// NewStore returns an empty store. It runs inside a spec, so it may use
// GinkgoT().TempDir() and DeferCleanup.
type NewStore func() projects.Store

var t0 = time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)

func proj(id, owner, slug, name string) projects.Project {
	return projects.Project{ID: id, Owner: owner, Slug: slug, Name: name, Node: 7,
		Status: projects.StatusActive, Updated: t0}
}

// Seed writes one project, for a test that reopens a store.
func Seed(s projects.Store) {
	Expect(s.Put(context.Background(), proj("seed", "u1", "seeded", "Seeded"))).To(Succeed())
}

// CheckSeed checks what Seed wrote.
func CheckSeed(s projects.Store) {
	p, err := s.Get(context.Background(), "u1", "seed")
	Expect(err).NotTo(HaveOccurred())
	Expect(p.Name).To(Equal("Seeded"))
	Expect(p.Slug).To(Equal("seeded"))
}

// Run registers the store specs. Call it inside a Describe.
func Run(newStore NewStore) {
	ctx := context.Background()
	It("round-trips a project and lists by name", func() {
		s := newStore()
		a := proj("p1", "u1", "zeta", "Zeta")
		a.Status, a.Error = projects.StatusBroken, "manifest unreadable"
		Expect(s.Put(ctx, a)).To(Succeed())
		Expect(s.Put(ctx, proj("p2", "u1", "alpha", "Alpha"))).To(Succeed())
		got, err := s.Get(ctx, "u1", "p1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Owner).To(Equal("u1"))
		Expect(got.Slug).To(Equal("zeta"))
		Expect(got.Name).To(Equal("Zeta"))
		Expect(got.Node).To(Equal(int64(7)))
		Expect(got.Status).To(Equal(projects.StatusBroken))
		Expect(got.Error).To(Equal("manifest unreadable"))
		Expect(got.Updated.Equal(t0)).To(BeTrue())
		l, err := s.List(ctx, "u1")
		Expect(err).NotTo(HaveOccurred())
		Expect(l).To(HaveLen(2))
		Expect(l[0].ID).To(Equal("p2"), "sorted by name")
		Expect(l[1].ID).To(Equal("p1"))
	})
	It("isolates owners", func() {
		s := newStore()
		Expect(s.Put(ctx, proj("p1", "u1", "docs", "Docs"))).To(Succeed())
		Expect(s.Put(ctx, proj("p2", "u2", "other", "Other"))).To(Succeed())
		_, err := s.Get(ctx, "u2", "p1")
		Expect(err).To(MatchError(projects.ErrNotFound), "another owner's project")
		_, err = s.Get(ctx, "u1", "nope")
		Expect(err).To(MatchError(projects.ErrNotFound))
		l, _ := s.List(ctx, "u1")
		Expect(l).To(HaveLen(1))
		Expect(l[0].ID).To(Equal("p1"))
		Expect(s.Delete(ctx, "u2", "p1")).To(Succeed())
		_, err = s.Get(ctx, "u1", "p1")
		Expect(err).NotTo(HaveOccurred(), "another owner cannot delete it")
	})
	It("keeps a slug unique per owner, ignoring case", func() {
		s := newStore()
		Expect(s.Put(ctx, proj("a", "u1", "same", "Same"))).To(Succeed())
		Expect(s.Put(ctx, proj("b", "u1", "SAME", "Same 2"))).To(MatchError(projects.ErrNameTaken))
		Expect(s.Put(ctx, proj("c", "u2", "same", "Same"))).To(Succeed(), "another owner may use the slug")
		Expect(s.Put(ctx, proj("a", "u1", "Same", "Same"))).To(Succeed(), "a project keeps its own slug")
		got, _ := s.Get(ctx, "u1", "a")
		Expect(got.Slug).To(Equal("same"), "stored in lower case")
	})
	It("updates in place on the same ID", func() {
		s := newStore()
		Expect(s.Put(ctx, proj("p1", "u1", "docs", "Docs"))).To(Succeed())
		b := proj("p1", "u1", "docs", "Docs 2")
		b.Status = projects.StatusArchived
		Expect(s.Put(ctx, b)).To(Succeed())
		l, _ := s.List(ctx, "u1")
		Expect(l).To(HaveLen(1))
		Expect(l[0].Name).To(Equal("Docs 2"))
		Expect(l[0].Status).To(Equal(projects.StatusArchived))
	})
	It("deletes a project", func() {
		s := newStore()
		Expect(s.Put(ctx, proj("p1", "u1", "docs", "Docs"))).To(Succeed())
		Expect(s.Delete(ctx, "u1", "p1")).To(Succeed())
		Expect(s.Delete(ctx, "u1", "p1")).To(Succeed(), "deleting a missing project")
		_, err := s.Get(ctx, "u1", "p1")
		Expect(err).To(MatchError(projects.ErrNotFound), "after delete")
		Expect(s.Put(ctx, proj("p2", "u1", "docs", "Docs"))).To(Succeed(), "the slug is free again")
	})
	It("refuses an ID that another owner holds and leaves that project alone", func() {
		s := newStore()
		Expect(s.Put(ctx, proj("p1", "u1", "docs", "Docs"))).To(Succeed())
		err := s.Put(ctx, proj("p1", "u2", "mine", "Mine"))
		Expect(err).To(MatchError(projects.ErrInvalid))
		Expect(err.Error()).NotTo(ContainSubstring("u1"), "the message does not name the other owner")
		got, err := s.Get(ctx, "u1", "p1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Name).To(Equal("Docs"))
		Expect(got.Slug).To(Equal("docs"))
		l1, _ := s.List(ctx, "u1")
		Expect(l1).To(HaveLen(1))
		Expect(l1[0].Owner).To(Equal("u1"))
		l2, _ := s.List(ctx, "u2")
		Expect(l2).To(BeEmpty())
		Expect(s.Put(ctx, proj("p1", "u1", "docs", "Docs 2"))).To(Succeed(), "the owner still updates in place")
	})
	It("lists an empty non-nil slice for an owner with no projects", func() {
		s := newStore()
		l, err := s.List(ctx, "nobody")
		Expect(err).NotTo(HaveOccurred())
		Expect(l).NotTo(BeNil())
		Expect(l).To(BeEmpty())
	})
	It("trims the slug and refuses an empty one", func() {
		s := newStore()
		Expect(s.Put(ctx, proj("a", "u1", "a", "A"))).To(Succeed())
		Expect(s.Put(ctx, proj("b", "u1", "a ", "B"))).To(MatchError(projects.ErrNameTaken))
		Expect(s.Put(ctx, proj("c", "u1", "  ", "C"))).To(MatchError(projects.ErrInvalid))
		Expect(s.Put(ctx, proj("d", "u1", "", "D"))).To(MatchError(projects.ErrInvalid))
		l, _ := s.List(ctx, "u1")
		Expect(l).To(HaveLen(1))
	})
	It("refuses a rename onto a slug the owner holds and keeps the row", func() {
		s := newStore()
		Expect(s.Put(ctx, proj("a", "u1", "one", "One"))).To(Succeed())
		Expect(s.Put(ctx, proj("b", "u1", "two", "Two"))).To(Succeed())
		Expect(s.Put(ctx, proj("b", "u1", "ONE", "Two renamed"))).To(MatchError(projects.ErrNameTaken))
		got, err := s.Get(ctx, "u1", "b")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Slug).To(Equal("two"))
		Expect(got.Name).To(Equal("Two"))
	})
}
