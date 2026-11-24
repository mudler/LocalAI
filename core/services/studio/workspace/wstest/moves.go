package wstest

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func describeMoves(newStore NewStore) {
	Describe("Moves", func() {
		It("move a file", func() {
			s := newStore()
			n1 := MustPut(s, AdminActor, "/a.txt", "a")
			n2, err := s.Move(Ctx(), AdminActor, "/a.txt", "/b.txt")
			Expect(err).ToNot(HaveOccurred())
			Expect(n2.Path).To(Equal("/b.txt"))
			Expect(n2.ID).To(Equal(n1.ID))
			Expect(n2.Version).To(BeEquivalentTo(2))
			Expect(Tree(s)).To(Equal(map[string]string{"/b.txt": "a"}))
		})
		It("case-only rename is allowed", func() {
			s := newStore()
			MustPut(s, AdminActor, "/a.txt", "a")
			n, err := s.Move(Ctx(), AdminActor, "/a.txt", "/A.txt")
			Expect(err).ToNot(HaveOccurred())
			Expect(n.Path).To(Equal("/A.txt"))
		})
		It("move creates missing folders", func() {
			s := newStore()
			MustPut(s, AdminActor, "/a.txt", "a")
			_, err := s.Move(Ctx(), AdminActor, "/a.txt", "/x/y/a.txt")
			Expect(err).ToNot(HaveOccurred())
			want := map[string]string{"/x": "/", "/x/y": "/", "/x/y/a.txt": "a"}
			Expect(Tree(s)).To(Equal(want))
		})
		It("move onto a taken name is refused", func() {
			s := newStore()
			MustPut(s, AdminActor, "/a.txt", "a")
			MustPut(s, AdminActor, "/B.txt", "b")
			_, err := s.Move(Ctx(), AdminActor, "/a.txt", "/b.txt")
			Expect(err).To(MatchError(workspace.ErrExists))
		})
		It("a folder cannot move into itself", func() {
			s := newStore()
			MustPut(s, AdminActor, "/d/f.txt", "f")
			before := Tree(s)
			_, err := s.Move(Ctx(), AdminActor, "/d", "/d/e/d")
			Expect(err).To(MatchError(workspace.ErrInvalid))
			Expect(Tree(s)).To(Equal(before), "a refused move changed the tree")
			_, err = s.Move(Ctx(), AdminActor, "/", "/x")
			Expect(err).To(MatchError(workspace.ErrInvalid), "move root")
		})
		It("a moved folder keeps its children", func() {
			s := newStore()
			MustPut(s, AdminActor, "/d/c.txt", "c")
			_, err := s.Move(Ctx(), AdminActor, "/d", "/e")
			Expect(err).ToNot(HaveOccurred())
			Expect(MustRead(s, AdminActor, "/e/c.txt")).To(Equal("c"), "child not moved")
		})
		It("delete sets tombstones on the subtree", func() {
			s := newStore()
			MustPut(s, AdminActor, "/d/a.txt", "a")
			MustPut(s, AdminActor, "/d/b.txt", "b")
			MustPut(s, AdminActor, "/keep.txt", "k")
			tx, err := s.Begin(Ctx(), AdminActor, "Clean")
			Expect(err).ToNot(HaveOccurred())
			Expect(tx.Delete(Ctx(), "/d")).To(Succeed())
			cs, err := tx.Commit()
			Expect(err).ToNot(HaveOccurred())
			Expect(cs.Ops).To(HaveLen(3), "want the two files first, then the folder")
			Expect(cs.Ops[2].NewPath).To(Equal("/d"), "want the two files first, then the folder")
			for _, op := range cs.Ops {
				Expect(op.Kind).To(Equal(workspace.OpDelete), "op = %+v", op)
				// Op has no deleted flags. A tombstone keeps the node, so both
				// paths name it; a hard delete leaves no new path.
				Expect(op.NodeID).ToNot(BeZero(), "a delete must keep the node and its path: %+v", op)
				Expect(op.OldPath).ToNot(BeEmpty(), "a delete must keep the node and its path: %+v", op)
				Expect(op.NewPath).To(Equal(op.OldPath), "a delete must keep the node and its path: %+v", op)
			}
			Expect(Tree(s)).To(Equal(map[string]string{"/keep.txt": "k"}))
			_, err = s.Stat(Ctx(), AdminActor, "/d/a.txt")
			Expect(err).To(MatchError(workspace.ErrNotFound), "stat deleted")
			MustPut(s, AdminActor, "/d/a.txt", "again")
			Expect(s.Delete(Ctx(), AdminActor, "/")).To(MatchError(workspace.ErrInvalid), "delete root")
		})
		It("moves and deletes need write access", func() {
			s := newStore()
			MustPut(s, AdminActor, "/w/a.txt", "a")
			MustPut(s, AdminActor, "/r/b.txt", "b")
			MustPut(s, AdminActor, "/hidden/c.txt", "c")
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/w", workspace.ModeWrite)).To(Succeed())
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/r", workspace.ModeRead)).To(Succeed())
			_, err := s.Move(Ctx(), Alice, "/w/a.txt", "/r/a.txt")
			Expect(err).To(MatchError(workspace.ErrReadOnly), "move into a read-only folder")
			Expect(s.Delete(Ctx(), Alice, "/r/b.txt")).To(MatchError(workspace.ErrReadOnly), "delete read-only")
			Expect(s.Delete(Ctx(), Alice, "/hidden/c.txt")).To(MatchError(workspace.ErrNotFound), "delete hidden")
			_, err = s.Move(Ctx(), Alice, "/w/a.txt", "/w/z.txt")
			Expect(err).ToNot(HaveOccurred(), "move inside the write grant")
			_, err = s.Move(Ctx(), Alice, "/w/z.txt", "/hidden/z.txt")
			Expect(err).To(MatchError(workspace.ErrNotFound), "move into a hidden folder")
			_, err = s.Move(Ctx(), Alice, "/w/z.txt", "/hidden/new/z.txt")
			Expect(err).To(MatchError(workspace.ErrNotFound), "move below a missing path in a hidden folder")
			Expect(Tree(s)["/hidden/new"]).To(BeEmpty(), "a refused move created a folder")
		})
		It("a write grant under a read-only parent protects the folder itself", func() {
			s := newStore()
			MustPut(s, AdminActor, "/shared/mine/f.txt", "f")
			MustPut(s, AdminActor, "/w/x.txt", "x")
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeRead)).To(Succeed())
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared/mine", workspace.ModeWrite)).To(Succeed())
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/w", workspace.ModeWrite)).To(Succeed())
			_, err := s.Move(Ctx(), Alice, "/shared/mine", "/w/mine")
			Expect(err).To(MatchError(workspace.ErrReadOnly), "move the folder out")
			Expect(s.Delete(Ctx(), Alice, "/shared/mine")).To(MatchError(workspace.ErrReadOnly), "delete the folder")
			_, err = s.Move(Ctx(), Alice, "/shared/mine/f.txt", "/shared/mine/g.txt")
			Expect(err).ToNot(HaveOccurred(), "rename inside the folder")
			MustPut(s, Alice, "/shared/mine/f2.txt", "2")
			Expect(Tree(s)["/shared/mine/g.txt"]).To(Equal("f"))
		})
	})
}
