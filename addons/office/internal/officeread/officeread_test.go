package officeread

import (
	"archive/zip"
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/addons/office/internal/deck"
	"github.com/mudler/LocalAI/addons/office/internal/deck/pptx"
	"github.com/mudler/LocalAI/addons/office/internal/docx"
	"github.com/mudler/LocalAI/addons/office/internal/xlsx"
)

func sampleXLSX(t *testing.T) []byte {
	t.Helper()
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
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestXLSXRoundTrip(t *testing.T) {
	doc, err := Read(sampleXLSX(t))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format != "xlsx" || len(doc.Parts) != 2 {
		t.Fatalf("format %q, %d parts", doc.Format, len(doc.Parts))
	}
	s := doc.Parts[0]
	if s.Title != "Sales" || doc.Parts[1].Title != "Notes" {
		t.Fatalf("titles %q, %q", s.Title, doc.Parts[1].Title)
	}
	want := [][]string{
		{"Item", "Qty", "", ""},
		{"Apples & pears", "3.5", "", ""},
		{"TRUE", "FALSE", "", ""},
		{"Double", "7", "", ""},
		{"", "", "", ""},
		{"", "", "", "far"},
	}
	if len(s.Tables) != 1 || !reflect.DeepEqual(s.Tables[0], want) {
		t.Fatalf("grid = %q", s.Tables)
	}
	wantText := "Item\tQty\nApples & pears\t3.5\nTRUE\tFALSE\nDouble\t7\n\n\t\t\tfar"
	if s.Text != wantText {
		t.Errorf("text = %q, want %q", s.Text, wantText)
	}
	if strings.Contains(s.Text, "B2*2") || strings.Contains(s.Text, "=") {
		t.Errorf("formula text leaked: %q", s.Text)
	}
	if doc.Parts[1].Text != "second sheet" {
		t.Errorf("second sheet = %q", doc.Parts[1].Text)
	}
}

func TestDOCXRoundTrip(t *testing.T) {
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
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Read(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format != "docx" || len(doc.Parts) != 1 || doc.Parts[0].Title != "Document" {
		t.Fatalf("doc = %+v", doc)
	}
	want := "# Plan\nIntro line.\n## Steps\n- one\n- two\nbold plain\n### Data\nName\tScore\na\t1\nb\t2\n\nAfter."
	if got := doc.Parts[0].Text; got != want {
		t.Errorf("text = %q\nwant   %q", got, want)
	}
	wantTable := [][][]string{{{"Name", "Score"}, {"a", "1"}, {"b", "2"}}}
	if !reflect.DeepEqual(doc.Parts[0].Tables, wantTable) {
		t.Errorf("tables = %q", doc.Parts[0].Tables)
	}
}

func samplePPTX(t *testing.T) []byte {
	t.Helper()
	slides := []deck.StudioSlide{
		{ID: "s1", Title: "Q3 review", Sub: "Partners", Layout: deck.LayoutTitle, Status: deck.SlideOK},
		{ID: "s2", Title: "What we heard", Layout: deck.LayoutText, Status: deck.SlideOK,
			Notes:   "Say this first.\nThen pause.",
			Bullets: []deck.StudioBullet{{Text: "Revenue grew"}, {Text: "Churn is low"}}},
	}
	var buf bytes.Buffer
	if err := pptx.Write(&buf, slides, pptx.Options{Title: "Q3", Notes: true}, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPPTXRoundTrip(t *testing.T) {
	doc, err := Read(samplePPTX(t))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Format != "pptx" || len(doc.Parts) != 2 {
		t.Fatalf("format %q, %d parts", doc.Format, len(doc.Parts))
	}
	if doc.Parts[0].Title != "Slide 1" || doc.Parts[1].Title != "Slide 2" {
		t.Errorf("titles %q, %q", doc.Parts[0].Title, doc.Parts[1].Title)
	}
	for _, want := range []string{"Q3 review", "Partners"} {
		if !strings.Contains(doc.Parts[0].Text, want) {
			t.Errorf("slide 1 misses %q: %q", want, doc.Parts[0].Text)
		}
	}
	if strings.Contains(doc.Parts[0].Text, "Notes:") {
		t.Errorf("slide 1 has no notes: %q", doc.Parts[0].Text)
	}
	s2 := doc.Parts[1].Text
	for _, want := range []string{"What we heard", "Revenue grew\n", "Churn is low", "\nNotes:\nSay this first.\nThen pause."} {
		if !strings.Contains(s2, want) {
			t.Errorf("slide 2 misses %q: %q", want, s2)
		}
	}
	if !strings.HasSuffix(s2, "Notes:\nSay this first.\nThen pause.") {
		t.Errorf("notes must come last: %q", s2)
	}
}

// rewrite returns a copy of a zip with fn applied to each entry. fn returns
// the new content, or nil to drop the entry.
func rewrite(t *testing.T, data []byte, fn func(name string, b []byte) []byte) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
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

func TestXLSXSharedStringsAndInline(t *testing.T) {
	sheet := `<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
		`<row r="1"><c r="A1" t="s"><v>1</v></c><c r="B1" t="s"><v>0</v></c><c r="C1" t="inlineStr"><is><t>in</t><r><t>line</t></r></is></c></row>` +
		`<row r="2"><c r="A2" t="str"><f>A1&amp;B1</f><v>cached</v></c><c r="B2" t="b"><v>1</v></c><c r="C2"><v>42</v></c></row>` +
		`</sheetData></worksheet>`
	sst := `<?xml version="1.0"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
		`<si><t>zero</t></si><si><r><t>on</t></r><r><t xml:space="preserve">e </t></r><rPh><t>ignored</t></rPh></si></sst>`
	data := rewrite(t, sampleXLSX(t), func(name string, b []byte) []byte {
		switch name {
		case "xl/worksheets/sheet1.xml":
			return []byte(sheet)
		case "xl/sharedStrings.xml":
			return []byte(sst)
		}
		return b
	})
	doc, err := Read(data)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"on" + "e ", "zero", "inline"}, {"cached", "TRUE", "42"}}
	if got := doc.Parts[0].Tables[0]; !reflect.DeepEqual(got, want) {
		t.Errorf("grid = %q, want %q", got, want)
	}
}

func addEntry(t *testing.T, data []byte, name, content string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
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

func makeZip(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for n, c := range entries {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(c))
	}
	zw.Close()
	return out.Bytes()
}

func TestErrors(t *testing.T) {
	good := sampleXLSX(t)
	cases := map[string][]byte{
		"not a zip":        []byte("hello"),
		"empty":            nil,
		"no content types": makeZip(t, map[string]string{"a.txt": "x"}),
		"unrecognised format": makeZip(t, map[string]string{
			contentTypesPart: `<Types xmlns="x"><Override PartName="/a.xml" ContentType="text/plain"/></Types>`,
			"a.xml":          "<a/>",
		}),
		"missing workbook": rewrite(t, good, func(n string, b []byte) []byte {
			if n == "xl/workbook.xml" {
				return nil
			}
			return b
		}),
		"malformed sheet": rewrite(t, good, func(n string, b []byte) []byte {
			if n == "xl/worksheets/sheet1.xml" {
				return []byte("<worksheet><sheetData><row>")
			}
			return b
		}),
		"malformed content types": rewrite(t, good, func(n string, b []byte) []byte {
			if n == contentTypesPart {
				return []byte("<Types><x></Types>")
			}
			return b
		}),
		"dotdot entry":   addEntry(t, good, "../evil.xml", "<a/>"),
		"absolute entry": addEntry(t, good, "/evil.xml", "<a/>"),
	}
	for name, data := range cases {
		if _, err := Read(data); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	for _, f := range []struct{ name, part string }{{"docx", "word/document.xml"}, {"pptx", "ppt/presentation.xml"}} {
		var data []byte
		if f.name == "docx" {
			var buf bytes.Buffer
			docx.Write(&buf, docx.Document{Blocks: []docx.Block{docx.Paragraph{Text: "x"}}})
			data = buf.Bytes()
		} else {
			data = samplePPTX(t)
		}
		data = rewrite(t, data, func(n string, b []byte) []byte {
			if n == f.part {
				return nil
			}
			return b
		})
		if _, err := Read(data); err == nil {
			t.Errorf("missing %s: no error", f.part)
		}
	}
}

func TestTruncatedInputNeverPanics(t *testing.T) {
	var d bytes.Buffer
	docx.Write(&d, docx.Document{Blocks: []docx.Block{docx.Heading{Level: 1, Text: "h"}, docx.Bullets{Items: []string{"a"}}}})
	for name, good := range map[string][]byte{"xlsx": sampleXLSX(t), "docx": d.Bytes(), "pptx": samplePPTX(t)} {
		step := len(good)/300 + 1
		for n := 0; n < len(good); n += step {
			if _, err := Read(good[:n]); err == nil {
				t.Fatalf("%s truncated to %d bytes: no error", name, n)
			}
		}
		if _, err := Read(good); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func TestSizeLimits(t *testing.T) {
	oldE, oldT := maxEntry, maxTotal
	defer func() { maxEntry, maxTotal = oldE, oldT }()
	data := sampleXLSX(t)
	maxEntry = 100
	if _, err := Read(data); err == nil || !strings.Contains(err.Error(), "larger") || !errors.Is(err, ErrTooLarge) {
		t.Errorf("entry limit: %v", err)
	}
	maxEntry, maxTotal = oldE, 500
	if _, err := Read(data); err == nil || !strings.Contains(err.Error(), "larger") || !errors.Is(err, ErrTooLarge) {
		t.Errorf("total limit: %v", err)
	}
}

func TestSparseSheetTooLarge(t *testing.T) {
	sheet := `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
		`<row r="1048576"><c r="XFD1048576"><v>1</v></c></row></sheetData></worksheet>`
	data := rewrite(t, sampleXLSX(t), func(n string, b []byte) []byte {
		if n == "xl/worksheets/sheet1.xml" {
			return []byte(sheet)
		}
		return b
	})
	if _, err := Read(data); err == nil {
		t.Error("no error for a huge sparse sheet")
	}
}
