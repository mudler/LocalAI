// Package agentstest is the contract suite every agents.Store passes. Run
// registers Ginkgo specs, so call it inside a Describe of the implementation's
// test package.
package agentstest

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/agents"
)

// NewStore returns an empty store. Each spec calls it once; clean up with
// DeferCleanup.
type NewStore func() agents.Store

func sample(id, principal, name string) agents.Agent {
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	return agents.Agent{
		ID: id, Principal: principal, Name: name, Description: "d", Instructions: "Be brief.", Model: "m1",
		MCPServers: []string{"github"}, Addons: []string{"office", "sandbox"}, Collections: []string{"docs"}, Skills: []string{"review"},
		Created: now, Updated: now,
	}
}

// Run registers the specs that check the store.
func Run(newStore NewStore) {
	ctx := context.Background()
	It("RoundTrip", func() {
		s := newStore()
		a := sample("a1", "u1", "Reviewer")
		Expect(s.Put(ctx, a)).To(Succeed())
		got, err := s.Get(ctx, "u1", "a1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Name).To(Equal("Reviewer"))
		Expect(got.Instructions).To(Equal("Be brief."))
		Expect(got.Model).To(Equal("m1"))
		Expect(strings.Join(got.Addons, ",")).To(Equal("office,sandbox"))
		Expect(strings.Join(got.MCPServers, ",")).To(Equal("github"))
		Expect(strings.Join(got.Collections, ",")).To(Equal("docs"))
		Expect(strings.Join(got.Skills, ",")).To(Equal("review"))
		Expect(got.Created.Equal(a.Created)).To(BeTrue(), "got %+v", got)
		a.Name, a.Addons, a.Updated = "Reviewer 2", nil, a.Updated.Add(time.Hour)
		a.Created = time.Time{}
		Expect(s.Put(ctx, a)).To(Succeed())
		got, _ = s.Get(ctx, "u1", "a1")
		Expect(got.Name).To(Equal("Reviewer 2"))
		Expect(got.Addons).To(BeEmpty())
		Expect(got.Created.IsZero()).To(BeFalse(), "after replace: %+v", got)
	})
	It("ListAllSpansPrincipals", func() {
		s := newStore()
		for _, a := range []agents.Agent{sample("a", "u2", "Zed"), sample("b", "u1", "Beta"), sample("c", "u1", "Alpha")} {
			Expect(s.Put(ctx, a)).To(Succeed())
		}
		all, err := s.ListAll(ctx)
		Expect(err).NotTo(HaveOccurred())
		got := ""
		for _, a := range all {
			got += a.Principal + ":" + a.Name + " "
		}
		Expect(got).To(Equal("u1:Alpha u1:Beta u2:Zed "))
	})
	It("ListSortedByName", func() {
		s := newStore()
		for i, n := range []string{"beta", "Alpha", "gamma"} {
			Expect(s.Put(ctx, sample(string(rune('a'+i)), "u1", n))).To(Succeed())
		}
		got, err := s.List(ctx, "u1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(3))
		Expect(got[0].Name).To(Equal("Alpha"))
		Expect(got[1].Name).To(Equal("beta"))
		Expect(got[2].Name).To(Equal("gamma"))
	})
	It("OtherPrincipal", func() {
		s := newStore()
		Expect(s.Put(ctx, sample("a1", "u1", "x"))).To(Succeed())
		_, err := s.Get(ctx, "u2", "a1")
		Expect(err).To(MatchError(agents.ErrNotFound), "get")
		Expect(s.Delete(ctx, "u2", "a1")).To(MatchError(agents.ErrNotFound), "delete")
		Expect(s.Put(ctx, sample("a1", "u2", "y"))).To(MatchError(agents.ErrNotFound), "put over another user's id")
		l, _ := s.List(ctx, "u2")
		Expect(l).To(BeEmpty(), "u2 sees %+v", l)
		got, err := s.Get(ctx, "u1", "a1")
		Expect(err).NotTo(HaveOccurred(), "u1 lost its agent")
		Expect(got.Name).To(Equal("x"))
	})
	It("NameUniquePerUser", func() {
		s := newStore()
		Expect(s.Put(ctx, sample("a1", "u1", "Helper"))).To(Succeed())
		Expect(s.Put(ctx, sample("a2", "u1", " helper "))).To(MatchError(agents.ErrNameTaken), "same name other case")
		Expect(s.Put(ctx, sample("a1", "u1", "HELPER"))).To(Succeed(), "an agent may change the case of its own name")
		Expect(s.Put(ctx, sample("a3", "u2", "Helper"))).To(Succeed(), "another user may use the name")
	})
	It("Delete", func() {
		s := newStore()
		_ = s.Put(ctx, sample("a1", "u1", "x"))
		Expect(s.Delete(ctx, "u1", "a1")).To(Succeed())
		_, err := s.Get(ctx, "u1", "a1")
		Expect(err).To(MatchError(agents.ErrNotFound), "get after delete")
		Expect(s.Delete(ctx, "u1", "a1")).To(MatchError(agents.ErrNotFound), "second delete")
		Expect(s.Put(ctx, sample("a2", "u1", "x"))).To(Succeed(), "the name is free after delete")
	})
	It("ReturnedListsAreCopies", func() {
		s := newStore()
		_ = s.Put(ctx, sample("a1", "u1", "x"))
		got, _ := s.Get(ctx, "u1", "a1")
		got.Addons[0] = "changed"
		again, _ := s.Get(ctx, "u1", "a1")
		Expect(again.Addons[0]).To(Equal("office"), "a caller changed the stored agent: %+v", again)
	})
}

// Seed stores one agent; CheckSeed finds it in another store on the same data.
func Seed(s agents.Store) {
	Expect(s.Put(context.Background(), sample("seed", "u1", "Seeded"))).To(Succeed())
}

// CheckSeed checks what Seed wrote.
func CheckSeed(s agents.Store) {
	got, err := s.Get(context.Background(), "u1", "seed")
	Expect(err).NotTo(HaveOccurred())
	Expect(got.Name).To(Equal("Seeded"))
	Expect(got.Addons).To(HaveLen(2))
}
