package docxedit

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"

	"github.com/mudler/LocalAI/addons/office/internal/docx"
	"github.com/mudler/LocalAI/addons/office/internal/officecheck"
	"github.com/mudler/LocalAI/addons/office/internal/officeread"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Body order of the source document, top-level paragraphs only:
//
//	0 Heading1 "Title"
//	1 "Hello world, hello"
//	2 "Bold lead" (bold run) + " plain tail"
//	3 ListBullet "alpha"
//	4 ListBullet "beta"
//	  (table: header Name, Note; row "hello", "hello table")
//	5 empty paragraph that docx.Write puts after a table
//	6 "Last hello"
func source() []byte {
	GinkgoHelper()
	var buf bytes.Buffer
	err := docx.Write(&buf, docx.Document{
		Title: "Sample", Author: "Tester",
		Blocks: []docx.Block{
			docx.Heading{Level: 1, Text: "Title"},
			docx.Paragraph{Text: "Hello world, hello"},
			docx.Paragraph{Runs: []docx.Run{{Text: "Bold lead", Bold: true}, {Text: " plain tail"}}},
			docx.Bullets{Items: []string{"alpha", "beta"}},
			docx.Table{Header: []string{"Name", "Note"}, Rows: [][]string{{"hello", "hello table"}}},
			docx.Paragraph{Text: "Last hello"},
		},
	})
	Expect(err).ToNot(HaveOccurred())
	return buf.Bytes()
}

func open() *Document {
	GinkgoHelper()
	d, err := Open(source())
	Expect(err).ToNot(HaveOccurred())
	return d
}

// finish writes the document, checks it with officecheck and returns the
// bytes and the text read back.
func finish(d *Document) ([]byte, string) {
	GinkgoHelper()
	b, err := d.Bytes()
	Expect(err).ToNot(HaveOccurred())
	Expect(officecheck.Check(b)).To(BeEmpty(), "officecheck problems")
	r, err := officeread.Read(b)
	Expect(err).ToNot(HaveOccurred())
	return b, r.Parts[0].Text
}

func texts(d *Document) []string {
	var out []string
	for _, p := range d.Paragraphs() {
		out = append(out, p.Text)
	}
	return out
}

func zipParts(b []byte) (names []string, data map[string][]byte) {
	GinkgoHelper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	Expect(err).ToNot(HaveOccurred())
	data = map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		Expect(err).ToNot(HaveOccurred())
		c, _ := io.ReadAll(rc)
		rc.Close()
		names = append(names, f.Name)
		data[f.Name] = c
	}
	return
}

func replacePart(b []byte, name string, content string) []byte {
	GinkgoHelper()
	names, data := zipParts(b)
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, n := range names {
		w, _ := zw.Create(n)
		if n == name {
			_, _ = w.Write([]byte(content))
		} else {
			_, _ = w.Write(data[n])
		}
	}
	_ = zw.Close()
	return out.Bytes()
}

var _ = Describe("docxedit", func() {
	It("opens a document and lists its top-level paragraphs", func() {
		src := source()
		d, err := Open(src)
		Expect(err).ToNot(HaveOccurred())
		ps := d.Paragraphs()
		want := []ParagraphInfo{
			{0, "Heading1", "Title"},
			{1, "", "Hello world, hello"},
			{2, "", "Bold lead plain tail"},
			{3, "ListBullet", "alpha"},
			{4, "ListBullet", "beta"},
			{5, "", ""},
			{6, "", "Last hello"},
		}
		Expect(ps).To(Equal(want))
		r, err := officeread.Read(src)
		Expect(err).ToNot(HaveOccurred())
		for _, s := range []string{"# Title", "Hello world, hello", "- alpha", "hello\thello table", "Last hello"} {
			Expect(r.Parts[0].Text).To(ContainSubstring(s), "source text lacks %q", s)
		}
	})

	It("keeps every part when nothing is edited", func() {
		src := source()
		d, _ := Open(src)
		b, _ := finish(d)
		n1, d1 := zipParts(src)
		n2, d2 := zipParts(b)
		Expect(n2).To(Equal(n1), "entry order changed")
		for _, n := range n1 {
			Expect(bytes.Equal(d1[n], d2[n])).To(BeTrue(), "part %s changed", n)
		}
	})

	It("replaces paragraph text and keeps style, run properties and list properties", func() {
		d := open()
		Expect(d.SetText(0, "New <title> & more")).To(Succeed())
		Expect(d.SetText(2, "Rewritten")).To(Succeed())
		b, text := finish(d)
		Expect(text).To(ContainSubstring("# New <title> & more"), "heading not replaced (style lost?)")
		Expect(text).To(ContainSubstring("Rewritten"))
		Expect(text).ToNot(ContainSubstring("Bold lead"))
		Expect(text).ToNot(ContainSubstring("plain tail"))
		Expect(text).To(ContainSubstring("Hello world, hello"), "other paragraphs changed")
		Expect(text).To(ContainSubstring("- alpha"), "other paragraphs changed")
		d2, err := Open(b)
		Expect(err).ToNot(HaveOccurred())
		ps := d2.Paragraphs()
		Expect(ps[0].Style).To(Equal("Heading1"))
		Expect(ps[2].Text).To(Equal("Rewritten"))
		// The first run's run properties (bold) are kept.
		_, data := zipParts(b)
		Expect(string(data["word/document.xml"])).To(ContainSubstring(`<w:r><w:rPr><w:b/></w:rPr><w:t xml:space="preserve">Rewritten</w:t></w:r>`), "run properties lost")
		// List numbering properties are kept.
		d3 := open()
		Expect(d3.SetText(3, "first item")).To(Succeed())
		_, text = finish(d3)
		Expect(text).To(ContainSubstring("- first item"), "list paragraph lost its list properties")
	})

	It("inserts paragraphs with a style at the start, the middle and the end", func() {
		d := open()
		Expect(d.InsertParagraph(1, "Heading2", "Inserted <h2>")).To(Succeed())
		Expect(d.InsertParagraph(-1, "", "At the start")).To(Succeed())
		Expect(d.InsertParagraph(len(d.Paragraphs())-1, "ListBullet", "At the end")).To(Succeed())
		b, text := finish(d)
		lines := strings.Split(text, "\n")
		Expect(lines[:4]).To(Equal([]string{"At the start", "# Title", "Hello world, hello", "## Inserted <h2>"}), "unexpected order:\n%s", text)
		Expect(lines[len(lines)-1]).To(Equal("- At the end"), "last line wrong:\n%s", text)
		d2, _ := Open(b)
		Expect(d2.Paragraphs()[3].Style).To(Equal("Heading2"), "style not set")
	})

	It("refuses to insert a paragraph with an undefined style", func() {
		d := open()
		err := d.InsertParagraph(0, "NoSuchStyle", "x")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("NoSuchStyle"))
		// A table style id is not a paragraph style.
		Expect(d.InsertParagraph(0, "TableGrid", "x")).ToNot(Succeed(), "table style accepted as paragraph style")
		Expect(d.Paragraphs()).To(HaveLen(7), "a failed insert changed the document")
	})

	It("deletes paragraphs", func() {
		d := open()
		Expect(d.DeleteParagraph(1)).To(Succeed())
		Expect(d.DeleteParagraph(2)).To(Succeed()) // was "alpha"
		_, text := finish(d)
		Expect(text).ToNot(ContainSubstring("Hello world"), "paragraphs still there")
		Expect(text).ToNot(ContainSubstring("alpha"), "paragraphs still there")
		Expect(text).To(ContainSubstring("- beta"), "other content lost")
		Expect(text).To(ContainSubstring("hello\thello table"), "other content lost")
	})

	It("keeps a paragraph after the last table", func() {
		// Paragraph 5 sits between the table and paragraph 6, so it can go.
		d := open()
		Expect(d.DeleteParagraph(5)).To(Succeed())
		finish(d)
		// Without paragraph 6 the table would end the body.
		d = open()
		Expect(d.DeleteParagraph(6)).To(Succeed())
		Expect(d.DeleteParagraph(5)).ToNot(Succeed(), "deleted the paragraph after the last table")
		finish(d)
	})

	It("replaces text in paragraphs but not in table cells", func() {
		d := open()
		n, err := d.ReplaceAll("hello", "a<b & c")
		Expect(err).ToNot(HaveOccurred())
		Expect(n).To(Equal(2), "table cells are not searched")
		_, text := finish(d)
		Expect(text).To(ContainSubstring("Hello world, a<b & c"), "paragraphs not replaced")
		Expect(text).To(ContainSubstring("Last a<b & c"), "paragraphs not replaced")
		Expect(text).To(ContainSubstring("hello\thello table"), "table cell changed")
		Expect(text).To(ContainSubstring("Bold lead plain tail"), "untouched paragraph changed")
	})

	It("does not match text across runs and rejects empty search text", func() {
		d := open()
		n, err := d.ReplaceAll("lead plain", "x") // spans two runs
		Expect(err).ToNot(HaveOccurred())
		Expect(n).To(BeZero())
		n, err = d.ReplaceAll("lead", "head") // inside the bold run
		Expect(err).ToNot(HaveOccurred())
		Expect(n).To(Equal(1))
		_, text := finish(d)
		Expect(text).To(ContainSubstring("Bold head plain tail"))
		_, err = d.ReplaceAll("", "x")
		Expect(err).To(HaveOccurred(), "empty search text accepted")
	})

	It("keeps edge spaces of replaced text", func() {
		// A w:t without xml:space gets it when the new text has edge spaces.
		src := source()
		_, data := zipParts(src)
		doc := strings.Replace(string(data["word/document.xml"]), `<w:t xml:space="preserve">Last hello</w:t>`, `<w:t>Last hello</w:t>`, 1)
		d, err := Open(replacePart(src, "word/document.xml", doc))
		Expect(err).ToNot(HaveOccurred())
		n, err := d.ReplaceAll("hello", " hi ")
		Expect(err).ToNot(HaveOccurred())
		Expect(n).To(BeNumerically(">=", 1))
		b, _ := finish(d)
		d2, _ := Open(b)
		ps := d2.Paragraphs()
		Expect(ps[6].Text).To(Equal("Last  hi "))
	})

	It("changes only the document part and only the edited paragraph", func() {
		src := source()
		d, _ := Open(src)
		Expect(d.SetText(1, "changed")).To(Succeed())
		b, _ := finish(d)
		n1, d1 := zipParts(src)
		n2, d2 := zipParts(b)
		Expect(n2).To(Equal(n1), "entry order changed")
		for _, n := range n1 {
			same := bytes.Equal(d1[n], d2[n])
			if n == "word/document.xml" {
				Expect(same).To(BeFalse(), "document.xml did not change")
			} else {
				Expect(same).To(BeTrue(), "part %s changed", n)
			}
		}
		// Bytes outside the edited paragraph are untouched.
		old, cur := string(d1["word/document.xml"]), string(d2["word/document.xml"])
		i := strings.Index(old, "<w:p><w:r><w:t xml:space=\"preserve\">Hello")
		Expect(i).To(BeNumerically(">=", 0), "prefix of document.xml changed")
		Expect(cur).To(HavePrefix(old[:i]), "prefix of document.xml changed")
		j := strings.Index(old, "<w:p><w:r><w:rPr><w:b/>")
		Expect(cur).To(HaveSuffix(old[j:]), "suffix of document.xml changed")
	})

	It("composes several edits", func() {
		d := open()
		Expect(d.InsertParagraph(0, "Heading2", "Section")).To(Succeed())
		Expect(d.SetText(2, "Second")).To(Succeed())
		Expect(d.DeleteParagraph(3)).To(Succeed())
		_, err := d.ReplaceAll("Last", "Final")
		Expect(err).ToNot(HaveOccurred())
		_, text := finish(d)
		Expect(text).To(HavePrefix("# Title\n## Section\nSecond\n"), text)
		Expect(text).To(ContainSubstring("Final hello"), text)
		Expect(text).ToNot(ContainSubstring("Bold lead"), "deleted paragraph still there")
	})

	It("rejects out-of-range edits and edits of an empty paragraph without changing the document", func() {
		d := open()
		for _, i := range []int{-1, 7, 100} {
			Expect(d.SetText(i, "x")).ToNot(Succeed(), "SetText(%d)", i)
			Expect(d.DeleteParagraph(i)).ToNot(Succeed(), "DeleteParagraph(%d)", i)
		}
		for _, i := range []int{-2, 7} {
			Expect(d.InsertParagraph(i, "", "x")).ToNot(Succeed(), "InsertParagraph(%d)", i)
		}
		// Paragraph 5 is empty: it has no text run.
		err := d.SetText(5, "x")
		Expect(err).To(MatchError(ContainSubstring("no text run")))
		Expect(d.Paragraphs()).To(HaveLen(7), "failed edits changed the document")
		finish(d)
	})

	It("fails to open a package that is damaged or incomplete", func() {
		src := source()
		bad := [][]byte{
			nil,
			[]byte("not a zip"),
			replacePart(src, "_rels/.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"></Relationships>`),
			replacePart(src, "_rels/.rels", `<Relationships`),
			replacePart(src, "word/document.xml", `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p>`),
			replacePart(src, "word/document.xml", `<html/>`),
			replacePart(src, "word/document.xml", `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"/>`),
			replacePart(src, "word/styles.xml", `<w:styles`),
		}
		// A zip with no main part.
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create("_rels/.rels")
		_, data := zipParts(src)
		_, _ = w.Write(data["_rels/.rels"])
		_ = zw.Close()
		bad = append(bad, buf.Bytes())
		for i, b := range bad {
			d, err := Open(b)
			Expect(err).To(HaveOccurred(), "case %d: Open succeeded", i)
			Expect(d).To(BeNil(), "case %d: Open succeeded", i)
		}
	})

	It("does not list the paragraphs inside tables", func() {
		d := open()
		for _, s := range texts(d) {
			Expect(s).ToNot(ContainSubstring("table"), "table text listed: %q", s)
			Expect(s).ToNot(Equal("Name"), "table text listed: %q", s)
		}
	})

	It("accepts only the default style when the document has no styles part", func() {
		src := source()
		_, data := zipParts(src)
		// Drop the relationship to the styles part: no style is defined.
		rels := strings.Replace(string(data["word/_rels/document.xml.rels"]), `Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles"`, `Type="urn:other"`, 1)
		d, err := Open(replacePart(src, "word/_rels/document.xml.rels", rels))
		Expect(err).ToNot(HaveOccurred())
		Expect(d.InsertParagraph(0, "Heading1", "x")).ToNot(Succeed(), "style accepted without a styles part")
		Expect(d.InsertParagraph(0, "", "x")).To(Succeed())
	})
})
