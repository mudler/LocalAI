package officeread

import (
	"archive/zip"
	"bytes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/office/internal/deck"
	"github.com/mudler/LocalAI/addons/office/internal/deck/pptx"
	"github.com/mudler/LocalAI/addons/office/internal/docx"
	"github.com/mudler/LocalAI/addons/office/internal/xlsx"
)

func sampleXLSX() []byte {
	GinkgoHelper()
	var buf bytes.Buffer
	err := xlsx.Write(&buf, xlsx.Workbook{Sheets: []xlsx.Sheet{
		{Name: "Sales", Cells: []xlsx.Cell{
			{Ref: "A1", Value: xlsx.Str("Item")}, {Ref: "B1", Value: xlsx.Str("Qty")},
			{Ref: "A2", Value: xlsx.Str("Apples & pears")}, {Ref: "B2", Value: xlsx.Num(3.5)},
			{Ref: "A3", Value: xlsx.Boolean(true)}, {Ref: "B3", Value: xlsx.Boolean(false)},
			{Ref: "A4", Value: xlsx.Str("Double")}, {Ref: "B4", Formula: "=B2*2", Value: xlsx.Num(7)},
			{Ref: "D6", Value: xlsx.Str("far")},
		}},
		{Name: "Notes", Cells: []xlsx.Cell{{Ref: "A1", Value: xlsx.Str("second sheet")}}},
	}})
	Expect(err).ToNot(HaveOccurred())
	return buf.Bytes()
}

func samplePPTX() []byte {
	GinkgoHelper()
	slides := []deck.StudioSlide{
		{ID: "s1", Title: "Q3 review", Sub: "Partners", Layout: deck.LayoutTitle, Status: deck.SlideOK},
		{ID: "s2", Title: "What we heard", Layout: deck.LayoutText, Status: deck.SlideOK,
			Notes:   "Say this first.\nThen pause.",
			Bullets: []deck.StudioBullet{{Text: "Revenue grew"}, {Text: "Churn is low"}}},
	}
	var buf bytes.Buffer
	Expect(pptx.Write(&buf, slides, pptx.Options{Title: "Q3", Notes: true}, nil)).To(Succeed())
	return buf.Bytes()
}

// rewrite returns a copy of a zip with fn applied to each entry. fn returns
// the new content, or nil to drop the entry.
func rewrite(data []byte, fn func(name string, b []byte) []byte) []byte {
	GinkgoHelper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	Expect(err).ToNot(HaveOccurred())
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		rc, _ := f.Open()
		var b bytes.Buffer
		b.ReadFrom(rc)
		rc.Close()
		nb := fn(f.Name, b.Bytes())
		if nb == nil {
			continue
		}
		w, _ := zw.Create(f.Name)
		w.Write(nb)
	}
	zw.Close()
	return out.Bytes()
}

func addEntry(data []byte, name, content string) []byte {
	GinkgoHelper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	Expect(err).ToNot(HaveOccurred())
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		rc, _ := f.Open()
		var b bytes.Buffer
		b.ReadFrom(rc)
		rc.Close()
		w, _ := zw.Create(f.Name)
		w.Write(b.Bytes())
	}
	w, _ := zw.Create(name)
	w.Write([]byte(content))
	zw.Close()
	return out.Bytes()
}

func makeZip(entries map[string]string) []byte {
	GinkgoHelper()
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for n, c := range entries {
		w, err := zw.Create(n)
		Expect(err).ToNot(HaveOccurred())
		w.Write([]byte(c))
	}
	zw.Close()
	return out.Bytes()
}

var _ = Describe("officeread", func() {
	It("reads an XLSX round trip", func() {
		doc, err := Read(sampleXLSX())
		Expect(err).ToNot(HaveOccurred())
		Expect(doc.Format).To(Equal("xlsx"))
		Expect(doc.Parts).To(HaveLen(2))
		s := doc.Parts[0]
		Expect(s.Title).To(Equal("Sales"))
		Expect(doc.Parts[1].Title).To(Equal("Notes"))
		want := [][]string{
			{"Item", "Qty", "", ""},
			{"Apples & pears", "3.5", "", ""},
			{"TRUE", "FALSE", "", ""},
			{"Double", "7", "", ""},
			{"", "", "", ""},
			{"", "", "", "far"},
		}
		Expect(s.Tables).To(HaveLen(1))
		Expect(s.Tables[0]).To(Equal(want))
		wantText := "Item\tQty\nApples & pears\t3.5\nTRUE\tFALSE\nDouble\t7\n\n\t\t\tfar"
		Expect(s.Text).To(Equal(wantText))
		Expect(s.Text).ToNot(ContainSubstring("B2*2"), "formula text leaked")
		Expect(s.Text).ToNot(ContainSubstring("="), "formula text leaked")
		Expect(doc.Parts[1].Text).To(Equal("second sheet"))
	})

	It("reads a DOCX round trip", func() {
		var buf bytes.Buffer
		err := docx.Write(&buf, docx.Document{Title: "T", Blocks: []docx.Block{
			docx.Heading{Level: 1, Text: "Plan"},
			docx.Paragraph{Text: "Intro line."},
			docx.Heading{Level: 2, Text: "Steps"},
			docx.Bullets{Items: []string{"one", "two"}},
			docx.Paragraph{Runs: []docx.Run{{Text: "bold ", Bold: true}, {Text: "plain"}}},
			docx.Heading{Level: 3, Text: "Data"},
			docx.Table{Header: []string{"Name", "Score"}, Rows: [][]string{{"a", "1"}, {"b", "2"}}},
			docx.Paragraph{Text: "After."},
		}})
		Expect(err).ToNot(HaveOccurred())
		doc, err := Read(buf.Bytes())
		Expect(err).ToNot(HaveOccurred())
		Expect(doc.Format).To(Equal("docx"))
		Expect(doc.Parts).To(HaveLen(1))
		Expect(doc.Parts[0].Title).To(Equal("Document"))
		want := "# Plan\nIntro line.\n## Steps\n- one\n- two\nbold plain\n### Data\nName\tScore\na\t1\nb\t2\n\nAfter."
		Expect(doc.Parts[0].Text).To(Equal(want))
		wantTable := [][][]string{{{"Name", "Score"}, {"a", "1"}, {"b", "2"}}}
		Expect(doc.Parts[0].Tables).To(Equal(wantTable))
	})

	It("reads a PPTX round trip", func() {
		doc, err := Read(samplePPTX())
		Expect(err).ToNot(HaveOccurred())
		Expect(doc.Format).To(Equal("pptx"))
		Expect(doc.Parts).To(HaveLen(2))
		Expect(doc.Parts[0].Title).To(Equal("Slide 1"))
		Expect(doc.Parts[1].Title).To(Equal("Slide 2"))
		for _, want := range []string{"Q3 review", "Partners"} {
			Expect(doc.Parts[0].Text).To(ContainSubstring(want), "slide 1")
		}
		Expect(doc.Parts[0].Text).ToNot(ContainSubstring("Notes:"), "slide 1 has no notes")
		s2 := doc.Parts[1].Text
		for _, want := range []string{"What we heard", "Revenue grew\n", "Churn is low", "\nNotes:\nSay this first.\nThen pause."} {
			Expect(s2).To(ContainSubstring(want), "slide 2")
		}
		Expect(s2).To(HaveSuffix("Notes:\nSay this first.\nThen pause."), "notes must come last")
	})

	It("reads XLSX shared strings and inline strings", func() {
		sheet := `<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
			`<row r="1"><c r="A1" t="s"><v>1</v></c><c r="B1" t="s"><v>0</v></c><c r="C1" t="inlineStr"><is><t>in</t><r><t>line</t></r></is></c></row>` +
			`<row r="2"><c r="A2" t="str"><f>A1&amp;B1</f><v>cached</v></c><c r="B2" t="b"><v>1</v></c><c r="C2"><v>42</v></c></row>` +
			`</sheetData></worksheet>`
		sst := `<?xml version="1.0"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
			`<si><t>zero</t></si><si><r><t>on</t></r><r><t xml:space="preserve">e </t></r><rPh><t>ignored</t></rPh></si></sst>`
		data := rewrite(sampleXLSX(), func(name string, b []byte) []byte {
			switch name {
			case "xl/worksheets/sheet1.xml":
				return []byte(sheet)
			case "xl/sharedStrings.xml":
				return []byte(sst)
			}
			return b
		})
		doc, err := Read(data)
		Expect(err).ToNot(HaveOccurred())
		want := [][]string{{"on" + "e ", "zero", "inline"}, {"cached", "TRUE", "42"}}
		Expect(doc.Parts[0].Tables[0]).To(Equal(want))
	})

	It("returns errors for bad input", func() {
		good := sampleXLSX()
		cases := map[string][]byte{
			"not a zip":        []byte("hello"),
			"empty":            nil,
			"no content types": makeZip(map[string]string{"a.txt": "x"}),
			"unrecognised format": makeZip(map[string]string{
				contentTypesPart: `<Types xmlns="x"><Override PartName="/a.xml" ContentType="text/plain"/></Types>`,
				"a.xml":          "<a/>",
			}),
			"missing workbook": rewrite(good, func(n string, b []byte) []byte {
				if n == "xl/workbook.xml" {
					return nil
				}
				return b
			}),
			"malformed sheet": rewrite(good, func(n string, b []byte) []byte {
				if n == "xl/worksheets/sheet1.xml" {
					return []byte("<worksheet><sheetData><row>")
				}
				return b
			}),
			"malformed content types": rewrite(good, func(n string, b []byte) []byte {
				if n == contentTypesPart {
					return []byte("<Types><x></Types>")
				}
				return b
			}),
			"dotdot entry":   addEntry(good, "../evil.xml", "<a/>"),
			"absolute entry": addEntry(good, "/evil.xml", "<a/>"),
		}
		for name, data := range cases {
			_, err := Read(data)
			Expect(err).To(HaveOccurred(), "%s", name)
		}
		for _, f := range []struct{ name, part string }{{"docx", "word/document.xml"}, {"pptx", "ppt/presentation.xml"}} {
			var data []byte
			if f.name == "docx" {
				var buf bytes.Buffer
				docx.Write(&buf, docx.Document{Blocks: []docx.Block{docx.Paragraph{Text: "x"}}})
				data = buf.Bytes()
			} else {
				data = samplePPTX()
			}
			data = rewrite(data, func(n string, b []byte) []byte {
				if n == f.part {
					return nil
				}
				return b
			})
			_, err := Read(data)
			Expect(err).To(HaveOccurred(), "missing %s", f.part)
		}
	})

	It("never panics on truncated input", func() {
		var d bytes.Buffer
		docx.Write(&d, docx.Document{Blocks: []docx.Block{docx.Heading{Level: 1, Text: "h"}, docx.Bullets{Items: []string{"a"}}}})
		for name, good := range map[string][]byte{"xlsx": sampleXLSX(), "docx": d.Bytes(), "pptx": samplePPTX()} {
			step := len(good)/300 + 1
			for n := 0; n < len(good); n += step {
				_, err := Read(good[:n])
				Expect(err).To(HaveOccurred(), "%s truncated to %d bytes", name, n)
			}
			_, err := Read(good)
			Expect(err).ToNot(HaveOccurred(), "%s", name)
		}
	})

	It("enforces the size limits", func() {
		oldE, oldT := maxEntry, maxTotal
		DeferCleanup(func() { maxEntry, maxTotal = oldE, oldT })
		data := sampleXLSX()
		maxEntry = 100
		_, err := Read(data)
		Expect(err).To(MatchError(ContainSubstring("larger")), "entry limit")
		Expect(err).To(MatchError(ErrTooLarge), "entry limit")
		maxEntry, maxTotal = oldE, 500
		_, err = Read(data)
		Expect(err).To(MatchError(ContainSubstring("larger")), "total limit")
		Expect(err).To(MatchError(ErrTooLarge), "total limit")
	})

	It("rejects a sparse sheet that is too large", func() {
		sheet := `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
			`<row r="1048576"><c r="XFD1048576"><v>1</v></c></row></sheetData></worksheet>`
		data := rewrite(sampleXLSX(), func(n string, b []byte) []byte {
			if n == "xl/worksheets/sheet1.xml" {
				return []byte(sheet)
			}
			return b
		})
		_, err := Read(data)
		Expect(err).To(HaveOccurred(), "huge sparse sheet")
	})

	// A formula cell that was written without a cached result has no value in
	// the file. The reader shows the formula so a model can check what it wrote.
	It("shows a formula that has no cached value", func() {
		var buf bytes.Buffer
		err := xlsx.Write(&buf, xlsx.Workbook{Sheets: []xlsx.Sheet{{Name: "S", Cells: []xlsx.Cell{
			{Ref: "A1", Value: xlsx.Num(2)}, {Ref: "A2", Value: xlsx.Num(3)},
			{Ref: "A3", Formula: "=SUM(A1:A2)"},
			{Ref: "B3", Formula: "A1*2", Value: xlsx.Num(4)},
		}}}})
		Expect(err).ToNot(HaveOccurred())
		doc, err := Read(buf.Bytes())
		Expect(err).ToNot(HaveOccurred())
		grid := doc.Parts[0].Tables[0]
		Expect(grid[2][0]).To(Equal("=SUM(A1:A2)"), "A3 is the formula")
		Expect(grid[2][1]).To(Equal("4"), "B3 is the cached result")
	})
})
