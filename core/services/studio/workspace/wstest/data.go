package wstest

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// describeDataMode checks the grant mode data: read access, plus changes to
// files that are not code, enforced on every kind of change.
func describeDataMode(newStore NewStore) {
	Describe("Data mode", func() {
		setup := func() workspace.Store {
			GinkgoHelper()
			s := newStore()
			MustPut(s, AdminActor, "/page/index.html", "<p>hi</p>")
			MustPut(s, AdminActor, "/page/page.json", "{}")
			MustPut(s, AdminActor, "/page/app.js", "var a")
			MustPut(s, AdminActor, "/page/data/todo.json", "[]")
			MustPut(s, AdminActor, "/page/lib/util.js", "var u")
			MustPut(s, AdminActor, "/page/lib/note.txt", "n")
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/page", workspace.ModeData)).To(Succeed())
			return s
		}
		put := func(s workspace.Store, p string) error {
			_, err := s.Put(Ctx(), Alice, p, strings.NewReader("x"), workspace.PutOptions{Overwrite: true})
			return err
		}
		It("reads, creates and changes data files and folders", func() {
			s := setup()
			Expect(MustRead(s, Alice, "/page/index.html")).To(Equal("<p>hi</p>"))
			Expect(put(s, "/page/data/todo.json")).To(Succeed())
			Expect(put(s, "/page/data/new/item.json")).To(Succeed())
			Expect(put(s, "/page/notes.txt")).To(Succeed())
			_, err := s.Mkdir(Ctx(), Alice, "/page/more")
			Expect(err).ToNot(HaveOccurred())
			_, err = s.Move(Ctx(), Alice, "/page/notes.txt", "/page/data/notes.txt")
			Expect(err).ToNot(HaveOccurred())
			Expect(s.Delete(Ctx(), Alice, "/page/data/notes.txt")).To(Succeed())
		})
		It("refuses every code extension, index and page.json, new or existing", func() {
			s := setup()
			for _, ext := range []string{"js", "mjs", "cjs", "css", "html", "htm", "svg", "xml", "xhtml", "wasm", "JS", "Html"} {
				Expect(put(s, "/page/new."+ext)).To(MatchError(workspace.ErrReadOnly), ext)
				Expect(put(s, "/page/data/new."+ext)).To(MatchError(workspace.ErrReadOnly), "below a folder: "+ext)
			}
			for _, p := range []string{"/page/index.html", "/page/page.json", "/page/app.js", "/page/lib/util.js", "/page/index", "/page/new.js.", "/page/new.js:x"} {
				Expect(put(s, p)).To(MatchError(workspace.ErrReadOnly), p)
			}
			Expect(MustRead(s, Alice, "/page/index.html")).To(Equal("<p>hi</p>"), "the code is unchanged")
			_, err := s.Mkdir(Ctx(), Alice, "/page/x.js")
			Expect(err).To(MatchError(workspace.ErrReadOnly), "a folder named like code")
		})
		It("refuses to delete, move or rename code, and to turn data into code", func() {
			s := setup()
			Expect(s.Delete(Ctx(), Alice, "/page/index.html")).To(MatchError(workspace.ErrReadOnly))
			Expect(s.Delete(Ctx(), Alice, "/page/app.js")).To(MatchError(workspace.ErrReadOnly))
			Expect(s.Delete(Ctx(), Alice, "/page/lib")).To(MatchError(workspace.ErrReadOnly), "a folder with code in it")
			_, err := s.Move(Ctx(), Alice, "/page/app.js", "/page/app.txt")
			Expect(err).To(MatchError(workspace.ErrReadOnly), "code to data")
			_, err = s.Move(Ctx(), Alice, "/page/data/todo.json", "/page/todo.js")
			Expect(err).To(MatchError(workspace.ErrReadOnly), "data to code")
			_, err = s.Move(Ctx(), Alice, "/page/lib", "/page/library")
			Expect(err).To(MatchError(workspace.ErrReadOnly), "a folder with code in it")
			Expect(MustRead(s, Alice, "/page/lib/util.js")).To(Equal("var u"))
			Expect(MustRead(s, Alice, "/page/app.js")).To(Equal("var a"))
			_, err = s.Move(Ctx(), Alice, "/page/data", "/page/state")
			Expect(err).ToNot(HaveOccurred(), "a folder of data moves")
		})
		It("does not let an undo bring code back or take it away", func() {
			s := setup()
			Expect(s.Delete(Ctx(), AdminActor, "/page/app.js")).To(Succeed())
			cs, err := s.History(Ctx(), AdminActor, workspace.HistoryQuery{Path: "/page", Limit: 1})
			Expect(err).ToNot(HaveOccurred())
			Expect(cs).ToNot(BeEmpty())
			_, _, err = s.Undo(Ctx(), Alice, cs[0].Rev, workspace.UndoOptions{})
			Expect(err).To(MatchError(workspace.ErrReadOnly), "the undo is a change of code")
		})
		It("does not reach outside the folder, and a grant elsewhere is not narrowed", func() {
			s := setup()
			MustPut(s, AdminActor, "/other/a.txt", "a")
			Expect(put(s, "/other/b.txt")).To(MatchError(workspace.ErrNotFound))
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/other", workspace.ModeWrite)).To(Succeed())
			Expect(put(s, "/other/b.js")).To(Succeed(), "write access is not narrowed")
			_, err := s.Move(Ctx(), Alice, "/other/b.js", "/page/b.js")
			Expect(err).To(MatchError(workspace.ErrReadOnly), "a move into the data folder is narrowed")
		})
		It("is the highest of the grants a principal holds, by order", func() {
			s := setup()
			Expect(s.Grant(Ctx(), AdminActor, "group:office", "/page", workspace.ModeRead)).To(Succeed())
			Expect(put(s, "/page/data/b.json")).To(Succeed(), "Bob's group read does not lower Alice")
			Expect(put(s, "/page/data/c.json")).To(Succeed())
			Expect(s.Grant(Ctx(), AdminActor, Bob.Principal, "/page", workspace.ModeData)).To(Succeed())
			_, err := s.Put(Ctx(), Bob, "/page/data/d.json", strings.NewReader("x"), workspace.PutOptions{Overwrite: true})
			Expect(err).ToNot(HaveOccurred())
			Expect(s.Grant(Ctx(), AdminActor, "group:office", "/page", workspace.ModeWrite)).To(Succeed())
			_, err = s.Put(Ctx(), Bob, "/page/e.js", strings.NewReader("x"), workspace.PutOptions{Overwrite: true})
			Expect(err).ToNot(HaveOccurred(), "write wins over data")
		})
		It("lists the grant, mounts the folder without write access and shows it as readable", func() {
			s := setup()
			g, ok := s.(workspace.GrantLister)
			Expect(ok).To(BeTrue())
			got, err := g.Grants(Ctx(), AdminActor, "/page")
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal([]workspace.Grant{{Principal: Alice.Principal, Mode: workspace.ModeData}}))
			v, err := s.View(Ctx(), Alice, []workspace.MountRequest{{Path: "/page", At: "p", Mode: workspace.ModeWrite}})
			Expect(err).ToNot(HaveOccurred())
			Expect(v.Mounts).To(HaveLen(1))
			Expect(v.Mounts[0].Mode).To(Equal(workspace.ModeData), "a view never gives more than the grant")
			Expect(v.Mounts[0].Mode).To(BeNumerically("<", workspace.ModeWrite))
			prefixes, err := s.ReadablePrefixes(Ctx(), Alice)
			Expect(err).ToNot(HaveOccurred())
			Expect(prefixes).To(Equal([]string{"/page"}))
		})
		It("refuses the grant of a mode that does not exist", func() {
			s := setup()
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/page", workspace.Mode(9))).To(MatchError(workspace.ErrInvalid))
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/page", workspace.ModeNone)).To(MatchError(workspace.ErrInvalid))
		})
	})
}

// describePageGrants checks the mark of a grant made through the page routes.
func describePageGrants(newStore NewStore) {
	Describe("Page grants", func() {
		pg := func(s workspace.Store) workspace.PageGranter {
			GinkgoHelper()
			g, ok := s.(workspace.PageGranter)
			Expect(ok).To(BeTrue(), "the store marks page grants")
			return g
		}
		It("marks a grant, lists the mark, and clears it on a plain grant or a revoke", func() {
			s := newStore()
			MustPut(s, AdminActor, "/page/index.html", "x")
			g := pg(s)
			lister := s.(workspace.GrantLister)
			tree := s.(workspace.GrantTree)
			Expect(g.GrantPage(Ctx(), AdminActor, Alice.Principal, "/page", workspace.ModeData)).To(Succeed())
			Expect(s.Grant(Ctx(), AdminActor, Bob.Principal, "/page", workspace.ModeRead)).To(Succeed())
			got, err := lister.Grants(Ctx(), AdminActor, "/page")
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(ConsistOf(
				workspace.Grant{Principal: Alice.Principal, Mode: workspace.ModeData, Page: true},
				workspace.Grant{Principal: Bob.Principal, Mode: workspace.ModeRead}))
			below, err := tree.GrantsBelow(Ctx(), AdminActor, "/")
			Expect(err).ToNot(HaveOccurred())
			Expect(below).To(ContainElement(HaveField("Grant", workspace.Grant{Principal: Alice.Principal, Mode: workspace.ModeData, Page: true})))
			ok, err := g.PageShared(Ctx(), Alice, "/page")
			Expect(err).ToNot(HaveOccurred())
			Expect(ok).To(BeTrue())
			ok, _ = g.PageShared(Ctx(), Bob, "/page")
			Expect(ok).To(BeFalse(), "a plain grant has no mark")
			ok, _ = g.PageShared(Ctx(), workspace.Actor{Principal: "user:carol"}, "/page")
			Expect(ok).To(BeFalse())
			ok, _ = g.PageShared(Ctx(), Alice, "/nope")
			Expect(ok).To(BeFalse())
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/page", workspace.ModeWrite)).To(Succeed())
			ok, _ = g.PageShared(Ctx(), Alice, "/page")
			Expect(ok).To(BeFalse(), "a plain grant replaces the page grant")
			Expect(g.GrantPage(Ctx(), AdminActor, Alice.Principal, "/page", workspace.ModeRead)).To(Succeed())
			Expect(s.Revoke(Ctx(), AdminActor, Alice.Principal, "/page")).To(Succeed())
			ok, _ = g.PageShared(Ctx(), Alice, "/page")
			Expect(ok).To(BeFalse(), "a revoke drops the mark")
			Expect(s.Grant(Ctx(), AdminActor, Alice.Principal, "/page", workspace.ModeRead)).To(Succeed())
			ok, _ = g.PageShared(Ctx(), Alice, "/page")
			Expect(ok).To(BeFalse(), "a new plain grant does not bring the mark back")
		})
		It("sees a group's mark, and only the admin marks", func() {
			s := newStore()
			MustPut(s, AdminActor, "/page/index.html", "x")
			g := pg(s)
			Expect(g.GrantPage(Ctx(), Alice, Bob.Principal, "/page", workspace.ModeRead)).To(MatchError(workspace.ErrNotAdmin))
			Expect(g.GrantPage(Ctx(), AdminActor, "group:office", "/page", workspace.ModeRead)).To(Succeed())
			ok, err := g.PageShared(Ctx(), Bob, "/page")
			Expect(err).ToNot(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(g.GrantPage(Ctx(), AdminActor, Alice.Principal, "/page/index.html", workspace.ModeRead)).To(MatchError(workspace.ErrInvalid), "a file")
		})
	})
}
