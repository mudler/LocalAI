package pptxedit

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/addons/office/internal/deck"
	"github.com/mudler/LocalAI/addons/office/internal/deck/pptx"
	"github.com/mudler/LocalAI/addons/office/internal/officecheck"
	"github.com/mudler/LocalAI/addons/office/internal/officeread"
)

const testLayout = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
	`<p:sldLayout xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" type="obj" preserve="1">` +
	`<p:cSld name="Title and Content"><p:spTree>` + groupHeader +
	`<p:sp><p:nvSpPr><p:cNvPr id="2" name="Title 1"/><p:cNvSpPr/><p:nvPr><p:ph type="title"/></p:nvPr></p:nvSpPr><p:spPr/><p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:t>Click to edit</a:t></a:r></a:p></p:txBody></p:sp>` +
	`<p:sp><p:nvSpPr><p:cNvPr id="3" name="Content 2"/><p:cNvSpPr/><p:nvPr><p:ph idx="1"/></p:nvPr></p:nvSpPr><p:spPr/><p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:t>Click to edit</a:t></a:r></a:p></p:txBody></p:sp>` +
	`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>`

const (
	slide1Shapes = `<p:sp><p:nvSpPr><p:cNvPr id="90" name="Title 1"/><p:cNvSpPr/><p:nvPr><p:ph type="ctrTitle"/></p:nvPr></p:nvSpPr><p:spPr/>` +
		`<p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr lang="en-US" b="1"/><a:t>Deck title</a:t></a:r></a:p></p:txBody></p:sp>`
	slide2Shapes = `<p:sp><p:nvSpPr><p:cNvPr id="90" name="Title 1"/><p:cNvSpPr/><p:nvPr><p:ph type="title"/></p:nvPr></p:nvSpPr><p:spPr/>` +
		`<p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr lang="en-US" sz="3000"/><a:t>Findings</a:t></a:r></a:p></p:txBody></p:sp>` +
		`<p:sp><p:nvSpPr><p:cNvPr id="91" name="Body 2"/><p:cNvSpPr/><p:nvPr><p:ph idx="1"/></p:nvPr></p:nvSpPr><p:spPr/>` +
		`<p:txBody><a:bodyPr/><a:lstStyle/>` +
		`<a:p><a:pPr marL="342900" indent="-342900"/><a:r><a:rPr lang="en-US" b="1"/><a:t>Alpha </a:t></a:r><a:r><a:rPr lang="en-US" i="1"/><a:t>beta</a:t></a:r></a:p>` +
		`<a:p><a:r><a:rPr lang="en-US"/><a:t>Gamma</a:t></a:r></a:p></p:txBody></p:sp>` +
		`<p:sp><p:nvSpPr><p:cNvPr id="92" name="Footer 3"/><p:cNvSpPr/><p:nvPr><p:ph type="ftr" idx="11"/></p:nvPr></p:nvSpPr><p:spPr/>` +
		`<p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:t>Footer text</a:t></a:r></a:p></p:txBody></p:sp>`
)

// rewriteZip returns data with the named parts changed by fn, in the same order.
func rewriteZip(t *testing.T, data []byte, fns map[string]func(string) string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
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
		if fn := fns[f.Name]; fn != nil {
			b = []byte(fn(string(b)))
		}
		w, err := zw.Create(f.Name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(b)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func addShapes(shapes string) func(string) string {
	return func(s string) string {
		return strings.Replace(s, "</p:spTree>", shapes+"</p:spTree>", 1)
	}
}

// replaceShapes drops every shape of a slide written by deck/pptx and puts
// shapes in their place.
func replaceShapes(shapes string) func(string) string {
	return func(s string) string {
		start := strings.Index(s, groupHeader) + len(groupHeader)
		end := strings.Index(s, "</p:spTree>")
		return s[:start] + shapes + s[end:]
	}
}

// sourceDeck builds a three-slide deck with pptx.Write (slide 1 has notes),
// then gives it a layout with placeholders and title and body shapes on the
// first two slides, as a deck made by an office application has. The third
// slide has no title, so it has no title placeholder.
func sourceDeck(t *testing.T) []byte {
	t.Helper()
	slides := []deck.StudioSlide{
		{ID: "s1", Title: "Opening", Layout: deck.LayoutTitle, Status: deck.SlideOK, Notes: "Say hello first."},
		{ID: "s2", Title: "Second", Layout: deck.LayoutText, Status: deck.SlideOK, Bullets: []deck.StudioBullet{{Text: "Writer bullet"}}},
		{ID: "s3", Title: "", Layout: deck.LayoutText, Status: deck.SlideOK, Bullets: []deck.StudioBullet{{Text: "Last point"}}},
	}
	var buf bytes.Buffer
	if err := pptx.Write(&buf, slides, pptx.Options{Title: "Source", Application: "test", Notes: true}, nil); err != nil {
		t.Fatal(err)
	}
	return rewriteZip(t, buf.Bytes(), map[string]func(string) string{
		"ppt/slideLayouts/slideLayout2.xml": func(string) string { return testLayout },
		"ppt/slides/slide1.xml":             replaceShapes(slide1Shapes),
		"ppt/slides/slide2.xml":             replaceShapes(slide2Shapes),
	})
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

func mustCheckClean(t *testing.T, data []byte) {
	t.Helper()
	if probs := officecheck.Check(data); len(probs) != 0 {
		t.Fatalf("officecheck problems: %v", probs)
	}
}

// result returns the bytes of d, checked by officecheck, and the text of each slide.
func result(t *testing.T, d *Deck) ([]byte, []string) {
	t.Helper()
	out, err := d.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	mustCheckClean(t, out)
	doc, err := officeread.Read(out)
	if err != nil {
		t.Fatalf("officeread: %v", err)
	}
	var texts []string
	for _, p := range doc.Parts {
		texts = append(texts, p.Text)
	}
	return out, texts
}

func openSource(t *testing.T) (*Deck, []byte) {
	t.Helper()
	src := sourceDeck(t)
	mustCheckClean(t, src)
	d, err := Open(src)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return d, src
}

func TestOpenAndSlides(t *testing.T) {
	d, src := openSource(t)
	got := d.Slides()
	if len(got) != 3 {
		t.Fatalf("%d slides, want 3", len(got))
	}
	for i, s := range got {
		if s.Index != i+1 {
			t.Errorf("slide %d has Index %d", i+1, s.Index)
		}
	}
	if got[0].Title != "Deck title" || got[1].Title != "Findings" || got[2].Title != "" {
		t.Errorf("titles: %q %q %q", got[0].Title, got[1].Title, got[2].Title)
	}
	if !contains(got[1].Text, "Alpha beta") || !contains(got[1].Text, "Gamma") {
		t.Errorf("slide 2 text: %q", got[1].Text)
	}
	if contains(got[1].Text, "Footer text") {
		t.Errorf("footer placeholder text must not be listed: %q", got[1].Text)
	}
	// Text matches what officeread reads.
	doc, err := officeread.Read(src)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range got {
		want := strings.Split(doc.Parts[i].Text, "\n")
		if i == 0 { // officeread appends the notes after a "Notes:" line
			want = want[:len(want)-2]
		}
		if strings.Join(s.Text, "\n") != strings.Join(want, "\n") {
			t.Errorf("slide %d text %q, officeread %q (%q)", i+1, s.Text, want, doc.Parts[i].Text)
		}
	}
	// Opening and saving without an edit changes nothing.
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	assertSameParts(t, src, out, nil)
}

func contains(lines []string, s string) bool {
	for _, l := range lines {
		if l == s {
			return true
		}
	}
	return false
}

// assertSameParts fails unless every part of a and b outside changed is
// byte-identical and the common parts keep their order.
func assertSameParts(t *testing.T, a, b []byte, changed map[string]bool) {
	t.Helper()
	an, ap := zipParts(t, a)
	bn, bp := zipParts(t, b)
	var common []string
	for _, n := range an {
		if _, ok := bp[n]; ok {
			common = append(common, n)
		}
	}
	var bcommon []string
	for _, n := range bn {
		if _, ok := ap[n]; ok {
			bcommon = append(bcommon, n)
		}
	}
	if strings.Join(common, "|") != strings.Join(bcommon, "|") {
		t.Errorf("part order changed:\n%v\n%v", common, bcommon)
	}
	for _, n := range common {
		if changed[n] {
			if bytes.Equal(ap[n], bp[n]) {
				t.Errorf("%s should have changed", n)
			}
			continue
		}
		if !bytes.Equal(ap[n], bp[n]) {
			t.Errorf("%s changed but should not have", n)
		}
	}
}

func TestSetTitle(t *testing.T) {
	d, src := openSource(t)
	if err := d.SetTitle(1, `R&D <review> "2026"`); err != nil {
		t.Fatal(err)
	}
	if err := d.SetTitle(2, "What we found"); err != nil {
		t.Fatal(err)
	}
	out, texts := result(t, d)
	if !strings.Contains(texts[0], `R&D <review> "2026"`) || strings.Contains(texts[0], "Deck title") {
		t.Errorf("slide 1: %q", texts[0])
	}
	if !strings.Contains(texts[1], "What we found") || strings.Contains(texts[1], "Findings") {
		t.Errorf("slide 2: %q", texts[1])
	}
	if !strings.Contains(texts[1], "Alpha beta") {
		t.Errorf("body of slide 2 changed: %q", texts[1])
	}
	re, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := re.Slides()[0].Title; got != `R&D <review> "2026"` {
		t.Errorf("reopened title %q", got)
	}
	_, parts := zipParts(t, out)
	if !strings.Contains(string(parts["ppt/slides/slide1.xml"]), `<a:rPr lang="en-US" b="1"/>`) {
		t.Error("run properties of the title were lost")
	}
	assertSameParts(t, src, out, map[string]bool{"ppt/slides/slide1.xml": true, "ppt/slides/slide2.xml": true})
}

func TestSetTitleDropsExtraParagraphs(t *testing.T) {
	src := rewriteZip(t, sourceDeck(t), map[string]func(string) string{
		"ppt/slides/slide2.xml": func(s string) string {
			return strings.Replace(s, `<a:t>Findings</a:t></a:r></a:p>`, `<a:t>Findings</a:t></a:r></a:p><a:p><a:r><a:t>Second line</a:t></a:r></a:p>`, 1)
		},
	})
	d, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Slides()[1].Title; got != "Findings\nSecond line" {
		t.Fatalf("title %q", got)
	}
	if err := d.SetTitle(2, "One"); err != nil {
		t.Fatal(err)
	}
	_, texts := result(t, d)
	if strings.Contains(texts[1], "Second line") || !strings.HasPrefix(texts[1], "One\nAlpha beta\n") {
		t.Errorf("slide 2: %q", texts[1])
	}
}

func TestSetTitleWithoutTextBody(t *testing.T) {
	src := rewriteZip(t, sourceDeck(t), map[string]func(string) string{
		"ppt/slides/slide1.xml": func(s string) string {
			i := strings.Index(s, `<p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr lang="en-US" b="1"/><a:t>Deck title`)
			j := strings.Index(s[i:], "</p:txBody>") + len("</p:txBody>")
			return s[:i] + s[i+j:]
		},
	})
	d, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.SetTitle(1, "Fresh"); err != nil {
		t.Fatal(err)
	}
	_, texts := result(t, d)
	if !strings.Contains(texts[0], "Fresh") {
		t.Errorf("slide 1: %q", texts[0])
	}
}

func TestSetParagraph(t *testing.T) {
	d, src := openSource(t)
	if err := d.SetParagraph(2, "Body 2", 0, "Delta & <epsilon>\nnext line"); err != nil {
		t.Fatal(err)
	}
	out, texts := result(t, d)
	if !strings.Contains(texts[1], "Delta & <epsilon>\nnext line") {
		t.Errorf("slide 2: %q", texts[1])
	}
	if strings.Contains(texts[1], "Alpha") || strings.Contains(texts[1], "beta") {
		t.Errorf("old runs remain: %q", texts[1])
	}
	if !strings.Contains(texts[1], "Gamma") {
		t.Errorf("other paragraph lost: %q", texts[1])
	}
	_, parts := zipParts(t, out)
	x := string(parts["ppt/slides/slide2.xml"])
	if !strings.Contains(x, `<a:pPr marL="342900" indent="-342900"/><a:r><a:rPr lang="en-US" b="1"/><a:t>Delta &amp; &lt;epsilon&gt;</a:t></a:r><a:br/>`) {
		t.Errorf("formatting of the first run not kept:\n%s", x)
	}
	if strings.Contains(x, `i="1"`) {
		t.Error("the properties of the dropped run remain")
	}
	assertSameParts(t, src, out, map[string]bool{"ppt/slides/slide2.xml": true})

	// The second paragraph, and an emptied one.
	if err := d.SetParagraph(2, "Body 2", 1, ""); err != nil {
		t.Fatal(err)
	}
	_, texts = result(t, d)
	if strings.Contains(texts[1], "Gamma") {
		t.Errorf("slide 2: %q", texts[1])
	}
}

func TestAddSlide(t *testing.T) {
	d, src := openSource(t)
	if err := d.AddSlide("Title and Content", "Next steps & risks", []string{"Ship it", "Watch <errors>", "Rest"}); err != nil {
		t.Fatal(err)
	}
	out, texts := result(t, d)
	if len(texts) != 4 {
		t.Fatalf("%d slides, want 4", len(texts))
	}
	for _, want := range []string{"Next steps & risks", "Ship it", "Watch <errors>", "Rest"} {
		if !strings.Contains(texts[3], want) {
			t.Errorf("new slide lacks %q: %q", want, texts[3])
		}
	}
	re, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	s := re.Slides()[3]
	if s.Index != 4 || s.Title != "Next steps & risks" || len(s.Text) != 4 {
		t.Errorf("new slide: %+v", s)
	}
	names, parts := zipParts(t, out)
	if names[len(names)-1] != "ppt/slides/_rels/slide4.xml.rels" || names[len(names)-2] != "ppt/slides/slide4.xml" {
		t.Errorf("new parts are not at the end: %v", names[len(names)-3:])
	}
	if !strings.Contains(string(parts["docProps/app.xml"]), "<Slides>4</Slides>") {
		t.Error("slide count in app.xml not updated")
	}
	assertSameParts(t, src, out, map[string]bool{
		"[Content_Types].xml":             true,
		"ppt/presentation.xml":            true,
		"ppt/_rels/presentation.xml.rels": true,
		"docProps/app.xml":                true,
	})

	// An empty bullet list gives an empty body; the title may be empty.
	if err := d.AddSlide("Title and Content", "", nil); err != nil {
		t.Fatal(err)
	}
	_, texts = result(t, d)
	if len(texts) != 5 {
		t.Fatalf("%d slides, want 5", len(texts))
	}
}

func TestDeleteSlide(t *testing.T) {
	d, src := openSource(t)
	// Slide 1 has notes: the notes page must go with it.
	if err := d.DeleteSlide(1); err != nil {
		t.Fatal(err)
	}
	out, texts := result(t, d)
	if len(texts) != 2 {
		t.Fatalf("%d slides, want 2", len(texts))
	}
	all := strings.Join(texts, "\n")
	for _, gone := range []string{"Deck title", "Opening", "Say hello first."} {
		if strings.Contains(all, gone) {
			t.Errorf("deleted slide text %q remains", gone)
		}
	}
	if !strings.Contains(texts[0], "Findings") {
		t.Errorf("slide 2 did not move up: %q", texts[0])
	}
	names, parts := zipParts(t, out)
	for _, n := range names {
		if n == "ppt/slides/slide1.xml" || n == "ppt/slides/_rels/slide1.xml.rels" || strings.Contains(n, "notesSlide") {
			t.Errorf("part %s should be gone", n)
		}
	}
	for n, b := range parts {
		if strings.Contains(string(b), "slide1.xml") && n != "[Content_Types].xml" && !strings.Contains(n, "notesMaster") {
			t.Errorf("%s still mentions slide1.xml", n)
		}
	}
	if !strings.Contains(string(parts["docProps/app.xml"]), "<Slides>2</Slides>") {
		t.Error("slide count in app.xml not updated")
	}
	if strings.Contains(string(parts["[Content_Types].xml"]), "slide1.xml") || strings.Contains(string(parts["[Content_Types].xml"]), "notesSlide1") {
		t.Error("content type overrides remain")
	}
	// Layout, master and theme stay.
	for _, keep := range []string{"ppt/slideLayouts/slideLayout1.xml", "ppt/slideMasters/slideMaster1.xml", "ppt/theme/theme1.xml"} {
		if _, ok := parts[keep]; !ok {
			t.Errorf("%s was removed", keep)
		}
	}
	_ = src

	// Delete a middle slide of a bigger deck, then add again: ids stay unique.
	if err := d.AddSlide("Title and Content", "Again", []string{"x"}); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteSlide(1); err != nil {
		t.Fatal(err)
	}
	if err := d.AddSlide("Title and Content", "Once more", nil); err != nil {
		t.Fatal(err)
	}
	_, texts = result(t, d)
	if len(texts) != 3 || !strings.Contains(texts[1], "Again") || !strings.Contains(texts[2], "Once more") {
		t.Errorf("slides: %q", texts)
	}
	out, _ = result(t, d)
	_, parts = zipParts(t, out)
	pres := string(parts["ppt/presentation.xml"])
	if strings.Count(pres, "<p:sldId ") != 3 {
		t.Errorf("sldIdLst: %s", pres)
	}
}

func TestDeleteSlideDropsOwnImages(t *testing.T) {
	src := sourceDeck(t)
	src = rewriteZip(t, src, map[string]func(string) string{
		"ppt/slides/_rels/slide3.xml.rels": func(s string) string {
			return strings.Replace(s, "</Relationships>", `<Relationship Id="rId9" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="../media/image9.png"/></Relationships>`, 1)
		},
	})
	// Add the media part with one more rewrite pass that appends it.
	var buf bytes.Buffer
	zr, _ := zip.NewReader(bytes.NewReader(src), int64(len(src)))
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		w, _ := zw.Create(f.Name)
		w.Write(b)
	}
	w, _ := zw.Create("ppt/media/image9.png")
	w.Write([]byte("png"))
	zw.Close()
	d, err := Open(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteSlide(3); err != nil {
		t.Fatal(err)
	}
	out, _ := result(t, d)
	names, _ := zipParts(t, out)
	for _, n := range names {
		if strings.Contains(n, "image9") {
			t.Errorf("image used only by the deleted slide remains: %s", n)
		}
	}
}

func TestEditErrors(t *testing.T) {
	d, src := openSource(t)
	tests := []struct {
		name string
		do   func() error
	}{
		{"unknown shape", func() error { return d.SetParagraph(2, "Nope", 0, "x") }},
		{"paragraph out of range", func() error { return d.SetParagraph(2, "Body 2", 2, "x") }},
		{"negative paragraph", func() error { return d.SetParagraph(2, "Body 2", -1, "x") }},
		{"slide out of range (SetParagraph)", func() error { return d.SetParagraph(9, "Body 2", 0, "x") }},
		{"slide zero (SetParagraph)", func() error { return d.SetParagraph(0, "Body 2", 0, "x") }},
		{"slide out of range (SetTitle)", func() error { return d.SetTitle(4, "x") }},
		{"no title placeholder", func() error { return d.SetTitle(3, "x") }},
		{"unknown layout", func() error { return d.AddSlide("No such layout", "t", nil) }},
		{"slide out of range (DeleteSlide)", func() error { return d.DeleteSlide(4) }},
		{"slide negative (DeleteSlide)", func() error { return d.DeleteSlide(-1) }},
	}
	for _, tc := range tests {
		if err := tc.do(); err == nil {
			t.Errorf("%s: no error", tc.name)
		}
	}
	// None of the failed edits changed the deck.
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	assertSameParts(t, src, out, nil)

	// The last slide stays.
	if err := d.DeleteSlide(1); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteSlide(1); err != nil {
		t.Fatal(err)
	}
	if err := d.DeleteSlide(1); err == nil {
		t.Error("deleting the last slide must fail")
	}
	if n := len(d.Slides()); n != 1 {
		t.Errorf("%d slides left, want 1", n)
	}
	result(t, d)
}

func TestAddSlideNeedsPlaceholders(t *testing.T) {
	// The writer's own layout is blank: no title and no body.
	var buf bytes.Buffer
	if err := pptx.Write(&buf, []deck.StudioSlide{{ID: "a", Title: "A", Layout: deck.LayoutText, Status: deck.SlideOK}}, pptx.Options{}, nil); err != nil {
		t.Fatal(err)
	}
	d, err := Open(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AddSlide("Blank", "title", nil); err == nil {
		t.Error("a title for a layout without a title placeholder must fail")
	}
	if err := d.AddSlide("Blank", "", []string{"x"}); err == nil {
		t.Error("bullets for a layout without a body placeholder must fail")
	}
	if err := d.AddSlide("Blank", "", nil); err != nil {
		t.Fatal(err)
	}
	if len(d.Slides()) != 2 {
		t.Error("blank slide not added")
	}
	result(t, d)
}

func TestOpenRejects(t *testing.T) {
	var plain bytes.Buffer
	zw := zip.NewWriter(&plain)
	w, _ := zw.Create("a.txt")
	w.Write([]byte("hello"))
	zw.Close()

	var traversal bytes.Buffer
	zw = zip.NewWriter(&traversal)
	w, _ = zw.Create("../evil.xml")
	w.Write([]byte("<a/>"))
	zw.Close()

	src := sourceDeck(t)
	broken := rewriteZip(t, src, map[string]func(string) string{
		"ppt/presentation.xml": func(s string) string { return s[:len(s)/2] },
	})
	noSlides := rewriteZip(t, src, map[string]func(string) string{
		"ppt/_rels/presentation.xml.rels": func(s string) string {
			return strings.ReplaceAll(s, `Target="slides/slide2.xml"`, `Target="slides/missing.xml"`)
		},
	})
	for name, data := range map[string][]byte{
		"nil":          nil,
		"text":         []byte("not a deck"),
		"plain zip":    plain.Bytes(),
		"traversal":    traversal.Bytes(),
		"broken xml":   broken,
		"missing part": noSlides,
		"truncated":    src[:len(src)/2],
	} {
		if d, err := Open(data); err == nil {
			t.Errorf("%s: Open succeeded (%v)", name, d)
		}
	}
}

func TestOpenIsNotFooledByXLSX(t *testing.T) {
	// A zip with content types and a main part that is not a presentation.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"/>`,
		"_rels/.rels":         `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`,
		"xl/workbook.xml":     `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"/>`,
	} {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	if _, err := Open(buf.Bytes()); err == nil {
		t.Error("an xlsx must not open")
	}
}

// TestMalformedPartsNeverPanic cuts each XML part in half and runs every
// operation: each must return, with an error or not, and never panic.
func TestMalformedPartsNeverPanic(t *testing.T) {
	src := sourceDeck(t)
	names, _ := zipParts(t, src)
	for _, name := range names {
		if !strings.HasSuffix(name, ".xml") && !strings.HasSuffix(name, ".rels") {
			continue
		}
		for _, cut := range []func(string) string{
			func(s string) string { return s[:len(s)/2] },
			func(s string) string { return "" },
			func(s string) string { return strings.ReplaceAll(s, "<", "<<") },
		} {
			data := rewriteZip(t, src, map[string]func(string) string{name: cut})
			d, err := Open(data)
			if err != nil {
				continue
			}
			_ = d.Slides()
			_ = d.SetTitle(1, "x")
			_ = d.SetParagraph(2, "Body 2", 0, "x")
			_ = d.AddSlide("Title and Content", "t", []string{"b"})
			_ = d.DeleteSlide(1)
			_, _ = d.Bytes()
		}
	}
}
