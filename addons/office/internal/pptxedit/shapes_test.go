package pptxedit

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/addons/office/internal/deck"
	"github.com/mudler/LocalAI/addons/office/internal/deck/pptx"
)

// writerDeck is a deck made by pptx.Write: a titled bullet slide and a chart
// slide whose label is a plain text box.
func writerDeck(t *testing.T) []byte {
	t.Helper()
	slides := []deck.StudioSlide{
		{ID: "t", Title: "Findings", Layout: deck.LayoutText, Status: deck.SlideOK, Bullets: []deck.StudioBullet{{Text: "First"}, {Text: "Second"}}},
		{ID: "c", Title: "Costs", Layout: deck.LayoutChart, Status: deck.SlideOK, Chart: &deck.StudioChart{
			Kind: deck.ChartBar, Label: "Cost by office", Unit: "%", Data: []deck.ChartPoint{{K: "Rome", V: 12}, {K: "Milan", V: 49}},
		}},
	}
	var buf bytes.Buffer
	if err := pptx.Write(&buf, slides, pptx.Options{Title: "T", Style: deck.StylePaper}, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// findShape returns the first shape of the list with this kind and text, or
// fails the test.
func findShape(t *testing.T, list []ShapeInfo, kind, text string) ShapeInfo {
	t.Helper()
	for _, s := range list {
		if s.Kind == kind && len(s.Text) > 0 && s.Text[0] == text {
			return s
		}
	}
	t.Fatalf("no %s shape with text %q in %+v", kind, text, list)
	return ShapeInfo{}
}

func TestShapesOfGeneratedDeck(t *testing.T) {
	d, err := Open(writerDeck(t))
	if err != nil {
		t.Fatal(err)
	}
	list, err := d.Shapes(1)
	if err != nil {
		t.Fatal(err)
	}
	title := findShape(t, list, "title", "Findings")
	body := findShape(t, list, "body", "First")
	if strings.Join(body.Text, "|") != "First|Second" {
		t.Errorf("body paragraphs %q", body.Text)
	}
	if title.W <= 0 || title.H <= 0 || body.W <= 0 || body.H <= 0 {
		t.Errorf("geometry missing: title %+v body %+v", title, body)
	}
	seen := map[int]bool{}
	for _, s := range list {
		if s.ID <= 0 || seen[s.ID] {
			t.Errorf("shape id %d is zero or repeated", s.ID)
		}
		seen[s.ID] = true
	}

	chart, err := d.Shapes(2)
	if err != nil {
		t.Fatal(err)
	}
	label := findShape(t, chart, "text", "Cost by office")
	if label.Name == "" {
		t.Errorf("text box has no name: %+v", label)
	}
	findShape(t, chart, "title", "Costs")

	if _, err := d.Shapes(3); err == nil {
		t.Error("Shapes of a missing slide must fail")
	}
}

func TestSetShapeTextOnGeneratedDeck(t *testing.T) {
	d, err := Open(writerDeck(t))
	if err != nil {
		t.Fatal(err)
	}
	list, _ := d.Shapes(1)
	body := findShape(t, list, "body", "First")
	if err := d.SetShapeText(1, body.ID, 1, "Second & <final>\nline two"); err != nil {
		t.Fatal(err)
	}
	out, texts := result(t, d)
	if !strings.Contains(texts[0], "First") || !strings.Contains(texts[0], "Second & <final>\nline two") || strings.Contains(texts[0], "\nSecond\n") {
		t.Errorf("slide 1: %q", texts[0])
	}
	_, parts := zipParts(t, out)
	if !strings.Contains(string(parts["ppt/slides/slide1.xml"]), "<a:buChar") {
		t.Error("paragraph properties of the bullet were lost")
	}

	chart, _ := d.Shapes(2)
	label := findShape(t, chart, "text", "Cost by office")
	if err := d.SetShapeText(2, label.ID, 0, "Cost by region"); err != nil {
		t.Fatal(err)
	}
	_, texts = result(t, d)
	if strings.Contains(texts[1], "Cost by office") || !strings.Contains(texts[1], "Cost by region") {
		t.Errorf("slide 2: %q", texts[1])
	}
	re, err := Open(mustBytes(t, d))
	if err != nil {
		t.Fatal(err)
	}
	reChart, _ := re.Shapes(2)
	findShape(t, reChart, "text", "Cost by region")
}

func mustBytes(t *testing.T, d *Deck) []byte {
	t.Helper()
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

const plainBox = `<p:sp><p:nvSpPr><p:cNvPr id="77" name="Note box"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr>` +
	`<p:spPr><a:xfrm><a:off x="10" y="20"/><a:ext cx="300" cy="400"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr>` +
	`<p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr lang="en-US" i="1"/><a:t>Plain note</a:t></a:r></a:p></p:txBody></p:sp>`

const bareShape = `<p:sp><p:nvSpPr><p:cNvPr id="78" name="Bare"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr/></p:sp>`

// handBuilt is sourceDeck with a plain text box and a shape without a text
// body added to its untitled slide 3.
func handBuilt(t *testing.T) []byte {
	t.Helper()
	return rewriteZip(t, sourceDeck(t), map[string]func(string) string{
		"ppt/slides/slide3.xml": addShapes(plainBox + bareShape),
	})
}

func TestShapesAndSetTextOnHandBuiltDeck(t *testing.T) {
	src := handBuilt(t)
	d, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	first, err := d.Shapes(1)
	if err != nil {
		t.Fatal(err)
	}
	findShape(t, first, "title", "Deck title")

	list, err := d.Shapes(3)
	if err != nil {
		t.Fatal(err)
	}
	note := findShape(t, list, "text", "Plain note")
	if note.ID != 77 || note.X != 10 || note.Y != 20 || note.W != 300 || note.H != 400 {
		t.Errorf("note: %+v", note)
	}
	if bare := shapeByID(t, list, 78); bare.Kind != "other" || len(bare.Text) != 0 {
		t.Errorf("bare shape: %+v", bare)
	}

	if err := d.SetShapeText(3, 77, 0, "Edited <note>\nsecond"); err != nil {
		t.Fatal(err)
	}
	out, texts := result(t, d)
	if !strings.Contains(texts[2], "Edited <note>\nsecond") || strings.Contains(texts[2], "Plain note") {
		t.Errorf("slide 3: %q", texts[2])
	}
	_, parts := zipParts(t, out)
	if !strings.Contains(string(parts["ppt/slides/slide3.xml"]), `<a:rPr lang="en-US" i="1"/>`) {
		t.Error("first run formatting of the text box was lost")
	}
	assertSameParts(t, src, out, map[string]bool{"ppt/slides/slide3.xml": true})
}

func shapeByID(t *testing.T, list []ShapeInfo, id int) ShapeInfo {
	t.Helper()
	for _, s := range list {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("no shape %d", id)
	return ShapeInfo{}
}

func TestSetShapeTextErrors(t *testing.T) {
	src := handBuilt(t)
	d, err := Open(src)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		do   func() error
	}{
		{"unknown id", func() error { return d.SetShapeText(3, 999, 0, "x") }},
		{"no text body", func() error { return d.SetShapeText(3, 78, 0, "x") }},
		{"paragraph out of range", func() error { return d.SetShapeText(3, 77, 1, "x") }},
		{"negative paragraph", func() error { return d.SetShapeText(3, 77, -1, "x") }},
		{"slide out of range", func() error { return d.SetShapeText(9, 77, 0, "x") }},
		{"slide zero", func() error { return d.SetShapeText(0, 77, 0, "x") }},
		{"Shapes of a missing slide", func() error { _, err := d.Shapes(9); return err }},
	}
	for _, tc := range tests {
		if err := tc.do(); err == nil {
			t.Errorf("%s: no error", tc.name)
		}
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	assertSameParts(t, src, out, nil)
}
