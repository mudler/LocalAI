package deck_test

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/addons/office/internal/deck"
)

func okSlide(id string, layout deck.StudioLayout) deck.StudioSlide {
	return deck.StudioSlide{
		ID:      id,
		Title:   "Slide " + id,
		Layout:  layout,
		Bullets: []deck.StudioBullet{{Text: "First point", Evidence: []string{"e1"}}, {Text: "Unsourced", Flag: deck.FlagNoSource}},
		Sources: []string{"e1"},
		Status:  deck.SlideOK,
	}
}

func allLayouts() []deck.StudioSlide {
	title := okSlide("s1", deck.LayoutTitle)
	title.Sub = "A subtitle"
	text := okSlide("s2", deck.LayoutText)
	text.Notes = "Say this."
	img := okSlide("s3", deck.LayoutImage)
	img.Image = &deck.StudioImage{Ref: "/api/documents/abc/keyframes/2/image", Label: "Whiteboard", Evidence: "e2", Weak: true}
	chart := okSlide("s4", deck.LayoutChart)
	chart.Chart = &deck.StudioChart{Kind: deck.ChartBar, Label: "Cost by office", Data: []deck.ChartPoint{{K: "Rome", V: 12}, {K: "Milan", V: 28}}, Evidence: "e3"}
	num := okSlide("s5", deck.LayoutNumber)
	num.Big = &deck.BigNumber{Num: "49k EUR", Label: "to approve", Evidence: "e4"}
	return []deck.StudioSlide{title, text, img, chart, num}
}

func decodeDeck(t *testing.T, raw []byte) deck.Deck {
	t.Helper()
	var d deck.Deck
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func blockTypes(s deck.Slide) []string {
	var out []string
	for _, b := range s.Blocks {
		out = append(out, b["type"].(string))
	}
	return out
}

func TestStudioToDeckJSONAllLayouts(t *testing.T) {
	raw, err := deck.StudioToDeckJSON("Pilot", deck.StylePaper, allLayouts())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if errs := deck.Validate(raw); len(errs) != 0 {
		t.Fatalf("deck invalid: %v", errs)
	}
	d := decodeDeck(t, raw)
	if d.Theme != "paper" || d.Accent != "#FF6B3D" || d.Title != "Pilot" || d.Version != 1 {
		t.Fatalf("bad deck header: %+v", d)
	}
	want := []string{"title", "single", "media-right", "media-right", "stat"}
	for i, s := range d.Slides {
		if s.Layout != want[i] {
			t.Errorf("slide %d layout %q, want %q", i, s.Layout, want[i])
		}
	}
	if got := strings.Join(blockTypes(d.Slides[0]), ","); got != "heading,subtitle" {
		t.Errorf("title blocks: %s", got)
	}
	if got := strings.Join(blockTypes(d.Slides[1]), ","); got != "heading,bullets" {
		t.Errorf("text blocks: %s", got)
	}
	if d.Slides[1].Notes != "Say this." {
		t.Errorf("notes not copied")
	}
	if got := strings.Join(blockTypes(d.Slides[2]), ","); got != "heading,bullets,image" {
		t.Errorf("image blocks: %s", got)
	}
	if c := d.Slides[2].Blocks[2]["caption"]; c != "Whiteboard (low confidence)" {
		t.Errorf("image caption: %v", c)
	}
	if got := strings.Join(blockTypes(d.Slides[3]), ","); got != "heading,bullets,svg" {
		t.Errorf("chart blocks: %s", got)
	}
	if c := d.Slides[3].Blocks[2]["caption"]; c != "Cost by office" {
		t.Errorf("chart caption: %v", c)
	}
	if svg, _ := d.Slides[3].Blocks[2]["svg"].(string); !strings.HasPrefix(svg, "<svg") {
		t.Errorf("chart svg missing: %q", svg)
	}
	if got := strings.Join(blockTypes(d.Slides[4]), ","); got != "heading,stat,bullets" {
		t.Errorf("number blocks: %s", got)
	}
	if st := d.Slides[4].Blocks[1]; st["value"] != "49k EUR" || st["label"] != "to approve" {
		t.Errorf("stat block: %v", st)
	}
	if strings.Contains(string(raw), "nosrc") || strings.Contains(string(raw), "flag") {
		t.Errorf("flags must not reach the presenter deck: %s", raw)
	}
}

func TestStudioToDeckStyles(t *testing.T) {
	cases := map[deck.StudioStyle][2]string{
		deck.StylePaper:  {"paper", "#FF6B3D"},
		deck.StyleNight:  {"midnight", "#FFB26B"},
		deck.StyleStudio: {"paper", "#B23610"},
		deck.StylePlain:  {"mono", "#1B4A98"},
	}
	for style, want := range cases {
		d := deck.StudioToDeck("t", style, allLayouts())
		if d.Theme != want[0] || d.Accent != want[1] {
			t.Errorf("%s: got %s %s", style, d.Theme, d.Accent)
		}
	}
}

func TestStudioToDeckSkipsUnfinishedSlides(t *testing.T) {
	slides := allLayouts()
	slides[0].Status = deck.SlideFailed
	slides[1].Status = deck.SlideQueued
	slides[2].Status = deck.SlideWorking
	d := deck.StudioToDeck("t", deck.StyleNight, slides)
	if len(d.Slides) != 2 {
		t.Fatalf("expected 2 slides, got %d", len(d.Slides))
	}
}

func TestStudioToDeckJSONNoFinishedSlidesFails(t *testing.T) {
	s := okSlide("s1", deck.LayoutText)
	s.Status = deck.SlideFailed
	if _, err := deck.StudioToDeckJSON("t", deck.StylePaper, []deck.StudioSlide{s}); err == nil {
		t.Fatal("expected an error for a deck with no finished slides")
	}
}

func TestStudioToDeckEmptyBulletsAndFallbacks(t *testing.T) {
	text := okSlide("s1", deck.LayoutText)
	text.Bullets = nil
	img := okSlide("s2", deck.LayoutImage)
	chart := okSlide("s3", deck.LayoutChart)
	num := okSlide("s4", deck.LayoutNumber)
	raw, err := deck.StudioToDeckJSON("t", deck.StylePlain, []deck.StudioSlide{text, img, chart, num})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	d := decodeDeck(t, raw)
	for i, s := range d.Slides {
		if s.Layout != "single" {
			t.Errorf("slide %d: layout %q, want single", i, s.Layout)
		}
	}
}

func TestBriefValidate(t *testing.T) {
	good := deck.Brief{Title: "Pilot", Goal: "Get a go", Audience: "Partners", Length: deck.LengthStandard, Tone: deck.TonePlain, Style: deck.StylePaper, Sources: []string{"d1"}}
	if errs := good.Validate(); len(errs) != 0 {
		t.Fatalf("expected valid, got %v", errs)
	}
	bad := good
	bad.Title = ""
	bad.Length = "huge"
	bad.Tone = "angry"
	bad.Style = "neon"
	bad.Goal = strings.Repeat("g", 2001)
	bad.Sources = make([]string, 51)
	if errs := bad.Validate(); len(errs) != 6 {
		t.Fatalf("expected 6 errors, got %v", errs)
	}
	long := good
	long.Title = strings.Repeat("é", 201)
	if errs := long.Validate(); len(errs) != 1 {
		t.Fatalf("expected title length error, got %v", errs)
	}
}

func TestDeckLengthSlides(t *testing.T) {
	if deck.LengthShort.Slides() != 5 || deck.LengthStandard.Slides() != 7 || deck.LengthLong.Slides() != 8 {
		t.Fatal("unexpected slide counts")
	}
}

func TestEnumsValid(t *testing.T) {
	if !deck.StyleStudio.Valid() || deck.StudioStyle("x").Valid() {
		t.Fatal("style Valid")
	}
	if !deck.LayoutNumber.Valid() || deck.StudioLayout("x").Valid() {
		t.Fatal("layout Valid")
	}
}

func TestValidateStudioSlides(t *testing.T) {
	if errs := deck.ValidateStudioSlides(allLayouts()); len(errs) != 0 {
		t.Fatalf("expected valid, got %v", errs)
	}

	mixed := allLayouts()
	mixed[3].Chart.Data = []deck.ChartPoint{{K: "a", V: -3}, {K: "b", V: 4}}
	if errs := deck.ValidateStudioSlides(mixed); len(errs) != 0 {
		t.Fatalf("negative chart values should be accepted, got %v", errs)
	}

	cases := map[string]func(s []deck.StudioSlide){
		"empty id":       func(s []deck.StudioSlide) { s[0].ID = "" },
		"duplicate id":   func(s []deck.StudioSlide) { s[1].ID = s[0].ID },
		"bad layout":     func(s []deck.StudioSlide) { s[0].Layout = "grid" },
		"bad status":     func(s []deck.StudioSlide) { s[0].Status = "done" },
		"bad flag":       func(s []deck.StudioSlide) { s[0].Bullets[0].Flag = "maybe" },
		"too many":       func(s []deck.StudioSlide) { s[1].Bullets = make([]deck.StudioBullet, 9) },
		"long title":     func(s []deck.StudioSlide) { s[0].Title = strings.Repeat("x", 201) },
		"long bullet":    func(s []deck.StudioSlide) { s[0].Bullets[0].Text = strings.Repeat("x", 501) },
		"long notes":     func(s []deck.StudioSlide) { s[0].Notes = strings.Repeat("x", 5001) },
		"no chart data":  func(s []deck.StudioSlide) { s[3].Chart.Data = nil },
		"chart 13 pts":   func(s []deck.StudioSlide) { s[3].Chart.Data = make([]deck.ChartPoint, 13) },
		"chart NaN":      func(s []deck.StudioSlide) { s[3].Chart.Data[0].V = math.NaN() },
		"chart Inf":      func(s []deck.StudioSlide) { s[3].Chart.Data[0].V = math.Inf(-1) },
		"chart kind":     func(s []deck.StudioSlide) { s[3].Chart.Kind = "pie" },
		"external image": func(s []deck.StudioSlide) { s[2].Image.Ref = "https://example.com/x.png" },
		"long number":    func(s []deck.StudioSlide) { s[4].Big.Num = strings.Repeat("9", 41) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := allLayouts()
			mutate(s)
			if errs := deck.ValidateStudioSlides(s); len(errs) == 0 {
				t.Fatal("expected a validation error")
			}
		})
	}
}

func TestStudioJSONTags(t *testing.T) {
	raw, err := json.Marshal(deck.StudioSlide{ID: "a", Layout: deck.LayoutText, Status: deck.SlideOK, SourceHashes: map[string]string{"d": "h"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"id"`, `"title"`, `"layout"`, `"bullets"`, `"sources"`, `"status"`, `"sourceHashes"`} {
		if !strings.Contains(string(raw), k) {
			t.Errorf("missing %s in %s", k, raw)
		}
	}
	for _, k := range []string{`"sub"`, `"image"`, `"chart"`, `"big"`, `"notes"`, `"error"`} {
		if strings.Contains(string(raw), k) {
			t.Errorf("%s should be omitted in %s", k, raw)
		}
	}
}
