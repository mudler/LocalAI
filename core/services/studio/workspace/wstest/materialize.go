package wstest

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func latestRev(s workspace.Store) int64 {
	GinkgoHelper()
	h, err := s.History(Ctx(), AdminActor, workspace.HistoryQuery{Limit: 1})
	Expect(err).ToNot(HaveOccurred())
	if len(h) == 0 {
		return 0
	}
	return h[0].Rev
}

func describeMaterialize(newStore NewStore) {
	Describe("Materialize", func() {
		It("builds a read-only tree of the view", func() {
			s := newStore()
			MustPut(s, AdminActor, "/w/a.txt", "a")
			MustPut(s, AdminActor, "/w/sub/b.txt", "b")
			MustPut(s, AdminActor, "/w/gone.txt", "g")
			Expect(s.Delete(Ctx(), AdminActor, "/w/gone.txt")).To(Succeed())
			MustPut(s, AdminActor, "/docs/d.txt", "d")
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/w", workspace.ModeRead)).To(Succeed())
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/docs", workspace.ModeWrite)).To(Succeed())
			v, err := s.View(Ctx(), Alice, []workspace.MountRequest{
				{Path: "/w", At: "work", Mode: workspace.ModeRead},
				{Path: "/docs", At: "docs", Mode: workspace.ModeWrite},
			})
			Expect(err).ToNot(HaveOccurred())
			dir := filepath.Join(GinkgoT().TempDir(), "view")
			base, err := s.Materialize(Ctx(), v, dir)
			Expect(err).ToNot(HaveOccurred())
			Expect(base).To(Equal(latestRev(s)))
			want := map[string]string{"work/a.txt": "a", "work/sub/b.txt": "b", "docs/d.txt": "d"}
			for rel, content := range want {
				p := filepath.Join(dir, filepath.FromSlash(rel))
				got, err := os.ReadFile(p) // #nosec G304 -- a test reads a file it wrote in its own temp directory
				Expect(err).ToNot(HaveOccurred(), rel)
				Expect(string(got)).To(Equal(content), rel)
				fi, err := os.Stat(p)
				Expect(err).ToNot(HaveOccurred(), rel)
				Expect(fi.Mode().Perm()&0o222).To(BeZero(), "%s is writable: %v", rel, fi.Mode().Perm())
			}
			_, err = os.Stat(filepath.Join(dir, "work", "gone.txt"))
			Expect(os.IsNotExist(err)).To(BeTrue(), "deleted file is in the view: %v", err)
		})
		It("refuses a folder that is not empty", func() {
			s := newStore()
			MustPut(s, AdminActor, "/w/a.txt", "a")
			v, err := s.View(Ctx(), AdminActor, []workspace.MountRequest{{Path: "/w", At: "w", Mode: workspace.ModeRead}})
			Expect(err).ToNot(HaveOccurred())
			dir := GinkgoT().TempDir()
			Expect(os.WriteFile(filepath.Join(dir, "x"), []byte("x"), 0o600)).To(Succeed())
			_, err = s.Materialize(Ctx(), v, dir)
			Expect(err).To(MatchError(workspace.ErrExists))
		})
		It("a deleted mount is not found", func() {
			s := newStore()
			MustPut(s, AdminActor, "/w/a.txt", "a")
			v, err := s.View(Ctx(), AdminActor, []workspace.MountRequest{{Path: "/w", At: "w", Mode: workspace.ModeRead}})
			Expect(err).ToNot(HaveOccurred())
			Expect(s.Delete(Ctx(), AdminActor, "/w")).To(Succeed())
			_, err = s.Materialize(Ctx(), v, filepath.Join(GinkgoT().TempDir(), "v"))
			Expect(err).To(MatchError(workspace.ErrNotFound))
		})
		It("refuses a mount point that escapes the folder", func() {
			for _, at := range []string{"../escape", "a/../../x", "/abs", "", ".", "a//b", "a/./b"} {
				s := newStore()
				MustPut(s, AdminActor, "/w/a.txt", "a")
				v, err := s.View(Ctx(), AdminActor, []workspace.MountRequest{{Path: "/w", At: "w", Mode: workspace.ModeRead}})
				Expect(err).ToNot(HaveOccurred())
				v.Mounts[0].At = at
				parent := GinkgoT().TempDir()
				dir := filepath.Join(parent, "view")
				_, err = s.Materialize(Ctx(), v, dir)
				Expect(err).To(MatchError(workspace.ErrInvalid), "At %q", at)
				ents, _ := os.ReadDir(parent)
				for _, e := range ents {
					Expect(e.Name()).To(Equal("view"), "At %q: %s appeared next to the target", at, e.Name())
				}
				_, err = os.Stat(filepath.Join(parent, "..", "escape"))
				Expect(err).To(HaveOccurred(), "At %q: escape exists", at)
			}
		})
		It("refuses duplicate and nested mount points", func() {
			s := newStore()
			MustPut(s, AdminActor, "/w/a.txt", "a")
			MustPut(s, AdminActor, "/x/b.txt", "b")
			for _, ats := range [][2]string{{"m", "m"}, {"m", "m/n"}, {"m/n", "m"}} {
				v, err := s.View(Ctx(), AdminActor, []workspace.MountRequest{
					{Path: "/w", At: "p", Mode: workspace.ModeRead},
					{Path: "/x", At: "q", Mode: workspace.ModeRead},
				})
				Expect(err).ToNot(HaveOccurred())
				v.Mounts[0].At, v.Mounts[1].At = ats[0], ats[1]
				_, err = s.Materialize(Ctx(), v, filepath.Join(GinkgoT().TempDir(), "v"))
				Expect(err).To(MatchError(workspace.ErrInvalid), "%v", ats)
			}
		})
		It("a nested mount point works", func() {
			s := newStore()
			MustPut(s, AdminActor, "/w/a.txt", "a")
			v, err := s.View(Ctx(), AdminActor, []workspace.MountRequest{{Path: "/w", At: "p", Mode: workspace.ModeRead}})
			Expect(err).ToNot(HaveOccurred())
			v.Mounts[0].At = "work/in" // View accepts one name; Materialize accepts a relative path
			dir := filepath.Join(GinkgoT().TempDir(), "v")
			_, err = s.Materialize(Ctx(), v, dir)
			Expect(err).ToNot(HaveOccurred())
			got, err := os.ReadFile(filepath.Join(dir, "work", "in", "a.txt")) // #nosec G304 -- a test reads a file it wrote in its own temp directory
			Expect(err).ToNot(HaveOccurred())
			Expect(string(got)).To(Equal("a"))
		})
		It("a renamed mounted folder still materializes", func() {
			s := newStore()
			MustPut(s, AdminActor, "/w/a.txt", "a")
			v, err := s.View(Ctx(), AdminActor, []workspace.MountRequest{{Path: "/w", At: "w", Mode: workspace.ModeRead}})
			Expect(err).ToNot(HaveOccurred())
			_, err = s.Move(Ctx(), AdminActor, "/w", "/renamed")
			Expect(err).ToNot(HaveOccurred())
			dir := filepath.Join(GinkgoT().TempDir(), "v")
			_, err = s.Materialize(Ctx(), v, dir)
			Expect(err).ToNot(HaveOccurred())
			got, err := os.ReadFile(filepath.Join(dir, "w", "a.txt")) // #nosec G304 -- a test reads a file it wrote in its own temp directory
			Expect(err).ToNot(HaveOccurred())
			Expect(string(got)).To(Equal("a"))
		})
	})
}
