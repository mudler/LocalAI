package docxedit

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/addons/office/internal/docx"
	"github.com/mudler/LocalAI/addons/office/internal/officecheck"
	"github.com/mudler/LocalAI/addons/office/internal/officeread"
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
func source(t *testing.T) []byte {
	t.Helper()
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
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func open(t *testing.T) *Document {
	t.Helper()
	d, err := Open(source(t))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// finish writes the document, checks it with officecheck and returns the
// bytes and the text read back.
func finish(t *testing.T, d *Document) ([]byte, string) {
	t.Helper()
	b, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if probs := officecheck.Check(b); len(probs) != 0 {
		t.Fatalf("officecheck problems: %v", probs)
	}
	r, err := officeread.Read(b)
	if err != nil {
		t.Fatal(err)
	}
	return b, r.Parts[0].Text
}

func texts(d *Document) []string {
	var out []string
	for _, p := range d.Paragraphs() {
		out = append(out, p.Text)
	}
	return out
}

func zipParts(t *testing.T, b []byte) (names []string, data map[string][]byte) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	data = map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		c, _ := io.ReadAll(rc)
		rc.Close()
		names = append(names, f.Name)
		data[f.Name] = c
	}
	return
}

func replacePart(t *testing.T, b []byte, name string, content string) []byte {
	t.Helper()
	names, data := zipParts(t, b)
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, n := range names {
		w, _ := zw.Create(n)
		if n == name {
			w.Write([]byte(content))
		} else {
			w.Write(data[n])
		}
	}
	zw.Close()
	return out.Bytes()
}

func TestOpenAndList(t *testing.T) {
	src := source(t)
	d, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
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
	if len(ps) != len(want) {
		t.Fatalf("got %d paragraphs, want %d: %+v", len(ps), len(want), ps)
	}
	for i := range want {
		if ps[i] != want[i] {
			t.Errorf("paragraph %d = %+v, want %+v", i, ps[i], want[i])
		}
	}
	r, err := officeread.Read(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"# Title", "Hello world, hello", "- alpha", "hello\thello table", "Last hello"} {
		if !strings.Contains(r.Parts[0].Text, s) {
			t.Errorf("source text lacks %q:\n%s", s, r.Parts[0].Text)
		}
	}
}

func TestNoEditKeepsEveryPart(t *testing.T) {
	src := source(t)
	d, _ := Open(src)
	b, _ := finish(t, d)
	n1, d1 := zipParts(t, src)
	n2, d2 := zipParts(t, b)
	if strings.Join(n1, ",") != strings.Join(n2, ",") {
		t.Fatalf("entry order changed: %v vs %v", n1, n2)
	}
	for _, n := range n1 {
		if !bytes.Equal(d1[n], d2[n]) {
			t.Errorf("part %s changed", n)
		}
	}
}

func TestSetText(t *testing.T) {
	d := open(t)
	if err := d.SetText(0, "New <title> & more"); err != nil {
		t.Fatal(err)
	}
	if err := d.SetText(2, "Rewritten"); err != nil {
		t.Fatal(err)
	}
	b, text := finish(t, d)
	if !strings.Contains(text, "# New <title> & more") {
		t.Errorf("heading not replaced (style lost?):\n%s", text)
	}
	if !strings.Contains(text, "Rewritten") || strings.Contains(text, "Bold lead") || strings.Contains(text, "plain tail") {
		t.Errorf("paragraph 2 not replaced:\n%s", text)
	}
	if !strings.Contains(text, "Hello world, hello") || !strings.Contains(text, "- alpha") {
		t.Errorf("other paragraphs changed:\n%s", text)
	}
	d2, err := Open(b)
	if err != nil {
		t.Fatal(err)
	}
	if ps := d2.Paragraphs(); ps[0].Style != "Heading1" || ps[2].Text != "Rewritten" {
		t.Errorf("reopened paragraphs wrong: %+v", ps)
	}
	// The first run's run properties (bold) are kept.
	_, data := zipParts(t, b)
	if !strings.Contains(string(data["word/document.xml"]), `<w:r><w:rPr><w:b/></w:rPr><w:t xml:space="preserve">Rewritten</w:t></w:r>`) {
		t.Errorf("run properties lost: %s", data["word/document.xml"])
	}
	// List numbering properties are kept.
	d3 := open(t)
	if err := d3.SetText(3, "first item"); err != nil {
		t.Fatal(err)
	}
	_, text = finish(t, d3)
	if !strings.Contains(text, "- first item") {
		t.Errorf("list paragraph lost its list properties:\n%s", text)
	}
}

func TestInsertParagraph(t *testing.T) {
	d := open(t)
	if err := d.InsertParagraph(1, "Heading2", "Inserted <h2>"); err != nil {
		t.Fatal(err)
	}
	if err := d.InsertParagraph(-1, "", "At the start"); err != nil {
		t.Fatal(err)
	}
	if err := d.InsertParagraph(len(d.Paragraphs())-1, "ListBullet", "At the end"); err != nil {
		t.Fatal(err)
	}
	b, text := finish(t, d)
	lines := strings.Split(text, "\n")
	if lines[0] != "At the start" || lines[1] != "# Title" || lines[2] != "Hello world, hello" || lines[3] != "## Inserted <h2>" {
		t.Errorf("unexpected order:\n%s", text)
	}
	if lines[len(lines)-1] != "- At the end" {
		t.Errorf("last line wrong:\n%s", text)
	}
	d2, _ := Open(b)
	if ps := d2.Paragraphs(); ps[3].Style != "Heading2" {
		t.Errorf("style not set: %+v", ps[3])
	}
}

func TestInsertUndefinedStyle(t *testing.T) {
	d := open(t)
	err := d.InsertParagraph(0, "NoSuchStyle", "x")
	if err == nil || !strings.Contains(err.Error(), "NoSuchStyle") {
		t.Fatalf("err = %v", err)
	}
	// A table style id is not a paragraph style.
	if err := d.InsertParagraph(0, "TableGrid", "x"); err == nil {
		t.Error("table style accepted as paragraph style")
	}
	if len(d.Paragraphs()) != 7 {
		t.Error("a failed insert changed the document")
	}
}

func TestDeleteParagraph(t *testing.T) {
	d := open(t)
	if err := d.DeleteParagraph(1); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteParagraph(2); err != nil { // was "alpha"
		t.Fatal(err)
	}
	_, text := finish(t, d)
	if strings.Contains(text, "Hello world") || strings.Contains(text, "alpha") {
		t.Errorf("paragraphs still there:\n%s", text)
	}
	if !strings.Contains(text, "- beta") || !strings.Contains(text, "hello\thello table") {
		t.Errorf("other content lost:\n%s", text)
	}
}

func TestDeleteGuards(t *testing.T) {
	// Paragraph 5 sits between the table and paragraph 6, so it can go.
	d := open(t)
	if err := d.DeleteParagraph(5); err != nil {
		t.Fatal(err)
	}
	finish(t, d)
	// Without paragraph 6 the table would end the body.
	d = open(t)
	if err := d.DeleteParagraph(6); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteParagraph(5); err == nil {
		t.Error("deleted the paragraph after the last table")
	}
	finish(t, d)
}

func TestReplaceAll(t *testing.T) {
	d := open(t)
	n, err := d.ReplaceAll("hello", "a<b & c")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("count = %d, want 2 (table cells are not searched)", n)
	}
	_, text := finish(t, d)
	if !strings.Contains(text, "Hello world, a<b & c") || !strings.Contains(text, "Last a<b & c") {
		t.Errorf("paragraphs not replaced:\n%s", text)
	}
	if !strings.Contains(text, "hello\thello table") {
		t.Errorf("table cell changed:\n%s", text)
	}
	if !strings.Contains(text, "Bold lead plain tail") {
		t.Errorf("untouched paragraph changed:\n%s", text)
	}
}

func TestReplaceAllAcrossRunsAndNoMatch(t *testing.T) {
	d := open(t)
	n, err := d.ReplaceAll("lead plain", "x") // spans two runs
	if err != nil || n != 0 {
		t.Fatalf("n = %d, err = %v", n, err)
	}
	n, err = d.ReplaceAll("lead", "head") // inside the bold run
	if err != nil || n != 1 {
		t.Fatalf("n = %d, err = %v", n, err)
	}
	_, text := finish(t, d)
	if !strings.Contains(text, "Bold head plain tail") {
		t.Errorf("text:\n%s", text)
	}
	if _, err := d.ReplaceAll("", "x"); err == nil {
		t.Error("empty search text accepted")
	}
}

func TestReplaceAllKeepsSpaces(t *testing.T) {
	// A w:t without xml:space gets it when the new text has edge spaces.
	src := source(t)
	_, data := zipParts(t, src)
	doc := strings.Replace(string(data["word/document.xml"]), `<w:t xml:space="preserve">Last hello</w:t>`, `<w:t>Last hello</w:t>`, 1)
	d, err := Open(replacePart(t, src, "word/document.xml", doc))
	if err != nil {
		t.Fatal(err)
	}
	if n, err := d.ReplaceAll("hello", " hi "); err != nil || n < 1 {
		t.Fatalf("n = %d, err = %v", n, err)
	}
	b, _ := finish(t, d)
	d2, _ := Open(b)
	ps := d2.Paragraphs()
	if ps[6].Text != "Last  hi " {
		t.Errorf("text = %q", ps[6].Text)
	}
}

func TestOnlyDocumentPartChanges(t *testing.T) {
	src := source(t)
	d, _ := Open(src)
	if err := d.SetText(1, "changed"); err != nil {
		t.Fatal(err)
	}
	b, _ := finish(t, d)
	n1, d1 := zipParts(t, src)
	n2, d2 := zipParts(t, b)
	if strings.Join(n1, ",") != strings.Join(n2, ",") {
		t.Fatalf("entry order changed: %v vs %v", n1, n2)
	}
	for _, n := range n1 {
		same := bytes.Equal(d1[n], d2[n])
		if n == "word/document.xml" && same {
			t.Error("document.xml did not change")
		}
		if n != "word/document.xml" && !same {
			t.Errorf("part %s changed", n)
		}
	}
	// Bytes outside the edited paragraph are untouched.
	old, cur := string(d1["word/document.xml"]), string(d2["word/document.xml"])
	i := strings.Index(old, "<w:p><w:r><w:t xml:space=\"preserve\">Hello")
	if i < 0 || !strings.HasPrefix(cur, old[:i]) {
		t.Error("prefix of document.xml changed")
	}
	j := strings.Index(old, "<w:p><w:r><w:rPr><w:b/>")
	if !strings.HasSuffix(cur, old[j:]) {
		t.Error("suffix of document.xml changed")
	}
}

func TestEditsCompose(t *testing.T) {
	d := open(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(d.InsertParagraph(0, "Heading2", "Section"))
	must(d.SetText(2, "Second"))
	must(d.DeleteParagraph(3))
	_, err := d.ReplaceAll("Last", "Final")
	must(err)
	_, text := finish(t, d)
	if !strings.HasPrefix(text, "# Title\n## Section\nSecond\n") || !strings.Contains(text, "Final hello") {
		t.Errorf("text:\n%s", text)
	}
	if strings.Contains(text, "Bold lead") {
		t.Errorf("deleted paragraph still there:\n%s", text)
	}
}

func TestErrors(t *testing.T) {
	d := open(t)
	for _, i := range []int{-1, 7, 100} {
		if err := d.SetText(i, "x"); err == nil {
			t.Errorf("SetText(%d) succeeded", i)
		}
		if err := d.DeleteParagraph(i); err == nil {
			t.Errorf("DeleteParagraph(%d) succeeded", i)
		}
	}
	for _, i := range []int{-2, 7} {
		if err := d.InsertParagraph(i, "", "x"); err == nil {
			t.Errorf("InsertParagraph(%d) succeeded", i)
		}
	}
	// Paragraph 5 is empty: it has no text run.
	err := d.SetText(5, "x")
	if err == nil || !strings.Contains(err.Error(), "no text run") {
		t.Errorf("SetText on empty paragraph: %v", err)
	}
	if len(d.Paragraphs()) != 7 {
		t.Error("failed edits changed the document")
	}
	finish(t, d)
}

func TestOpenErrors(t *testing.T) {
	src := source(t)
	bad := [][]byte{
		nil,
		[]byte("not a zip"),
		replacePart(t, src, "_rels/.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"></Relationships>`),
		replacePart(t, src, "_rels/.rels", `<Relationships`),
		replacePart(t, src, "word/document.xml", `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p>`),
		replacePart(t, src, "word/document.xml", `<html/>`),
		replacePart(t, src, "word/document.xml", `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"/>`),
		replacePart(t, src, "word/styles.xml", `<w:styles`),
	}
	// A zip with no main part.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("_rels/.rels")
	_, data := zipParts(t, src)
	w.Write(data["_rels/.rels"])
	zw.Close()
	bad = append(bad, buf.Bytes())
	for i, b := range bad {
		if d, err := Open(b); err == nil || d != nil {
			t.Errorf("case %d: Open succeeded", i)
		}
	}
}

func TestParagraphsInTablesAreNotAddressable(t *testing.T) {
	d := open(t)
	for _, s := range texts(d) {
		if strings.Contains(s, "table") || s == "Name" {
			t.Errorf("table text listed: %q", s)
		}
	}
}

func TestPlainDocumentWithoutStylesPart(t *testing.T) {
	src := source(t)
	_, data := zipParts(t, src)
	// Drop the relationship to the styles part: no style is defined.
	rels := strings.Replace(string(data["word/_rels/document.xml.rels"]), `Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles"`, `Type="urn:other"`, 1)
	d, err := Open(replacePart(t, src, "word/_rels/document.xml.rels", rels))
	if err != nil {
		t.Fatal(err)
	}
	if err := d.InsertParagraph(0, "Heading1", "x"); err == nil {
		t.Error("style accepted without a styles part")
	}
	if err := d.InsertParagraph(0, "", "x"); err != nil {
		t.Error(err)
	}
}
