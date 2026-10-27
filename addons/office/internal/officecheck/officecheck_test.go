package officecheck

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/addons/office/internal/deck"
	"github.com/mudler/LocalAI/addons/office/internal/deck/pptx"
	"github.com/mudler/LocalAI/addons/office/internal/docx"
	"github.com/mudler/LocalAI/addons/office/internal/xlsx"
)

func goodXLSX(t *testing.T) []byte {
	t.Helper()
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
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func goodDOCX(t *testing.T) []byte {
	t.Helper()
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
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func goodPPTX(t *testing.T) []byte {
	t.Helper()
	slides := []deck.StudioSlide{
		{ID: "s1", Title: "Intro", Layout: deck.LayoutTitle, Status: deck.SlideOK, Notes: "say hi"},
		{ID: "s2", Title: "Points", Layout: deck.LayoutText, Status: deck.SlideOK, Bullets: []deck.StudioBullet{{Text: "One"}, {Text: "Two"}}},
	}
	var buf bytes.Buffer
	if err := pptx.Write(&buf, slides, pptx.Options{Title: "Deck", Notes: true}, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// rewrite returns a copy of zipped with one entry changed. fn gets the entry
// content and returns the new content; a nil result drops the entry.
func rewrite(t *testing.T, zipped []byte, name string, fn func([]byte) []byte) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	found := false
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		if _, err := b.ReadFrom(rc); err != nil {
			t.Fatal(err)
		}
		rc.Close()
		data := b.Bytes()
		if f.Name == name {
			found = true
			if data = fn(data); data == nil {
				continue
			}
		}
		w, err := zw.Create(f.Name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(data)
	}
	if !found {
		t.Fatalf("entry %s not in the package", name)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// replace returns an fn that swaps old for new once and fails when old is absent.
func replace(t *testing.T, old, new string) func([]byte) []byte {
	return func(b []byte) []byte {
		if !strings.Contains(string(b), old) {
			t.Fatalf("%q not found in part", old)
		}
		return []byte(strings.Replace(string(b), old, new, 1))
	}
}

func drop([]byte) []byte { return nil }

func wantProblem(t *testing.T, data []byte, part, substr string) {
	t.Helper()
	ps := Check(data)
	for _, p := range ps {
		if p.Part == part && strings.Contains(p.Message, substr) {
			return
		}
	}
	t.Errorf("want a problem in %q containing %q, got %v", part, substr, ps)
}

func TestCleanFilesFromTheWriters(t *testing.T) {
	for name, data := range map[string][]byte{"xlsx": goodXLSX(t), "docx": goodDOCX(t), "pptx": goodPPTX(t)} {
		if ps := Check(data); len(ps) != 0 {
			t.Errorf("%s: want no problem, got %v", name, ps)
		}
	}
}

func TestNotAZip(t *testing.T) {
	for _, in := range [][]byte{nil, []byte("hello"), goodDOCX(t)[:100]} {
		ps := Check(in)
		if len(ps) != 1 || !strings.Contains(ps[0].Message, "zip") {
			t.Errorf("want one zip problem, got %v", ps)
		}
	}
}

func TestUnknownFormat(t *testing.T) {
	data := rewrite(t, goodDOCX(t), "[Content_Types].xml", replace(t, "wordprocessingml.document.main+xml", "text/plain"))
	wantProblem(t, data, "[Content_Types].xml", "format not recognised")
}

func TestZipEntryNames(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("../evil.xml")
	w.Write([]byte("<a/>"))
	zw.Close()
	wantProblem(t, buf.Bytes(), "../evil.xml", "absolute or contains ..")

	// Also in an otherwise good package.
	good := goodDOCX(t)
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
	wantProblem(t, out.Bytes(), "word/../../x.xml", "absolute or contains ..")
}

func TestMissingContentType(t *testing.T) {
	data := rewrite(t, goodDOCX(t), "[Content_Types].xml", replace(t, `<Override PartName="/word/document.xml"`, `<Override PartName="/word/other.xml"`))
	wantProblem(t, data, "[Content_Types].xml", "word/other.xml")

	// A part with an extension that has no Default.
	data = rewrite(t, goodXLSX(t), "[Content_Types].xml", replace(t, `<Override PartName="/xl/sharedStrings.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"/>`, ""))
	if ps := Check(data); len(ps) != 0 {
		// sharedStrings.xml still has the Default xml type, so it is fine.
		t.Errorf("a part covered by a Default type must pass, got %v", ps)
	}
	data = renameEntry(t, goodDOCX(t), "word/styles.xml", "word/styles.bin")
	wantProblem(t, data, "word/styles.bin", "no content type")
}

func renameEntry(t *testing.T, zipped []byte, from, to string) []byte {
	t.Helper()
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

func TestBrokenRelationshipTarget(t *testing.T) {
	data := rewrite(t, goodXLSX(t), "xl/_rels/workbook.xml.rels", replace(t, `Target="styles.xml"`, `Target="nostyles.xml"`))
	wantProblem(t, data, "xl/_rels/workbook.xml.rels", "xl/nostyles.xml")

	data = rewrite(t, goodDOCX(t), "_rels/.rels", replace(t, `Target="word/document.xml"`, `Target="word/missing.xml"`))
	wantProblem(t, data, "_rels/.rels", "word/missing.xml")
	wantProblem(t, data, "_rels/.rels", "no officeDocument relationship points at word/document.xml")

	data = rewrite(t, goodDOCX(t), "_rels/.rels", replace(t, `Target="word/document.xml"`, `Target="../../escape.xml"`))
	wantProblem(t, data, "_rels/.rels", "leaves the package")
}

func TestRelationshipsRelativeToOwnerFolder(t *testing.T) {
	// "../docProps/core.xml" from xl/ is valid; the checker must not report it.
	data := rewrite(t, goodXLSX(t), "xl/_rels/workbook.xml.rels", replace(t, `Target="styles.xml"`, `Target="../xl/styles.xml"`))
	if ps := Check(data); len(ps) != 0 {
		t.Errorf("want no problem, got %v", ps)
	}
	// An external target is not looked up.
	data = rewrite(t, goodXLSX(t), "xl/_rels/workbook.xml.rels", replace(t, `Target="styles.xml"`, `Target="https://example.com/x" TargetMode="External"`))
	wantProblem(t, rewrite(t, data, "xl/styles.xml", drop), "xl/styles.xml", "required part is missing")
}

func TestMalformedXML(t *testing.T) {
	data := rewrite(t, goodDOCX(t), "word/styles.xml", func(b []byte) []byte { return append(b[:len(b)-5:len(b)-5], []byte("<<")...) })
	wantProblem(t, data, "word/styles.xml", "not well-formed")
	data = rewrite(t, goodDOCX(t), "[Content_Types].xml", func(b []byte) []byte { return []byte("<Types>") })
	wantProblem(t, data, "[Content_Types].xml", "not well-formed")
	data = rewrite(t, goodDOCX(t), "_rels/.rels", func(b []byte) []byte { return []byte("not xml <") })
	wantProblem(t, data, "_rels/.rels", "not well-formed")
}

func TestRequiredParts(t *testing.T) {
	wantProblem(t, rewrite(t, goodPPTX(t), "ppt/presentation.xml", drop), "ppt/presentation.xml", "required part is missing")
	wantProblem(t, rewrite(t, goodDOCX(t), "word/document.xml", drop), "word/document.xml", "required part is missing")
	wantProblem(t, rewrite(t, goodXLSX(t), "xl/workbook.xml", drop), "xl/workbook.xml", "required part is missing")
	wantProblem(t, rewrite(t, goodXLSX(t), "xl/styles.xml", drop), "xl/styles.xml", "required part is missing")
	data := rewrite(t, goodXLSX(t), "xl/worksheets/sheet1.xml", drop)
	data = rewrite(t, data, "xl/worksheets/sheet2.xml", drop)
	wantProblem(t, data, "xl/worksheets", "no xl/worksheets/sheet*.xml")
}

func TestXLSXCells(t *testing.T) {
	const sheet = "xl/worksheets/sheet1.xml"
	wantProblem(t, rewrite(t, goodXLSX(t), sheet, replace(t, `<f>SUM(B2:B2)</f>`, `<f></f>`)), sheet, "<f> is empty")
	wantProblem(t, rewrite(t, goodXLSX(t), sheet, replace(t, `<c r="B2"`, `<c r="B2" s="99"`)), sheet, "style index")
	wantProblem(t, rewrite(t, goodXLSX(t), sheet, replace(t, `<c r="B2"`, `<c r="a0"`)), sheet, `"a0"`)
	wantProblem(t, rewrite(t, goodXLSX(t), sheet, replace(t, `<c r="B2"`, `<c r="XFE1"`)), sheet, `"XFE1"`)
	wantProblem(t, rewrite(t, goodXLSX(t), sheet, replace(t, `<c r="B2"`, `<c r="A1048577"`)), sheet, `"A1048577"`)
	wantProblem(t, rewrite(t, goodXLSX(t), sheet, replace(t, `<c r="B2"`, `<c r="A2"`)), sheet, "duplicate cell A2")
	for _, ok := range []string{"XFD1", "A1048576"} {
		if ps := Check(rewrite(t, goodXLSX(t), sheet, replace(t, `<c r="B2"`, `<c r="`+ok+`"`))); len(ps) != 0 {
			t.Errorf("%s is valid, got %v", ok, ps)
		}
	}
}

func TestXLSXSheetNames(t *testing.T) {
	long := strings.Repeat("x", 32)
	wantProblem(t, rewrite(t, goodXLSX(t), "xl/workbook.xml", replace(t, `name="Q1"`, `name="`+long+`"`)), "xl/workbook.xml", "limit is 31")
	wantProblem(t, rewrite(t, goodXLSX(t), "xl/workbook.xml", replace(t, `name="Q1"`, `name="a/b"`)), "xl/workbook.xml", "contains one of")
	if ps := Check(rewrite(t, goodXLSX(t), "xl/workbook.xml", replace(t, `name="Q1"`, `name="`+strings.Repeat("é", 31)+`"`))); len(ps) != 0 {
		t.Errorf("31 runes is valid, got %v", ps)
	}
}

func TestDOCXBodyAndNumbering(t *testing.T) {
	const doc = "word/document.xml"
	wantProblem(t, rewrite(t, goodDOCX(t), doc, replace(t, `<w:numId w:val="1"/>`, `<w:numId w:val="77"/>`)), doc, "numId 77")
	empty := func([]byte) []byte {
		return []byte(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body/></w:document>`)
	}
	wantProblem(t, rewrite(t, goodDOCX(t), doc, empty), doc, "no paragraph or table")
}

func TestPPTXSlideList(t *testing.T) {
	const pres = "ppt/presentation.xml"
	wantProblem(t, rewrite(t, goodPPTX(t), "ppt/slides/slide2.xml", drop), pres, "2 sldId entries but 1 slide parts")
	wantProblem(t, rewrite(t, goodPPTX(t), pres, replace(t, `<p:sldId id="257" r:id="rId`, `<p:sldId id="257" r:id="rIdNope`)), pres, "has no relationship")
	data := rewrite(t, goodPPTX(t), "ppt/_rels/presentation.xml.rels", replace(t, `slides/slide2.xml`, `slides/slide9.xml`))
	wantProblem(t, data, pres, "ppt/slides/slide9.xml")
}

func TestCheckNeverPanics(t *testing.T) {
	for _, data := range [][]byte{goodXLSX(t), goodDOCX(t), goodPPTX(t)} {
		for _, n := range []int{0, 1, 30, 200, len(data) / 2, len(data) - 1} {
			Check(data[:n])
		}
	}
	// Every part replaced by junk, one at a time.
	data := goodXLSX(t)
	zr, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	for _, f := range zr.File {
		junk := rewrite(t, data, f.Name, func([]byte) []byte { return []byte("\x00<\xff") })
		Check(junk)
	}
}
