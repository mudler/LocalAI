package wstest

import (
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// checkpoint commits src as a checkpoint of one sandbox: base stays fixed and
// own holds the revisions of the earlier checkpoints.
func checkpoint(s workspace.Store, v workspace.View, base int64, own *[]int64, src workspace.FixtureSource) []workspace.Conflict {
	GinkgoHelper()
	cs, conflicts, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, src, workspace.UpperOptions{Title: "turn", Own: *own})
	Expect(err).ToNot(HaveOccurred(), "commit")
	if cs.Rev != 0 {
		*own = append(*own, cs.Rev)
	}
	return conflicts
}

func describeUpperOwn(newStore NewStore) {
	F, D, W := workspace.FixtureFile, workspace.FixtureDir, workspace.FixtureWhiteout
	Describe("UpperOwn", func() {
		It("a second edit after a checkpoint is not a conflict", func() {
			s := newStore()
			v, base := upperSetup(s)
			var own []int64
			Expect(checkpoint(s, v, base, &own, workspace.FixtureSource{D("w"), F("w/edit.txt", "one"), F("w/new.txt", "n1")})).To(BeEmpty(), "first")
			Expect(checkpoint(s, v, base, &own, workspace.FixtureSource{D("w"), F("w/edit.txt", "two"), F("w/new.txt", "n2")})).To(BeEmpty(), "second")
			tree := Tree(s)
			Expect(tree["/w/edit.txt"]).To(Equal("two"), "tree = %v", tree)
			Expect(tree["/w/new.txt"]).To(Equal("n2"), "tree = %v", tree)
		})
		It("a long list of earlier checkpoints is not a conflict", func() {
			// The own revisions travel as a bound IN list. PostgreSQL counts
			// each placeholder, so a list this long checks the binding.
			s := newStore()
			v, base := upperSetup(s)
			var own []int64
			for i := 0; i < 60; i++ {
				Expect(checkpoint(s, v, base, &own, workspace.FixtureSource{D("w"), F("w/edit.txt", strconv.Itoa(i))})).To(BeEmpty(), "checkpoint %d", i)
			}
			Expect(own).To(HaveLen(60))
			Expect(Tree(s)["/w/edit.txt"]).To(Equal("59"))
		})
		It("a delete and a rename after a checkpoint are not conflicts", func() {
			s := newStore()
			v, base := upperSetup(s)
			var own []int64
			checkpoint(s, v, base, &own, workspace.FixtureSource{D("w"), F("w/new.txt", "n"), F("w/edit.txt", "mine"), F("w/m.txt", "move me too")})
			c := checkpoint(s, v, base, &own, workspace.FixtureSource{D("w"), W("w/new.txt"), W("w/edit.txt"), W("w/m.txt"), F("w/m2.txt", "move me too")})
			Expect(c).To(BeEmpty(), "conflicts")
			tree := Tree(s)
			for _, p := range []string{"/w/new.txt", "/w/edit.txt", "/w/m.txt"} {
				Expect(tree).ToNot(HaveKey(p), "%s survived", p)
			}
			Expect(tree["/w/m2.txt"]).To(Equal("move me too"), "tree = %v", tree)
		})
		It("an edit of a file another writer changed between checkpoints is a conflict copy", func() {
			s := newStore()
			v, base := upperSetup(s)
			var own []int64
			checkpoint(s, v, base, &own, workspace.FixtureSource{D("w"), F("w/edit.txt", "one")})
			MustPut(s, AdminActor, "/w/edit.txt", "theirs")
			c := checkpoint(s, v, base, &own, workspace.FixtureSource{D("w"), F("w/edit.txt", "two")})
			Expect(c).To(HaveLen(1))
			Expect(c[0].Reason).To(Equal(workspace.ReasonChanged))
			Expect(c[0].Kept).To(Equal("/w/edit (from chat 7).txt"))
			Expect(MustRead(s, AdminActor, "/w/edit.txt")).To(Equal("theirs"), "both versions must be kept")
			Expect(MustRead(s, AdminActor, c[0].Kept)).To(Equal("two"), "both versions must be kept")
		})
		It("a change by another writer before an own change is still a conflict", func() {
			s := newStore()
			v, base := upperSetup(s)
			var own []int64
			MustPut(s, AdminActor, "/w/d/x.txt", "theirs")
			checkpoint(s, v, base, &own, workspace.FixtureSource{D("w"), F("w/d/new.txt", "n")})
			c := checkpoint(s, v, base, &own, workspace.FixtureSource{D("w"), W("w/d")})
			Expect(reasons(c)).To(Equal([]string{workspace.ReasonChanged}), "conflicts = %+v", c)
			Expect(Tree(s)["/w/d/x.txt"]).To(Equal("theirs"), "the folder with a change by another writer was deleted")
		})
		It("own revisions of another actor or chat are ignored", func() {
			s := newStore()
			v, base := upperSetup(s)
			Expect(s.Grant(Ctx(), AdminActor, Bob.Principal, "/w", workspace.ModeWrite)).To(Succeed())
			other := []workspace.Actor{AdminActor, Bob, {Principal: Admin, ChatID: "8"}}
			for _, a := range other {
				n := MustPut(s, a, "/w/edit.txt", "theirs "+a.Principal+a.ChatID)
				_, c, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), F("w/edit.txt", "mine")},
					workspace.UpperOptions{Title: "turn", Own: []int64{n.Rev}})
				Expect(err).ToNot(HaveOccurred())
				Expect(c).To(HaveLen(1), "actor %+v: conflicts = %+v", a, c)
				Expect(c[0].Reason).To(Equal(workspace.ReasonChanged), "actor %+v: conflicts = %+v", a, c)
			}
		})
		It("a revision that does not exist is ignored", func() {
			s := newStore()
			v, base := upperSetup(s)
			_, c, err := s.CommitUpper(Ctx(), v, AliceAdmin(), base, workspace.FixtureSource{D("w"), F("w/edit.txt", "mine")},
				workspace.UpperOptions{Title: "turn", Own: []int64{base + 100, -1}})
			Expect(err).ToNot(HaveOccurred())
			Expect(c).To(BeEmpty())
		})
	})
}
