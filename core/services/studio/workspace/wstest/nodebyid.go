package wstest

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func describeNodeByID(newStore NewStore) {
	Describe("NodeByID", func() {
		It("returns a live node at its current path", func() {
			w := newFenceWorld(newStore)
			n, err := w.s.NodeByID(Ctx(), Alice, w.proj.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(n.ID).To(Equal(w.proj.ID))
			Expect(n.Path).To(Equal("/users/alice/proj"))
			Expect(n.Name).To(Equal("proj"))
			Expect(n.Kind).To(Equal(workspace.KindDir))

			f := adminStat(w.s, "/users/alice/proj/a.txt")
			n, err = w.s.NodeByID(Ctx(), Alice, f.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(n.Kind).To(Equal(workspace.KindFile))
			Expect(n.Path).To(Equal("/users/alice/proj/a.txt"))
			Expect(n.Size).To(Equal(int64(1)))
		})

		It("follows a rename, a move and a move of a parent", func() {
			w := newFenceWorld(newStore)
			_, err := w.s.Move(Ctx(), Alice, "/users/alice/proj", "/users/alice/renamed")
			Expect(err).ToNot(HaveOccurred())
			n, err := w.s.NodeByID(Ctx(), Alice, w.sub.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(n.Path).To(Equal("/users/alice/renamed/sub"), "a rename of the parent")

			_, err = w.s.Move(Ctx(), Alice, "/users/alice/renamed/sub", "/users/alice/other/deep/sub2")
			Expect(err).ToNot(HaveOccurred())
			n, err = w.s.NodeByID(Ctx(), Alice, w.sub.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(n.Path).To(Equal("/users/alice/other/deep/sub2"))
			Expect(n.Name).To(Equal("sub2"))

			_, err = w.s.Move(Ctx(), Alice, "/users/alice/other", "/users/alice/moved")
			Expect(err).ToNot(HaveOccurred())
			n, err = w.s.NodeByID(Ctx(), Alice, w.sub.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(n.Path).To(Equal("/users/alice/moved/deep/sub2"), "a move of a folder above")
		})

		It("does not find a deleted node, nor one under a deleted folder, until Undo", func() {
			w := newFenceWorld(newStore)
			Expect(w.s.Delete(Ctx(), Alice, "/users/alice/proj")).To(Succeed())
			del := latestRev(w.s)
			_, err := w.s.NodeByID(Ctx(), Alice, w.proj.ID)
			Expect(err).To(MatchError(workspace.ErrNotFound), "the deleted folder")
			_, err = w.s.NodeByID(Ctx(), Alice, w.sub.ID)
			Expect(err).To(MatchError(workspace.ErrNotFound), "a folder inside it")
			_, err = w.s.NodeByID(Ctx(), AdminActor, w.proj.ID)
			Expect(err).To(MatchError(workspace.ErrNotFound), "the admin does not see the Trash either")

			_, _, err = w.s.Undo(Ctx(), Alice, del, workspace.UndoOptions{})
			Expect(err).ToNot(HaveOccurred())
			n, err := w.s.NodeByID(Ctx(), Alice, w.sub.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(n.Path).To(Equal("/users/alice/proj/sub"))
		})

		It("does not find a node the actor cannot read, nor an unknown ID", func() {
			w := newFenceWorld(newStore)
			hidden := adminStat(w.s, "/shared/hidden")
			bobs := adminStat(w.s, "/users/bob/b.txt")
			for _, id := range []int64{hidden.ID, bobs.ID, 0, -1, 1 << 40} {
				_, err := w.s.NodeByID(Ctx(), Alice, id)
				Expect(err).To(MatchError(workspace.ErrNotFound), "node %d", id)
			}
			n, err := w.s.NodeByID(Ctx(), Alice, w.shared.ID)
			Expect(err).ToNot(HaveOccurred(), "a shared folder Alice may read")
			Expect(n.Path).To(Equal("/shared/s"))
			n, err = w.s.NodeByID(Ctx(), AdminActor, bobs.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(n.Path).To(Equal("/users/bob/b.txt"))
			_, err = w.s.NodeByID(Ctx(), workspace.Actor{}, w.proj.ID)
			Expect(err).To(MatchError(workspace.ErrNotFound), "an actor with no principal")
		})

		It("keeps a fenced actor inside its fence", func() {
			w := newFenceWorld(newStore)
			a := w.fenced(workspace.ModeRead)
			n, err := w.s.NodeByID(Ctx(), a, w.sub.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(n.Path).To(Equal("/users/alice/proj/sub"))
			home := adminStat(w.s, "/users/alice")
			for _, id := range []int64{w.other.ID, home.ID, w.shared.ID} {
				_, err := w.s.NodeByID(Ctx(), a, id)
				Expect(err).To(MatchError(workspace.ErrNotFound), "node %d outside the fence", id)
			}
			_, err = w.s.NodeByID(Ctx(), AliceChat.Fenced(workspace.FenceRoot{Node: 0, Mode: workspace.ModeRead}), w.proj.ID)
			Expect(err).To(MatchError(workspace.ErrInvalid), "an invalid fence")
		})
	})
}
