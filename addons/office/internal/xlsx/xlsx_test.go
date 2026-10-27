package xlsx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
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

func writeBook(t *testing.T, wb Workbook) map[string][]byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, wb); err != nil {
		t.Fatalf("Write: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = data
	}
	return files
}

func unmarshal(t *testing.T, files map[string][]byte, name string, v any) {
	t.Helper()
	data, ok := files[name]
	if !ok {
		t.Fatalf("part %s missing", name)
	}
	if err := xml.Unmarshal(data, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
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

func TestPackageParts(t *testing.T) {
	files := writeBook(t, sampleBook())
	if _, ok := files["[Content_Types].xml"]; !ok {
		t.Fatal("[Content_Types].xml missing")
	}
	ct := string(files["[Content_Types].xml"])
	for _, name := range []string{
		"_rels/.rels", "xl/workbook.xml", "xl/_rels/workbook.xml.rels",
		"xl/worksheets/sheet1.xml", "xl/styles.xml", "xl/sharedStrings.xml",
		"docProps/core.xml", "docProps/app.xml",
	} {
		if _, ok := files[name]; !ok {
			t.Errorf("part %s missing", name)
		}
		if !strings.HasSuffix(name, ".rels") && !strings.Contains(ct, `PartName="/`+name+`"`) {
			t.Errorf("content types do not list %s", name)
		}
	}
	for name, data := range files {
		if err := xml.Unmarshal(data, new(struct{ XMLName xml.Name })); err != nil {
			t.Errorf("%s is not well formed: %v", name, err)
		}
	}
}

func TestApplicationOptional(t *testing.T) {
	files := writeBook(t, sampleBook())
	if strings.Contains(string(files["docProps/app.xml"]), "Application") {
		t.Error("app.xml names an application without a caller option")
	}
	wb := sampleBook()
	wb.Application = "Some <App>"
	files = writeBook(t, wb)
	if !strings.Contains(string(files["docProps/app.xml"]), "<Application>Some &lt;App&gt;</Application>") {
		t.Errorf("app.xml: %s", files["docProps/app.xml"])
	}
}

func TestCellsAndFreeze(t *testing.T) {
	files := writeBook(t, sampleBook())
	var sh xSheet
	unmarshal(t, files, "xl/worksheets/sheet1.xml", &sh)
	if sh.Pane.State != "frozen" || sh.Pane.YSplit != "1" || sh.Pane.TopLeft != "A2" {
		t.Errorf("pane = %+v", sh.Pane)
	}
	var sst struct {
		Items []string `xml:"si>t"`
	}
	unmarshal(t, files, "xl/sharedStrings.xml", &sst)
	// "Widget" is shared by two cells.
	if len(sst.Items) != 4 {
		t.Errorf("shared strings = %v", sst.Items)
	}
	cells := map[string]xCell{}
	for _, c := range sh.Cells {
		cells[c.Ref] = c
	}
	str := func(c xCell) string { return sst.Items[atoi(t, c.Value)] }
	if c := cells["A2"]; c.Type != "s" || str(c) != "Widget" {
		t.Errorf("A2 = %+v", c)
	}
	if cells["A2"].Value != cells["A3"].Value {
		t.Error("equal strings are not shared")
	}
	if c := cells["B2"]; c.Type != "" || c.Value != "1234.5" {
		t.Errorf("B2 = %+v", c)
	}
	if c := cells["C2"]; c.Type != "b" || c.Value != "1" {
		t.Errorf("C2 = %+v", c)
	}
	if c := cells["B4"]; c.Formula != "SUM(B2:B3)" || c.Value != "1244.5" {
		t.Errorf("B4 = %+v", c)
	}
	if c := cells["C3"]; c.Type != "str" || c.Formula != `IF(B3>5,"big","small")` || c.Value != "big" {
		t.Errorf("C3 = %+v", c)
	}
	if c := cells["C4"]; c.Formula != "NOW()" || c.Value != "" {
		t.Errorf("C4 = %+v", c)
	}
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			t.Fatalf("not an index: %q", s)
		}
		n = n*10 + int(r-'0')
	}
	return n
}

func TestStylesReferenced(t *testing.T) {
	files := writeBook(t, sampleBook())
	var st xStyles
	unmarshal(t, files, "xl/styles.xml", &st)
	var sh xSheet
	unmarshal(t, files, "xl/worksheets/sheet1.xml", &sh)
	cells := map[string]xCell{}
	for _, c := range sh.Cells {
		cells[c.Ref] = c
	}

	// Header cells use the header font and fill.
	for _, ref := range []string{"A1", "B1"} {
		xf := st.Xfs[cells[ref].Style]
		if xf.FontID == 0 || xf.FillID < 2 {
			t.Fatalf("%s is not styled as header: %+v", ref, xf)
		}
		f := st.Fonts[xf.FontID]
		if f.Bold == nil || f.Color.RGB != "FFFFFFFF" {
			t.Errorf("header font = %+v", f)
		}
		if st.Fills[xf.FillID].FG.RGB != "FF1F2937" {
			t.Errorf("header fill = %+v", st.Fills[xf.FillID])
		}
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
		if code != "#,##0.00" {
			t.Errorf("%s format = %q", ref, code)
		}
	}
	// Text cells and cells outside column B keep the default format.
	for _, ref := range []string{"A2", "C2", "C3"} {
		if cells[ref].Style != 0 {
			t.Errorf("%s has style %d", ref, cells[ref].Style)
		}
	}
	if len(sh.Cols) != 2 || sh.Cols[0].Width != "20" || sh.Cols[1].Style != cells["B2"].Style {
		t.Errorf("cols = %+v", sh.Cols)
	}
}

func TestValidation(t *testing.T) {
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
		if err := Write(&buf, wb); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if err := Write(io.Discard, Workbook{Sheets: []Sheet{{Name: strings.Repeat("x", 31), Cells: []Cell{{Ref: "XFD1048576", Value: Num(1)}}}}}); err != nil {
		t.Errorf("limits should be valid: %v", err)
	}
}

func TestEscaping(t *testing.T) {
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
	files := writeBook(t, wb)

	var sst struct {
		Items []string `xml:"si>t"`
	}
	unmarshal(t, files, "xl/sharedStrings.xml", &sst)
	if len(sst.Items) != 1 || sst.Items[0] != nasty {
		t.Errorf("shared string = %q", sst.Items)
	}
	var sh xSheet
	unmarshal(t, files, "xl/worksheets/sheet1.xml", &sh)
	if c := sh.Cells[1]; c.Formula != `"<" & "&"` || c.Value != nasty {
		t.Errorf("formula cell = %+v", c)
	}
	var book struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
		} `xml:"sheets>sheet"`
	}
	unmarshal(t, files, "xl/workbook.xml", &book)
	if book.Sheets[0].Name != "Q&A 'x' y" {
		t.Errorf("sheet name = %q", book.Sheets[0].Name)
	}
	var st xStyles
	unmarshal(t, files, "xl/styles.xml", &st)
	if len(st.NumFmts) != 1 || st.NumFmts[0].Code != `0.00" <u> & "` {
		t.Errorf("numFmts = %+v", st.NumFmts)
	}
	var core struct {
		Title string `xml:"title"`
	}
	unmarshal(t, files, "docProps/core.xml", &core)
	if core.Title != nasty {
		t.Errorf("title = %q", core.Title)
	}
}

func TestCellsSortedByPosition(t *testing.T) {
	wb := Workbook{Sheets: []Sheet{{Name: "S", Cells: []Cell{
		{Ref: "B2", Value: Num(4)}, {Ref: "A2", Value: Num(3)},
		{Ref: "B1", Value: Num(2)}, {Ref: "A1", Value: Num(1)},
	}}}}
	files := writeBook(t, wb)
	var sh xSheet
	unmarshal(t, files, "xl/worksheets/sheet1.xml", &sh)
	var got []string
	for _, c := range sh.Cells {
		got = append(got, c.Ref)
	}
	if strings.Join(got, ",") != "A1,B1,A2,B2" {
		t.Errorf("order = %v", got)
	}
}

func TestNotFinite(t *testing.T) {
	inf := 1.0
	for i := 0; i < 2000; i++ {
		inf *= 10
	}
	wb := Workbook{Sheets: []Sheet{{Name: "S", Cells: []Cell{{Ref: "A1", Value: Num(inf)}}}}}
	if err := Write(io.Discard, wb); err == nil {
		t.Error("expected an error for +Inf")
	}
}
