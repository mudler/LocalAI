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

	"github.com/mudler/LocalAI/addons/office/internal/deck"
	"github.com/mudler/LocalAI/addons/office/internal/officecheck"
	"github.com/mudler/LocalAI/addons/office/internal/pptxedit"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func tinyPNG() []byte {
	GinkgoHelper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	Expect(png.Encode(&buf, img)).To(Succeed())
	return buf.Bytes()
}

// unzipParts writes the deck and returns its parts by name. Every XML part
// must parse.
func unzipParts(slides []deck.StudioSlide, opts Options, load ImageLoader) map[string][]byte {
	GinkgoHelper()
	var buf bytes.Buffer
	Expect(Write(&buf, slides, opts, load)).To(Succeed(), "Write")
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	Expect(err).NotTo(HaveOccurred(), "not a zip")
	parts := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		Expect(err).NotTo(HaveOccurred())
		b, err := io.ReadAll(rc)
		rc.Close()
		Expect(err).NotTo(HaveOccurred())
		Expect(parts).NotTo(HaveKey(f.Name), "duplicate part")
		parts[f.Name] = b
		if strings.HasSuffix(f.Name, ".xml") || strings.HasSuffix(f.Name, ".rels") {
			dec := xml.NewDecoder(bytes.NewReader(b))
			for {
				_, err := dec.Token()
				if errors.Is(err, io.EOF) {
					break
				}
				Expect(err).NotTo(HaveOccurred(), "part %s does not parse:\n%s", f.Name, b)
			}
		}
	}
	Expect(zr.File[0].Name).To(Equal("[Content_Types].xml"), "first part")
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

// shapeWith returns the p:sp element that holds the placeholder element ph.
func shapeWith(xmlText, ph string) string {
	GinkgoHelper()
	i := strings.Index(xmlText, ph)
	Expect(i).To(BeNumerically(">=", 0), "no %s in %s", ph, xmlText)
	start := strings.LastIndex(xmlText[:i], "<p:sp>")
	end := strings.Index(xmlText[i:], "</p:sp>")
	Expect(start).To(BeNumerically(">=", 0), "%s is not inside a p:sp", ph)
	Expect(end).To(BeNumerically(">=", 0), "%s is not inside a p:sp", ph)
	return xmlText[start : i+end]
}

func contains(lines []string, s string) bool {
	for _, l := range lines {
		if l == s {
			return true
		}
	}
	return false
}

var _ = Describe("Write", func() {
	It("writes the parts of a deck", func() {
		pic := tinyPNG()
		var asked []string
		load := func(ref string) ([]byte, string, error) {
			asked = append(asked, ref)
			if ref == "/api/documents/a/file" {
				return pic, "image/png", nil
			}
			return nil, "", errors.New("gone")
		}
		for _, style := range []deck.StudioStyle{deck.StylePaper, deck.StyleNight, deck.StyleStudio, deck.StylePlain} {
			parts := unzipParts(sampleSlides(), Options{Title: "Q3 & <more>", Style: style}, load)
			for _, name := range []string{
				"[Content_Types].xml", "_rels/.rels", "docProps/app.xml", "docProps/core.xml",
				"ppt/presentation.xml", "ppt/_rels/presentation.xml.rels",
				"ppt/slideMasters/slideMaster1.xml", "ppt/slideMasters/_rels/slideMaster1.xml.rels",
				"ppt/slideLayouts/slideLayout1.xml", "ppt/slideLayouts/_rels/slideLayout1.xml.rels",
				"ppt/slideLayouts/slideLayout2.xml", "ppt/slideLayouts/_rels/slideLayout2.xml.rels",
				"ppt/theme/theme1.xml",
			} {
				Expect(parts).To(HaveKey(name), "%s: missing part", style)
			}
			Expect(countPrefix(parts, slidePartRe)).To(Equal(7), "%s: slides (ok slides only)", style)
			Expect(countPrefix(parts, notesPartRe)).To(Equal(0), "%s: notes slides without Notes", style)
			Expect(parts).NotTo(HaveKey("ppt/notesMasters/notesMaster1.xml"), "%s: notes master without Notes", style)
			Expect(countPrefix(parts, mediaPartRe)).To(Equal(1), "%s: media parts", style)
			pres := string(parts["ppt/presentation.xml"])
			Expect(pres).To(ContainSubstring(`<p:sldSz cx="12192000" cy="6858000"`), "%s: not 16:9", style)
			Expect(pres).To(ContainSubstring(`<p:sldId id="256"`), "%s: slide ids must start at 256", style)
			ct := string(parts["[Content_Types].xml"])
			for i := 1; i <= 7; i++ {
				Expect(ct).To(ContainSubstring(`PartName="/ppt/slides/slide`+strconv.Itoa(i)+`.xml"`), "%s: content type for slide %d missing", style, i)
			}
			Expect(ct).To(ContainSubstring(`Extension="png"`), "%s: png content type missing", style)
			Expect(string(parts["docProps/core.xml"])).To(ContainSubstring("Q3 &amp; &lt;more&gt;"), "%s: title not escaped in core.xml", style)
		}
		parts := unzipParts(sampleSlides(), Options{Title: "Q3", Style: deck.StylePaper}, load)
		s1 := string(parts["ppt/slides/slide1.xml"])
		Expect(s1).To(ContainSubstring("Q3 review"))
		Expect(s1).To(ContainSubstring("Partners &amp; &lt;friends&gt;"))
		s2 := string(parts["ppt/slides/slide2.xml"])
		for _, want := range []string{"What &#34;we&#34; heard", "Revenue grew 12% &lt;fast&gt; &amp; steady", "Teams like it", `<a:buChar char="•"/>`, "FF6B3D"} {
			Expect(s2).To(ContainSubstring(want), "text slide")
		}
		s3 := string(parts["ppt/slides/slide3.xml"])
		Expect(s3).To(ContainSubstring("Big one"), "number slide should be third after skipping queued and failed")
		Expect(s3).To(ContainSubstring("42%"))
		Expect(s3).To(ContainSubstring("of users came back"))
		bar := string(parts["ppt/slides/slide4.xml"])
		Expect(bar).To(ContainSubstring("Rome"))
		Expect(bar).To(ContainSubstring("49%"))
		Expect(strings.Count(bar, `prst="rect"`)).To(BeNumerically(">=", 2), "bar chart shapes")
		line := string(parts["ppt/slides/slide5.xml"])
		Expect(strings.Count(line, "<p:cxnSp>")).To(Equal(2), "line chart connectors")
		Expect(line).To(ContainSubstring("Feb"))
		img := string(parts["ppt/slides/slide6.xml"])
		Expect(img).To(ContainSubstring("<p:pic>"))
		Expect(img).To(ContainSubstring("Board photo (low confidence)"))
		rels := string(parts["ppt/slides/_rels/slide6.xml.rels"])
		Expect(rels).To(ContainSubstring("../media/image1.png"), "image rels")
		ph := string(parts["ppt/slides/slide7.xml"])
		Expect(ph).NotTo(ContainSubstring("<p:pic>"), "placeholder slide")
		Expect(ph).To(ContainSubstring("Lost frame"))
		Expect(ph).To(ContainSubstring(`prstDash val="dash"`))
		Expect(asked).NotTo(BeEmpty(), "loader never called")
	})

	It("writes notes and a sources slide", func() {
		sources := []SourceLine{
			{Label: "Survey p.1", Detail: "Slide 2", Kind: "ok"},
			{Label: "Interview 0:42", Detail: "Slide 2", Kind: "weak"},
			{Label: "Teams like it", Detail: "Slide 2", Kind: "nosrc"},
			{Label: "We lead the market", Detail: "Slide 3", Kind: "own"},
		}
		parts := unzipParts(sampleSlides(), Options{Title: "Q3", Style: deck.StyleNight, Notes: true, SourcesSlide: true, Sources: sources}, nil)
		Expect(countPrefix(parts, slidePartRe)).To(Equal(8), "slides (7 ok + sources)")
		Expect(parts).To(HaveKey("ppt/notesMasters/notesMaster1.xml"), "notes master missing")
		Expect(countPrefix(parts, notesPartRe)).To(Equal(2), "notes slides (slides with notes)")
		n1 := string(parts["ppt/notesSlides/notesSlide1.xml"])
		Expect(n1).To(ContainSubstring("Open with the headline."))
		Expect(n1).To(ContainSubstring("Then pause."))
		Expect(string(parts["ppt/slides/_rels/slide1.xml.rels"])).To(ContainSubstring("../notesSlides/notesSlide1.xml"), "slide 1 does not link its notes")
		Expect(string(parts["ppt/notesSlides/_rels/notesSlide1.xml.rels"])).To(ContainSubstring("../slides/slide1.xml"), "notes do not link back to slide 1")
		Expect(string(parts["ppt/presentation.xml"])).To(ContainSubstring("<p:notesMasterIdLst>"), "presentation does not list the notes master")
		ct := string(parts["[Content_Types].xml"])
		for _, want := range []string{"/ppt/notesMasters/notesMaster1.xml", "/ppt/notesSlides/notesSlide2.xml", "/ppt/theme/theme2.xml"} {
			Expect(ct).To(ContainSubstring(want), "content types")
		}
		last := string(parts["ppt/slides/slide8.xml"])
		for _, want := range []string{"Sources", "Survey p.1", "Interview 0:42", "(low confidence)", "Teams like it", "(no source)", "We lead the market"} {
			Expect(last).To(ContainSubstring(want), "sources slide")
		}
		// A nil loader leaves placeholders for both images.
		Expect(countPrefix(parts, mediaPartRe)).To(Equal(0), "media parts with a nil loader")
	})

	It("rejects bad image bytes", func() {
		slides := []deck.StudioSlide{{ID: "x", Title: "Pic", Layout: deck.LayoutImage, Status: deck.SlideOK,
			Image: &deck.StudioImage{Ref: "/api/documents/a/file", Label: "Not a picture"}}}
		load := func(string) ([]byte, string, error) { return []byte("<svg/>"), "image/png", nil }
		parts := unzipParts(slides, Options{Title: "T", Style: deck.StylePlain}, load)
		Expect(countPrefix(parts, mediaPartRe)).To(Equal(0), "bad bytes embedded")
		Expect(string(parts["ppt/slides/slide1.xml"])).To(ContainSubstring("Not a picture"), "placeholder caption missing")
	})

	It("writes an empty deck", func() {
		parts := unzipParts(nil, Options{Title: "", Style: ""}, nil)
		Expect(countPrefix(parts, slidePartRe)).To(Equal(0), "slides for an empty deck")
		Expect(string(parts["ppt/presentation.xml"])).To(ContainSubstring("<p:sldSz"), "presentation incomplete")
	})

	It("writes title and body placeholders", func() {
		slides := []deck.StudioSlide{
			{ID: "a", Title: "Titled slide", Layout: deck.LayoutText, Status: deck.SlideOK, Bullets: []deck.StudioBullet{
				{Text: "First point"}, {Text: "Second point"},
			}},
			{ID: "b", Title: "", Layout: deck.LayoutText, Status: deck.SlideOK, Bullets: []deck.StudioBullet{{Text: "Untitled point"}}},
		}
		parts := unzipParts(slides, Options{Title: "T", Style: deck.StylePaper}, nil)

		layout2 := string(parts["ppt/slideLayouts/slideLayout2.xml"])
		for _, want := range []string{`type="obj"`, `name="Title and Content"`, phTitle, phBody} {
			Expect(layout2).To(ContainSubstring(want), "Title and Content layout")
		}
		Expect(string(parts["ppt/slideLayouts/slideLayout1.xml"])).To(ContainSubstring(`name="Blank"`), "Blank layout lost")
		Expect(strings.Count(string(parts["ppt/slideMasters/slideMaster1.xml"]), "<p:sldLayoutId ")).To(Equal(2), "layouts listed by the master")
		Expect(string(parts["ppt/slideMasters/_rels/slideMaster1.xml.rels"])).To(ContainSubstring("../slideLayouts/slideLayout2.xml"), "master does not link the Title and Content layout")

		s1 := string(parts["ppt/slides/slide1.xml"])
		Expect(string(parts["ppt/slides/_rels/slide1.xml.rels"])).To(ContainSubstring("../slideLayouts/slideLayout2.xml"), "titled slide does not use the Title and Content layout")
		Expect(shapeWith(s1, phTitle)).To(ContainSubstring("Titled slide"), "title placeholder does not hold the title")
		body := shapeWith(s1, phBody)
		Expect(body).To(ContainSubstring("First point"))
		Expect(body).To(ContainSubstring("Second point"))
		Expect(body).To(ContainSubstring(`<a:buChar char="•"/>`))
		Expect(body).NotTo(ContainSubstring("txBox"), "a placeholder must not be a text box")

		s2 := string(parts["ppt/slides/slide2.xml"])
		Expect(string(parts["ppt/slides/_rels/slide2.xml.rels"])).To(ContainSubstring("../slideLayouts/slideLayout1.xml"), "untitled slide does not use the Blank layout")
		Expect(s2).NotTo(ContainSubstring("<p:ph"), "untitled slide has placeholders")
		Expect(s2).To(ContainSubstring("Untitled point"), "untitled slide lost its bullets")
	})

	It("gives the sources slide placeholders", func() {
		parts := unzipParts(sampleSlides(), Options{Title: "Q3", Style: deck.StylePlain, SourcesSlide: true,
			Sources: []SourceLine{{Label: "Survey p.1", Kind: SourceOK}}}, nil)
		last := string(parts["ppt/slides/slide8.xml"])
		Expect(shapeWith(last, phTitle)).To(ContainSubstring("Sources"), "sources title placeholder")
		Expect(shapeWith(last, phBody)).To(ContainSubstring("Survey p.1"), "sources body placeholder")
	})

	// The written deck opens with pptxedit: the title can be set, and a slide
	// can be added from the Title and Content layout.
	It("writes a deck that pptxedit can edit", func() {
		var buf bytes.Buffer
		Expect(Write(&buf, sampleSlides(), Options{Title: "Q3", Style: deck.StyleNight}, nil)).To(Succeed())
		d, err := pptxedit.Open(buf.Bytes())
		Expect(err).NotTo(HaveOccurred(), "pptxedit.Open")
		slides := d.Slides()
		Expect(slides).To(HaveLen(7))
		Expect(slides[1].Title).To(Equal(`What "we" heard`))
		Expect(d.SetTitle(2, "Edited title")).To(Succeed(), "SetTitle")
		Expect(d.AddSlide("Title and Content", "Added", []string{"New point"})).To(Succeed(), "AddSlide")
		out, err := d.Bytes()
		Expect(err).NotTo(HaveOccurred())
		Expect(officecheck.Check(out)).To(BeEmpty(), "officecheck problems")
		re, err := pptxedit.Open(out)
		Expect(err).NotTo(HaveOccurred())
		got := re.Slides()
		Expect(got).To(HaveLen(8))
		Expect(got[1].Title).To(Equal("Edited title"))
		Expect(got[7].Title).To(Equal("Added"))
		Expect(contains(got[7].Text, "New point")).To(BeTrue(), "added slide text %q", got[7].Text)
	})
})
