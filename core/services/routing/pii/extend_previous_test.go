package pii

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ExtendToPreviousWord", func() {
	ctx := context.Background()

	// A hotel receipt as a name model sees it: the surname is tagged with high
	// confidence, the first name in front of it scores below the threshold.
	const receipt = "Hotel Seeblick, Gast Greta Waldmeister, Kastanienallee 97\nBetrag 12,50 EUR"
	cfg := func(text string, extend ...string) []NERConfig {
		return []NERConfig{{
			Detector:             &stubNERDetector{entities: []NEREntity{ent(text, "Waldmeister", "LASTNAME", 0.97)}},
			DefaultAction:        ActionMask,
			ExtendToPreviousWord: extend,
		}}
	}

	It("leaves the first name in the clear without the option (the gap it closes)", func() {
		res, err := RedactNER(ctx, receipt, cfg(receipt))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(ContainSubstring("Gast Greta [REDACTED:ner:LASTNAME]"))
	})

	It("masks the word before a configured group together with it", func() {
		res, err := RedactNER(ctx, receipt, cfg(receipt, "LASTNAME"))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).NotTo(ContainSubstring("Greta"))
		Expect(res.Redacted).To(ContainSubstring("Gast [REDACTED:ner:LASTNAME], Kastanienallee"))
		s, _ := span(receipt, "Greta")
		_, e := span(receipt, "Waldmeister")
		Expect(res.Spans).To(ContainElement(SatisfyAll(
			HaveField("Start", s), HaveField("End", e), HaveField("Pattern", "ner:LASTNAME"))))
	})

	It("only extends the configured groups", func() {
		res, err := RedactNER(ctx, receipt, cfg(receipt, "FIRSTNAME"))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(ContainSubstring("Greta"))
	})

	It("never swallows a word with digits (amounts, dates, codes stay intact)", func() {
		for _, text := range []string{"Betrag 12,50 Waldmeister", "am 11.08.2026 Waldmeister", "Zimmer 4711 Waldmeister", "Code A1b Waldmeister"} {
			det := &stubNERDetector{entities: []NEREntity{ent(text, "Waldmeister", "LASTNAME", 0.9)}}
			res, err := RedactNER(ctx, text, []NERConfig{{Detector: det, DefaultAction: ActionMask, ExtendToPreviousWord: []string{"LASTNAME"}}})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Redacted).To(HaveSuffix(" [REDACTED:ner:LASTNAME]"), text)
			Expect(len(res.Redacted)).To(BeNumerically(">", len(" [REDACTED:ner:LASTNAME]")), text)
			Expect(res.Spans).To(HaveLen(1))
			Expect(res.Spans[0].Start).To(Equal(len(text)-len("Waldmeister")), "kept the word before: %q", text)
		}
	})

	It("does not cross a line break, and leaves the previous line intact", func() {
		text := "Greta\nWaldmeister"
		det := &stubNERDetector{entities: []NEREntity{ent(text, "Waldmeister", "LASTNAME", 0.9)}}
		res, err := RedactNER(ctx, text, []NERConfig{{Detector: det, DefaultAction: ActionMask, ExtendToPreviousWord: []string{"LASTNAME"}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(Equal("Greta\n[REDACTED:ner:LASTNAME]"))
	})

	It("does nothing at the start of the text or after punctuation only", func() {
		for _, text := range []string{"Waldmeister zahlt", ", Waldmeister zahlt"} {
			det := &stubNERDetector{entities: []NEREntity{ent(text, "Waldmeister", "LASTNAME", 0.9)}}
			res, err := RedactNER(ctx, text, []NERConfig{{Detector: det, DefaultAction: ActionMask, ExtendToPreviousWord: []string{"LASTNAME"}}})
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Redacted).To(HaveSuffix("[REDACTED:ner:LASTNAME] zahlt"), text)
			Expect(res.Spans[0].Start).To(Equal(len(text)-len("Waldmeister zahlt")), text)
		}
	})

	It("handles non-ASCII words and hyphens", func() {
		text := "Gast Anne-Sophie Müller-Lüdenscheidt"
		det := &stubNERDetector{entities: []NEREntity{ent(text, "Müller-Lüdenscheidt", "LASTNAME", 0.9)}}
		res, err := RedactNER(ctx, text, []NERConfig{{Detector: det, DefaultAction: ActionMask, ExtendToPreviousWord: []string{"LASTNAME"}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(Equal("Gast [REDACTED:ner:LASTNAME]"))
	})

	It("never swallows a protected term in front of the hit", func() {
		text := "Seeblick Waldmeister"
		// tags what it actually sees: after shielding, the protected term is a placeholder
		det := &lexiconDetector{words: map[string]NEREntity{"Waldmeister": {Group: "LASTNAME", Score: 0.9}}}
		res, err := RedactNER(ctx, text, []NERConfig{{Detector: det, DefaultAction: ActionMask,
			ExtendToPreviousWord: []string{"LASTNAME"}, ProtectedTerms: []string{"Seeblick"}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(Equal("Seeblick [REDACTED:ner:LASTNAME]"))
	})
})
