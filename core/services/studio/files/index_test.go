package files_test

import (
	"bytes"
	"context"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/extract/extracttest"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/textindex"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
	"github.com/mudler/LocalAI/pkg/sqlitedb"
)

// newIndexedFiles is a files service with a text index on one SQLite
// database, as a server with a database has it.
func newIndexedFiles() (*files.Service, *textindex.Indexer) {
	dsn := "file:" + filepath.Join(GinkgoT().TempDir(), "ws.db") + "?_busy_timeout=5000&_txlock=immediate"
	db, err := sqlitedb.Open(dsn, &gorm.Config{})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		if raw, err := db.DB(); err == nil {
			_ = raw.Close()
		}
	})
	blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	Expect(err).NotTo(HaveOccurred())
	ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: files.AdminPrincipal})
	Expect(err).NotTo(HaveOccurred())
	st, err := textindex.NewGORM(db)
	Expect(err).NotTo(HaveOccurred())
	f := files.New(ws)
	ix := textindex.New(textindex.Options{Store: st, Workspace: ws, Admin: workspace.Actor{Principal: files.AdminPrincipal}})
	f.SetTextIndex(ix)
	return f, ix
}

// upload stores data in the user's home the way the REST upload does.
func upload(f *files.Service, user, rel string, data []byte) workspace.Node {
	abs, err := f.Abs(user, rel)
	Expect(err).NotTo(HaveOccurred())
	n, err := f.Store().Put(context.Background(), f.Actor(user), abs, bytes.NewReader(data), workspace.PutOptions{Overwrite: true})
	Expect(err).NotTo(HaveOccurred())
	f.Written(context.Background(), n, user)
	return n
}

const uuidName = "/inbox/7c9e6679-7425-40de-944b-e07fc1f90ae7.pdf"

var _ = Describe("the file tools on indexed documents", func() {
	var (
		f   *files.Service
		ix  *textindex.Indexer
		b   *toolBox
		ctx = context.Background()
	)
	BeforeEach(func() {
		f, ix = newIndexedFiles()
		b = newToolBox(f, "alice", "c1")
	})

	It("finds a PDF with a meaningless name by a word on one of its pages", func() {
		upload(f, "alice", uuidName, extracttest.TextPDF("Lease agreement", "The deposit is returned within thirty days"))
		upload(f, "alice", "/notes.txt", []byte("the deposit was paid\n"))
		Expect(ix.Pass(ctx)).To(Succeed())

		got, err := b.run("search_files", map[string]any{"query": "DEPOSIT"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ContainSubstring("/notes.txt:1: the deposit was paid"), "text files keep their format")
		Expect(got).To(MatchRegexp(`/inbox/7c9e6679-7425-40de-944b-e07fc1f90ae7\.pdf \(page 2, line \d+\): The deposit is returned within thirty days`))
		Expect(got).NotTo(ContainSubstring("still being indexed"))

		// Regular expressions run over the stored text line by line.
		got, err = b.run("search_files", map[string]any{"query": "thirty\\s+days$", "regex": true})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ContainSubstring("(page 2, line"))
		got, err = b.run("search_files", map[string]any{"query": "lease", "ignore_case": false})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("Nothing found."))

		// The search API says where the hit is.
		res, err := f.Search(ctx, "alice", "thirty days", "/inbox", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Hits).To(HaveLen(1))
		Expect(res.Hits[0].Document).To(BeTrue())
		Expect(res.Hits[0].Section.Name).To(Equal("2"))
	})

	It("names the sheet of a hit in a workbook and the slide in a deck", func() {
		upload(f, "alice", "/book.xlsx", extracttest.SharedWorkbook(map[string][][]string{
			"Summary": {{"total", "3"}}, "Suppliers": {{"Contoso Plumbing", "invoice 42"}},
		}, "Summary", "Suppliers"))
		upload(f, "alice", "/deck.pptx", extracttest.Pptx([2]string{"Intro", "hello"}, [2]string{"Plumbing", "Contoso again"}))
		Expect(ix.Pass(ctx)).To(Succeed())
		got, err := b.run("search_files", map[string]any{"query": "contoso"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ContainSubstring(`/book.xlsx (sheet "Suppliers", line`))
		Expect(got).To(ContainSubstring(`/deck.pptx (slide 2, line`))
	})

	It("says that a document is still being indexed instead of finding nothing", func() {
		upload(f, "alice", uuidName, extracttest.TextPDF("budget for the roof"))
		got, err := b.run("search_files", map[string]any{"query": "roof"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HavePrefix("Nothing found yet: 1 file is still being indexed."))
		res, err := f.Search(ctx, "alice", "roof", "/", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Indexing).To(Equal(1))

		Expect(ix.Pass(ctx)).To(Succeed())
		got, err = b.run("search_files", map[string]any{"query": "roof"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ContainSubstring("budget for the roof"))
	})

	It("indexes a document a chat tool writes", func() {
		_, err := b.run("write_file", map[string]any{"path": "/fake.pdf", "content": "not a pdf"})
		Expect(err).NotTo(HaveOccurred())
		pending, err := ix.Store().Pending(ctx, "/", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(HaveLen(1), "the write queued the file at once")
		Expect(ix.Pass(ctx)).To(Succeed())
		n, err := f.Store().Stat(ctx, f.Actor("alice"), session.HomePath("alice")+"/fake.pdf")
		Expect(err).NotTo(HaveOccurred())
		e, err := ix.Store().Get(ctx, n.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(e.Reason).To(Equal(textindex.ReasonNotType))
	})

	It("never shows the old text of a file that changed since it was read", func() {
		upload(f, "alice", "/a.pdf", extracttest.TextPDF("first draft"))
		Expect(ix.Pass(ctx)).To(Succeed())
		upload(f, "alice", "/a.pdf", extracttest.TextPDF("second draft"))
		got, err := b.run("search_files", map[string]any{"query": "first draft"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HavePrefix("Nothing found yet"))
	})

	It("reads a PDF or a workbook with read_file, in parts", func() {
		upload(f, "alice", "/report.pdf", extracttest.TextPDF("one\ntwo\nthree", "four"))
		Expect(ix.Pass(ctx)).To(Succeed())
		got, err := b.run("read_file", map[string]any{"path": "/report.pdf"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HavePrefix("## Page 1\n"))
		Expect(got).To(ContainSubstring("## Page 2\nfour"))
		got, err = b.run("read_file", map[string]any{"path": "/report.pdf", "offset": 1, "limit": 2})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(MatchRegexp(`^## Page 1\n\S.*\n\[\d+ more lines; continue with offset 3\]$`))

		// A file the index has not read yet is read on the spot.
		upload(f, "alice", "/book.xlsx", extracttest.InlineWorkbook(map[string][][]string{"S": {{"cell text"}}}, "S"))
		got, err = b.run("read_file", map[string]any{"path": "/book.xlsx"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(ContainSubstring("cell text"))

		upload(f, "alice", "/scan.pdf", extracttest.BlankPDF())
		_, err = b.run("read_file", map[string]any{"path": "/scan.pdf"})
		Expect(err).To(MatchError(ContainSubstring("no text layer")))
		Expect(ix.Pass(ctx)).To(Succeed())
		_, err = b.run("read_file", map[string]any{"path": "/scan.pdf"})
		Expect(err).To(MatchError(ContainSubstring("no text layer")))
	})

	It("shows a shared document to the users of the folder only, and drops purged files", func() {
		admin := workspace.Actor{Principal: files.AdminPrincipal}
		Expect(f.EnsureHome(ctx, "bob")).To(Succeed())
		Expect(f.EnsureHome(ctx, "carol")).To(Succeed())
		_, err := f.Store().Put(ctx, admin, "/shared/team/minutes.docx", bytes.NewReader(extracttest.Docx(extracttest.Para("", "We chose the blue tiles"))), workspace.PutOptions{})
		Expect(err).NotTo(HaveOccurred())
		for _, u := range []string{"alice", "bob"} {
			Expect(f.Store().Grant(ctx, admin, u, "/shared/team", workspace.ModeRead)).To(Succeed())
		}
		f.SetSharedRoots(func(context.Context, workspace.Actor) ([]session.SharedRoot, error) {
			return []session.SharedRoot{{Path: "/shared/team", Mode: workspace.ModeRead}}, nil
		})
		Expect(ix.Pass(ctx)).To(Succeed())
		for _, u := range []string{"alice", "bob"} {
			got, err := newToolBox(f, u, "c").run("search_files", map[string]any{"query": "blue tiles", "path": "/workspace/shared-team"})
			Expect(err).NotTo(HaveOccurred(), u)
			Expect(got).To(ContainSubstring("/workspace/shared-team/minutes.docx (line 1): We chose the blue tiles"), u)
		}
		_, err = newToolBox(f, "carol", "c").run("search_files", map[string]any{"query": "blue tiles", "path": "/workspace/shared-team"})
		Expect(err).To(HaveOccurred(), "carol has no grant on the folder")
		got, err := newToolBox(f, "carol", "c").run("search_files", map[string]any{"query": "blue tiles"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("Nothing found."))

		// A file of alice's that she deletes for good leaves no entry.
		n := upload(f, "alice", "/old.pdf", extracttest.TextPDF("obsolete"))
		Expect(ix.Pass(ctx)).To(Succeed())
		abs, _ := f.Abs("alice", "/old.pdf")
		Expect(f.Store().Delete(ctx, f.Actor("alice"), abs)).To(Succeed())
		_, err = f.EmptyTrash(ctx, "alice", false)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() error { _, err := ix.Store().Get(ctx, n.ID); return err }).Should(MatchError(textindex.ErrNotFound))
	})
})
