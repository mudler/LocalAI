package textindex_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/studio/extract"
	"github.com/mudler/LocalAI/core/services/studio/extract/extracttest"
	"github.com/mudler/LocalAI/core/services/studio/textindex"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
	"github.com/mudler/LocalAI/core/services/studio/workspace/gormstore"
)

var (
	ctx   = context.Background()
	admin = workspace.Actor{Principal: "admin"}
)

// rig is a workspace on one SQLite database with a text index in it.
type rig struct {
	db    *gorm.DB
	ws    workspace.Store
	store textindex.Store
	reads atomic.Int64 // calls of the reader
}

func newRig() *rig {
	db := openSQLite(filepath.Join(GinkgoT().TempDir(), "ws.db"))
	blobs, err := blob.NewFS(filepath.Join(GinkgoT().TempDir(), "blobs"))
	Expect(err).NotTo(HaveOccurred())
	ws, err := gormstore.New(db, blobs, gormstore.Options{Admin: admin.Principal})
	Expect(err).NotTo(HaveOccurred())
	st, err := textindex.NewGORM(db)
	Expect(err).NotTo(HaveOccurred())
	return &rig{db: db, ws: ws, store: st}
}

// indexer returns an indexer on the rig that counts its reads; extra, when
// set, runs before each read.
func (r *rig) indexer(o textindex.Options, extra func(name string)) *textindex.Indexer {
	o.Store, o.Workspace, o.Admin = r.store, r.ws, admin
	o.Extract = func(ctx context.Context, name string, data []byte, l extract.Limits) (string, extract.Structure, error) {
		r.reads.Add(1)
		if extra != nil {
			extra(name)
		}
		return extract.ExtractWith(ctx, name, data, l)
	}
	return textindex.New(o)
}

func (r *rig) put(p string, data []byte) workspace.Node {
	n, err := r.ws.Put(ctx, admin, p, bytes.NewReader(data), workspace.PutOptions{Overwrite: true})
	Expect(err).NotTo(HaveOccurred())
	return n
}

func (r *rig) entry(id int64) textindex.Entry {
	e, err := r.store.Get(ctx, id)
	Expect(err).NotTo(HaveOccurred())
	return e
}

func (r *rig) gone(id int64) {
	_, err := r.store.Get(ctx, id)
	Expect(err).To(MatchError(textindex.ErrNotFound))
}

func (r *rig) find(prefix, word string) []string {
	got, err := r.store.Search(ctx, textindex.Query{Prefix: prefix, Contains: strings.ToLower(word)})
	Expect(err).NotTo(HaveOccurred())
	var out []string
	for _, e := range got {
		out = append(out, e.Path)
	}
	return out
}

var _ = Describe("The indexer", func() {
	var r *rig
	BeforeEach(func() { r = newRig() })

	It("reads a new PDF, Word, Excel and PowerPoint file and follows overwrites", func() {
		ix := r.indexer(textindex.Options{}, nil)
		pdf := r.put("/home/alice/1b9d6bcd-bbfd-4b2d-9b5d-ab8dfbbd4bed.pdf", extracttest.TextPDF("Cover", "The tender closes on Friday"))
		doc := r.put("/home/alice/plan.docx", extracttest.Docx(extracttest.Para("", "Hire a plumber")))
		xls := r.put("/home/alice/book.xlsx", extracttest.SharedWorkbook(map[string][][]string{"Costs": {{"boiler", "1200"}}}, "Costs"))
		ppt := r.put("/home/alice/deck.pptx", extracttest.Pptx([2]string{"Roadmap", "Ship the indexer"}))
		r.put("/home/alice/notes.txt", []byte("plain text is searched by the walk"))
		Expect(ix.Pass(ctx)).To(Succeed())

		e := r.entry(pdf.ID)
		Expect(e.Status).To(Equal(textindex.StatusReady))
		Expect(e.Hash).To(Equal(pdf.Blob))
		Expect(e.Owner).To(Equal("admin"))
		Expect(e.Text).To(ContainSubstring("tender closes"))
		Expect(e.Sections).To(HaveLen(2))
		Expect(r.entry(doc.ID).Text).To(ContainSubstring("plumber"))
		Expect(r.entry(xls.ID).Sections[0].Name).To(Equal("Costs"))
		Expect(r.entry(ppt.ID).Text).To(ContainSubstring("Ship the indexer"))
		Expect(r.find("/home/alice", "TENDER")).To(ConsistOf(pdf.Path))
		Expect(r.reads.Load()).To(Equal(int64(4)))

		// An unchanged journal reads nothing again.
		Expect(ix.Pass(ctx)).To(Succeed())
		Expect(r.reads.Load()).To(Equal(int64(4)))

		// An overwrite is read again; the old words are gone.
		r.put(doc.Path, extracttest.Docx(extracttest.Para("", "Call the electrician")))
		Expect(ix.Pass(ctx)).To(Succeed())
		Expect(r.find("/", "plumber")).To(BeEmpty())
		Expect(r.find("/", "electrician")).To(ConsistOf(doc.Path))
	})

	It("follows renames and folder moves, and drops deleted files", func() {
		ix := r.indexer(textindex.Options{}, nil)
		a := r.put("/home/alice/in/a.pdf", extracttest.TextPDF("alpha words"))
		b := r.put("/home/alice/in/deep/b.docx", extracttest.Docx(extracttest.Para("", "bravo words")))
		Expect(ix.Pass(ctx)).To(Succeed())
		reads := r.reads.Load()

		_, err := r.ws.Move(ctx, admin, "/home/alice/in", "/home/alice/out")
		Expect(err).NotTo(HaveOccurred())
		_, err = r.ws.Move(ctx, admin, "/home/alice/out/a.pdf", "/home/alice/out/renamed.pdf")
		Expect(err).NotTo(HaveOccurred())
		Expect(ix.Pass(ctx)).To(Succeed())
		Expect(r.entry(a.ID).Path).To(Equal("/home/alice/out/renamed.pdf"))
		Expect(r.entry(b.ID).Path).To(Equal("/home/alice/out/deep/b.docx"))
		Expect(r.reads.Load()).To(Equal(reads), "a move reads nothing again")

		// A rename to a type that is not read drops the entry.
		_, err = r.ws.Move(ctx, admin, "/home/alice/out/renamed.pdf", "/home/alice/out/renamed.bin")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.ws.Delete(ctx, admin, "/home/alice/out/deep")).To(Succeed())
		Expect(ix.Pass(ctx)).To(Succeed())
		r.gone(a.ID)
		r.gone(b.ID)
	})

	It("indexes the files that were there before it, and resumes where it stopped", func() {
		var paths []string
		for _, n := range []string{"one", "two", "three", "four", "five"} {
			paths = append(paths, r.put("/home/alice/"+n+".pdf", extracttest.TextPDF("backfill "+n)).Path)
		}
		ix := r.indexer(textindex.Options{SetsPerPass: 2, FilesPerPass: 2}, nil)
		Expect(ix.Pass(ctx)).To(Succeed())
		Expect(r.reads.Load()).To(Equal(int64(2)), "one pass reads a bounded number of files")
		cur, err := r.store.Cursor(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(cur).To(BeNumerically(">", 0))

		// A new indexer (a restart) goes on from the stored cursor.
		again := r.indexer(textindex.Options{}, nil)
		Expect(again.Pass(ctx)).To(Succeed())
		Expect(r.find("/home/alice", "backfill")).To(ConsistOf(paths))
		Expect(r.reads.Load()).To(Equal(int64(5)))
	})

	It("marks a scan skipped, a broken or mislabelled file failed or skipped, and survives a panic", func() {
		ix := r.indexer(textindex.Options{}, func(name string) {
			if strings.Contains(name, "boom") {
				panic("reader bug")
			}
		})
		scan := r.put("/home/alice/scan.pdf", extracttest.BlankPDF())
		broken := r.put("/home/alice/broken.docx", []byte("PK\x03\x04 not really a zip"))
		fake := r.put("/home/alice/fake.pdf", []byte("just text"))
		boom := r.put("/home/alice/boom.pdf", extracttest.TextPDF("never stored"))
		ok := r.put("/home/alice/fine.pdf", extracttest.TextPDF("still indexed"))
		Expect(ix.Pass(ctx)).To(Succeed())
		Expect(r.entry(scan.ID)).To(And(HaveField("Status", textindex.StatusSkipped), HaveField("Reason", textindex.ReasonNoText)))
		Expect(r.entry(broken.ID)).To(And(HaveField("Status", textindex.StatusFailed), HaveField("Reason", textindex.ReasonInvalid)))
		Expect(r.entry(fake.ID)).To(And(HaveField("Status", textindex.StatusSkipped), HaveField("Reason", textindex.ReasonNotType)))
		Expect(r.entry(boom.ID)).To(And(HaveField("Status", textindex.StatusFailed), HaveField("Reason", textindex.ReasonPanic)))
		Expect(r.entry(ok.ID).Status).To(Equal(textindex.StatusReady))
	})

	It("keeps the text and the file within their caps", func() {
		long := strings.Repeat("a line of the long report\n", 400)
		ix := r.indexer(textindex.Options{MaxTextBytes: func() int { return 1024 }, MaxFileBytes: 64 << 10}, nil)
		n := r.put("/home/alice/long.docx", extracttest.Docx(extracttest.Para("", long)))
		big := r.put("/home/alice/big.pdf", append([]byte("%PDF-1.4\n"), make([]byte, 70<<10)...))
		Expect(ix.Pass(ctx)).To(Succeed())
		e := r.entry(n.ID)
		Expect(e.Truncated).To(BeTrue())
		Expect(len(e.Text)).To(BeNumerically("<", 1200))
		// The reader stops at the cap, or the index cuts what it returned.
		Expect(e.Text).To(Or(HaveSuffix("[The index keeps the first 1024 bytes of the text.]"), ContainSubstring("[The text stops here")))
		Expect(r.entry(big.ID)).To(And(HaveField("Status", textindex.StatusSkipped), HaveField("Reason", textindex.ReasonTooLarge)))
		Expect(r.reads.Load()).To(Equal(int64(1)))
	})

	It("reads one copy of the same content once", func() {
		ix := r.indexer(textindex.Options{}, nil)
		data := extracttest.TextPDF("shared minutes")
		r.put("/home/alice/a.pdf", data)
		Expect(ix.Pass(ctx)).To(Succeed())
		b := r.put("/home/bob/copy.pdf", data)
		Expect(ix.Pass(ctx)).To(Succeed())
		Expect(r.entry(b.ID).Text).To(ContainSubstring("shared minutes"))
		Expect(r.reads.Load()).To(Equal(int64(1)))
	})

	It("does nothing while indexing is off, and catches up when it is on again", func() {
		var on atomic.Bool
		ix := r.indexer(textindex.Options{Enabled: on.Load}, nil)
		n := r.put("/home/alice/a.pdf", extracttest.TextPDF("later"))
		ix.Enqueue(ctx, n, "alice")
		Expect(ix.Pass(ctx)).To(Succeed())
		_, err := r.store.Get(ctx, n.ID)
		Expect(err).To(MatchError(textindex.ErrNotFound), "an enqueue while off records nothing")
		on.Store(true)
		Expect(ix.Pass(ctx)).To(Succeed())
		Expect(r.entry(n.ID).Status).To(Equal(textindex.StatusReady))
	})

	It("queues an upload at once, before any pass", func() {
		ix := r.indexer(textindex.Options{}, nil)
		n := r.put("/home/alice/a.pdf", extracttest.TextPDF("soon"))
		ix.Enqueue(ctx, n, "alice")
		e := r.entry(n.ID)
		Expect(e.Status).To(Equal(textindex.StatusPending))
		pending, err := r.store.Pending(ctx, "/home/alice", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(HaveLen(1))
		Expect(ix.Pass(ctx)).To(Succeed())
		Expect(r.entry(n.ID).Status).To(Equal(textindex.StatusReady))
	})

	It("forgets the entries of purged files", func() {
		ix := r.indexer(textindex.Options{}, nil)
		keep := r.put("/home/alice/keep.pdf", extracttest.TextPDF("keep"))
		drop := r.put("/home/alice/drop.pdf", extracttest.TextPDF("drop"))
		Expect(ix.Pass(ctx)).To(Succeed())
		// The purge erases the node and its journal rows before any pass saw
		// the delete.
		Expect(r.ws.Delete(ctx, admin, drop.Path)).To(Succeed())
		_, err := r.ws.(workspace.Purger).PurgeAll(ctx, admin, "/home/alice", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(ix.Forget(ctx, "/home/alice")).To(Succeed())
		r.gone(drop.ID)
		Expect(r.entry(keep.ID).Status).To(Equal(textindex.StatusReady))
	})

	It("reads each file once when two frontends share the database", func() {
		var started sync.WaitGroup
		gate := make(chan struct{})
		// On SQLite the advisory lock is per process, so two indexers of one
		// process stand for two frontends of one PostgreSQL database.
		first := r.indexer(textindex.Options{DB: r.db}, func(string) { started.Done(); <-gate })
		second := r.indexer(textindex.Options{DB: r.db}, nil)
		n := r.put("/home/alice/a.pdf", extracttest.TextPDF("cluster words"))
		started.Add(1)
		done := make(chan error, 1)
		go func() { done <- first.Pass(ctx) }()
		started.Wait()
		// While the first frontend holds the lock, the second one's pass
		// does nothing.
		Expect(second.Pass(ctx)).To(Succeed())
		close(gate)
		Expect(<-done).To(Succeed())
		Expect(second.Pass(ctx)).To(Succeed())
		Expect(r.reads.Load()).To(Equal(int64(1)))
		Expect(r.find("/", "cluster")).To(ConsistOf(n.Path))
	})
})
