package officecheck

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/addons/office/internal/deck"
	"github.com/mudler/LocalAI/addons/office/internal/deck/pptx"
	"github.com/mudler/LocalAI/addons/office/internal/docx"
	"github.com/mudler/LocalAI/addons/office/internal/xlsx"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func goodXLSX() []byte {
	GinkgoHelper()
	var buf bytes.Buffer
	err := xlsx.Write(&buf, xlsx.Workbook{
		Title:       "Budget",
		HeaderStyle: xlsx.Style{Bold: true, FillColor: "DDDDDD"},
		Sheets: []xlsx.Sheet{{
			Name:      "Q1",
			HeaderRow: 1,
			Columns:   []xlsx.Column{{Width: 20}, {NumberFormat: "0.00"}},
			Cells: []xlsx.Cell{
				{Ref: "A1", Value: xlsx.Str("Item")},
				{Ref: "B1", Value: xlsx.Str("Cost")},
				{Ref: "A2", Value: xlsx.Str("Rent")},
				{Ref: "B2", Value: xlsx.Num(1200.5)},
				{Ref: "B3", Formula: "SUM(B2:B2)", Value: xlsx.Num(1200.5)},
			},
		}, {Name: "Notes", Cells: []xlsx.Cell{{Ref: "A1", Value: xlsx.Boolean(true)}}}},
	})
	Expect(err).NotTo(HaveOccurred())
	return buf.Bytes()
}

func goodDOCX() []byte {
	GinkgoHelper()
	var buf bytes.Buffer
	err := docx.Write(&buf, docx.Document{
		Title: "Report",
		Blocks: []docx.Block{
			docx.Heading{Level: 1, Text: "Summary"},
			docx.Paragraph{Text: "Hello"},
			docx.Bullets{Items: []string{"one", "two"}},
			docx.Table{Header: []string{"A", "B"}, Rows: [][]string{{"1", "2"}}},
		},
	})
	Expect(err).NotTo(HaveOccurred())
	return buf.Bytes()
}

func goodPPTX() []byte {
	GinkgoHelper()
	slides := []deck.StudioSlide{
		{ID: "s1", Title: "Intro", Layout: deck.LayoutTitle, Status: deck.SlideOK, Notes: "say hi"},
		{ID: "s2", Title: "Points", Layout: deck.LayoutText, Status: deck.SlideOK, Bullets: []deck.StudioBullet{{Text: "One"}, {Text: "Two"}}},
	}
	var buf bytes.Buffer
	Expect(pptx.Write(&buf, slides, pptx.Options{Title: "Deck", Notes: true}, nil)).To(Succeed())
	return buf.Bytes()
}

// rewrite returns a copy of zipped with one entry changed. fn gets the entry
// content and returns the new content; a nil result drops the entry.
func rewrite(zipped []byte, name string, fn func([]byte) []byte) []byte {
	GinkgoHelper()
	zr, err := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
	Expect(err).NotTo(HaveOccurred())
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	found := false
	for _, f := range zr.File {
		rc, err := f.Open()
		Expect(err).NotTo(HaveOccurred())
		var b bytes.Buffer
		_, err = b.ReadFrom(rc)
		Expect(err).NotTo(HaveOccurred())
		rc.Close()
		data := b.Bytes()
		if f.Name == name {
			found = true
			if data = fn(data); data == nil {
				continue
			}
		}
		w, err := zw.Create(f.Name)
		Expect(err).NotTo(HaveOccurred())
		w.Write(data)
	}
	Expect(found).To(BeTrue(), "entry %s not in the package", name)
	Expect(zw.Close()).To(Succeed())
	return out.Bytes()
}

// replace returns an fn that swaps old for new once and fails when old is absent.
func replace(old, new string) func([]byte) []byte {
	return func(b []byte) []byte {
		Expect(string(b)).To(ContainSubstring(old), "not found in part")
		return []byte(strings.Replace(string(b), old, new, 1))
	}
}

func drop([]byte) []byte { return nil }

func wantProblem(data []byte, part, substr string) {
	GinkgoHelper()
	ps := Check(data)
	for _, p := range ps {
		if p.Part == part && strings.Contains(p.Message, substr) {
			return
		}
	}
	Fail(fmt.Sprintf("want a problem in %q containing %q, got %v", part, substr, ps))
}

func renameEntry(zipped []byte, from, to string) []byte {
	GinkgoHelper()
	zr, _ := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		rc, _ := f.Open()
		var b bytes.Buffer
		b.ReadFrom(rc)
		rc.Close()
		name := f.Name
		if name == from {
			name = to
		}
		w, _ := zw.Create(name)
		w.Write(b.Bytes())
	}
	zw.Close()
	return out.Bytes()
}

var _ = Describe("Check", func() {
	It("finds no problem in files from the writers", func() {
		for name, data := range map[string][]byte{"xlsx": goodXLSX(), "docx": goodDOCX(), "pptx": goodPPTX()} {
			Expect(Check(data)).To(BeEmpty(), name)
		}
	})

	It("reports input that is not a zip", func() {
		for _, in := range [][]byte{nil, []byte("hello"), goodDOCX()[:100]} {
			ps := Check(in)
			Expect(ps).To(HaveLen(1))
			Expect(ps[0].Message).To(ContainSubstring("zip"))
		}
	})

	It("reports an unknown format", func() {
		data := rewrite(goodDOCX(), "[Content_Types].xml", replace("wordprocessingml.document.main+xml", "text/plain"))
		wantProblem(data, "[Content_Types].xml", "format not recognised")
	})

	It("reports bad zip entry names", func() {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create("../evil.xml")
		w.Write([]byte("<a/>"))
		zw.Close()
		wantProblem(buf.Bytes(), "../evil.xml", "absolute or contains ..")

		// Also in an otherwise good package.
		good := goodDOCX()
		zr, _ := zip.NewReader(bytes.NewReader(good), int64(len(good)))
		var out bytes.Buffer
		zw = zip.NewWriter(&out)
		for _, f := range zr.File {
			rc, _ := f.Open()
			var b bytes.Buffer
			b.ReadFrom(rc)
			rc.Close()
			w, _ := zw.Create(f.Name)
			w.Write(b.Bytes())
		}
		w, _ = zw.Create("word/../../x.xml")
		w.Write([]byte("<a/>"))
		zw.Close()
		wantProblem(out.Bytes(), "word/../../x.xml", "absolute or contains ..")
	})

	It("reports a missing content type", func() {
		data := rewrite(goodDOCX(), "[Content_Types].xml", replace(`<Override PartName="/word/document.xml"`, `<Override PartName="/word/other.xml"`))
		wantProblem(data, "[Content_Types].xml", "word/other.xml")

		// A part with an extension that has no Default.
		data = rewrite(goodXLSX(), "[Content_Types].xml", replace(`<Override PartName="/xl/sharedStrings.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"/>`, ""))
		// sharedStrings.xml still has the Default xml type, so it is fine.
		Expect(Check(data)).To(BeEmpty(), "a part covered by a Default type must pass")
		data = renameEntry(goodDOCX(), "word/styles.xml", "word/styles.bin")
		wantProblem(data, "word/styles.bin", "no content type")
	})

	It("reports a broken relationship target", func() {
		data := rewrite(goodXLSX(), "xl/_rels/workbook.xml.rels", replace(`Target="styles.xml"`, `Target="nostyles.xml"`))
		wantProblem(data, "xl/_rels/workbook.xml.rels", "xl/nostyles.xml")

		data = rewrite(goodDOCX(), "_rels/.rels", replace(`Target="word/document.xml"`, `Target="word/missing.xml"`))
		wantProblem(data, "_rels/.rels", "word/missing.xml")
		wantProblem(data, "_rels/.rels", "no officeDocument relationship points at word/document.xml")

		data = rewrite(goodDOCX(), "_rels/.rels", replace(`Target="word/document.xml"`, `Target="../../escape.xml"`))
		wantProblem(data, "_rels/.rels", "leaves the package")
	})

	It("resolves relationships relative to the owner folder", func() {
		// "../docProps/core.xml" from xl/ is valid; the checker must not report it.
		data := rewrite(goodXLSX(), "xl/_rels/workbook.xml.rels", replace(`Target="styles.xml"`, `Target="../xl/styles.xml"`))
		Expect(Check(data)).To(BeEmpty())
		// An external target is not looked up.
		data = rewrite(goodXLSX(), "xl/_rels/workbook.xml.rels", replace(`Target="styles.xml"`, `Target="https://example.com/x" TargetMode="External"`))
		wantProblem(rewrite(data, "xl/styles.xml", drop), "xl/styles.xml", "required part is missing")
	})

	It("reports malformed XML", func() {
		data := rewrite(goodDOCX(), "word/styles.xml", func(b []byte) []byte { return append(b[:len(b)-5:len(b)-5], []byte("<<")...) })
		wantProblem(data, "word/styles.xml", "not well-formed")
		data = rewrite(goodDOCX(), "[Content_Types].xml", func(b []byte) []byte { return []byte("<Types>") })
		wantProblem(data, "[Content_Types].xml", "not well-formed")
		data = rewrite(goodDOCX(), "_rels/.rels", func(b []byte) []byte { return []byte("not xml <") })
		wantProblem(data, "_rels/.rels", "not well-formed")
	})

	It("reports missing required parts", func() {
		wantProblem(rewrite(goodPPTX(), "ppt/presentation.xml", drop), "ppt/presentation.xml", "required part is missing")
		wantProblem(rewrite(goodDOCX(), "word/document.xml", drop), "word/document.xml", "required part is missing")
		wantProblem(rewrite(goodXLSX(), "xl/workbook.xml", drop), "xl/workbook.xml", "required part is missing")
		wantProblem(rewrite(goodXLSX(), "xl/styles.xml", drop), "xl/styles.xml", "required part is missing")
		data := rewrite(goodXLSX(), "xl/worksheets/sheet1.xml", drop)
		data = rewrite(data, "xl/worksheets/sheet2.xml", drop)
		wantProblem(data, "xl/worksheets", "no xl/worksheets/sheet*.xml")
	})

	It("checks xlsx cells", func() {
		const sheet = "xl/worksheets/sheet1.xml"
		wantProblem(rewrite(goodXLSX(), sheet, replace(`<f>SUM(B2:B2)</f>`, `<f></f>`)), sheet, "<f> is empty")
		wantProblem(rewrite(goodXLSX(), sheet, replace(`<c r="B2"`, `<c r="B2" s="99"`)), sheet, "style index")
		wantProblem(rewrite(goodXLSX(), sheet, replace(`<c r="B2"`, `<c r="a0"`)), sheet, `"a0"`)
		wantProblem(rewrite(goodXLSX(), sheet, replace(`<c r="B2"`, `<c r="XFE1"`)), sheet, `"XFE1"`)
		wantProblem(rewrite(goodXLSX(), sheet, replace(`<c r="B2"`, `<c r="A1048577"`)), sheet, `"A1048577"`)
		wantProblem(rewrite(goodXLSX(), sheet, replace(`<c r="B2"`, `<c r="A2"`)), sheet, "duplicate cell A2")
		for _, ok := range []string{"XFD1", "A1048576"} {
			Expect(Check(rewrite(goodXLSX(), sheet, replace(`<c r="B2"`, `<c r="`+ok+`"`)))).To(BeEmpty(), "%s is valid", ok)
		}
	})

	It("checks xlsx sheet names", func() {
		long := strings.Repeat("x", 32)
		wantProblem(rewrite(goodXLSX(), "xl/workbook.xml", replace(`name="Q1"`, `name="`+long+`"`)), "xl/workbook.xml", "limit is 31")
		wantProblem(rewrite(goodXLSX(), "xl/workbook.xml", replace(`name="Q1"`, `name="a/b"`)), "xl/workbook.xml", "contains one of")
		Expect(Check(rewrite(goodXLSX(), "xl/workbook.xml", replace(`name="Q1"`, `name="`+strings.Repeat("é", 31)+`"`)))).To(BeEmpty(), "31 runes is valid")
	})

	It("checks the docx body and numbering", func() {
		const doc = "word/document.xml"
		wantProblem(rewrite(goodDOCX(), doc, replace(`<w:numId w:val="1"/>`, `<w:numId w:val="77"/>`)), doc, "numId 77")
		empty := func([]byte) []byte {
			return []byte(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body/></w:document>`)
		}
		wantProblem(rewrite(goodDOCX(), doc, empty), doc, "no paragraph or table")
	})

	It("checks the pptx slide list", func() {
		const pres = "ppt/presentation.xml"
		wantProblem(rewrite(goodPPTX(), "ppt/slides/slide2.xml", drop), pres, "2 sldId entries but 1 slide parts")
		wantProblem(rewrite(goodPPTX(), pres, replace(`<p:sldId id="257" r:id="rId`, `<p:sldId id="257" r:id="rIdNope`)), pres, "has no relationship")
		data := rewrite(goodPPTX(), "ppt/_rels/presentation.xml.rels", replace(`slides/slide2.xml`, `slides/slide9.xml`))
		wantProblem(data, pres, "ppt/slides/slide9.xml")
	})

	It("never panics", func() {
		for _, data := range [][]byte{goodXLSX(), goodDOCX(), goodPPTX()} {
			for _, n := range []int{0, 1, 30, 200, len(data) / 2, len(data) - 1} {
				Check(data[:n])
			}
		}
		// Every part replaced by junk, one at a time.
		data := goodXLSX()
		zr, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		for _, f := range zr.File {
			junk := rewrite(data, f.Name, func([]byte) []byte { return []byte("\x00<\xff") })
			Check(junk)
		}
	})
})
