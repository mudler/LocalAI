package wstest

import (
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func describeSandboxAccess(newStore NewStore) {
	Describe("SandboxAccess", func() {
		setup := func() workspace.Store {
			GinkgoHelper()
			s := newStore()
			MustPut(s, AdminActor, "/w/a.txt", "a")
			MustPut(s, AdminActor, "/shared/proj/p.txt", "p")
			MustPut(s, AdminActor, "/private/p.txt", "p")
			return s
		}
		adminView := func(s workspace.Store, p string, m workspace.Mode) workspace.View {
			GinkgoHelper()
			v, err := s.View(Ctx(), AdminActor, []workspace.MountRequest{{Path: p, At: "w", Mode: m}})
			Expect(err).ToNot(HaveOccurred())
			return v
		}
		src := workspace.FixtureSource{workspace.FixtureDir("w"), workspace.FixtureFile("w/new.txt", "n")}
		It("CommitUpper without a grant is not found", func() {
			s := setup()
			v := adminView(s, "/w", workspace.ModeWrite)
			base := latestRev(s)
			_, _, err := s.CommitUpper(Ctx(), v, Alice, base, src, workspace.UpperOptions{Title: "x"})
			Expect(err).To(MatchError(workspace.ErrNotFound))
			_, err = s.Stat(Ctx(), AdminActor, "/w/new.txt")
			Expect(err).To(MatchError(workspace.ErrNotFound), "a file was written")
		})
		It("CommitUpper with a read grant is read-only", func() {
			s := setup()
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/w", workspace.ModeRead)).To(Succeed())
			v := adminView(s, "/w", workspace.ModeWrite)
			_, _, err := s.CommitUpper(Ctx(), v, Alice, latestRev(s), src, workspace.UpperOptions{Title: "x"})
			Expect(err).To(MatchError(workspace.ErrReadOnly))
		})
		It("CommitUpper refuses a view whose mode was raised by hand", func() {
			s := setup()
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/w", workspace.ModeRead)).To(Succeed())
			v, err := s.View(Ctx(), Alice, []workspace.MountRequest{{Path: "/w", At: "w", Mode: workspace.ModeWrite}})
			Expect(err).ToNot(HaveOccurred())
			Expect(v.Mounts[0].Mode).To(Equal(workspace.ModeRead), "mode, want read")
			v.Mounts[0].Mode = workspace.ModeWrite
			_, _, err = s.CommitUpper(Ctx(), v, Alice, latestRev(s), src, workspace.UpperOptions{Title: "x"})
			Expect(err).To(MatchError(workspace.ErrReadOnly))
			_, err = s.Stat(Ctx(), AdminActor, "/w/new.txt")
			Expect(err).To(MatchError(workspace.ErrNotFound), "a file was written")
		})
		It("Materialize after the grant is revoked is not found", func() {
			s := setup()
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/w", workspace.ModeRead)).To(Succeed())
			v, err := s.View(Ctx(), Alice, []workspace.MountRequest{{Path: "/w", At: "w", Mode: workspace.ModeRead}})
			Expect(err).ToNot(HaveOccurred())
			Expect(s.Revoke(Ctx(), AdminActor, Alice.Principal, "/w")).To(Succeed())
			dir := filepath.Join(GinkgoT().TempDir(), "view")
			_, err = s.Materialize(Ctx(), v, dir)
			Expect(err).To(MatchError(workspace.ErrNotFound))
		})
		It("Materialize of a mount moved out of the readable tree is not found", func() {
			s := setup()
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeRead)).To(Succeed())
			v, err := s.View(Ctx(), Alice, []workspace.MountRequest{{Path: "/shared/proj", At: "w", Mode: workspace.ModeRead}})
			Expect(err).ToNot(HaveOccurred())
			_, err = s.Move(Ctx(), AdminActor, "/shared/proj", "/private/proj")
			Expect(err).ToNot(HaveOccurred())
			dir := filepath.Join(GinkgoT().TempDir(), "view")
			_, err = s.Materialize(Ctx(), v, dir)
			Expect(err).To(MatchError(workspace.ErrNotFound))
		})
		It("View of a file in a hidden folder is not found", func() {
			s := setup()
			_, err := s.View(Ctx(), Alice, []workspace.MountRequest{{Path: "/private/p.txt", At: "w", Mode: workspace.ModeRead}})
			Expect(err).To(MatchError(workspace.ErrNotFound))
		})
	})
}
