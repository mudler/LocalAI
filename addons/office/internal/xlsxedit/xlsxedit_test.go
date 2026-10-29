package xlsxedit

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"

	"github.com/mudler/LocalAI/addons/office/internal/officecheck"
	"github.com/mudler/LocalAI/addons/office/internal/officeread"
	"github.com/mudler/LocalAI/addons/office/internal/xlsx"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func sourceBook() []byte {
	GinkgoHelper()
	wb := xlsx.Workbook{
		Title:       "Source",
		HeaderStyle: xlsx.Style{Bold: true, FillColor: "DDDDDD"},
		Sheets: []xlsx.Sheet{
			{
				Name:      "Data",
				HeaderRow: 1,
				Cells: []xlsx.Cell{
					{Ref: "A1", Value: xlsx.Str("Name")},
					{Ref: "B1", Value: xlsx.Str("Qty")},
					{Ref: "C1", Value: xlsx.Str("Flag")},
					{Ref: "A2", Value: xlsx.Str("apples")},
					{Ref: "B2", Value: xlsx.Num(3)},
					{Ref: "C2", Value: xlsx.Boolean(true)},
					{Ref: "A3", Value: xlsx.Str("total")},
					{Ref: "B3", Formula: "SUM(B2:B2)", Value: xlsx.Num(3)},
					{Ref: "C5", Value: xlsx.Str("tail")},
				},
			},
			{Name: "Notes", Cells: []xlsx.Cell{{Ref: "A1", Value: xlsx.Str("hello & <bye>")}}},
		},
	}
	var buf bytes.Buffer
	Expect(xlsx.Write(&buf, wb)).To(Succeed())
	return buf.Bytes()
}

func zipParts(data []byte) (names []string, parts map[string][]byte) {
	GinkgoHelper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	Expect(err).ToNot(HaveOccurred())
	parts = map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		Expect(err).ToNot(HaveOccurred())
		b, err := io.ReadAll(rc)
		rc.Close()
		Expect(err).ToNot(HaveOccurred())
		names = append(names, f.Name)
		parts[f.Name] = b
	}
	return names, parts
}

// mutate returns a copy of the package with one part replaced.
func mutate(data []byte, name string, fn func(string) string) []byte {
	GinkgoHelper()
	names, parts := zipParts(data)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		b := parts[n]
		if n == name {
			b = []byte(fn(string(b)))
		}
		w, err := zw.Create(n)
		Expect(err).ToNot(HaveOccurred())
		w.Write(b)
	}
	_ = zw.Close()
	return buf.Bytes()
}

func open(data []byte) *Workbook {
	GinkgoHelper()
	wb, err := Open(data)
	Expect(err).ToNot(HaveOccurred())
	return wb
}

// finish writes the workbook, checks the result and returns it.
func finish(wb *Workbook) []byte {
	GinkgoHelper()
	out, err := wb.Bytes()
	Expect(err).ToNot(HaveOccurred())
	Expect(officecheck.Check(out)).To(BeEmpty(), "officecheck problems")
	return out
}

func grids(data []byte) map[string][][]string {
	GinkgoHelper()
	doc, err := officeread.Read(data)
	Expect(err).ToNot(HaveOccurred())
	out := map[string][][]string{}
	for _, s := range doc.Parts {
		out[s.Title] = s.Tables[0]
	}
	return out
}

func nan() float64 { var z float64; return z / z }

func emptyZip() []byte {
	GinkgoHelper()
	var buf bytes.Buffer
	Expect(zip.NewWriter(&buf).Close()).To(Succeed())
	return buf.Bytes()
}

func docxLike() []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("word/document.xml")
	w.Write([]byte("<document/>"))
	zw.Close()
	return buf.Bytes()
}

type setErrCase struct {
	name, sheet, ref string
	v                Value
}

var setErrs = []setErrCase{
	{"empty ref", "Data", "", Str("x")},
	{"lower case ref", "Data", "a1", Str("x")},
	{"no row", "Data", "A", Str("x")},
	{"no column", "Data", "12", Str("x")},
	{"row zero", "Data", "A0", Str("x")},
	{"leading zero row", "Data", "A01", Str("x")},
	{"range", "Data", "A1:B2", Str("x")},
	{"column beyond XFD", "Data", "XFE1", Str("x")},
	{"column way beyond", "Data", "AAAAAAAAAAAA1", Str("x")},
	{"row beyond limit", "Data", "A1048577", Str("x")},
	{"row overflow", "Data", "A99999999999999999999", Str("x")},
	{"unknown sheet", "Nope", "A1", Str("x")},
	{"empty value", "Data", "A1", Value{}},
	{"empty formula", "Data", "A1", Formula("")},
	{"formula of only =", "Data", "A1", Formula(" = ")},
	{"nan", "Data", "A1", Num(nan())},
	{"control character", "Data", "A1", Str("a\x01b")},
	{"formula as cached value", "Data", "A1", FormulaCached("1", Formula("2"))},
}

var _ = Describe("xlsxedit", func() {
	It("opens a workbook, reads its sheets and writes it back unchanged", func() {
		src := sourceBook()
		wb := open(src)
		got := wb.Sheets()
		Expect(got).To(HaveLen(2))
		Expect(got[0].Name).To(Equal("Data"))
		Expect(got[0].Index).To(Equal(1))
		Expect(got[1].Name).To(Equal("Notes"))
		Expect(got[1].Index).To(Equal(2))
		g := grids(src)
		want := [][]string{
			{"Name", "Qty", "Flag"},
			{"apples", "3", "TRUE"},
			{"total", "3", ""},
			{"", "", ""},
			{"", "", "tail"},
		}
		Expect(g["Data"]).To(Equal(want))
		// A workbook with no edit is written back with the same parts.
		out := finish(wb)
		Expect(grids(out)).To(Equal(g), "grid changed without an edit")
		_, p0 := zipParts(src)
		_, p1 := zipParts(out)
		Expect(p1).To(Equal(p0), "parts changed without an edit")
	})

	DescribeTable("setting a cell of each kind",
		func(ref string, value Value, want string) {
			wb := open(sourceBook())
			Expect(wb.SetCell("Data", ref, value)).To(Succeed())
			g := grids(finish(wb))["Data"]
			col := int(ref[0] - 'A')
			Expect(g[1][col]).To(Equal(want), ref)
		},
		Entry("string", "A2", Str("pears <&> \"x\""), `pears <&> "x"`),
		Entry("string keeps spaces", "A2", Str("  padded  "), "  padded  "),
		Entry("number", "B2", Num(2.5), "2.5"),
		Entry("big number", "B2", Num(1e21), "1e+21"),
		Entry("bool false", "C2", Boolean(false), "FALSE"),
		Entry("bool true", "B2", Boolean(true), "TRUE"),
		Entry("formula no cache", "D2", Formula("=B2*2"), "=B2*2"),
		Entry("formula cached number", "D2", FormulaCached("B2*2", Num(6)), "6"),
		Entry("formula cached text", "D2", FormulaCached("A2&\"!\"", Str("apples!")), "apples!"),
		Entry("formula cached bool", "D2", FormulaCached("B2>1", Boolean(true)), "TRUE"),
	)

	It("stores a formula without a cached value", func() {
		wb := open(sourceBook())
		Expect(wb.SetCell("Data", "D2", Formula("=B2*2"))).To(Succeed())
		_, parts := zipParts(finish(wb))
		Expect(string(parts["xl/worksheets/sheet1.xml"])).To(ContainSubstring(`<c r="D2"><f>B2*2</f></c>`))
	})

	It("replaces an existing formula", func() {
		wb := open(sourceBook())
		Expect(wb.SetCell("Data", "B3", FormulaCached("SUM(B2:B2)*10", Num(30)))).To(Succeed())
		out := finish(wb)
		Expect(grids(out)["Data"][2][1]).To(Equal("30"))
		_, parts := zipParts(out)
		s := string(parts["xl/worksheets/sheet1.xml"])
		Expect(strings.Count(s, `r="B3"`)).To(Equal(1), "old formula still there: %s", s)
		Expect(s).ToNot(ContainSubstring("<f>SUM(B2:B2)</f>"))
	})

	It("keeps the style of a cell that is overwritten", func() {
		wb := open(sourceBook())
		Expect(wb.SetCell("Data", "A1", Str("Item"))).To(Succeed())
		_, parts := zipParts(finish(wb))
		Expect(string(parts["xl/worksheets/sheet1.xml"])).To(ContainSubstring(`<c r="A1" s="1" t="inlineStr">`), "header style lost")
	})

	DescribeTable("adding a cell where none exists",
		func(ref string, row, col int) {
			src := mutate(sourceBook(), "xl/worksheets/sheet1.xml", func(s string) string {
				// An empty row, and a row with a spans hint.
				s = strings.Replace(s, `<row r="5">`, `<row r="4"/><row r="5" spans="3:3">`, 1)
				return s
			})
			wb := open(src)
			Expect(wb.SetCell("Data", ref, Str("new"))).To(Succeed())
			g := grids(finish(wb))["Data"]
			Expect(g[row][col]).To(Equal("new"), "%s (grid %v)", ref, g)
		},
		Entry("empty self-closed row", "B4", 3, 1),
		Entry("before an existing cell", "A5", 4, 0),
		Entry("between existing cells", "B5", 4, 1),
		Entry("after the last cell", "E2", 1, 4),
		Entry("missing row between rows", "A10", 9, 0),
		Entry("new row before others", "A1", 0, 0),
	)

	It("keeps rows and cells sorted after many inserts", func() {
		wb := open(sourceBook())
		for _, ref := range []string{"C7", "A7", "B7", "A6", "Z1", "D2", "A4"} {
			Expect(wb.SetCell("Data", ref, Num(1))).To(Succeed())
		}
		_, parts := zipParts(finish(wb))
		sc, err := scanSheet(parts["xl/worksheets/sheet1.xml"])
		Expect(err).ToNot(HaveOccurred())
		prevRow := 0
		for _, r := range sc.rows {
			Expect(r.r).To(BeNumerically(">", prevRow), "rows not sorted")
			prevRow = r.r
			prevCol := 0
			for _, c := range r.cells {
				Expect(c.col).To(BeNumerically(">", prevCol), "row %d: cells not sorted", r.r)
				prevCol = c.col
			}
		}
	})

	It("grows the dimension of a sheet that is edited", func() {
		src := mutate(sourceBook(), "xl/worksheets/sheet1.xml", func(s string) string {
			return strings.Replace(s, "<sheetViews>", `<dimension ref="A1:C5"/><sheetViews>`, 1)
		})
		wb := open(src)
		Expect(wb.Sheets()[0].Dimension).To(Equal("A1:C5"))
		Expect(wb.SetCell("Data", "E9", Num(1))).To(Succeed())
		Expect(wb.SetCell("Data", "B2", Num(1))).To(Succeed())
		finish(wb)
		Expect(wb.Sheets()[0].Dimension).To(Equal("A1:E9"))
		Expect(wb.Sheets()[1].Dimension).To(BeEmpty(), "dimension of Notes")
	})

	It("clears cells and keeps the style and the neighbours", func() {
		wb := open(sourceBook())
		for _, ref := range []string{"B3", "A2", "A1", "D9"} {
			Expect(wb.ClearCell("Data", ref)).To(Succeed())
		}
		out := finish(wb)
		g := grids(out)["Data"]
		Expect(g[2][1]).To(BeEmpty(), "cells not cleared: %v", g)
		Expect(g[1][0]).To(BeEmpty(), "cells not cleared: %v", g)
		Expect(g[0][0]).To(BeEmpty(), "cells not cleared: %v", g)
		Expect(g[0][1]).To(Equal("Qty"), "neighbours changed: %v", g)
		Expect(g[1][1]).To(Equal("3"), "neighbours changed: %v", g)
		_, parts := zipParts(out)
		s := string(parts["xl/worksheets/sheet1.xml"])
		Expect(s).To(ContainSubstring(`<c r="A1" s="1"/>`), "styled cell was not kept")
		Expect(s).ToNot(ContainSubstring(`r="B3"`))
		Expect(s).ToNot(ContainSubstring("<f>"))
	})

	It("adds sheets that survive a round trip", func() {
		src := sourceBook()
		wb := open(src)
		Expect(wb.AddSheet("Summary")).To(Succeed())
		Expect(wb.SetCell("summary", "B2", Str("on the new sheet"))).To(Succeed())
		Expect(wb.AddSheet("Third")).To(Succeed())
		got := wb.Sheets()
		Expect(got).To(HaveLen(4))
		Expect(got[2].Name).To(Equal("Summary"))
		Expect(got[2].Index).To(Equal(3))
		Expect(got[3].Name).To(Equal("Third"))
		out := finish(wb)
		g := grids(out)
		Expect(g).To(HaveLen(4))
		Expect(g["Summary"]).To(Equal([][]string{{"", ""}, {"", "on the new sheet"}}))
		Expect(g["Third"]).To(BeEmpty())
		Expect(g["Data"][1][0]).To(Equal("apples"), "old sheet changed")
		// Reopen the result: the sheets survive a round trip.
		again := open(out)
		Expect(again.Sheets()).To(HaveLen(4))
		_, parts := zipParts(out)
		wbx := string(parts["xl/workbook.xml"])
		Expect(wbx).To(ContainSubstring(`name="Summary" sheetId="3"`))
		Expect(wbx).To(ContainSubstring(`name="Third" sheetId="4"`))
		Expect(string(parts["[Content_Types].xml"])).To(ContainSubstring(`PartName="/xl/worksheets/sheet3.xml"`), "content type missing")
		Expect(string(parts["xl/_rels/workbook.xml.rels"])).To(ContainSubstring(`Target="worksheets/sheet3.xml"`), "relationship missing")
	})

	It("gives a new sheet unique ids when the existing ones have gaps", func() {
		// Gaps in sheetId and rId, and a sheet part name that is already taken.
		src := mutate(sourceBook(), "xl/workbook.xml", func(s string) string {
			return strings.Replace(s, `sheetId="2"`, `sheetId="9"`, 1)
		})
		src = mutate(src, "xl/_rels/workbook.xml.rels", func(s string) string {
			return strings.Replace(s, `Id="rId3"`, `Id="rId40"`, 1)
		})
		wb := open(src)
		Expect(wb.AddSheet("New")).To(Succeed())
		_, parts := zipParts(finish(wb))
		Expect(string(parts["xl/workbook.xml"])).To(ContainSubstring(`name="New" sheetId="10"`))
		Expect(string(parts["xl/_rels/workbook.xml.rels"])).To(ContainSubstring(`Id="rId41"`))
	})

	It("asks for a full calculation on load", func() {
		wb := open(sourceBook())
		Expect(wb.SetCell("Data", "A2", Str("x"))).To(Succeed())
		_, parts := zipParts(finish(wb))
		Expect(string(parts["xl/workbook.xml"])).To(ContainSubstring(`<calcPr fullCalcOnLoad="1"/>`))

		// An existing calcPr keeps its other attributes.
		src := mutate(sourceBook(), "xl/workbook.xml", func(s string) string {
			return strings.Replace(s, "</workbook>", `<calcPr calcId="191029" fullCalcOnLoad="0"/></workbook>`, 1)
		})
		wb = open(src)
		Expect(wb.AddSheet("More")).To(Succeed())
		_, parts = zipParts(finish(wb))
		s := string(parts["xl/workbook.xml"])
		Expect(strings.Count(s, "calcPr")).To(Equal(1), s)
		Expect(s).To(ContainSubstring(`calcId="191029"`))
		Expect(s).To(ContainSubstring(`fullCalcOnLoad="1"`))
		Expect(s).ToNot(ContainSubstring(`fullCalcOnLoad="0"`))

		// calcPr goes before later elements.
		src = mutate(sourceBook(), "xl/workbook.xml", func(s string) string {
			return strings.Replace(s, "</workbook>", `<extLst/></workbook>`, 1)
		})
		wb = open(src)
		Expect(wb.SetCell("Data", "A2", Str("x"))).To(Succeed())
		_, parts = zipParts(finish(wb))
		Expect(string(parts["xl/workbook.xml"])).To(ContainSubstring(`<calcPr fullCalcOnLoad="1"/><extLst/>`))
	})

	It("leaves the parts that were not edited byte for byte identical", func() {
		src := sourceBook()
		names0, parts0 := zipParts(src)
		wb := open(src)
		Expect(wb.SetCell("Data", "A2", Str("changed"))).To(Succeed())
		out := finish(wb)
		names1, parts1 := zipParts(out)
		Expect(names1).To(Equal(names0), "entry order changed")
		changed := map[string]bool{"xl/worksheets/sheet1.xml": true, "xl/workbook.xml": true}
		for _, n := range names0 {
			same := bytes.Equal(parts0[n], parts1[n])
			if changed[n] {
				Expect(same).To(BeFalse(), "%s should have changed", n)
			} else {
				Expect(same).To(BeTrue(), "%s changed", n)
			}
		}
		// AddSheet changes the workbook parts, adds a part last, and keeps the rest.
		wb = open(src)
		Expect(wb.AddSheet("Extra")).To(Succeed())
		names2, parts2 := zipParts(finish(wb))
		Expect(names2).To(HaveLen(len(names0) + 1))
		Expect(names2[:len(names0)]).To(Equal(names0))
		changed = map[string]bool{"xl/workbook.xml": true, "xl/_rels/workbook.xml.rels": true, "[Content_Types].xml": true}
		for _, n := range names0 {
			if !changed[n] {
				Expect(bytes.Equal(parts0[n], parts2[n])).To(BeTrue(), "%s changed", n)
			}
		}
	})

	It("keeps unrelated XML of the sheet as it was", func() {
		src := mutate(sourceBook(), "xl/worksheets/sheet1.xml", func(s string) string {
			s = strings.Replace(s, "</sheetData>", `</sheetData><mergeCells count="1"><mergeCell ref="A1:B1"/></mergeCells>`, 1)
			return strings.Replace(s, "<sheetData>", "<sheetData>\n  ", 1)
		})
		wb := open(src)
		Expect(wb.SetCell("Data", "B2", Num(7))).To(Succeed())
		_, parts := zipParts(finish(wb))
		s := string(parts["xl/worksheets/sheet1.xml"])
		Expect(s).To(ContainSubstring(`<mergeCells count="1"><mergeCell ref="A1:B1"/></mergeCells>`), "unrelated XML changed")
		Expect(s).To(ContainSubstring("<sheetData>\n  "), "unrelated XML changed")
	})

	It("edits a worksheet that uses a namespace prefix", func() {
		sheet := `<?xml version="1.0"?><x:worksheet xmlns:x="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><x:sheetData><x:row r="2"><x:c r="B2"><x:v>1</x:v></x:c></x:row></x:sheetData></x:worksheet>`
		src := mutate(sourceBook(), "xl/worksheets/sheet2.xml", func(string) string { return sheet })
		wb := open(src)
		for _, ref := range []string{"A2", "A1", "C3"} {
			Expect(wb.SetCell("Notes", ref, Str("p"))).To(Succeed())
		}
		g := grids(finish(wb))["Notes"]
		Expect(g[0][0]).To(Equal("p"), "grid = %v", g)
		Expect(g[1][0]).To(Equal("p"), "grid = %v", g)
		Expect(g[1][1]).To(Equal("1"), "grid = %v", g)
		Expect(g[2][2]).To(Equal("p"), "grid = %v", g)
	})

	It("refuses to change the master of a shared formula", func() {
		src := mutate(sourceBook(), "xl/worksheets/sheet1.xml", func(s string) string {
			return strings.Replace(s, "<f>SUM(B2:B2)</f>", `<f t="shared" ref="B3:B4" si="0">SUM(B2:B2)</f>`, 1)
		})
		wb := open(src)
		Expect(wb.SetCell("Data", "B3", Num(1))).ToNot(Succeed(), "expected an error for a shared formula master")
		Expect(wb.ClearCell("Data", "B3")).ToNot(Succeed(), "expected an error for a shared formula master")
	})

	Describe("errors", func() {
		entries := []any{func(c setErrCase) {
			wb := open(sourceBook())
			Expect(wb.SetCell(c.sheet, c.ref, c.v)).ToNot(Succeed())
		}}
		for _, tc := range setErrs {
			entries = append(entries, Entry(tc.name, tc))
		}
		DescribeTable("rejects SetCell", entries...)

		It("rejects bad ClearCell calls and bad sheet names, and failed calls change nothing", func() {
			src := sourceBook()
			wb := open(src)
			long := strings.Repeat("a", 32)
			Expect(wb.ClearCell("Data", "1A")).ToNot(Succeed(), "ClearCell: expected an error for a bad ref")
			Expect(wb.ClearCell("Nope", "A1")).ToNot(Succeed(), "ClearCell: expected an error for an unknown sheet")
			for _, name := range []string{"", "data", "DATA", "notes", long, "a:b", "a/b", `a\b`, "a?b", "a*b", "a[b", "a]b", "'quoted", "x'"} {
				Expect(wb.AddSheet(name)).ToNot(Succeed(), "AddSheet(%q): expected an error", name)
			}
			Expect(wb.AddSheet(strings.Repeat("é", 31))).To(Succeed(), "a 31 character name should work")
			// Failed calls change nothing.
			clean := open(src)
			for _, tc := range setErrs {
				_ = clean.SetCell(tc.sheet, tc.ref, tc.v)
			}
			out, err := clean.Bytes()
			Expect(err).ToNot(HaveOccurred())
			_, p0 := zipParts(src)
			_, p1 := zipParts(out)
			Expect(p1).To(Equal(p0), "failed calls changed the package")
		})
	})

	DescribeTable("opening a file that is not a workbook",
		func(mk func(src []byte) []byte) {
			_, err := Open(mk(sourceBook()))
			Expect(err).To(HaveOccurred())
		},
		Entry("nil", func([]byte) []byte { return nil }),
		Entry("text", func([]byte) []byte { return []byte("not a zip") }),
		Entry("truncated", func(src []byte) []byte { return src[:len(src)/2] }),
		Entry("empty zip", func([]byte) []byte { return emptyZip() }),
		Entry("docx", func([]byte) []byte { return docxLike() }),
		Entry("bad rels", func(src []byte) []byte {
			return mutate(src, "_rels/.rels", func(string) string { return "<Relationships>" })
		}),
		Entry("no workbook", func(src []byte) []byte {
			return mutate(src, "xl/workbook.xml", func(string) string { return "" })
		}),
		Entry("wrong root", func(src []byte) []byte {
			return mutate(src, "xl/workbook.xml", func(string) string { return "<other/>" })
		}),
		Entry("broken xml", func(src []byte) []byte {
			return mutate(src, "xl/workbook.xml", func(string) string { return "<workbook><sheets>" })
		}),
	)

	DescribeTable("a broken sheet gives an error on edit",
		func(body string) {
			wb := open(mutate(sourceBook(), "xl/worksheets/sheet2.xml", func(string) string { return body }))
			_ = wb.Sheets()
			Expect(wb.SetCell("Notes", "A1", Str("x"))).ToNot(Succeed(), "SetCell: expected an error")
			Expect(wb.ClearCell("Notes", "A1")).ToNot(Succeed(), "ClearCell: expected an error")
		},
		Entry("not xml", "garbage"),
		Entry("wrong root", "<other/>"),
		Entry("no sheet", `<worksheet xmlns="x"/>`),
		Entry("bad row", `<worksheet><sheetData><row r="zero"/></sheetData></worksheet>`),
		Entry("bad cell", `<worksheet><sheetData><row r="1"><c r="1A"/></row></sheetData></worksheet>`),
		Entry("cell in row", `<worksheet><sheetData><row r="1"><c r="A2"/></row></sheetData></worksheet>`),
		Entry("truncated", `<worksheet><sheetData><row r="1">`),
	)

	It("fails to open a workbook whose sheet relationship dangles", func() {
		src := mutate(sourceBook(), "xl/workbook.xml", func(s string) string {
			return strings.Replace(s, `r:id="rId2"`, `r:id="rId99"`, 1)
		})
		_, err := Open(src)
		Expect(err).To(HaveOccurred(), "expected an error for a dangling sheet relationship")
	})
})
