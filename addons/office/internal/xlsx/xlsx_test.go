package xlsx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type xCell struct {
	Ref     string `xml:"r,attr"`
	Type    string `xml:"t,attr"`
	Style   int    `xml:"s,attr"`
	Formula string `xml:"f"`
	Value   string `xml:"v"`
}

type xSheet struct {
	Pane struct {
		YSplit  string `xml:"ySplit,attr"`
		TopLeft string `xml:"topLeftCell,attr"`
		State   string `xml:"state,attr"`
	} `xml:"sheetViews>sheetView>pane"`
	Cols []struct {
		Min   int    `xml:"min,attr"`
		Width string `xml:"width,attr"`
		Style int    `xml:"style,attr"`
	} `xml:"cols>col"`
	Cells []xCell `xml:"sheetData>row>c"`
}

type xStyles struct {
	NumFmts []struct {
		ID   int    `xml:"numFmtId,attr"`
		Code string `xml:"formatCode,attr"`
	} `xml:"numFmts>numFmt"`
	Fonts []struct {
		Bold  *struct{} `xml:"b"`
		Color struct {
			RGB string `xml:"rgb,attr"`
		} `xml:"color"`
	} `xml:"fonts>font"`
	Fills []struct {
		FG struct {
			RGB string `xml:"rgb,attr"`
		} `xml:"patternFill>fgColor"`
	} `xml:"fills>fill"`
	Xfs []struct {
		NumFmtID int `xml:"numFmtId,attr"`
		FontID   int `xml:"fontId,attr"`
		FillID   int `xml:"fillId,attr"`
	} `xml:"cellXfs>xf"`
}

func writeBook(wb Workbook) map[string][]byte {
	GinkgoHelper()
	var buf bytes.Buffer
	Expect(Write(&buf, wb)).To(Succeed())
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	Expect(err).ToNot(HaveOccurred(), "not a zip")
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		Expect(err).ToNot(HaveOccurred())
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		Expect(err).ToNot(HaveOccurred())
		files[f.Name] = data
	}
	return files
}

func unmarshal(files map[string][]byte, name string, v any) {
	GinkgoHelper()
	data, ok := files[name]
	Expect(ok).To(BeTrue(), "part %s missing", name)
	Expect(xml.Unmarshal(data, v)).To(Succeed(), name)
}

func sampleBook() Workbook {
	return Workbook{
		Title:       "Report",
		HeaderStyle: Style{Bold: true, FontColor: "FFFFFF", FillColor: "1F2937"},
		Sheets: []Sheet{{
			Name:         "Data",
			HeaderRow:    1,
			FreezeTopRow: true,
			Columns:      []Column{{Width: 20}, {Width: 12, NumberFormat: "#,##0.00"}},
			Cells: []Cell{
				{Ref: "A1", Value: Str("Item")},
				{Ref: "B1", Value: Str("Amount")},
				{Ref: "A2", Value: Str("Widget")},
				{Ref: "B2", Value: Num(1234.5)},
				{Ref: "A3", Value: Str("Widget")},
				{Ref: "B3", Value: Num(10)},
				{Ref: "A4", Value: Str("Total")},
				{Ref: "B4", Formula: "=SUM(B2:B3)", Value: Num(1244.5)},
				{Ref: "C2", Value: Boolean(true)},
				{Ref: "C3", Formula: `IF(B3>5,"big","small")`, Value: Str("big")},
				{Ref: "C4", Formula: "NOW()"},
			},
		}},
	}
}

func atoi(s string) int {
	GinkgoHelper()
	n := 0
	for _, r := range s {
		Expect(r >= '0' && r <= '9').To(BeTrue(), "not an index: %q", s)
		n = n*10 + int(r-'0')
	}
	return n
}

var _ = Describe("xlsx writer", func() {
	It("writes every package part, lists it in the content types and keeps each part well formed", func() {
		files := writeBook(sampleBook())
		Expect(files).To(HaveKey("[Content_Types].xml"))
		ct := string(files["[Content_Types].xml"])
		for _, name := range []string{
			"_rels/.rels", "xl/workbook.xml", "xl/_rels/workbook.xml.rels",
			"xl/worksheets/sheet1.xml", "xl/styles.xml", "xl/sharedStrings.xml",
			"docProps/core.xml", "docProps/app.xml",
		} {
			Expect(files).To(HaveKey(name), "part %s missing", name)
			if !strings.HasSuffix(name, ".rels") {
				Expect(ct).To(ContainSubstring(`PartName="/`+name+`"`), "content types do not list %s", name)
			}
		}
		for name, data := range files {
			Expect(xml.Unmarshal(data, new(struct{ XMLName xml.Name }))).To(Succeed(), "%s is not well formed", name)
		}
	})

	It("names an application only when the caller sets one", func() {
		files := writeBook(sampleBook())
		Expect(string(files["docProps/app.xml"])).ToNot(ContainSubstring("Application"))
		wb := sampleBook()
		wb.Application = "Some <App>"
		files = writeBook(wb)
		Expect(string(files["docProps/app.xml"])).To(ContainSubstring("<Application>Some &lt;App&gt;</Application>"))
	})

	It("writes cells with the right types, shares equal strings and freezes the top row", func() {
		files := writeBook(sampleBook())
		var sh xSheet
		unmarshal(files, "xl/worksheets/sheet1.xml", &sh)
		Expect(sh.Pane.State).To(Equal("frozen"))
		Expect(sh.Pane.YSplit).To(Equal("1"))
		Expect(sh.Pane.TopLeft).To(Equal("A2"))
		var sst struct {
			Items []string `xml:"si>t"`
		}
		unmarshal(files, "xl/sharedStrings.xml", &sst)
		// "Widget" is shared by two cells.
		Expect(sst.Items).To(HaveLen(4))
		cells := map[string]xCell{}
		for _, c := range sh.Cells {
			cells[c.Ref] = c
		}
		str := func(c xCell) string { return sst.Items[atoi(c.Value)] }
		c := cells["A2"]
		Expect(c.Type).To(Equal("s"))
		Expect(str(c)).To(Equal("Widget"))
		Expect(cells["A3"].Value).To(Equal(cells["A2"].Value), "equal strings are not shared")
		c = cells["B2"]
		Expect(c.Type).To(BeEmpty())
		Expect(c.Value).To(Equal("1234.5"))
		c = cells["C2"]
		Expect(c.Type).To(Equal("b"))
		Expect(c.Value).To(Equal("1"))
		c = cells["B4"]
		Expect(c.Formula).To(Equal("SUM(B2:B3)"))
		Expect(c.Value).To(Equal("1244.5"))
		c = cells["C3"]
		Expect(c.Type).To(Equal("str"))
		Expect(c.Formula).To(Equal(`IF(B3>5,"big","small")`))
		Expect(c.Value).To(Equal("big"))
		c = cells["C4"]
		Expect(c.Formula).To(Equal("NOW()"))
		Expect(c.Value).To(BeEmpty())
	})

	It("references the header, column and default styles from the cells", func() {
		files := writeBook(sampleBook())
		var st xStyles
		unmarshal(files, "xl/styles.xml", &st)
		var sh xSheet
		unmarshal(files, "xl/worksheets/sheet1.xml", &sh)
		cells := map[string]xCell{}
		for _, c := range sh.Cells {
			cells[c.Ref] = c
		}

		// Header cells use the header font and fill.
		for _, ref := range []string{"A1", "B1"} {
			xf := st.Xfs[cells[ref].Style]
			Expect(xf.FontID).ToNot(BeZero(), "%s is not styled as header: %+v", ref, xf)
			Expect(xf.FillID).To(BeNumerically(">=", 2), "%s is not styled as header: %+v", ref, xf)
			f := st.Fonts[xf.FontID]
			Expect(f.Bold).ToNot(BeNil())
			Expect(f.Color.RGB).To(Equal("FFFFFFFF"))
			Expect(st.Fills[xf.FillID].FG.RGB).To(Equal("FF1F2937"))
		}
		// Number cells and formulas in column B use the column format.
		for _, ref := range []string{"B2", "B3", "B4"} {
			xf := st.Xfs[cells[ref].Style]
			code := ""
			for _, nf := range st.NumFmts {
				if nf.ID == xf.NumFmtID {
					code = nf.Code
				}
			}
			Expect(code).To(Equal("#,##0.00"), ref)
		}
		// Text cells and cells outside column B keep the default format.
		for _, ref := range []string{"A2", "C2", "C3"} {
			Expect(cells[ref].Style).To(BeZero(), ref)
		}
		Expect(sh.Cols).To(HaveLen(2))
		Expect(sh.Cols[0].Width).To(Equal("20"))
		Expect(sh.Cols[1].Style).To(Equal(cells["B2"].Style))
	})

	It("rejects invalid workbooks and accepts the documented limits", func() {
		one := func(s Sheet) Workbook { return Workbook{Sheets: []Sheet{s}} }
		cases := map[string]Workbook{
			"no sheets":      {},
			"empty name":     one(Sheet{}),
			"duplicate name": {Sheets: []Sheet{{Name: "A"}, {Name: "a"}}},
			"too long":       one(Sheet{Name: strings.Repeat("x", 32)}),
			"bracket":        one(Sheet{Name: "a[b"}),
			"colon":          one(Sheet{Name: "a:b"}),
			"star":           one(Sheet{Name: "a*b"}),
			"question":       one(Sheet{Name: "a?b"}),
			"slash":          one(Sheet{Name: "a/b"}),
			"backslash":      one(Sheet{Name: `a\b`}),
			"lower ref":      one(Sheet{Name: "S", Cells: []Cell{{Ref: "a1"}}}),
			"no row":         one(Sheet{Name: "S", Cells: []Cell{{Ref: "A"}}}),
			"row zero":       one(Sheet{Name: "S", Cells: []Cell{{Ref: "A0"}}}),
			"row too big":    one(Sheet{Name: "S", Cells: []Cell{{Ref: "A1048577"}}}),
			"col too big":    one(Sheet{Name: "S", Cells: []Cell{{Ref: "XFE1"}}}),
			"junk ref":       one(Sheet{Name: "S", Cells: []Cell{{Ref: "A1B"}}}),
			"duplicate cell": one(Sheet{Name: "S", Cells: []Cell{{Ref: "A1"}, {Ref: "A1"}}}),
		}
		for name, wb := range cases {
			var buf bytes.Buffer
			Expect(Write(&buf, wb)).ToNot(Succeed(), "%s: expected an error", name)
		}
		Expect(Write(io.Discard, Workbook{Sheets: []Sheet{{Name: strings.Repeat("x", 31), Cells: []Cell{{Ref: "XFD1048576", Value: Num(1)}}}}})).To(Succeed(), "limits should be valid")
	})

	It("escapes special characters in strings, formulas, names, formats and the title", func() {
		nasty := `a < b & "c" 'd' > e`
		wb := Workbook{
			Title: nasty,
			Sheets: []Sheet{{
				Name: "Q&A 'x' y",
				Columns: []Column{
					{NumberFormat: `0.00" <u> & "`},
				},
				Cells: []Cell{
					{Ref: "A1", Value: Str(nasty)},
					{Ref: "A2", Formula: `"<" & "&"`, Value: Str(nasty)},
					{Ref: "A3", Value: Num(1)},
				},
			}},
		}
		files := writeBook(wb)

		var sst struct {
			Items []string `xml:"si>t"`
		}
		unmarshal(files, "xl/sharedStrings.xml", &sst)
		Expect(sst.Items).To(Equal([]string{nasty}))
		var sh xSheet
		unmarshal(files, "xl/worksheets/sheet1.xml", &sh)
		Expect(sh.Cells[1].Formula).To(Equal(`"<" & "&"`))
		Expect(sh.Cells[1].Value).To(Equal(nasty))
		var book struct {
			Sheets []struct {
				Name string `xml:"name,attr"`
			} `xml:"sheets>sheet"`
		}
		unmarshal(files, "xl/workbook.xml", &book)
		Expect(book.Sheets[0].Name).To(Equal("Q&A 'x' y"))
		var st xStyles
		unmarshal(files, "xl/styles.xml", &st)
		Expect(st.NumFmts).To(HaveLen(1))
		Expect(st.NumFmts[0].Code).To(Equal(`0.00" <u> & "`))
		var core struct {
			Title string `xml:"title"`
		}
		unmarshal(files, "docProps/core.xml", &core)
		Expect(core.Title).To(Equal(nasty))
	})

	It("sorts cells by row and column position", func() {
		wb := Workbook{Sheets: []Sheet{{Name: "S", Cells: []Cell{
			{Ref: "B2", Value: Num(4)}, {Ref: "A2", Value: Num(3)},
			{Ref: "B1", Value: Num(2)}, {Ref: "A1", Value: Num(1)},
		}}}}
		files := writeBook(wb)
		var sh xSheet
		unmarshal(files, "xl/worksheets/sheet1.xml", &sh)
		var got []string
		for _, c := range sh.Cells {
			got = append(got, c.Ref)
		}
		Expect(got).To(Equal([]string{"A1", "B1", "A2", "B2"}))
	})

	It("rejects a non-finite number", func() {
		inf := 1.0
		for range 2000 {
			inf *= 10
		}
		wb := Workbook{Sheets: []Sheet{{Name: "S", Cells: []Cell{{Ref: "A1", Value: Num(inf)}}}}}
		Expect(Write(io.Discard, wb)).ToNot(Succeed(), "expected an error for +Inf")
	})
})
