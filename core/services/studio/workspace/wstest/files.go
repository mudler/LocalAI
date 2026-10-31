package wstest

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func describeFiles(newStore NewStore) {
	Describe("Files", func() {
		It("root exists and is empty", func() {
			s := newStore()
			n, err := s.Stat(Ctx(), AdminActor, "/")
			Expect(err).ToNot(HaveOccurred())
			Expect(n.Kind).To(Equal(workspace.KindDir))
			Expect(n.Path).To(Equal("/"))
			kids, err := s.List(Ctx(), AdminActor, "/")
			Expect(err).ToNot(HaveOccurred())
			Expect(kids).To(BeEmpty())
		})
		It("put creates parents and reads back", func() {
			s := newStore()
			n := MustPut(s, AdminActor, "/clients/rossi/a.pdf", "pdf")
			Expect(n.Path).To(Equal("/clients/rossi/a.pdf"))
			Expect(n.Kind).To(Equal(workspace.KindFile))
			Expect(n.Size).To(BeEquivalentTo(3))
			Expect(n.Version).To(BeEquivalentTo(1))
			Expect(n.Blob).ToNot(BeEmpty())
			want := map[string]string{"/clients": "/", "/clients/rossi": "/", "/clients/rossi/a.pdf": "pdf"}
			Expect(Tree(s)).To(Equal(want))
		})
		It("list is sorted by name", func() {
			s := newStore()
			for _, p := range []string{"/d/c.txt", "/d/a.txt", "/d/b"} {
				if strings.HasSuffix(p, ".txt") {
					MustPut(s, AdminActor, p, "x")
				} else {
					_, err := s.Mkdir(Ctx(), AdminActor, p)
					Expect(err).ToNot(HaveOccurred())
				}
			}
			kids, err := s.List(Ctx(), AdminActor, "/d")
			Expect(err).ToNot(HaveOccurred())
			var names []string
			for _, k := range kids {
				names = append(names, k.Name)
			}
			Expect(names).To(Equal([]string{"a.txt", "b", "c.txt"}))
		})
		It("list says when each entry last changed: the time of the change set of its rev", func() {
			s := newStore()
			MustPut(s, AdminActor, "/m/a.txt", "one")
			MustPut(s, AdminActor, "/m/b.txt", "two")
			MustPut(s, AdminActor, "/m/a.txt", "three")
			kids, err := s.List(Ctx(), AdminActor, "/m")
			Expect(err).ToNot(HaveOccurred())
			Expect(kids).To(HaveLen(2))
			hist, err := s.History(Ctx(), AdminActor, workspace.HistoryQuery{Path: "/m"})
			Expect(err).ToNot(HaveOccurred())
			at := map[int64]int64{}
			for _, cs := range hist {
				at[cs.Rev] = cs.CreatedAt.UnixMilli()
			}
			for _, k := range kids {
				Expect(k.Modified.IsZero()).To(BeFalse(), "%s has no time", k.Name)
				Expect(k.Modified.UnixMilli()).To(Equal(at[k.Rev]), "%s", k.Name)
			}
			Expect(kids[0].Rev).To(BeNumerically(">", kids[1].Rev), "a.txt changed last")
		})
		It("NFD and NFC are one name", func() {
			s := newStore()
			MustPut(s, AdminActor, "/café.txt", "one")
			n, err := s.Put(Ctx(), AdminActor, "/café.txt", strings.NewReader("two"), workspace.PutOptions{BaseVersion: 1})
			Expect(err).ToNot(HaveOccurred())
			Expect(n.Version).To(BeEquivalentTo(2))
			Expect(n.ConflictOf).To(BeEmpty())
			got := Tree(s)
			Expect(got).To(HaveLen(1))
			Expect(got["/café.txt"]).To(Equal("two"))
		})
		It("same content is not a change", func() {
			s := newStore()
			n1 := MustPut(s, AdminActor, "/a.txt", "x")
			n2 := MustPut(s, AdminActor, "/a.txt", "x")
			Expect(n2.Version).To(Equal(n1.Version), "a second put of the same bytes changed the node: %+v -> %+v", n1, n2)
			Expect(n2.Rev).To(Equal(n1.Rev), "a second put of the same bytes changed the node: %+v -> %+v", n1, n2)
		})
		It("matching base version writes", func() {
			s := newStore()
			n1 := MustPut(s, AdminActor, "/r.xlsx", "v1")
			n2, err := s.Put(Ctx(), AliceAdmin(), "/r.xlsx", strings.NewReader("v2"), workspace.PutOptions{BaseVersion: n1.Version})
			Expect(err).ToNot(HaveOccurred())
			Expect(n2.Version).To(BeEquivalentTo(2))
			Expect(n2.ConflictOf).To(BeEmpty())
			Expect(MustRead(s, AdminActor, "/r.xlsx")).To(Equal("v2"), "content not written")
		})
		It("stale base version keeps both", func() {
			s := newStore()
			MustPut(s, AdminActor, "/r.xlsx", "v1")
			MustPut(s, AdminActor, "/r.xlsx", "v2") // now version 2
			a := workspace.Actor{Principal: Admin, ChatID: "12"}
			n, err := s.Put(Ctx(), a, "/r.xlsx", strings.NewReader("mine"), workspace.PutOptions{BaseVersion: 1})
			Expect(err).ToNot(HaveOccurred())
			Expect(n.Path).To(Equal("/r (from chat 12).xlsx"))
			Expect(n.ConflictOf).To(Equal("/r.xlsx"))
			Expect(MustRead(s, AdminActor, "/r.xlsx")).To(Equal("v2"), "both versions must be kept")
			Expect(MustRead(s, AdminActor, n.Path)).To(Equal("mine"), "both versions must be kept")
			n2, err := s.Put(Ctx(), a, "/r.xlsx", strings.NewReader("again"), workspace.PutOptions{BaseVersion: 1})
			Expect(err).ToNot(HaveOccurred())
			Expect(n2.Path).To(Equal("/r (from chat 12) 2.xlsx"))
		})
		It("zero base version on an existing file keeps both", func() {
			s := newStore()
			MustPut(s, AdminActor, "/a.txt", "old")
			n, err := s.Put(Ctx(), AdminActor, "/a.txt", strings.NewReader("new"), workspace.PutOptions{})
			Expect(err).ToNot(HaveOccurred())
			Expect(n.ConflictOf).To(Equal("/a.txt"))
		})
		It("overwrite skips the version check", func() {
			s := newStore()
			MustPut(s, AdminActor, "/a.txt", "old")
			n := MustPut(s, AdminActor, "/a.txt", "new")
			Expect(n.ConflictOf).To(BeEmpty())
			Expect(MustRead(s, AdminActor, "/a.txt")).To(Equal("new"))
		})
		It("case-only sibling is refused", func() {
			s := newStore()
			MustPut(s, AdminActor, "/Report.pdf", "x")
			_, err := s.Put(Ctx(), AdminActor, "/report.pdf", strings.NewReader("y"), workspace.PutOptions{})
			Expect(err).To(MatchError(workspace.ErrExists))
			_, err = s.Mkdir(Ctx(), AdminActor, "/REPORT.PDF")
			Expect(err).To(MatchError(workspace.ErrExists))
		})
		It("file and folder names do not mix", func() {
			s := newStore()
			MustPut(s, AdminActor, "/f", "x")
			_, err := s.Put(Ctx(), AdminActor, "/f/g", strings.NewReader("y"), workspace.PutOptions{})
			Expect(err).To(MatchError(workspace.ErrExists), "put under a file")
			_, err = s.Mkdir(Ctx(), AdminActor, "/d")
			Expect(err).ToNot(HaveOccurred())
			_, err = s.Put(Ctx(), AdminActor, "/d", strings.NewReader("y"), workspace.PutOptions{Overwrite: true})
			Expect(err).To(MatchError(workspace.ErrExists), "put on a folder")
			_, err = s.Mkdir(Ctx(), AdminActor, "/d")
			Expect(err).To(MatchError(workspace.ErrExists), "mkdir twice")
			_, _, err = s.Open(Ctx(), AdminActor, "/d")
			Expect(err).To(MatchError(workspace.ErrInvalid), "open a folder")
			_, err = s.List(Ctx(), AdminActor, "/f")
			Expect(err).To(MatchError(workspace.ErrInvalid), "list a file")
		})
		It("bad names are refused", func() {
			s := newStore()
			for _, p := range []string{"/a/../b", "/a\x00", "//a", "/a\nb"} {
				_, err := s.Put(Ctx(), AdminActor, p, strings.NewReader("x"), workspace.PutOptions{})
				Expect(err).To(MatchError(workspace.ErrBadName), "put %q", p)
			}
			_, err := s.Put(Ctx(), AdminActor, "/", strings.NewReader("x"), workspace.PutOptions{})
			Expect(err).To(MatchError(workspace.ErrInvalid), "put root")
		})
		It("missing path is not found", func() {
			s := newStore()
			_, err := s.Stat(Ctx(), AdminActor, "/nope")
			Expect(err).To(MatchError(workspace.ErrNotFound))
		})
	})
}

// AliceAdmin returns the admin principal acting in Alice's chat, for tests
// that need a chat ID with full access.
func AliceAdmin() workspace.Actor {
	return workspace.Actor{Principal: Admin, ChatID: AliceChat.ChatID}
}
