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
		Network: "registries", Created: now, Updated: now,
	}
}

// Run registers the specs that check the store.
func Run(newStore NewStore) {
	ctx := context.Background()
	It("MemoryPolicyRoundTripAndLegacyReplacement", func() {
		s := newStore()
		for _, mode := range []string{"off", "incoming_only", "incoming_and_replies", "summary"} {
			a := sample("memory", "u1", "Memory")
			a.Collections = nil
			a.CollectionPermissions = &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: "docs", Search: true}, {Collection: "archive", DeleteEntries: true}, {Collection: "destination"}}}
			a.Memory = &agents.MemorySettings{Version: 1, AddMemory: true, Destination: "destination", ToolStorage: "summary", AutomaticIngestion: mode}
			Expect(s.Put(ctx, a)).To(Succeed())
			got, err := s.Get(ctx, "u1", "memory")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Memory).To(Equal(a.Memory))
			Expect(got.CollectionPermissions).To(Equal(a.CollectionPermissions))
			Expect(got.Collections).To(Equal([]string{"docs"}))
			got.Memory.AddMemory = false
			got.CollectionPermissions.Grants[0].Search = false
			again, err := s.Get(ctx, "u1", "memory")
			Expect(err).NotTo(HaveOccurred())
			Expect(again.Memory.AddMemory).To(BeTrue())
			Expect(again.CollectionPermissions.Grants[0].Search).To(BeTrue())
			list, err := s.List(ctx, "u1")
			Expect(err).NotTo(HaveOccurred())
			Expect(list[0].Memory).To(Equal(a.Memory))
			all, err := s.ListAll(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(all[0].CollectionPermissions).To(Equal(a.CollectionPermissions))
		}
		legacy := sample("memory", "u1", "Memory")
		Expect(s.Put(ctx, legacy)).To(Succeed())
		got, err := s.Get(ctx, "u1", "memory")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Memory.AddMemory).To(BeFalse())
		Expect(got.Memory.AutomaticIngestion).To(Equal("off"))
		Expect(got.CollectionPermissions.Grants).To(Equal([]agents.CollectionGrant{{Collection: "docs", Search: true}}))
	})
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
		Expect(got.Network).To(Equal("registries"))
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
	It("round-trips the folder and its mode", func() {
		s := newStore()
		a := sample("a1", "u1", "Scoped")
		a.FolderID, a.FolderMode = 42, agents.FolderRead
		Expect(s.Put(ctx, a)).To(Succeed())
		got, err := s.Get(ctx, "u1", "a1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.FolderID).To(Equal(int64(42)))
		Expect(got.FolderMode).To(Equal(agents.FolderRead))

		a.FolderMode = agents.FolderWrite
		Expect(s.Put(ctx, a)).To(Succeed())
		got, _ = s.Get(ctx, "u1", "a1")
		Expect(got.FolderMode).To(Equal(agents.FolderWrite), "after replace")

		// A mode without a folder is an agent whose folder is not chosen yet.
		a.FolderID = 0
		Expect(s.Put(ctx, a)).To(Succeed())
		l, err := s.List(ctx, "u1")
		Expect(err).NotTo(HaveOccurred())
		Expect(l).To(HaveLen(1))
		Expect(l[0].FolderID).To(BeZero())
		Expect(l[0].FolderMode).To(Equal(agents.FolderWrite), "the unset state is kept")

		a.FolderMode = ""
		Expect(s.Put(ctx, a)).To(Succeed())
		all, err := s.ListAll(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(all[0].FolderID).To(BeZero())
		Expect(all[0].FolderMode).To(BeEmpty(), "after clearing")
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
	a := sample("seed", "u1", "Seeded")
	a.CollectionPermissions = &agents.CollectionPermissions{Version: 1, Grants: []agents.CollectionGrant{{Collection: "docs", Search: true}, {Collection: "destination"}}}
	a.Memory = &agents.MemorySettings{Version: 1, AddMemory: true, Destination: "destination", ToolStorage: "summary", AutomaticIngestion: "incoming_and_replies"}
	Expect(s.Put(context.Background(), a)).To(Succeed())
}

// CheckSeed checks what Seed wrote.
func CheckSeed(s agents.Store) {
	got, err := s.Get(context.Background(), "u1", "seed")
	Expect(err).NotTo(HaveOccurred())
	Expect(got.Name).To(Equal("Seeded"))
	Expect(got.Addons).To(HaveLen(2))
	Expect(got.Memory).To(Equal(&agents.MemorySettings{Version: 1, AddMemory: true, Destination: "destination", ToolStorage: "summary", AutomaticIngestion: "incoming_and_replies"}))
	Expect(got.CollectionPermissions.Grants).To(Equal([]agents.CollectionGrant{{Collection: "docs", Search: true}, {Collection: "destination"}}))
}
