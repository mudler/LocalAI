// Package textindextest is the contract of a textindex.Store. Each store runs
// it from its own tests.
package textindextest

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/extract"
	"github.com/mudler/LocalAI/core/services/studio/textindex"
)

var ctx = context.Background()

func ready(id int64, path, text string) textindex.Entry {
	return textindex.Entry{NodeID: id, Path: path, Owner: "alice", Hash: "h" + path, Extractor: 1,
		Status: textindex.StatusReady, Kind: extract.KindPages, Text: text,
		Sections: []extract.Section{{Kind: extract.SectionPage, Name: "1", Line: 1}},
		Updated:  time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)}
}

func ids(es []textindex.Entry) []int64 {
	out := make([]int64, len(es))
	for i, e := range es {
		out[i] = e.NodeID
	}
	return out
}

// Run registers the contract specs. open returns a new, empty store.
func Run(open func() textindex.Store) {
	var s textindex.Store
	BeforeEach(func() { s = open() })

	It("keeps an entry with its text, sections and status", func() {
		e := ready(7, "/home/alice/report.pdf", "## Page 1\nQuarterly Revenue")
		e.Truncated = true
		Expect(s.Put(ctx, e)).To(Succeed())
		got, err := s.Get(ctx, 7)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Text).To(Equal(e.Text))
		Expect(got.Sections).To(Equal(e.Sections))
		Expect(got.Status).To(Equal(textindex.StatusReady))
		Expect(got.Truncated).To(BeTrue())
		Expect(got.Owner).To(Equal("alice"))
		Expect(got.Updated.Equal(e.Updated)).To(BeTrue())

		e.Status, e.Text, e.Reason = textindex.StatusSkipped, "", "no text layer"
		Expect(s.Put(ctx, e)).To(Succeed())
		got, err = s.Get(ctx, 7)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Text).To(BeEmpty())
		Expect(got.Reason).To(Equal("no text layer"))

		_, err = s.Get(ctx, 8)
		Expect(err).To(MatchError(textindex.ErrNotFound))
	})

	It("finds ready entries by text, ignoring case, below a path", func() {
		Expect(s.Put(ctx, ready(1, "/home/alice/a.pdf", "Invoice from ACME"))).To(Succeed())
		Expect(s.Put(ctx, ready(2, "/home/alice/sub/b.docx", "the acme contract"))).To(Succeed())
		Expect(s.Put(ctx, ready(3, "/home/alicebob/c.pdf", "acme too"))).To(Succeed())
		Expect(s.Put(ctx, ready(4, "/home/alice/d.pdf", "Ünïcode Straße"))).To(Succeed())
		p := ready(5, "/home/alice/e.pdf", "")
		p.Status = textindex.StatusPending
		Expect(s.Put(ctx, p)).To(Succeed())

		got, err := s.Search(ctx, textindex.Query{Prefix: "/home/alice", Contains: "acme"})
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(got)).To(Equal([]int64{1, 2}))
		Expect(got[0].Text).To(Equal("Invoice from ACME"))

		got, err = s.Search(ctx, textindex.Query{Prefix: "/", Contains: "acme"})
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(got)).To(Equal([]int64{1, 2, 3}))

		// Short needles and other scripts.
		got, err = s.Search(ctx, textindex.Query{Prefix: "/home/alice", Contains: "ac"})
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(got)).To(Equal([]int64{1, 2}))
		got, err = s.Search(ctx, textindex.Query{Prefix: "/home/alice", Contains: strings.ToLower("STRASSE")})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeEmpty())
		got, err = s.Search(ctx, textindex.Query{Prefix: "/home/alice", Contains: strings.ToLower("ÜNÏCODE")})
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(got)).To(Equal([]int64{4}))

		// LIKE wildcards in the needle and the prefix are plain characters.
		got, err = s.Search(ctx, textindex.Query{Prefix: "/home/alice", Contains: "%"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeEmpty())
		got, err = s.Search(ctx, textindex.Query{Prefix: "/home/alic_", Contains: "acme"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeEmpty())

		// One file as the prefix.
		got, err = s.Search(ctx, textindex.Query{Prefix: "/home/alice/sub/b.docx", Contains: "contract"})
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(got)).To(Equal([]int64{2}))
	})

	It("pages through every ready entry for a caller that matches by itself", func() {
		for i := int64(1); i <= 5; i++ {
			Expect(s.Put(ctx, ready(i, "/home/alice/f"+string(rune('0'+i))+".pdf", "x"))).To(Succeed())
		}
		first, err := s.Search(ctx, textindex.Query{Prefix: "/home/alice", Limit: 2})
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(first)).To(Equal([]int64{1, 2}))
		next, err := s.Search(ctx, textindex.Query{Prefix: "/home/alice", Limit: 2, AfterID: 2})
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(next)).To(Equal([]int64{3, 4}))
	})

	It("lists pending entries oldest first and every entry by node", func() {
		t0 := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)
		for i, id := range []int64{3, 1, 2} {
			e := ready(id, "/home/alice/p"+string(rune('0'+id))+".pdf", "")
			e.Status, e.Updated = textindex.StatusPending, t0.Add(time.Duration(i)*time.Minute)
			Expect(s.Put(ctx, e)).To(Succeed())
		}
		Expect(s.Put(ctx, ready(9, "/home/bob/r.pdf", "done"))).To(Succeed())
		got, err := s.Pending(ctx, "/", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(got)).To(Equal([]int64{3, 1, 2}))
		Expect(got[0].Text).To(BeEmpty())
		got, err = s.Pending(ctx, "/home/bob", 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeEmpty())
		got, err = s.Pending(ctx, "/", 2)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveLen(2))

		all, err := s.List(ctx, "/", 1, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(all)).To(Equal([]int64{2, 3, 9}))
		Expect(all[2].Text).To(BeEmpty())
	})

	It("moves a file and every entry below a moved folder", func() {
		Expect(s.Put(ctx, ready(1, "/home/alice/docs/a.pdf", "a"))).To(Succeed())
		Expect(s.Put(ctx, ready(2, "/home/alice/docs/deep/b.pdf", "b"))).To(Succeed())
		Expect(s.Put(ctx, ready(3, "/home/alice/docs2/c.pdf", "c"))).To(Succeed())
		Expect(s.Put(ctx, ready(4, "/home/alice/x.pdf", "x"))).To(Succeed())
		Expect(s.Move(ctx, "/home/alice/docs", "/home/alice/archive/docs")).To(Succeed())
		Expect(s.Move(ctx, "/home/alice/x.pdf", "/home/alice/y.pdf")).To(Succeed())
		paths := map[int64]string{}
		all, err := s.List(ctx, "/", 0, 10)
		Expect(err).NotTo(HaveOccurred())
		for _, e := range all {
			paths[e.NodeID] = e.Path
		}
		Expect(paths).To(Equal(map[int64]string{
			1: "/home/alice/archive/docs/a.pdf", 2: "/home/alice/archive/docs/deep/b.pdf",
			3: "/home/alice/docs2/c.pdf", 4: "/home/alice/y.pdf",
		}))
		got, err := s.Search(ctx, textindex.Query{Prefix: "/home/alice/archive", Contains: "b"})
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(got)).To(Equal([]int64{2}))
	})

	It("deletes entries and finds a ready text by its hash", func() {
		Expect(s.Put(ctx, ready(1, "/home/alice/a.pdf", "same words"))).To(Succeed())
		got, ok, err := s.ReadyByHash(ctx, "h/home/alice/a.pdf", 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(got.Text).To(Equal("same words"))
		_, ok, err = s.ReadyByHash(ctx, "h/home/alice/a.pdf", 2)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())

		Expect(s.Delete(ctx, 1, 99)).To(Succeed())
		_, err = s.Get(ctx, 1)
		Expect(err).To(MatchError(textindex.ErrNotFound))
		found, err := s.Search(ctx, textindex.Query{Contains: "same"})
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeEmpty())
		Expect(s.Delete(ctx)).To(Succeed())
	})

	It("replaces the text of a node in the search", func() {
		Expect(s.Put(ctx, ready(1, "/home/alice/a.pdf", "old words"))).To(Succeed())
		Expect(s.Put(ctx, ready(1, "/home/alice/a.pdf", "new words"))).To(Succeed())
		got, err := s.Search(ctx, textindex.Query{Contains: "old"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeEmpty())
		got, err = s.Search(ctx, textindex.Query{Contains: "new"})
		Expect(err).NotTo(HaveOccurred())
		Expect(ids(got)).To(Equal([]int64{1}))
	})

	It("counts the pending entries below a path", func() {
		for _, id := range []int64{1, 2, 3} {
			e := ready(id, "/home/alice/p"+string(rune('0'+id))+".pdf", "")
			e.Status = textindex.StatusPending
			Expect(s.Put(ctx, e)).To(Succeed())
		}
		e := ready(4, "/home/bob/q.pdf", "")
		e.Status = textindex.StatusPending
		Expect(s.Put(ctx, e)).To(Succeed())
		Expect(s.Put(ctx, ready(5, "/home/alice/done.pdf", "done"))).To(Succeed())
		n, err := s.CountPending(ctx, "/home/alice")
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(3))
		n, err = s.CountPending(ctx, "/")
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(4))
	})

	It("keeps the whole text of a document by hash and prunes the texts no entry has", func() {
		const kept, orphan = "aaaa1111", "bbbb2222"
		e := ready(1, "/home/alice/long.pdf", "the first part")
		e.Hash, e.Truncated = kept, true
		Expect(s.Put(ctx, e)).To(Succeed())
		Expect(s.PutDocument(ctx, kept, 1, "the first part and the rest")).To(Succeed())
		Expect(s.PutDocument(ctx, kept, 1, "the whole text")).To(Succeed())
		Expect(s.PutDocument(ctx, orphan, 1, "a purged file")).To(Succeed())
		text, ok, err := s.Document(ctx, kept, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(text).To(Equal("the whole text"))
		_, ok, err = s.Document(ctx, kept, 2)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse(), "another extractor version reads again")

		Expect(s.PruneDocuments(ctx)).To(Succeed())
		_, ok, err = s.Document(ctx, orphan, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse())
		_, ok, err = s.Document(ctx, kept, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())

		Expect(s.Delete(ctx, 1)).To(Succeed())
		Expect(s.PruneDocuments(ctx)).To(Succeed())
		_, ok, err = s.Document(ctx, kept, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeFalse(), "the text of a deleted file goes with its entry")
	})

	It("keeps the journal cursor", func() {
		rev, err := s.Cursor(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rev).To(BeZero())
		Expect(s.SetCursor(ctx, 41)).To(Succeed())
		Expect(s.SetCursor(ctx, 42)).To(Succeed())
		rev, err = s.Cursor(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rev).To(Equal(int64(42)))
	})
}

// Seed writes entries that CheckSeed reads back, for a reopen test.
func Seed(s textindex.Store) {
	Expect(s.Put(ctx, ready(1, "/home/alice/a.pdf", "kept across a restart"))).To(Succeed())
	Expect(s.SetCursor(ctx, 12)).To(Succeed())
}

// CheckSeed checks what Seed wrote.
func CheckSeed(s textindex.Store) {
	got, err := s.Search(ctx, textindex.Query{Contains: "restart"})
	Expect(err).NotTo(HaveOccurred())
	Expect(ids(got)).To(Equal([]int64{1}))
	rev, err := s.Cursor(ctx)
	Expect(err).NotTo(HaveOccurred())
	Expect(rev).To(Equal(int64(12)))
}
