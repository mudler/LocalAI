package wstest

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// The homes of the grant move rules: a home is /users/<principal>, and its
// owner holds write on it, as the host's own homes do.
var (
	marta = workspace.Actor{Principal: "u-marta"}
	sara  = workspace.Actor{Principal: "u-sara"}
)

func setupHomes(s workspace.Store) {
	GinkgoHelper()
	for _, a := range []workspace.Actor{marta, sara} {
		home := "/users/" + a.Principal
		_, err := s.Mkdir(Ctx(), AdminActor, home)
		Expect(err).ToNot(HaveOccurred())
		Expect(s.Grant(Ctx(), AdminActor, a.Principal, home, workspace.ModeWrite)).To(Succeed())
	}
}

func describeGrantMoves(newStore NewStore) {
	Describe("Moves of folders that hold a grant", func() {
		It("refuse a grant that a grantee moves into the home they may write", func() {
			s := newStore()
			setupHomes(s)
			_, err := s.Mkdir(Ctx(), marta, "/users/u-marta/Projects")
			Expect(err).ToNot(HaveOccurred())
			Expect(s.Grant(Ctx(), AdminActor, sara.Principal, "/users/u-marta/Projects", workspace.ModeWrite)).To(Succeed())
			MustPut(s, sara, "/users/u-sara/Y/a.txt", "a")
			Expect(s.Grant(Ctx(), AdminActor, "u-root", "/users/u-sara/Y", workspace.ModeWrite)).To(Succeed())

			_, err = s.Move(Ctx(), sara, "/users/u-sara/Y", "/users/u-marta/Projects/Y")
			Expect(err).To(MatchError(workspace.ErrSharedMove))
			Expect(MustRead(s, sara, "/users/u-sara/Y/a.txt")).To(Equal("a"), "nothing moved")
			_, err = s.Stat(Ctx(), AdminActor, "/users/u-marta/Projects/Y")
			Expect(err).To(MatchError(workspace.ErrNotFound))

			// The same in a change set.
			tx, err := s.Begin(Ctx(), sara, "move")
			Expect(err).ToNot(HaveOccurred())
			_, err = tx.Move(Ctx(), "/users/u-sara/Y", "/users/u-marta/Projects/Y")
			Expect(err).To(MatchError(workspace.ErrSharedMove))
			Expect(tx.Rollback()).To(Succeed())
		})
		It("refuse a grant on a folder below the one that moves", func() {
			s := newStore()
			setupHomes(s)
			_, err := s.Mkdir(Ctx(), sara, "/users/u-sara/Y/inner")
			Expect(err).ToNot(HaveOccurred())
			Expect(s.Grant(Ctx(), AdminActor, "u-root", "/users/u-sara/Y/inner", workspace.ModeRead)).To(Succeed())
			Expect(s.Grant(Ctx(), AdminActor, sara.Principal, "/users/u-marta", workspace.ModeWrite)).To(Succeed())
			_, err = s.Move(Ctx(), sara, "/users/u-sara/Y", "/users/u-marta/Y")
			Expect(err).To(MatchError(workspace.ErrSharedMove))
			_, err = s.Mkdir(Ctx(), AdminActor, "/elsewhere")
			Expect(err).ToNot(HaveOccurred())
			Expect(s.Grant(Ctx(), AdminActor, sara.Principal, "/elsewhere", workspace.ModeWrite)).To(Succeed())
			_, err = s.Move(Ctx(), sara, "/users/u-sara/Y", "/elsewhere/Y")
			Expect(err).To(MatchError(workspace.ErrSharedMove), "out of a home")
		})
		It("move a folder without grants across homes", func() {
			s := newStore()
			setupHomes(s)
			_, err := s.Mkdir(Ctx(), marta, "/users/u-marta/Projects")
			Expect(err).ToNot(HaveOccurred())
			Expect(s.Grant(Ctx(), AdminActor, sara.Principal, "/users/u-marta/Projects", workspace.ModeWrite)).To(Succeed())
			MustPut(s, sara, "/users/u-sara/Y/a.txt", "a")
			n, err := s.Move(Ctx(), sara, "/users/u-sara/Y", "/users/u-marta/Projects/Y")
			Expect(err).ToNot(HaveOccurred())
			Expect(n.Path).To(Equal("/users/u-marta/Projects/Y"))
		})
		It("refuse a shared folder that moves into a folder LocalAI manages, and keep other moves", func() {
			s := newStore()
			setupHomes(s)
			_, err := s.Mkdir(Ctx(), marta, "/users/u-marta/Projects")
			Expect(err).ToNot(HaveOccurred())
			Expect(s.Grant(Ctx(), AdminActor, sara.Principal, "/users/u-marta/Projects", workspace.ModeWrite)).To(Succeed())
			for _, to := range []string{
				"/users/u-marta/LocalAI/Projects", "/users/u-marta/Chats/Projects", "/users/u-marta/Memory/Projects",
				"/users/u-marta/pages/projects", "/users/u-marta/.skills", "/users/u-marta/Work/.skills/Projects",
				"/users/u-marta/localai/Projects", "/users/u-marta/Memory",
			} {
				_, err := s.Move(Ctx(), marta, "/users/u-marta/Projects", to)
				Expect(err).To(MatchError(workspace.ErrSharedMove), to)
			}
			n, err := s.Move(Ctx(), marta, "/users/u-marta/Projects", "/users/u-marta/Archive/Projects")
			Expect(err).ToNot(HaveOccurred(), "a move inside the home keeps the share")
			Expect(n.Path).To(Equal("/users/u-marta/Archive/Projects"))
			MustPut(s, sara, "/users/u-marta/Archive/Projects/b.txt", "b")
		})
		It("refuse an undo that takes a grant into another home", func() {
			s := newStore()
			setupHomes(s)
			_, err := s.Mkdir(Ctx(), marta, "/users/u-marta/Projects")
			Expect(err).ToNot(HaveOccurred())
			Expect(s.Grant(Ctx(), AdminActor, sara.Principal, "/users/u-marta/Projects", workspace.ModeWrite)).To(Succeed())
			_, err = s.Mkdir(Ctx(), sara, "/users/u-sara/Y")
			Expect(err).ToNot(HaveOccurred())
			moved, err := s.Move(Ctx(), sara, "/users/u-sara/Y", "/users/u-marta/Projects/Y")
			Expect(err).ToNot(HaveOccurred())
			Expect(s.Grant(Ctx(), AdminActor, "u-root", "/users/u-marta/Projects/Y", workspace.ModeWrite)).To(Succeed())
			_, _, err = s.Undo(Ctx(), sara, moved.Rev, workspace.UndoOptions{})
			Expect(err).To(MatchError(workspace.ErrSharedMove))
			_, err = s.Stat(Ctx(), AdminActor, "/users/u-marta/Projects/Y")
			Expect(err).ToNot(HaveOccurred(), "the folder stays where the grant was given")
		})
		It("let the administrator move a folder that holds a grant", func() {
			s := newStore()
			setupHomes(s)
			_, err := s.Mkdir(Ctx(), marta, "/users/u-marta/Projects")
			Expect(err).ToNot(HaveOccurred())
			Expect(s.Grant(Ctx(), AdminActor, sara.Principal, "/users/u-marta/Projects", workspace.ModeRead)).To(Succeed())
			_, err = s.Move(Ctx(), AdminActor, "/users/u-marta/Projects", "/users/u-marta/LocalAI/Projects")
			Expect(err).ToNot(HaveOccurred())
		})
	})
}
