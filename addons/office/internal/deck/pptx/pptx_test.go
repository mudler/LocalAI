package pptx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/addons/office/internal/deck"
	"github.com/mudler/LocalAI/addons/office/internal/officecheck"
	"github.com/mudler/LocalAI/addons/office/internal/pptxedit"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// unzipParts writes the deck and returns its parts by name. Every XML part
// must parse.
func unzipParts(t *testing.T, slides []deck.StudioSlide, opts Options, load ImageLoader) map[string][]byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, slides, opts, load); err != nil {
		t.Fatalf("Write: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	parts := map[string][]byte{}
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
		if _, dup := parts[f.Name]; dup {
			t.Fatalf("duplicate part %s", f.Name)
		}
		parts[f.Name] = b
		if strings.HasSuffix(f.Name, ".xml") || strings.HasSuffix(f.Name, ".rels") {
			dec := xml.NewDecoder(bytes.NewReader(b))
			for {
				_, err := dec.Token()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatalf("part %s does not parse: %v\n%s", f.Name, err, b)
				}
			}
		}
	}
	if zr.File[0].Name != "[Content_Types].xml" {
		t.Errorf("first part is %s, want [Content_Types].xml", zr.File[0].Name)
	}
	return parts
}

func countPrefix(parts map[string][]byte, re *regexp.Regexp) int {
	n := 0
	for name := range parts {
		if re.MatchString(name) {
			n++
		}
	}
	return n
}

var (
	slidePartRe = regexp.MustCompile(`^ppt/slides/slide\d+\.xml$`)
	notesPartRe = regexp.MustCompile(`^ppt/notesSlides/notesSlide\d+\.xml$`)
	mediaPartRe = regexp.MustCompile(`^ppt/media/image\d+\.(png|jpeg)$`)
)

func sampleSlides() []deck.StudioSlide {
	return []deck.StudioSlide{
		{ID: "s1", Title: "Q3 review", Sub: "Partners & <friends>", Layout: deck.LayoutTitle, Status: deck.SlideOK, Notes: "Open with the headline.\nThen pause."},
		{ID: "s2", Title: "What \"we\" heard", Layout: deck.LayoutText, Status: deck.SlideOK, Bullets: []deck.StudioBullet{
			{Text: "Revenue grew 12% <fast> & steady", Evidence: []string{"e1"}},
			{Text: "Teams like it", Flag: deck.FlagNoSource},
			{Text: "Churn is low", Flag: deck.FlagWeak},
		}},
		{ID: "s3", Title: "Queued", Layout: deck.LayoutText, Status: deck.SlideQueued},
		{ID: "s4", Title: "Broken", Layout: deck.LayoutText, Status: deck.SlideFailed},
		{ID: "s5", Title: "Big one", Layout: deck.LayoutNumber, Status: deck.SlideOK, Big: &deck.BigNumber{Num: "42%", Label: "of users came back"}},
		{ID: "s6", Title: "By office", Layout: deck.LayoutChart, Status: deck.SlideOK, Chart: &deck.StudioChart{
			Kind: deck.ChartBar, Label: "Cost by office", Unit: "%", Data: []deck.ChartPoint{{K: "Rome", V: 12}, {K: "Milan", V: 49}},
		}},
		{ID: "s7", Title: "Trend", Layout: deck.LayoutChart, Status: deck.SlideOK, Chart: &deck.StudioChart{
			Kind: deck.ChartLine, Label: "Users", Data: []deck.ChartPoint{{K: "Jan", V: 3}, {K: "Feb", V: 9}, {K: "Mar", V: 5}},
		}},
		{ID: "s8", Title: "Whiteboard", Layout: deck.LayoutImage, Status: deck.SlideOK, Notes: "Point at the board.",
			Image: &deck.StudioImage{Ref: "/api/documents/a/file", Label: "Board photo", Weak: true}},
		{ID: "s9", Title: "Missing", Layout: deck.LayoutImage, Status: deck.SlideOK,
			Image: &deck.StudioImage{Ref: "/api/documents/b/file", Label: "Lost frame"}},
	}
}

func TestWritePPTXParts(t *testing.T) {
	pic := tinyPNG(t)
	var asked []string
	load := func(ref string) ([]byte, string, error) {
		asked = append(asked, ref)
		if ref == "/api/documents/a/file" {
			return pic, "image/png", nil
		}
		return nil, "", errors.New("gone")
	}
	for _, style := range []deck.StudioStyle{deck.StylePaper, deck.StyleNight, deck.StyleStudio, deck.StylePlain} {
		parts := unzipParts(t, sampleSlides(), Options{Title: "Q3 & <more>", Style: style}, load)
		for _, name := range []string{
			"[Content_Types].xml", "_rels/.rels", "docProps/app.xml", "docProps/core.xml",
			"ppt/presentation.xml", "ppt/_rels/presentation.xml.rels",
			"ppt/slideMasters/slideMaster1.xml", "ppt/slideMasters/_rels/slideMaster1.xml.rels",
			"ppt/slideLayouts/slideLayout1.xml", "ppt/slideLayouts/_rels/slideLayout1.xml.rels",
			"ppt/slideLayouts/slideLayout2.xml", "ppt/slideLayouts/_rels/slideLayout2.xml.rels",
			"ppt/theme/theme1.xml",
		} {
			if _, ok := parts[name]; !ok {
				t.Fatalf("%s: missing part %s", style, name)
			}
		}
		if n := countPrefix(parts, slidePartRe); n != 7 {
			t.Fatalf("%s: %d slides, want 7 (ok slides only)", style, n)
		}
		if n := countPrefix(parts, notesPartRe); n != 0 {
			t.Fatalf("%s: %d notes slides without Notes", style, n)
		}
		if _, ok := parts["ppt/notesMasters/notesMaster1.xml"]; ok {
			t.Fatalf("%s: notes master without Notes", style)
		}
		if n := countPrefix(parts, mediaPartRe); n != 1 {
			t.Fatalf("%s: %d media parts, want 1", style, n)
		}
		pres := string(parts["ppt/presentation.xml"])
		if !strings.Contains(pres, `<p:sldSz cx="12192000" cy="6858000"`) {
			t.Errorf("%s: not 16:9: %s", style, pres)
		}
		if !strings.Contains(pres, `<p:sldId id="256"`) {
			t.Errorf("%s: slide ids must start at 256: %s", style, pres)
		}
		ct := string(parts["[Content_Types].xml"])
		for i := 1; i <= 7; i++ {
			if !strings.Contains(ct, `PartName="/ppt/slides/slide`+strconv.Itoa(i)+`.xml"`) {
				t.Errorf("%s: content type for slide %d missing", style, i)
			}
		}
		if !strings.Contains(ct, `Extension="png"`) {
			t.Errorf("%s: png content type missing", style)
		}
		if !strings.Contains(string(parts["docProps/core.xml"]), "Q3 &amp; &lt;more&gt;") {
			t.Errorf("%s: title not escaped in core.xml", style)
		}
	}
	parts := unzipParts(t, sampleSlides(), Options{Title: "Q3", Style: deck.StylePaper}, load)
	s1 := string(parts["ppt/slides/slide1.xml"])
	if !strings.Contains(s1, "Q3 review") || !strings.Contains(s1, "Partners &amp; &lt;friends&gt;") {
		t.Errorf("title slide text: %s", s1)
	}
	s2 := string(parts["ppt/slides/slide2.xml"])
	for _, want := range []string{"What &#34;we&#34; heard", "Revenue grew 12% &lt;fast&gt; &amp; steady", "Teams like it", `<a:buChar char="•"/>`, "FF6B3D"} {
		if !strings.Contains(s2, want) {
			t.Errorf("text slide misses %q: %s", want, s2)
		}
	}
	if strings.Contains(string(parts["ppt/slides/slide3.xml"]), "Big one") == false {
		t.Errorf("number slide should be third after skipping queued and failed")
	}
	s3 := string(parts["ppt/slides/slide3.xml"])
	if !strings.Contains(s3, "42%") || !strings.Contains(s3, "of users came back") {
		t.Errorf("number slide: %s", s3)
	}
	bar := string(parts["ppt/slides/slide4.xml"])
	if !strings.Contains(bar, "Rome") || !strings.Contains(bar, "49%") || strings.Count(bar, `prst="rect"`) < 2 {
		t.Errorf("bar chart shapes: %s", bar)
	}
	line := string(parts["ppt/slides/slide5.xml"])
	if strings.Count(line, "<p:cxnSp>") != 2 || !strings.Contains(line, "Feb") {
		t.Errorf("line chart connectors: %s", line)
	}
	img := string(parts["ppt/slides/slide6.xml"])
	if !strings.Contains(img, "<p:pic>") || !strings.Contains(img, "Board photo (low confidence)") {
		t.Errorf("image slide: %s", img)
	}
	rels := string(parts["ppt/slides/_rels/slide6.xml.rels"])
	if !strings.Contains(rels, "../media/image1.png") {
		t.Errorf("image rels: %s", rels)
	}
	ph := string(parts["ppt/slides/slide7.xml"])
	if strings.Contains(ph, "<p:pic>") || !strings.Contains(ph, "Lost frame") || !strings.Contains(ph, `prstDash val="dash"`) {
		t.Errorf("placeholder slide: %s", ph)
	}
	if len(asked) == 0 {
		t.Errorf("loader never called")
	}
}

func TestWritePPTXNotesAndSources(t *testing.T) {
	sources := []SourceLine{
		{Label: "Survey p.1", Detail: "Slide 2", Kind: "ok"},
		{Label: "Interview 0:42", Detail: "Slide 2", Kind: "weak"},
		{Label: "Teams like it", Detail: "Slide 2", Kind: "nosrc"},
		{Label: "We lead the market", Detail: "Slide 3", Kind: "own"},
	}
	parts := unzipParts(t, sampleSlides(), Options{Title: "Q3", Style: deck.StyleNight, Notes: true, SourcesSlide: true, Sources: sources}, nil)
	if n := countPrefix(parts, slidePartRe); n != 8 {
		t.Fatalf("%d slides, want 8 (7 ok + sources)", n)
	}
	if _, ok := parts["ppt/notesMasters/notesMaster1.xml"]; !ok {
		t.Fatal("notes master missing")
	}
	if n := countPrefix(parts, notesPartRe); n != 2 {
		t.Fatalf("%d notes slides, want 2 (slides with notes)", n)
	}
	n1 := string(parts["ppt/notesSlides/notesSlide1.xml"])
	if !strings.Contains(n1, "Open with the headline.") || !strings.Contains(n1, "Then pause.") {
		t.Errorf("notes text: %s", n1)
	}
	if !strings.Contains(string(parts["ppt/slides/_rels/slide1.xml.rels"]), "../notesSlides/notesSlide1.xml") {
		t.Errorf("slide 1 does not link its notes")
	}
	if !strings.Contains(string(parts["ppt/notesSlides/_rels/notesSlide1.xml.rels"]), "../slides/slide1.xml") {
		t.Errorf("notes do not link back to slide 1")
	}
	if !strings.Contains(string(parts["ppt/presentation.xml"]), "<p:notesMasterIdLst>") {
		t.Errorf("presentation does not list the notes master")
	}
	ct := string(parts["[Content_Types].xml"])
	for _, want := range []string{"/ppt/notesMasters/notesMaster1.xml", "/ppt/notesSlides/notesSlide2.xml", "/ppt/theme/theme2.xml"} {
		if !strings.Contains(ct, want) {
			t.Errorf("content types miss %s", want)
		}
	}
	last := string(parts["ppt/slides/slide8.xml"])
	for _, want := range []string{"Sources", "Survey p.1", "Interview 0:42", "(low confidence)", "Teams like it", "(no source)", "We lead the market"} {
		if !strings.Contains(last, want) {
			t.Errorf("sources slide misses %q: %s", want, last)
		}
	}
	// A nil loader leaves placeholders for both images.
	if n := countPrefix(parts, mediaPartRe); n != 0 {
		t.Errorf("%d media parts with a nil loader", n)
	}
}

func TestWritePPTXRejectsBadImageBytes(t *testing.T) {
	slides := []deck.StudioSlide{{ID: "x", Title: "Pic", Layout: deck.LayoutImage, Status: deck.SlideOK,
		Image: &deck.StudioImage{Ref: "/api/documents/a/file", Label: "Not a picture"}}}
	load := func(string) ([]byte, string, error) { return []byte("<svg/>"), "image/png", nil }
	parts := unzipParts(t, slides, Options{Title: "T", Style: deck.StylePlain}, load)
	if n := countPrefix(parts, mediaPartRe); n != 0 {
		t.Errorf("bad bytes embedded")
	}
	if !strings.Contains(string(parts["ppt/slides/slide1.xml"]), "Not a picture") {
		t.Errorf("placeholder caption missing")
	}
}

func TestWritePPTXEmpty(t *testing.T) {
	parts := unzipParts(t, nil, Options{Title: "", Style: ""}, nil)
	if n := countPrefix(parts, slidePartRe); n != 0 {
		t.Errorf("%d slides for an empty deck", n)
	}
	if !strings.Contains(string(parts["ppt/presentation.xml"]), "<p:sldSz") {
		t.Errorf("presentation incomplete")
	}
}

// shapeWith returns the p:sp element that holds the placeholder element ph.
func shapeWith(t *testing.T, xmlText, ph string) string {
	t.Helper()
	i := strings.Index(xmlText, ph)
	if i < 0 {
		t.Fatalf("no %s in %s", ph, xmlText)
	}
	start := strings.LastIndex(xmlText[:i], "<p:sp>")
	end := strings.Index(xmlText[i:], "</p:sp>")
	if start < 0 || end < 0 {
		t.Fatalf("%s is not inside a p:sp", ph)
	}
	return xmlText[start : i+end]
}

func TestWritePlaceholders(t *testing.T) {
	slides := []deck.StudioSlide{
		{ID: "a", Title: "Titled slide", Layout: deck.LayoutText, Status: deck.SlideOK, Bullets: []deck.StudioBullet{
			{Text: "First point"}, {Text: "Second point"},
		}},
		{ID: "b", Title: "", Layout: deck.LayoutText, Status: deck.SlideOK, Bullets: []deck.StudioBullet{{Text: "Untitled point"}}},
	}
	parts := unzipParts(t, slides, Options{Title: "T", Style: deck.StylePaper}, nil)

	layout2 := string(parts["ppt/slideLayouts/slideLayout2.xml"])
	for _, want := range []string{`type="obj"`, `name="Title and Content"`, phTitle, phBody} {
		if !strings.Contains(layout2, want) {
			t.Errorf("Title and Content layout misses %s: %s", want, layout2)
		}
	}
	if !strings.Contains(string(parts["ppt/slideLayouts/slideLayout1.xml"]), `name="Blank"`) {
		t.Error("Blank layout lost")
	}
	if n := strings.Count(string(parts["ppt/slideMasters/slideMaster1.xml"]), "<p:sldLayoutId "); n != 2 {
		t.Errorf("master lists %d layouts, want 2", n)
	}
	if !strings.Contains(string(parts["ppt/slideMasters/_rels/slideMaster1.xml.rels"]), "../slideLayouts/slideLayout2.xml") {
		t.Error("master does not link the Title and Content layout")
	}

	s1 := string(parts["ppt/slides/slide1.xml"])
	if !strings.Contains(string(parts["ppt/slides/_rels/slide1.xml.rels"]), "../slideLayouts/slideLayout2.xml") {
		t.Error("titled slide does not use the Title and Content layout")
	}
	if title := shapeWith(t, s1, phTitle); !strings.Contains(title, "Titled slide") {
		t.Errorf("title placeholder does not hold the title: %s", title)
	}
	body := shapeWith(t, s1, phBody)
	if !strings.Contains(body, "First point") || !strings.Contains(body, "Second point") || !strings.Contains(body, `<a:buChar char="•"/>`) {
		t.Errorf("body placeholder does not hold the bullets: %s", body)
	}
	if strings.Contains(body, "txBox") {
		t.Error("a placeholder must not be a text box")
	}

	s2 := string(parts["ppt/slides/slide2.xml"])
	if !strings.Contains(string(parts["ppt/slides/_rels/slide2.xml.rels"]), "../slideLayouts/slideLayout1.xml") {
		t.Error("untitled slide does not use the Blank layout")
	}
	if strings.Contains(s2, "<p:ph") {
		t.Errorf("untitled slide has placeholders: %s", s2)
	}
	if !strings.Contains(s2, "Untitled point") {
		t.Errorf("untitled slide lost its bullets: %s", s2)
	}
}

func TestWriteSourcesSlideHasPlaceholders(t *testing.T) {
	parts := unzipParts(t, sampleSlides(), Options{Title: "Q3", Style: deck.StylePlain, SourcesSlide: true,
		Sources: []SourceLine{{Label: "Survey p.1", Kind: SourceOK}}}, nil)
	last := string(parts["ppt/slides/slide8.xml"])
	if got := shapeWith(t, last, phTitle); !strings.Contains(got, "Sources") {
		t.Errorf("sources title placeholder: %s", got)
	}
	if got := shapeWith(t, last, phBody); !strings.Contains(got, "Survey p.1") {
		t.Errorf("sources body placeholder: %s", got)
	}
}

// TestWriteEditable opens the written deck with pptxedit: the title can be set,
// and a slide can be added from the Title and Content layout.
func TestWriteEditable(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, sampleSlides(), Options{Title: "Q3", Style: deck.StyleNight}, nil); err != nil {
		t.Fatal(err)
	}
	d, err := pptxedit.Open(buf.Bytes())
	if err != nil {
		t.Fatalf("pptxedit.Open: %v", err)
	}
	slides := d.Slides()
	if len(slides) != 7 {
		t.Fatalf("%d slides, want 7", len(slides))
	}
	if slides[1].Title != `What "we" heard` {
		t.Errorf("title of slide 2 is %q", slides[1].Title)
	}
	if err := d.SetTitle(2, "Edited title"); err != nil {
		t.Fatalf("SetTitle: %v", err)
	}
	if err := d.AddSlide("Title and Content", "Added", []string{"New point"}); err != nil {
		t.Fatalf("AddSlide: %v", err)
	}
	out, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if probs := officecheck.Check(out); len(probs) != 0 {
		t.Fatalf("officecheck problems: %v", probs)
	}
	re, err := pptxedit.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	got := re.Slides()
	if len(got) != 8 || got[1].Title != "Edited title" || got[7].Title != "Added" {
		t.Errorf("reopened slides: %+v", got)
	}
	if !contains(got[7].Text, "New point") {
		t.Errorf("added slide text %q", got[7].Text)
	}
}

func contains(lines []string, s string) bool {
	for _, l := range lines {
		if l == s {
			return true
		}
	}
	return false
}
