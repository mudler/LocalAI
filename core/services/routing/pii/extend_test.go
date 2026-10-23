package pii

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// span returns the byte range of the first occurrence of sub in text, so the
// stub detections below line up with the text they describe.
func span(text, sub string) (int, int) {
	i := strings.Index(text, sub)
	Expect(i).To(BeNumerically(">=", 0), "fixture: %q not in text", sub)
	return i, i + len(sub)
}

func ent(text, sub, group string, score float32) NEREntity {
	s, e := span(text, sub)
	return NEREntity{Group: group, Start: s, End: e, Score: score, Text: sub}
}

var _ = Describe("ExtendToNextWord", func() {
	ctx := context.Background()

	// An invoice line as an address model sees it: the postal code is tagged
	// with high confidence, the town after it is not tagged at all.
	const invoice = "Rechnung an Lena Kranzberger, Ahornweg 12, 67059 Ludwigshafen\nTel. 0612 345678"
	detections := func(text string) []NEREntity {
		return []NEREntity{
			ent(text, "Lena", "FIRSTNAME", 0.95),
			ent(text, "Kranzberger", "LASTNAME", 0.97),
			ent(text, "67059", "ZIPCODE", 0.96),
		}
	}
	cfg := func(text string, extend ...string) []NERConfig {
		return []NERConfig{{
			Detector:         &stubNERDetector{entities: detections(text)},
			DefaultAction:    ActionMask,
			ExtendToNextWord: extend,
		}}
	}

	It("leaves the town in the clear without the option (the gap it closes)", func() {
		res, err := RedactNER(ctx, invoice, cfg(invoice))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(ContainSubstring("Ludwigshafen"))
	})

	It("masks the word after a configured group together with it", func() {
		res, err := RedactNER(ctx, invoice, cfg(invoice, "ZIPCODE"))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).NotTo(ContainSubstring("Ludwigshafen"))
		Expect(res.Redacted).NotTo(ContainSubstring("67059"))
		Expect(res.Redacted).To(ContainSubstring(", [REDACTED:ner:ZIPCODE]\nTel."), "stops at the line break")
		s, _ := span(invoice, "67059")
		_, e := span(invoice, "Ludwigshafen")
		Expect(res.Spans).To(ContainElement(SatisfyAll(
			HaveField("Start", s), HaveField("End", e), HaveField("Pattern", "ner:ZIPCODE"))))
	})

	It("only extends the configured groups", func() {
		res, err := RedactNER(ctx, invoice, cfg(invoice, "CITY"))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(ContainSubstring("[REDACTED:ner:LASTNAME], Ahornweg"), "a name is not stretched")
		Expect(res.Redacted).To(ContainSubstring("Ludwigshafen"))
	})

	It("does not cross a line break, and leaves the next line intact", func() {
		text := "PLZ 67059\nLudwigshafen"
		det := &stubNERDetector{entities: []NEREntity{ent(text, "67059", "ZIPCODE", 0.9)}}
		res, err := RedactNER(ctx, text, []NERConfig{{Detector: det, DefaultAction: ActionMask, ExtendToNextWord: []string{"ZIPCODE"}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(Equal("PLZ [REDACTED:ner:ZIPCODE]\nLudwigshafen"))
	})

	It("handles non-ASCII words, hyphens and a sentence-ending dot", func() {
		text := "Lieferadresse 97070 Würzburg-Heidingsfeld. Danke"
		det := &stubNERDetector{entities: []NEREntity{ent(text, "97070", "ZIPCODE", 0.9)}}
		res, err := RedactNER(ctx, text, []NERConfig{{Detector: det, DefaultAction: ActionMask, ExtendToNextWord: []string{"ZIPCODE"}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(Equal("Lieferadresse [REDACTED:ner:ZIPCODE]. Danke"))
	})

	It("does not extend over trailing whitespace or punctuation only", func() {
		text := "PLZ 67059 , Rest"
		det := &stubNERDetector{entities: []NEREntity{ent(text, "67059", "ZIPCODE", 0.9)}}
		res, err := RedactNER(ctx, text, []NERConfig{{Detector: det, DefaultAction: ActionMask, ExtendToNextWord: []string{"ZIPCODE"}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(Equal("PLZ [REDACTED:ner:ZIPCODE] , Rest"))
	})

	It("merges with a town the model did detect", func() {
		text := "67059 Ludwigshafen am Rhein"
		det := &stubNERDetector{entities: []NEREntity{
			ent(text, "67059", "ZIPCODE", 0.9),
			ent(text, "Ludwigshafen", "CITY", 0.6),
		}}
		res, err := RedactNER(ctx, text, []NERConfig{{Detector: det, DefaultAction: ActionMask, ExtendToNextWord: []string{"ZIPCODE"}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(Equal("[REDACTED:ner:ZIPCODE] am Rhein"))
		Expect(res.Spans).To(HaveLen(1))
	})

	It("applies on the segment path without crossing the segment separator", func() {
		texts := []string{"Kunde: 67059", "Ludwigshafen ist schön"}
		doc := strings.Join(texts, segmentSeparator)
		det := &stubNERDetector{entities: []NEREntity{ent(doc, "67059", "ZIPCODE", 0.9)}}
		res, err := RedactNERSegments(ctx, texts, []NERConfig{{Detector: det, DefaultAction: ActionMask, ExtendToNextWord: []string{"ZIPCODE"}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(res[0].Redacted).To(Equal("Kunde: [REDACTED:ner:ZIPCODE]"))
		Expect(res[1].Redacted).To(Equal("Ludwigshafen ist schön"))
	})

	It("keeps a block action on the extended span", func() {
		text := "Code 4711 Geheim"
		det := &stubNERDetector{entities: []NEREntity{ent(text, "4711", "PIN", 0.9)}}
		res, err := RedactNER(ctx, text, []NERConfig{{
			Detector: det, EntityActions: map[string]Action{"PIN": ActionBlock}, ExtendToNextWord: []string{"PIN"},
		}})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Blocked).To(BeTrue())
		_, e := span(text, "Geheim")
		Expect(res.Spans[0].End).To(Equal(e))
	})
})
