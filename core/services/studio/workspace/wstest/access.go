package wstest

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func setupAccess(s workspace.Store) {
	GinkgoHelper()
	MustPut(s, AdminActor, "/shared/a.txt", "a")
	MustPut(s, AdminActor, "/private/p.txt", "p")
	MustPut(s, AdminActor, "/office/o.txt", "o")
}

func describeAccess(newStore NewStore) {
	Describe("Access", func() {
		It("no grant reads as not found", func() {
			s := newStore()
			setupAccess(s)
			for _, p := range []string{"/", "/shared", "/shared/a.txt", "/nope"} {
				_, err := s.Stat(Ctx(), Alice, p)
				Expect(err).To(MatchError(workspace.ErrNotFound), "stat %s", p)
			}
			_, err := s.Put(Ctx(), Alice, "/shared/b.txt", strings.NewReader("b"), workspace.PutOptions{})
			Expect(err).To(MatchError(workspace.ErrNotFound), "put")
			_, err = s.List(Ctx(), Alice, "/")
			Expect(err).To(MatchError(workspace.ErrNotFound), "list")
		})
		It("read grant allows reads only", func() {
			s := newStore()
			setupAccess(s)
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeRead)).To(Succeed())
			Expect(MustRead(s, Alice, "/shared/a.txt")).To(Equal("a"), "read through a read grant")
			_, err := s.Put(Ctx(), Alice, "/shared/b.txt", strings.NewReader("b"), workspace.PutOptions{})
			Expect(err).To(MatchError(workspace.ErrReadOnly), "put")
			_, err = s.Mkdir(Ctx(), Alice, "/shared/sub")
			Expect(err).To(MatchError(workspace.ErrReadOnly), "mkdir")
			_, err = s.Stat(Ctx(), Alice, "/private/p.txt")
			Expect(err).To(MatchError(workspace.ErrNotFound), "stat private")
		})
		It("write grant allows writes below the folder", func() {
			s := newStore()
			setupAccess(s)
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeWrite)).To(Succeed())
			MustPut(s, Alice, "/shared/deep/b.txt", "b")
			_, err := s.Put(Ctx(), Alice, "/elsewhere.txt", strings.NewReader("x"), workspace.PutOptions{})
			Expect(err).To(MatchError(workspace.ErrNotFound), "put outside the grant")
		})
		It("group grant applies to members", func() {
			s := newStore()
			setupAccess(s)
			Expect(s.Grant(Ctx(), AdminActor, "group:office", "/office", workspace.ModeWrite)).To(Succeed())
			MustPut(s, Bob, "/office/new.txt", "n")
			_, err := s.Stat(Ctx(), Alice, "/office/new.txt")
			Expect(err).To(MatchError(workspace.ErrNotFound), "non-member")
		})
		It("revoke removes access", func() {
			s := newStore()
			setupAccess(s)
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeWrite)).To(Succeed())
			Expect(s.Revoke(Ctx(), AdminActor, Alice.Principal, "/shared")).To(Succeed())
			_, err := s.Stat(Ctx(), Alice, "/shared")
			Expect(err).To(MatchError(workspace.ErrNotFound), "after revoke")
			Expect(s.Revoke(Ctx(), AdminActor, Alice.Principal, "/shared")).To(Succeed(), "second revoke")
		})
		It("only the admin manages grants", func() {
			s := newStore()
			setupAccess(s)
			Expect(s.Grant(Ctx(), Alice, Alice.Principal, "/shared", workspace.ModeWrite)).To(MatchError(workspace.ErrNotAdmin), "grant by a user")
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared/a.txt", workspace.ModeRead)).To(MatchError(workspace.ErrInvalid), "grant on a file")
			Expect(s.Grant(Ctx(), AdminActor, "", "/shared", workspace.ModeRead)).To(MatchError(workspace.ErrInvalid), "grant to nobody")
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeNone)).To(MatchError(workspace.ErrInvalid), "grant of no mode")
		})
		It("a view never grows a grant", func() {
			s := newStore()
			setupAccess(s)
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeRead)).To(Succeed())
			v, err := s.View(Ctx(), Alice, []workspace.MountRequest{{Path: "/shared", At: "shared", Mode: workspace.ModeWrite}})
			Expect(err).ToNot(HaveOccurred())
			Expect(v.Mounts).To(HaveLen(1), "view = %+v", v)
			Expect(v.Mounts[0].Mode).To(Equal(workspace.ModeRead))
			Expect(v.Mounts[0].Path).To(Equal("/shared"))
			Expect(v.Mounts[0].At).To(Equal("shared"))
			_, err = s.View(Ctx(), Alice, []workspace.MountRequest{{Path: "/private", At: "p", Mode: workspace.ModeRead}})
			Expect(err).To(MatchError(workspace.ErrNotFound), "view of a hidden folder")
		})
		It("view requests are checked", func() {
			s := newStore()
			setupAccess(s)
			cases := []struct {
				reqs []workspace.MountRequest
				want error
			}{
				{[]workspace.MountRequest{{Path: "/shared/a.txt", At: "a", Mode: workspace.ModeRead}}, workspace.ErrInvalid},
				{[]workspace.MountRequest{{Path: "/shared", At: "x", Mode: workspace.ModeRead}, {Path: "/office", At: "X", Mode: workspace.ModeRead}}, workspace.ErrInvalid},
				{[]workspace.MountRequest{{Path: "/shared", At: "a/b", Mode: workspace.ModeRead}}, workspace.ErrBadName},
				{[]workspace.MountRequest{{Path: "/shared", At: "s", Mode: workspace.ModeNone}}, workspace.ErrInvalid},
			}
			for i, c := range cases {
				_, err := s.View(Ctx(), AdminActor, c.reqs)
				Expect(err).To(MatchError(c.want), "case %d", i)
			}
		})
	})
}

func describeReadablePrefixes(newStore NewStore) {
	Describe("ReadablePrefixes", func() {
		prefixes := func(s workspace.Store, a workspace.Actor) []string {
			GinkgoHelper()
			got, err := s.ReadablePrefixes(Ctx(), a)
			Expect(err).ToNot(HaveOccurred(), "ReadablePrefixes")
			Expect(got).ToNot(BeNil(), "ReadablePrefixes returned nil, want a list")
			return got
		}
		want := func(what string, got []string, w ...string) {
			GinkgoHelper()
			if w == nil {
				w = []string{}
			}
			Expect(got).To(Equal(w), what)
		}
		It("the admin gets the root and an actor with no grant gets nothing", func() {
			s := newStore()
			setupAccess(s)
			want("admin", prefixes(s, AdminActor), "/")
			want("no grant", prefixes(s, Alice))
			want("no principal", prefixes(s, workspace.Actor{}))
		})
		It("a grant on a folder and one below it give the outer folder", func() {
			s := newStore()
			MustPut(s, AdminActor, "/a/b/f.txt", "f")
			MustPut(s, AdminActor, "/ab/g.txt", "g")
			for _, p := range []string{"/a/b", "/a", "/ab"} {
				Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, p, workspace.ModeRead)).To(Succeed())
			}
			want("nested", prefixes(s, Alice), "/a", "/ab")
		})
		It("a write grant counts and a grant on the root gives the root", func() {
			s := newStore()
			MustPut(s, AdminActor, "/x/f.txt", "f")
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/x", workspace.ModeWrite)).To(Succeed())
			want("write", prefixes(s, Alice), "/x")
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/", workspace.ModeRead)).To(Succeed())
			want("root", prefixes(s, Alice), "/")
		})
		It("group grants count", func() {
			s := newStore()
			setupAccess(s)
			Expect(s.Grant(Ctx(), AdminActor, "group:office", "/office", workspace.ModeRead)).To(Succeed())
			Expect(s.Grant(Ctx(), AdminActor, Bob.Principal, "/shared", workspace.ModeRead)).To(Succeed())
			want("member", prefixes(s, Bob), "/office", "/shared")
			want("non-member", prefixes(s, Alice))
		})
		It("a revoked grant drops out", func() {
			s := newStore()
			setupAccess(s)
			for _, p := range []string{"/shared", "/office"} {
				Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, p, workspace.ModeRead)).To(Succeed())
			}
			Expect(s.Revoke(Ctx(), AdminActor, Alice.Principal, "/shared")).To(Succeed())
			want("after revoke", prefixes(s, Alice), "/office")
		})
		It("a grant on a deleted folder drops out and comes back with an undo", func() {
			s := newStore()
			setupAccess(s)
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeRead)).To(Succeed())
			Expect(s.Delete(Ctx(), AdminActor, "/shared")).To(Succeed())
			want("deleted", prefixes(s, Alice))
			sets, err := s.History(Ctx(), AdminActor, workspace.HistoryQuery{Path: "/", Limit: 1})
			Expect(err).ToNot(HaveOccurred())
			Expect(sets).ToNot(BeEmpty(), "history")
			_, _, err = s.Undo(Ctx(), AdminActor, sets[0].Rev, workspace.UndoOptions{})
			Expect(err).ToNot(HaveOccurred())
			want("restored", prefixes(s, Alice), "/shared")
		})
		It("a sibling whose name sorts inside the path is not dropped", func() {
			s := newStore()
			MustPut(s, AdminActor, "/a/b/f.txt", "f")
			MustPut(s, AdminActor, "/a-b/g.txt", "g")
			for _, p := range []string{"/a", "/a-b", "/a/b"} {
				Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, p, workspace.ModeRead)).To(Succeed())
			}
			want("dash sibling", prefixes(s, Alice), "/a", "/a-b")
		})
		It("a grant on a subfolder of a deleted folder gives no prefix", func() {
			s := newStore()
			MustPut(s, AdminActor, "/dir/sub/f.txt", "f")
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/dir/sub", workspace.ModeRead)).To(Succeed())
			want("before delete", prefixes(s, Alice), "/dir/sub")
			Expect(s.Delete(Ctx(), AdminActor, "/dir")).To(Succeed())
			want("after delete", prefixes(s, Alice))
		})
		It("a moved folder keeps its grant at the new path", func() {
			s := newStore()
			setupAccess(s)
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/shared", workspace.ModeRead)).To(Succeed())
			_, err := s.Move(Ctx(), AdminActor, "/shared", "/moved")
			Expect(err).ToNot(HaveOccurred())
			want("moved", prefixes(s, Alice), "/moved")
		})
	})
}
