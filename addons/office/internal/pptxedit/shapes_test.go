package pptxedit

import (
	"bytes"
	"strings"

	"github.com/mudler/LocalAI/addons/office/internal/deck"
	"github.com/mudler/LocalAI/addons/office/internal/deck/pptx"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// writerDeck is a deck made by pptx.Write: a titled bullet slide and a chart
// slide whose label is a plain text box.
func writerDeck() []byte {
	GinkgoHelper()
	slides := []deck.StudioSlide{
		{ID: "t", Title: "Findings", Layout: deck.LayoutText, Status: deck.SlideOK, Bullets: []deck.StudioBullet{{Text: "First"}, {Text: "Second"}}},
		{ID: "c", Title: "Costs", Layout: deck.LayoutChart, Status: deck.SlideOK, Chart: &deck.StudioChart{
			Kind: deck.ChartBar, Label: "Cost by office", Unit: "%", Data: []deck.ChartPoint{{K: "Rome", V: 12}, {K: "Milan", V: 49}},
		}},
	}
	var buf bytes.Buffer
	Expect(pptx.Write(&buf, slides, pptx.Options{Title: "T", Style: deck.StylePaper}, nil)).To(Succeed())
	return buf.Bytes()
}

// findShape returns the first shape of the list with this kind and text, or
// fails the spec.
func findShape(list []ShapeInfo, kind, text string) ShapeInfo {
	GinkgoHelper()
	for _, s := range list {
		if s.Kind == kind && len(s.Text) > 0 && s.Text[0] == text {
			return s
		}
	}
	Fail("no " + kind + " shape with text " + text)
	return ShapeInfo{}
}

func mustBytes(d *Deck) []byte {
	GinkgoHelper()
	out, err := d.Bytes()
	Expect(err).NotTo(HaveOccurred())
	return out
}

const plainBox = `<p:sp><p:nvSpPr><p:cNvPr id="77" name="Note box"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr>` +
	`<p:spPr><a:xfrm><a:off x="10" y="20"/><a:ext cx="300" cy="400"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr>` +
	`<p:txBody><a:bodyPr/><a:lstStyle/><a:p><a:r><a:rPr lang="en-US" i="1"/><a:t>Plain note</a:t></a:r></a:p></p:txBody></p:sp>`

const bareShape = `<p:sp><p:nvSpPr><p:cNvPr id="78" name="Bare"/><p:cNvSpPr/><p:nvPr/></p:nvSpPr><p:spPr/></p:sp>`

// handBuilt is sourceDeck with a plain text box and a shape without a text
// body added to its untitled slide 3.
func handBuilt() []byte {
	GinkgoHelper()
	return rewriteZip(sourceDeck(), map[string]func(string) string{
		"ppt/slides/slide3.xml": addShapes(plainBox + bareShape),
	})
}

func shapeByID(list []ShapeInfo, id int) ShapeInfo {
	GinkgoHelper()
	for _, s := range list {
		if s.ID == id {
			return s
		}
	}
	Fail("no shape with the wanted id")
	return ShapeInfo{}
}

var _ = Describe("shapes", func() {
	It("lists the shapes of a generated deck", func() {
		d, err := Open(writerDeck())
		Expect(err).NotTo(HaveOccurred())
		list, err := d.Shapes(1)
		Expect(err).NotTo(HaveOccurred())
		title := findShape(list, "title", "Findings")
		body := findShape(list, "body", "First")
		Expect(strings.Join(body.Text, "|")).To(Equal("First|Second"))
		Expect(title.W).To(BeNumerically(">", 0), "geometry missing: title %+v", title)
		Expect(title.H).To(BeNumerically(">", 0), "geometry missing: title %+v", title)
		Expect(body.W).To(BeNumerically(">", 0), "geometry missing: body %+v", body)
		Expect(body.H).To(BeNumerically(">", 0), "geometry missing: body %+v", body)
		seen := map[int]bool{}
		for _, s := range list {
			Expect(s.ID).To(BeNumerically(">", 0))
			Expect(seen[s.ID]).To(BeFalse(), "shape id %d is repeated", s.ID)
			seen[s.ID] = true
		}

		chart, err := d.Shapes(2)
		Expect(err).NotTo(HaveOccurred())
		label := findShape(chart, "text", "Cost by office")
		Expect(label.Name).NotTo(BeEmpty(), "text box has no name")
		findShape(chart, "title", "Costs")

		_, err = d.Shapes(3)
		Expect(err).To(HaveOccurred(), "Shapes of a missing slide must fail")
	})

	It("sets shape text on a generated deck", func() {
		d, err := Open(writerDeck())
		Expect(err).NotTo(HaveOccurred())
		list, _ := d.Shapes(1)
		body := findShape(list, "body", "First")
		Expect(d.SetShapeText(1, body.ID, 1, "Second & <final>\nline two")).To(Succeed())
		out, texts := result(d)
		Expect(texts[0]).To(ContainSubstring("First"))
		Expect(texts[0]).To(ContainSubstring("Second & <final>\nline two"))
		Expect(texts[0]).NotTo(ContainSubstring("\nSecond\n"))
		_, parts := zipParts(out)
		Expect(string(parts["ppt/slides/slide1.xml"])).To(ContainSubstring("<a:buChar"), "paragraph properties of the bullet were lost")

		chart, _ := d.Shapes(2)
		label := findShape(chart, "text", "Cost by office")
		Expect(d.SetShapeText(2, label.ID, 0, "Cost by region")).To(Succeed())
		_, texts = result(d)
		Expect(texts[1]).NotTo(ContainSubstring("Cost by office"))
		Expect(texts[1]).To(ContainSubstring("Cost by region"))
		re, err := Open(mustBytes(d))
		Expect(err).NotTo(HaveOccurred())
		reChart, _ := re.Shapes(2)
		findShape(reChart, "text", "Cost by region")
	})

	It("lists shapes and sets text on a hand-built deck", func() {
		src := handBuilt()
		d, err := Open(src)
		Expect(err).NotTo(HaveOccurred())
		first, err := d.Shapes(1)
		Expect(err).NotTo(HaveOccurred())
		findShape(first, "title", "Deck title")

		list, err := d.Shapes(3)
		Expect(err).NotTo(HaveOccurred())
		note := findShape(list, "text", "Plain note")
		Expect([]int64{int64(note.ID), note.X, note.Y, note.W, note.H}).To(Equal([]int64{77, 10, 20, 300, 400}), "note: %+v", note)
		bare := shapeByID(list, 78)
		Expect(bare.Kind).To(Equal("other"))
		Expect(bare.Text).To(BeEmpty())

		Expect(d.SetShapeText(3, 77, 0, "Edited <note>\nsecond")).To(Succeed())
		out, texts := result(d)
		Expect(texts[2]).To(ContainSubstring("Edited <note>\nsecond"))
		Expect(texts[2]).NotTo(ContainSubstring("Plain note"))
		_, parts := zipParts(out)
		Expect(string(parts["ppt/slides/slide3.xml"])).To(ContainSubstring(`<a:rPr lang="en-US" i="1"/>`), "first run formatting of the text box was lost")
		assertSameParts(src, out, map[string]bool{"ppt/slides/slide3.xml": true})
	})

	It("rejects bad shape edits", func() {
		src := handBuilt()
		d, err := Open(src)
		Expect(err).NotTo(HaveOccurred())
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
			Expect(tc.do()).To(HaveOccurred(), tc.name)
		}
		out, err := d.Bytes()
		Expect(err).NotTo(HaveOccurred())
		assertSameParts(src, out, nil)
	})
})
