package deck_test

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/mudler/LocalAI/addons/office/internal/deck"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

func decodeDeck(raw []byte) deck.Deck {
	GinkgoHelper()
	var d deck.Deck
	Expect(json.Unmarshal(raw, &d)).To(Succeed())
	return d
}

func blockTypes(s deck.Slide) []string {
	var out []string
	for _, b := range s.Blocks {
		out = append(out, b["type"].(string))
	}
	return out
}

var _ = Describe("studio deck", func() {
	It("converts every layout to a valid presenter deck", func() {
		raw, err := deck.StudioToDeckJSON("Pilot", deck.StylePaper, allLayouts())
		Expect(err).NotTo(HaveOccurred())
		Expect(deck.Validate(raw)).To(BeEmpty(), "deck invalid")
		d := decodeDeck(raw)
		Expect(d.Theme).To(Equal("paper"))
		Expect(d.Accent).To(Equal("#FF6B3D"))
		Expect(d.Title).To(Equal("Pilot"))
		Expect(d.Version).To(Equal(1))
		want := []string{"title", "single", "media-right", "media-right", "stat"}
		for i, s := range d.Slides {
			Expect(s.Layout).To(Equal(want[i]), "slide %d layout", i)
		}
		Expect(strings.Join(blockTypes(d.Slides[0]), ",")).To(Equal("heading,subtitle"), "title blocks")
		Expect(strings.Join(blockTypes(d.Slides[1]), ",")).To(Equal("heading,bullets"), "text blocks")
		Expect(d.Slides[1].Notes).To(Equal("Say this."), "notes not copied")
		Expect(strings.Join(blockTypes(d.Slides[2]), ",")).To(Equal("heading,bullets,image"), "image blocks")
		Expect(d.Slides[2].Blocks[2]["caption"]).To(Equal("Whiteboard (low confidence)"), "image caption")
		Expect(strings.Join(blockTypes(d.Slides[3]), ",")).To(Equal("heading,bullets,svg"), "chart blocks")
		Expect(d.Slides[3].Blocks[2]["caption"]).To(Equal("Cost by office"), "chart caption")
		svg, _ := d.Slides[3].Blocks[2]["svg"].(string)
		Expect(svg).To(HavePrefix("<svg"), "chart svg missing")
		Expect(strings.Join(blockTypes(d.Slides[4]), ",")).To(Equal("heading,stat,bullets"), "number blocks")
		st := d.Slides[4].Blocks[1]
		Expect(st["value"]).To(Equal("49k EUR"))
		Expect(st["label"]).To(Equal("to approve"))
		Expect(string(raw)).NotTo(ContainSubstring("nosrc"), "flags must not reach the presenter deck")
		Expect(string(raw)).NotTo(ContainSubstring("flag"), "flags must not reach the presenter deck")
	})

	It("maps studio styles to themes and accents", func() {
		cases := map[deck.StudioStyle][2]string{
			deck.StylePaper:  {"paper", "#FF6B3D"},
			deck.StyleNight:  {"midnight", "#FFB26B"},
			deck.StyleStudio: {"paper", "#B23610"},
			deck.StylePlain:  {"mono", "#1B4A98"},
		}
		for style, want := range cases {
			d := deck.StudioToDeck("t", style, allLayouts())
			Expect([]string{d.Theme, d.Accent}).To(Equal(want[:]), "style %s", style)
		}
	})

	It("skips unfinished slides", func() {
		slides := allLayouts()
		slides[0].Status = deck.SlideFailed
		slides[1].Status = deck.SlideQueued
		slides[2].Status = deck.SlideWorking
		d := deck.StudioToDeck("t", deck.StyleNight, slides)
		Expect(d.Slides).To(HaveLen(2))
	})

	It("fails when no slide is finished", func() {
		s := okSlide("s1", deck.LayoutText)
		s.Status = deck.SlideFailed
		_, err := deck.StudioToDeckJSON("t", deck.StylePaper, []deck.StudioSlide{s})
		Expect(err).To(HaveOccurred(), "expected an error for a deck with no finished slides")
	})

	It("falls back to a single layout for empty bullets and missing payloads", func() {
		text := okSlide("s1", deck.LayoutText)
		text.Bullets = nil
		img := okSlide("s2", deck.LayoutImage)
		chart := okSlide("s3", deck.LayoutChart)
		num := okSlide("s4", deck.LayoutNumber)
		raw, err := deck.StudioToDeckJSON("t", deck.StylePlain, []deck.StudioSlide{text, img, chart, num})
		Expect(err).NotTo(HaveOccurred())
		d := decodeDeck(raw)
		for i, s := range d.Slides {
			Expect(s.Layout).To(Equal("single"), "slide %d layout", i)
		}
	})

	It("validates a brief", func() {
		good := deck.Brief{Title: "Pilot", Goal: "Get a go", Audience: "Partners", Length: deck.LengthStandard, Tone: deck.TonePlain, Style: deck.StylePaper, Sources: []string{"d1"}}
		Expect(good.Validate()).To(BeEmpty())
		bad := good
		bad.Title = ""
		bad.Length = "huge"
		bad.Tone = "angry"
		bad.Style = "neon"
		bad.Goal = strings.Repeat("g", 2001)
		bad.Sources = make([]string, 51)
		Expect(bad.Validate()).To(HaveLen(6))
		long := good
		long.Title = strings.Repeat("é", 201)
		Expect(long.Validate()).To(HaveLen(1), "expected title length error")
	})

	It("maps deck lengths to slide counts", func() {
		Expect(deck.LengthShort.Slides()).To(Equal(5))
		Expect(deck.LengthStandard.Slides()).To(Equal(7))
		Expect(deck.LengthLong.Slides()).To(Equal(8))
	})

	It("validates the enums", func() {
		Expect(deck.StyleStudio.Valid()).To(BeTrue())
		Expect(deck.StudioStyle("x").Valid()).To(BeFalse())
		Expect(deck.LayoutNumber.Valid()).To(BeTrue())
		Expect(deck.StudioLayout("x").Valid()).To(BeFalse())
	})

	Describe("ValidateStudioSlides", func() {
		It("accepts valid slides, including negative chart values", func() {
			Expect(deck.ValidateStudioSlides(allLayouts())).To(BeEmpty())

			mixed := allLayouts()
			mixed[3].Chart.Data = []deck.ChartPoint{{K: "a", V: -3}, {K: "b", V: 4}}
			Expect(deck.ValidateStudioSlides(mixed)).To(BeEmpty(), "negative chart values should be accepted")
		})

		DescribeTable("rejects invalid slides",
			func(mutate func(s []deck.StudioSlide)) {
				s := allLayouts()
				mutate(s)
				Expect(deck.ValidateStudioSlides(s)).NotTo(BeEmpty(), "expected a validation error")
			},
			Entry("empty id", func(s []deck.StudioSlide) { s[0].ID = "" }),
			Entry("duplicate id", func(s []deck.StudioSlide) { s[1].ID = s[0].ID }),
			Entry("bad layout", func(s []deck.StudioSlide) { s[0].Layout = "grid" }),
			Entry("bad status", func(s []deck.StudioSlide) { s[0].Status = "done" }),
			Entry("bad flag", func(s []deck.StudioSlide) { s[0].Bullets[0].Flag = "maybe" }),
			Entry("too many", func(s []deck.StudioSlide) { s[1].Bullets = make([]deck.StudioBullet, 9) }),
			Entry("long title", func(s []deck.StudioSlide) { s[0].Title = strings.Repeat("x", 201) }),
			Entry("long bullet", func(s []deck.StudioSlide) { s[0].Bullets[0].Text = strings.Repeat("x", 501) }),
			Entry("long notes", func(s []deck.StudioSlide) { s[0].Notes = strings.Repeat("x", 5001) }),
			Entry("no chart data", func(s []deck.StudioSlide) { s[3].Chart.Data = nil }),
			Entry("chart 13 pts", func(s []deck.StudioSlide) { s[3].Chart.Data = make([]deck.ChartPoint, 13) }),
			Entry("chart NaN", func(s []deck.StudioSlide) { s[3].Chart.Data[0].V = math.NaN() }),
			Entry("chart Inf", func(s []deck.StudioSlide) { s[3].Chart.Data[0].V = math.Inf(-1) }),
			Entry("chart kind", func(s []deck.StudioSlide) { s[3].Chart.Kind = "pie" }),
			Entry("external image", func(s []deck.StudioSlide) { s[2].Image.Ref = "https://example.com/x.png" }),
			Entry("long number", func(s []deck.StudioSlide) { s[4].Big.Num = strings.Repeat("9", 41) }),
		)
	})

	It("uses the expected JSON tags", func() {
		raw, err := json.Marshal(deck.StudioSlide{ID: "a", Layout: deck.LayoutText, Status: deck.SlideOK, SourceHashes: map[string]string{"d": "h"}})
		Expect(err).NotTo(HaveOccurred())
		for _, k := range []string{`"id"`, `"title"`, `"layout"`, `"bullets"`, `"sources"`, `"status"`, `"sourceHashes"`} {
			Expect(string(raw)).To(ContainSubstring(k))
		}
		for _, k := range []string{`"sub"`, `"image"`, `"chart"`, `"big"`, `"notes"`, `"error"`} {
			Expect(string(raw)).NotTo(ContainSubstring(k), "should be omitted")
		}
	})
})
