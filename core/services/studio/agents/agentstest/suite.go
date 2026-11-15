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
	"github.com/mudler/LocalAI/core/services/studio/connsets"
	"github.com/mudler/LocalAI/core/services/studio/resourcesets"
)

// NewStore returns an empty store. Each spec calls it once; clean up with
// DeferCleanup.
type NewStore func() agents.Store

func sample(id, principal, name string) agents.Agent {
	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	return agents.Agent{
		ID: id, Principal: principal, Name: name, Description: "d", Instructions: "Be brief.", Model: "m1",
		MCPServers: []string{"github"}, Addons: []string{"office", "sandbox"}, Collections: []string{"docs"}, Skills: []string{"review"},
		Network: "registries", SandboxProfile: "media", Memory: agents.MemoryMineAndOwn, MemorySave: agents.MemorySaveAlways, BuiltinsOff: []string{"triggers", "web"}, BuiltinsOn: []string{"saved_agents"}, Builtins: []string{"memory.write", "skills.write"},
		Connections: connsets.Sets{"tracker": {"b-work", "b-home"}},
		Resources:   resourcesets.Sets{"tables": {{ID: "db-one", Mode: "read"}, {ID: "db-two", Mode: "write"}}}, Created: now, Updated: now,
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
		Expect(got.Network).To(Equal("registries"))
		Expect(got.SandboxProfile).To(Equal("media"))
		Expect(got.Memory).To(Equal("mine_and_own"))
		Expect(got.MemorySave).To(Equal("always"))
		Expect(got.BuiltinsOff).To(Equal([]string{"triggers", "web"}))
		Expect(got.BuiltinsOn).To(Equal([]string{"saved_agents"}))
		Expect(got.Builtins).To(Equal([]string{"memory.write", "skills.write"}))
		Expect(got.Connections).To(Equal(connsets.Sets{"tracker": {"b-work", "b-home"}}))
		Expect(got.Resources).To(Equal(resourcesets.Sets{"tables": {{ID: "db-one", Mode: "read"}, {ID: "db-two", Mode: "write"}}}))
		got.Resources["tables"][0].Mode = "write"
		got.Connections["tracker"][0] = "changed"
		again, _ := s.Get(ctx, "u1", "a1")
		Expect(again.Connections).To(Equal(connsets.Sets{"tracker": {"b-work", "b-home"}}), "a caller cannot change a stored agent")
		Expect(again.Resources["tables"][0].Mode).To(Equal("read"), "a caller cannot change a stored agent")
		Expect(got.Created.Equal(a.Created)).To(BeTrue(), "got %+v", got)
		a.Name, a.Addons, a.Updated = "Reviewer 2", nil, a.Updated.Add(time.Hour)
		a.Created = time.Time{}
		Expect(s.Put(ctx, a)).To(Succeed())
		got, _ = s.Get(ctx, "u1", "a1")
		Expect(got.Name).To(Equal("Reviewer 2"))
		Expect(got.Addons).To(BeEmpty())
		Expect(got.Created.IsZero()).To(BeFalse(), "after replace: %+v", got)
	})
	It("ResourcesAreStoredAndCleared", func() {
		s := newStore()
		a := sample("r1", "u1", "Scoped")
		Expect(s.Put(ctx, a)).To(Succeed())
		got, _ := s.Get(ctx, "u1", "r1")
		Expect(got.Resources["tables"]).To(HaveLen(2))
		a.Resources = nil
		Expect(s.Put(ctx, a)).To(Succeed())
		got, _ = s.Get(ctx, "u1", "r1")
		Expect(got.Resources).To(BeEmpty())
		b := sample("r2", "u1", "Plain")
		b.Resources = nil
		Expect(s.Put(ctx, b)).To(Succeed())
		got, _ = s.Get(ctx, "u1", "r2")
		Expect(got.Resources).To(BeEmpty(), "an agent saved without the field has none")
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
	It("round-trips a list of folders in order and mirrors the first into the old pair", func() {
		s := newStore()
		a := sample("a1", "u1", "Multi")
		a.Folders = []agents.AgentFolder{{Node: 9, Mode: agents.FolderWrite}, {Node: 3, Mode: agents.FolderRead}, {Node: 5, Mode: agents.FolderRead}}
		Expect(s.Put(ctx, a)).To(Succeed())
		got, err := s.Get(ctx, "u1", "a1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Folders).To(Equal(a.Folders))
		Expect(got.FolderID).To(Equal(int64(9)))
		Expect(got.FolderMode).To(Equal(agents.FolderWrite))

		// A shorter list replaces the longer one.
		a.Folders = a.Folders[1:2]
		Expect(s.Put(ctx, a)).To(Succeed())
		l, _ := s.List(ctx, "u1")
		Expect(l[0].Folders).To(Equal([]agents.AgentFolder{{Node: 3, Mode: agents.FolderRead}}))

		// An agent that has only the old pair reads as a one-entry list.
		old := sample("a2", "u1", "Old")
		old.FolderID, old.FolderMode = 4, agents.FolderRead
		Expect(s.Put(ctx, old)).To(Succeed())
		got, _ = s.Get(ctx, "u1", "a2")
		Expect(got.Folders).To(Equal([]agents.AgentFolder{{Node: 4, Mode: agents.FolderRead}}))

		// No folders at all clears the list.
		a.Folders, a.FolderID, a.FolderMode = nil, 0, ""
		Expect(s.Put(ctx, a)).To(Succeed())
		got, _ = s.Get(ctx, "u1", "a1")
		Expect(got.Folders).To(BeEmpty())
		Expect(got.HasFolder()).To(BeFalse())
		all, _ := s.ListAll(ctx)
		Expect(all).To(HaveLen(2))

		Expect(s.Delete(ctx, "u1", "a2")).To(Succeed())
		again := sample("a2", "u1", "Old")
		Expect(s.Put(ctx, again)).To(Succeed())
		got, _ = s.Get(ctx, "u1", "a2")
		Expect(got.Folders).To(BeEmpty(), "the folders of a deleted agent do not come back")
	})
	It("ShareRefIsSetOnceAndKeptByPut", func() {
		s := newStore()
		Expect(s.Put(ctx, sample("a1", "u1", "Reviewer"))).To(Succeed())
		Expect(s.Put(ctx, sample("a2", "u2", "Other"))).To(Succeed())
		_, err := s.GetShared(ctx, "a1")
		Expect(err).To(MatchError(agents.ErrNotFound), "an agent that was never shared is not found by the shared lookup")
		_, err = s.SetShareRef(ctx, "u2", "a1", "rshare1")
		Expect(err).To(MatchError(agents.ErrNotFound), "only the owner sets the share reference")
		ref, err := s.SetShareRef(ctx, "u1", "a1", "rshare1")
		Expect(err).NotTo(HaveOccurred())
		Expect(ref).To(Equal("rshare1"))
		ref, err = s.SetShareRef(ctx, "u1", "a1", "rshare2")
		Expect(err).NotTo(HaveOccurred())
		Expect(ref).To(Equal("rshare1"), "the first reference wins")
		// A replace of the definition never drops or changes the reference.
		again := sample("a1", "u1", "Reviewer 2")
		again.ShareRef = "forged"
		Expect(s.Put(ctx, again)).To(Succeed())
		got, err := s.GetShared(ctx, "a1")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Principal).To(Equal("u1"))
		Expect(got.Name).To(Equal("Reviewer 2"))
		Expect(got.ShareRef).To(Equal("rshare1"))
		own, _ := s.Get(ctx, "u1", "a1")
		Expect(own.ShareRef).To(Equal("rshare1"))
		byRef, err := s.ListShared(ctx, []string{"rshare1", "rmissing"})
		Expect(err).NotTo(HaveOccurred())
		Expect(byRef).To(HaveLen(1))
		Expect(byRef[0].ID).To(Equal("a1"))
		none, err := s.ListShared(ctx, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(none).To(BeEmpty())
		Expect(s.Delete(ctx, "u1", "a1")).To(Succeed())
		_, err = s.GetShared(ctx, "a1")
		Expect(err).To(MatchError(agents.ErrNotFound))
		byRef, _ = s.ListShared(ctx, []string{"rshare1"})
		Expect(byRef).To(BeEmpty(), "a deleted agent is not found by its reference")
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
