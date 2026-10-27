package xlsxedit

import (
	"archive/zip"
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/addons/office/internal/officecheck"
	"github.com/mudler/LocalAI/addons/office/internal/officeread"
	"github.com/mudler/LocalAI/addons/office/internal/xlsx"
)

func sourceBook(t *testing.T) []byte {
	t.Helper()
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
	if err := xlsx.Write(&buf, wb); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipParts(t *testing.T, data []byte) (names []string, parts map[string][]byte) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	parts = map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, f.Name)
		parts[f.Name] = b
	}
	return names, parts
}

// mutate returns a copy of the package with one part replaced.
func mutate(t *testing.T, data []byte, name string, fn func(string) string) []byte {
	t.Helper()
	names, parts := zipParts(t, data)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		b := parts[n]
		if n == name {
			b = []byte(fn(string(b)))
		}
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(b)
	}
	_ = zw.Close()
	return buf.Bytes()
}

func open(t *testing.T, data []byte) *Workbook {
	t.Helper()
	wb, err := Open(data)
	if err != nil {
		t.Fatal(err)
	}
	return wb
}

// finish writes the workbook, checks the result and returns it.
func finish(t *testing.T, wb *Workbook) []byte {
	t.Helper()
	out, err := wb.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if problems := officecheck.Check(out); len(problems) != 0 {
		t.Fatalf("officecheck problems: %v", problems)
	}
	return out
}

func grids(t *testing.T, data []byte) map[string][][]string {
	t.Helper()
	doc, err := officeread.Read(data)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][][]string{}
	for _, s := range doc.Parts {
		out[s.Title] = s.Tables[0]
	}
	return out
}

func TestOpenAndRead(t *testing.T) {
	src := sourceBook(t)
	wb := open(t, src)
	got := wb.Sheets()
	if len(got) != 2 || got[0].Name != "Data" || got[0].Index != 1 || got[1].Name != "Notes" || got[1].Index != 2 {
		t.Fatalf("Sheets() = %+v", got)
	}
	g := grids(t, src)
	want := [][]string{
		{"Name", "Qty", "Flag"},
		{"apples", "3", "TRUE"},
		{"total", "3", ""},
		{"", "", ""},
		{"", "", "tail"},
	}
	if !reflect.DeepEqual(g["Data"], want) {
		t.Fatalf("grid = %v", g["Data"])
	}
	// A workbook with no edit is written back with the same parts.
	out := finish(t, wb)
	if !reflect.DeepEqual(grids(t, out), g) {
		t.Fatal("grid changed without an edit")
	}
	_, p0 := zipParts(t, src)
	_, p1 := zipParts(t, out)
	if !reflect.DeepEqual(p0, p1) {
		t.Fatal("parts changed without an edit")
	}
}

func TestSetCellKinds(t *testing.T) {
	tests := []struct {
		name  string
		ref   string
		value Value
		want  string
	}{
		{"string", "A2", Str("pears <&> \"x\""), `pears <&> "x"`},
		{"string keeps spaces", "A2", Str("  padded  "), "  padded  "},
		{"number", "B2", Num(2.5), "2.5"},
		{"big number", "B2", Num(1e21), "1e+21"},
		{"bool false", "C2", Boolean(false), "FALSE"},
		{"bool true", "B2", Boolean(true), "TRUE"},
		{"formula no cache", "D2", Formula("=B2*2"), ""},
		{"formula cached number", "D2", FormulaCached("B2*2", Num(6)), "6"},
		{"formula cached text", "D2", FormulaCached("A2&\"!\"", Str("apples!")), "apples!"},
		{"formula cached bool", "D2", FormulaCached("B2>1", Boolean(true)), "TRUE"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wb := open(t, sourceBook(t))
			if err := wb.SetCell("Data", tc.ref, tc.value); err != nil {
				t.Fatal(err)
			}
			g := grids(t, finish(t, wb))["Data"]
			col := int(tc.ref[0] - 'A')
			if got := g[1][col]; got != tc.want {
				t.Fatalf("%s = %q, want %q", tc.ref, got, tc.want)
			}
		})
	}
}

func TestFormulaIsStored(t *testing.T) {
	wb := open(t, sourceBook(t))
	if err := wb.SetCell("Data", "D2", Formula("=B2*2")); err != nil {
		t.Fatal(err)
	}
	_, parts := zipParts(t, finish(t, wb))
	if !strings.Contains(string(parts["xl/worksheets/sheet1.xml"]), `<c r="D2"><f>B2*2</f></c>`) {
		t.Fatalf("sheet1.xml = %s", parts["xl/worksheets/sheet1.xml"])
	}
}

func TestReplaceFormula(t *testing.T) {
	wb := open(t, sourceBook(t))
	if err := wb.SetCell("Data", "B3", FormulaCached("SUM(B2:B2)*10", Num(30))); err != nil {
		t.Fatal(err)
	}
	out := finish(t, wb)
	if g := grids(t, out)["Data"]; g[2][1] != "30" {
		t.Fatalf("B3 = %q", g[2][1])
	}
	_, parts := zipParts(t, out)
	s := string(parts["xl/worksheets/sheet1.xml"])
	if strings.Count(s, `r="B3"`) != 1 || strings.Contains(s, "<f>SUM(B2:B2)</f>") {
		t.Fatalf("old formula still there: %s", s)
	}
}

func TestStyleKept(t *testing.T) {
	wb := open(t, sourceBook(t))
	if err := wb.SetCell("Data", "A1", Str("Item")); err != nil {
		t.Fatal(err)
	}
	_, parts := zipParts(t, finish(t, wb))
	if !strings.Contains(string(parts["xl/worksheets/sheet1.xml"]), `<c r="A1" s="1" t="inlineStr">`) {
		t.Fatalf("header style lost: %s", parts["xl/worksheets/sheet1.xml"])
	}
}

func TestNewCells(t *testing.T) {
	src := mutate(t, sourceBook(t), "xl/worksheets/sheet1.xml", func(s string) string {
		// An empty row, and a row with a spans hint.
		s = strings.Replace(s, `<row r="5">`, `<row r="4"/><row r="5" spans="3:3">`, 1)
		return s
	})
	tests := []struct {
		name, ref string
		row, col  int
	}{
		{"empty self-closed row", "B4", 3, 1},
		{"before an existing cell", "A5", 4, 0},
		{"between existing cells", "B5", 4, 1},
		{"after the last cell", "E2", 1, 4},
		{"missing row between rows", "A10", 9, 0},
		{"new row before others", "A1", 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wb := open(t, src)
			if err := wb.SetCell("Data", tc.ref, Str("new")); err != nil {
				t.Fatal(err)
			}
			g := grids(t, finish(t, wb))["Data"]
			if got := g[tc.row][tc.col]; got != "new" {
				t.Fatalf("%s = %q (grid %v)", tc.ref, got, g)
			}
		})
	}
}

func TestOrderStaysSorted(t *testing.T) {
	wb := open(t, sourceBook(t))
	for _, ref := range []string{"C7", "A7", "B7", "A6", "Z1", "D2", "A4"} {
		if err := wb.SetCell("Data", ref, Num(1)); err != nil {
			t.Fatal(err)
		}
	}
	_, parts := zipParts(t, finish(t, wb))
	sc, err := scanSheet(parts["xl/worksheets/sheet1.xml"])
	if err != nil {
		t.Fatal(err)
	}
	prevRow := 0
	for _, r := range sc.rows {
		if r.r <= prevRow {
			t.Fatalf("rows not sorted: %d after %d", r.r, prevRow)
		}
		prevRow = r.r
		prevCol := 0
		for _, c := range r.cells {
			if c.col <= prevCol {
				t.Fatalf("row %d: cells not sorted", r.r)
			}
			prevCol = c.col
		}
	}
}

func TestDimensionGrows(t *testing.T) {
	src := mutate(t, sourceBook(t), "xl/worksheets/sheet1.xml", func(s string) string {
		return strings.Replace(s, "<sheetViews>", `<dimension ref="A1:C5"/><sheetViews>`, 1)
	})
	wb := open(t, src)
	if d := wb.Sheets()[0].Dimension; d != "A1:C5" {
		t.Fatalf("dimension = %q", d)
	}
	if err := wb.SetCell("Data", "E9", Num(1)); err != nil {
		t.Fatal(err)
	}
	if err := wb.SetCell("Data", "B2", Num(1)); err != nil {
		t.Fatal(err)
	}
	finish(t, wb)
	if d := wb.Sheets()[0].Dimension; d != "A1:E9" {
		t.Fatalf("dimension = %q", d)
	}
	if d := wb.Sheets()[1].Dimension; d != "" {
		t.Fatalf("dimension of Notes = %q", d)
	}
}

func TestClearCell(t *testing.T) {
	wb := open(t, sourceBook(t))
	for _, ref := range []string{"B3", "A2", "A1", "D9"} {
		if err := wb.ClearCell("Data", ref); err != nil {
			t.Fatal(err)
		}
	}
	out := finish(t, wb)
	g := grids(t, out)["Data"]
	if g[2][1] != "" || g[1][0] != "" || g[0][0] != "" {
		t.Fatalf("cells not cleared: %v", g)
	}
	if g[0][1] != "Qty" || g[1][1] != "3" {
		t.Fatalf("neighbours changed: %v", g)
	}
	_, parts := zipParts(t, out)
	s := string(parts["xl/worksheets/sheet1.xml"])
	if !strings.Contains(s, `<c r="A1" s="1"/>`) {
		t.Fatalf("styled cell was not kept: %s", s)
	}
	if strings.Contains(s, `r="B3"`) || strings.Contains(s, "<f>") {
		t.Fatalf("formula cell still there: %s", s)
	}
}

func TestAddSheet(t *testing.T) {
	src := sourceBook(t)
	wb := open(t, src)
	if err := wb.AddSheet("Summary"); err != nil {
		t.Fatal(err)
	}
	if err := wb.SetCell("summary", "B2", Str("on the new sheet")); err != nil {
		t.Fatal(err)
	}
	if err := wb.AddSheet("Third"); err != nil {
		t.Fatal(err)
	}
	got := wb.Sheets()
	if len(got) != 4 || got[2].Name != "Summary" || got[2].Index != 3 || got[3].Name != "Third" {
		t.Fatalf("Sheets() = %+v", got)
	}
	out := finish(t, wb)
	g := grids(t, out)
	if len(g) != 4 {
		t.Fatalf("sheets = %v", g)
	}
	if !reflect.DeepEqual(g["Summary"], [][]string{{"", ""}, {"", "on the new sheet"}}) {
		t.Fatalf("Summary = %v", g["Summary"])
	}
	if len(g["Third"]) != 0 {
		t.Fatalf("Third = %v", g["Third"])
	}
	if g["Data"][1][0] != "apples" {
		t.Fatal("old sheet changed")
	}
	// Reopen the result: the sheets survive a round trip.
	again := open(t, out)
	if n := len(again.Sheets()); n != 4 {
		t.Fatalf("reopened sheets = %d", n)
	}
	_, parts := zipParts(t, out)
	wbx := string(parts["xl/workbook.xml"])
	if !strings.Contains(wbx, `name="Summary" sheetId="3"`) || !strings.Contains(wbx, `name="Third" sheetId="4"`) {
		t.Fatalf("workbook.xml = %s", wbx)
	}
	if !strings.Contains(string(parts["[Content_Types].xml"]), `PartName="/xl/worksheets/sheet3.xml"`) {
		t.Fatal("content type missing")
	}
	if !strings.Contains(string(parts["xl/_rels/workbook.xml.rels"]), `Target="worksheets/sheet3.xml"`) {
		t.Fatal("relationship missing")
	}
}

func TestAddSheetUniqueIDs(t *testing.T) {
	// Gaps in sheetId and rId, and a sheet part name that is already taken.
	src := mutate(t, sourceBook(t), "xl/workbook.xml", func(s string) string {
		return strings.Replace(s, `sheetId="2"`, `sheetId="9"`, 1)
	})
	src = mutate(t, src, "xl/_rels/workbook.xml.rels", func(s string) string {
		return strings.Replace(s, `Id="rId3"`, `Id="rId40"`, 1)
	})
	wb := open(t, src)
	if err := wb.AddSheet("New"); err != nil {
		t.Fatal(err)
	}
	_, parts := zipParts(t, finish(t, wb))
	if !strings.Contains(string(parts["xl/workbook.xml"]), `name="New" sheetId="10"`) {
		t.Fatalf("workbook.xml = %s", parts["xl/workbook.xml"])
	}
	if !strings.Contains(string(parts["xl/_rels/workbook.xml.rels"]), `Id="rId41"`) {
		t.Fatalf("rels = %s", parts["xl/_rels/workbook.xml.rels"])
	}
}

func TestFullCalcOnLoad(t *testing.T) {
	wb := open(t, sourceBook(t))
	if err := wb.SetCell("Data", "A2", Str("x")); err != nil {
		t.Fatal(err)
	}
	_, parts := zipParts(t, finish(t, wb))
	if !strings.Contains(string(parts["xl/workbook.xml"]), `<calcPr fullCalcOnLoad="1"/>`) {
		t.Fatalf("workbook.xml = %s", parts["xl/workbook.xml"])
	}

	// An existing calcPr keeps its other attributes.
	src := mutate(t, sourceBook(t), "xl/workbook.xml", func(s string) string {
		return strings.Replace(s, "</workbook>", `<calcPr calcId="191029" fullCalcOnLoad="0"/></workbook>`, 1)
	})
	wb = open(t, src)
	if err := wb.AddSheet("More"); err != nil {
		t.Fatal(err)
	}
	_, parts = zipParts(t, finish(t, wb))
	s := string(parts["xl/workbook.xml"])
	if strings.Count(s, "calcPr") != 1 || !strings.Contains(s, `calcId="191029"`) || !strings.Contains(s, `fullCalcOnLoad="1"`) || strings.Contains(s, `fullCalcOnLoad="0"`) {
		t.Fatalf("workbook.xml = %s", s)
	}

	// calcPr goes before later elements.
	src = mutate(t, sourceBook(t), "xl/workbook.xml", func(s string) string {
		return strings.Replace(s, "</workbook>", `<extLst/></workbook>`, 1)
	})
	wb = open(t, src)
	if err := wb.SetCell("Data", "A2", Str("x")); err != nil {
		t.Fatal(err)
	}
	_, parts = zipParts(t, finish(t, wb))
	if !strings.Contains(string(parts["xl/workbook.xml"]), `<calcPr fullCalcOnLoad="1"/><extLst/>`) {
		t.Fatalf("workbook.xml = %s", parts["xl/workbook.xml"])
	}
}

func TestUntouchedPartsAreIdentical(t *testing.T) {
	src := sourceBook(t)
	names0, parts0 := zipParts(t, src)
	wb := open(t, src)
	if err := wb.SetCell("Data", "A2", Str("changed")); err != nil {
		t.Fatal(err)
	}
	out := finish(t, wb)
	names1, parts1 := zipParts(t, out)
	if !reflect.DeepEqual(names0, names1) {
		t.Fatalf("entry order changed: %v -> %v", names0, names1)
	}
	changed := map[string]bool{"xl/worksheets/sheet1.xml": true, "xl/workbook.xml": true}
	for _, n := range names0 {
		same := bytes.Equal(parts0[n], parts1[n])
		if changed[n] && same {
			t.Errorf("%s should have changed", n)
		}
		if !changed[n] && !same {
			t.Errorf("%s changed", n)
		}
	}
	// AddSheet changes the workbook parts, adds a part last, and keeps the rest.
	wb = open(t, src)
	if err := wb.AddSheet("Extra"); err != nil {
		t.Fatal(err)
	}
	names2, parts2 := zipParts(t, finish(t, wb))
	if !reflect.DeepEqual(names2[:len(names0)], names0) || len(names2) != len(names0)+1 {
		t.Fatalf("entries = %v", names2)
	}
	changed = map[string]bool{"xl/workbook.xml": true, "xl/_rels/workbook.xml.rels": true, "[Content_Types].xml": true}
	for _, n := range names0 {
		if !changed[n] && !bytes.Equal(parts0[n], parts2[n]) {
			t.Errorf("%s changed", n)
		}
	}
}

func TestUnrelatedXMLKept(t *testing.T) {
	src := mutate(t, sourceBook(t), "xl/worksheets/sheet1.xml", func(s string) string {
		s = strings.Replace(s, "</sheetData>", `</sheetData><mergeCells count="1"><mergeCell ref="A1:B1"/></mergeCells>`, 1)
		return strings.Replace(s, "<sheetData>", "<sheetData>\n  ", 1)
	})
	wb := open(t, src)
	if err := wb.SetCell("Data", "B2", Num(7)); err != nil {
		t.Fatal(err)
	}
	_, parts := zipParts(t, finish(t, wb))
	s := string(parts["xl/worksheets/sheet1.xml"])
	if !strings.Contains(s, `<mergeCells count="1"><mergeCell ref="A1:B1"/></mergeCells>`) || !strings.Contains(s, "<sheetData>\n  ") {
		t.Fatalf("unrelated XML changed: %s", s)
	}
}

func TestPrefixedWorksheet(t *testing.T) {
	sheet := `<?xml version="1.0"?><x:worksheet xmlns:x="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><x:sheetData><x:row r="2"><x:c r="B2"><x:v>1</x:v></x:c></x:row></x:sheetData></x:worksheet>`
	src := mutate(t, sourceBook(t), "xl/worksheets/sheet2.xml", func(string) string { return sheet })
	wb := open(t, src)
	for _, ref := range []string{"A2", "A1", "C3"} {
		if err := wb.SetCell("Notes", ref, Str("p")); err != nil {
			t.Fatal(err)
		}
	}
	g := grids(t, finish(t, wb))["Notes"]
	if g[0][0] != "p" || g[1][0] != "p" || g[1][1] != "1" || g[2][2] != "p" {
		t.Fatalf("grid = %v", g)
	}
}

func TestSharedFormulaMasterRefused(t *testing.T) {
	src := mutate(t, sourceBook(t), "xl/worksheets/sheet1.xml", func(s string) string {
		return strings.Replace(s, "<f>SUM(B2:B2)</f>", `<f t="shared" ref="B3:B4" si="0">SUM(B2:B2)</f>`, 1)
	})
	wb := open(t, src)
	if err := wb.SetCell("Data", "B3", Num(1)); err == nil {
		t.Fatal("expected an error for a shared formula master")
	}
	if err := wb.ClearCell("Data", "B3"); err == nil {
		t.Fatal("expected an error for a shared formula master")
	}
}

func TestErrors(t *testing.T) {
	src := sourceBook(t)
	wb := open(t, src)
	long := strings.Repeat("a", 32)
	setErrs := []struct {
		name, sheet, ref string
		v                Value
	}{
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
	for _, tc := range setErrs {
		t.Run(tc.name, func(t *testing.T) {
			if err := wb.SetCell(tc.sheet, tc.ref, tc.v); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
	if err := wb.ClearCell("Data", "1A"); err == nil {
		t.Error("ClearCell: expected an error for a bad ref")
	}
	if err := wb.ClearCell("Nope", "A1"); err == nil {
		t.Error("ClearCell: expected an error for an unknown sheet")
	}
	for _, name := range []string{"", "data", "DATA", "notes", long, "a:b", "a/b", `a\b`, "a?b", "a*b", "a[b", "a]b", "'quoted", "x'"} {
		if err := wb.AddSheet(name); err == nil {
			t.Errorf("AddSheet(%q): expected an error", name)
		}
	}
	if err := wb.AddSheet(strings.Repeat("é", 31)); err != nil {
		t.Errorf("a 31 character name should work: %v", err)
	}
	// Failed calls change nothing.
	clean := open(t, src)
	for _, tc := range setErrs {
		_ = clean.SetCell(tc.sheet, tc.ref, tc.v)
	}
	out, err := clean.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	_, p0 := zipParts(t, src)
	_, p1 := zipParts(t, out)
	if !reflect.DeepEqual(p0, p1) {
		t.Error("failed calls changed the package")
	}
}

func nan() float64 { var z float64; return z / z }

func TestOpenErrors(t *testing.T) {
	src := sourceBook(t)
	bad := map[string][]byte{
		"nil":         nil,
		"text":        []byte("not a zip"),
		"truncated":   src[:len(src)/2],
		"empty zip":   emptyZip(t),
		"docx":        docxLike(t),
		"bad rels":    mutate(t, src, "_rels/.rels", func(string) string { return "<Relationships>" }),
		"no workbook": mutate(t, src, "xl/workbook.xml", func(string) string { return "" }),
		"wrong root":  mutate(t, src, "xl/workbook.xml", func(string) string { return "<other/>" }),
		"broken xml":  mutate(t, src, "xl/workbook.xml", func(string) string { return "<workbook><sheets>" }),
	}
	for name, data := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := Open(data); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestBrokenSheetGivesError(t *testing.T) {
	src := sourceBook(t)
	for name, body := range map[string]string{
		"not xml":     "garbage",
		"wrong root":  "<other/>",
		"no sheet":    `<worksheet xmlns="x"/>`,
		"bad row":     `<worksheet><sheetData><row r="zero"/></sheetData></worksheet>`,
		"bad cell":    `<worksheet><sheetData><row r="1"><c r="1A"/></row></sheetData></worksheet>`,
		"cell in row": `<worksheet><sheetData><row r="1"><c r="A2"/></row></sheetData></worksheet>`,
		"truncated":   `<worksheet><sheetData><row r="1">`,
	} {
		t.Run(name, func(t *testing.T) {
			wb := open(t, mutate(t, src, "xl/worksheets/sheet2.xml", func(string) string { return body }))
			_ = wb.Sheets()
			if err := wb.SetCell("Notes", "A1", Str("x")); err == nil {
				t.Fatal("SetCell: expected an error")
			}
			if err := wb.ClearCell("Notes", "A1"); err == nil {
				t.Fatal("ClearCell: expected an error")
			}
		})
	}
}

func TestMissingSheetPart(t *testing.T) {
	src := mutate(t, sourceBook(t), "xl/workbook.xml", func(s string) string {
		return strings.Replace(s, `r:id="rId2"`, `r:id="rId99"`, 1)
	})
	if _, err := Open(src); err == nil {
		t.Fatal("expected an error for a dangling sheet relationship")
	}
}

func emptyZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := zip.NewWriter(&buf).Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func docxLike(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("word/document.xml")
	w.Write([]byte("<document/>"))
	zw.Close()
	return buf.Bytes()
}
