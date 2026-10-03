package pii

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// lexiconDetector tags every occurrence of its words in the text it is
// given, the way a NER model reacts to what it actually sees. It records
// the scanned texts so specs can assert what reached the detector.
type lexiconDetector struct {
	words map[string]NEREntity // value: Group + Score; offsets are filled in
	seen  []string
}

func (d *lexiconDetector) Detect(_ context.Context, text string) ([]NEREntity, error) {
	d.seen = append(d.seen, text)
	var out []NEREntity
	for w, tmpl := range d.words {
		for off := 0; ; {
			i := strings.Index(text[off:], w)
			if i < 0 {
				break
			}
			s := off + i
			out = append(out, NEREntity{Group: tmpl.Group, Score: tmpl.Score, Start: s, End: s + len(w), Text: w})
			off = s + len(w)
		}
	}
	return out, nil
}

var _ = Describe("ProtectedTerms", func() {
	ctx := context.Background()

	// A hotel invoice as a name/address model sees it: the business name is
	// read as a surname (measured: LASTNAME 0.78-0.98), the guest's name and
	// the postal code are tagged, the town is not.
	const hotel = "Hotel Seeblick, Gast Lena Kranzberger, Am Mühlbach 12, 67059 Ludwigshafen\nÜbernachtung netto 84,00 EUR"
	newDet := func() *lexiconDetector {
		return &lexiconDetector{words: map[string]NEREntity{
			"Seeblick":    {Group: "LASTNAME", Score: 0.81},
			"Lena":        {Group: "FIRSTNAME", Score: 0.95},
			"Kranzberger": {Group: "LASTNAME", Score: 0.97},
			"Mühlbach":    {Group: "STREET", Score: 0.48},
			"67059":       {Group: "ZIPCODE", Score: 0.96},
		}}
	}
	cfg := func(det NERDetector, terms ...string) []NERConfig {
		return []NERConfig{{
			Detector: det, MinScore: 0.4, DefaultAction: ActionMask,
			ExtendToNextWord: []string{"ZIPCODE"}, ProtectedTerms: terms,
		}}
	}

	It("masks the business name without protection (the gap it closes)", func() {
		res, err := RedactNER(ctx, hotel, cfg(newDet()))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(HavePrefix("Hotel [REDACTED:ner:LASTNAME],"))
	})

	It("keeps a protected term verbatim, hides it from the detector, and still masks the identity", func() {
		det := newDet()
		res, err := RedactNER(ctx, hotel, cfg(det, "hotel seeblick")) // learned memories store lower-case keys
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(Equal("Hotel Seeblick, Gast [REDACTED:ner:FIRSTNAME] [REDACTED:ner:LASTNAME], " +
			"Am [REDACTED:ner:STREET] 12, [REDACTED:ner:ZIPCODE]\nÜbernachtung netto 84,00 EUR"))
		Expect(det.seen).To(HaveLen(1))
		Expect(det.seen[0]).NotTo(ContainSubstring("Seeblick"), "the detector must not see the protected value")
		Expect(det.seen[0]).To(HavePrefix(protectedPlaceholder + ", Gast Lena"))
		// Spans address the ORIGINAL text, not the scanned one.
		for _, sp := range res.Spans {
			Expect(hotel[sp.Start:sp.End]).To(BeElementOf("Lena", "Kranzberger", "Mühlbach", "67059 Ludwigshafen"))
		}
	})

	It("cuts the protected part out of a detection that straddles it", func() {
		text := "Hotel Seeblick, Gast Lena"
		det := &lexiconDetector{words: map[string]NEREntity{
			// what the detector sees after shielding: one span over placeholder + name
			protectedPlaceholder + ", Gast Lena": {Group: "LASTNAME", Score: 0.9},
		}}
		res, err := RedactNER(ctx, text, cfg(det, "Hotel Seeblick"))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(Equal("Hotel Seeblick[REDACTED:ner:LASTNAME]"))
		Expect(res.Spans).To(HaveLen(1))
		Expect(text[res.Spans[0].Start:res.Spans[0].End]).To(Equal(", Gast Lena"))
	})

	It("matches whole words only, case-insensitively, across a line wrap, and ignores short terms", func() {
		text := "Café Lindenhof\nCAFÉ\nLINDENHOF Lindenhofer AG Lindenhof"
		det := &lexiconDetector{words: map[string]NEREntity{
			"Lindenhof": {Group: "LASTNAME", Score: 0.9},
			"AG":        {Group: "ORGANIZATION", Score: 0.9},
		}}
		res, err := RedactNER(ctx, text, cfg(det, "Café Lindenhof", "AG"))
		Expect(err).NotTo(HaveOccurred())
		// "Café Lindenhof" and "CAFÉ\nLINDENHOF" are protected; the bare
		// "Lindenhof" at the end is not the term and is masked; "AG" is too
		// short to protect; "Lindenhofer" is a different word.
		Expect(res.Redacted).To(Equal("Café Lindenhof\nCAFÉ\nLINDENHOF [REDACTED:ner:LASTNAME]er " +
			"[REDACTED:ner:ORGANIZATION] [REDACTED:ner:LASTNAME]"))
	})

	It("never stretches a word extension onto a protected term", func() {
		text := "PLZ 67059 Hotel Seeblick"
		det := &lexiconDetector{words: map[string]NEREntity{"67059": {Group: "ZIPCODE", Score: 0.9}}}
		res, err := RedactNER(ctx, text, cfg(det, "Hotel Seeblick"))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(Equal("PLZ [REDACTED:ner:ZIPCODE] Hotel Seeblick"))
	})

	It("maps offsets back per segment on the multi-message path", func() {
		texts := []string{"Beleg von Hotel Seeblick", "Gast Lena Kranzberger"}
		res, err := RedactNERSegments(ctx, texts, cfg(newDet(), "Hotel Seeblick"))
		Expect(err).NotTo(HaveOccurred())
		Expect(res[0].Redacted).To(Equal("Beleg von Hotel Seeblick"))
		Expect(res[1].Redacted).To(Equal("Gast [REDACTED:ner:FIRSTNAME] [REDACTED:ner:LASTNAME]"))
		Expect(texts[1][res[1].Spans[1].Start:res[1].Spans[1].End]).To(Equal("Kranzberger"))
	})

	It("is a no-op without terms", func() {
		scan, regions := shieldProtected(hotel, protectedMatcher(nil))
		Expect(scan).To(Equal(hotel))
		Expect(regions).To(BeNil())
	})
})

var _ = Describe("LoadProtectedTerms", func() {
	It("reads term files inside the base path, skips comments, and re-reads on change", func() {
		dir := GinkgoT().TempDir()
		f := filepath.Join(dir, "lieferanten.txt")
		Expect(os.WriteFile(f, []byte("# gelernte Lieferanten\nhotel seeblick\n\n  café lindenhof  \n"), 0o644)).To(Succeed())
		Expect(LoadProtectedTerms([]string{"Tankstelle Rheinblick"}, []string{"lieferanten.txt"}, dir)).
			To(Equal([]string{"Tankstelle Rheinblick", "hotel seeblick", "café lindenhof"}))

		Expect(os.WriteFile(f, []byte("druckerei morgenrot\n"), 0o644)).To(Succeed())
		later := time.Now().Add(2 * time.Second)
		Expect(os.Chtimes(f, later, later)).To(Succeed())
		Expect(LoadProtectedTerms(nil, []string{"lieferanten.txt"}, dir)).To(Equal([]string{"druckerei morgenrot"}))
	})

	It("skips a missing file and a path outside the base path (fewer terms = more masking)", func() {
		dir := GinkgoT().TempDir()
		outside := filepath.Join(filepath.Dir(dir), "outside.txt")
		Expect(os.WriteFile(outside, []byte("secret name\n"), 0o644)).To(Succeed())
		DeferCleanup(os.Remove, outside)
		Expect(LoadProtectedTerms([]string{"x-inline"}, []string{"missing.txt", "../outside.txt"}, dir)).
			To(Equal([]string{"x-inline"}))
	})
})

var _ = Describe("Protected matcher cache", func() {
	It("releases obsolete term lists while preserving matching after eviction", func() {
		terms := []string{"Evicted Supplier"}
		first := protectedMatcher(terms)
		for i := 0; i < 64; i++ {
			protectedMatcher([]string{strings.Repeat("x", i+3)})
		}
		reloaded := protectedMatcher(terms)
		Expect(reloaded).NotTo(BeIdenticalTo(first), "old file revisions must not remain cached indefinitely")
		text, regions := shieldProtected("Evicted Supplier, customer Lena", reloaded)
		Expect(text).To(Equal(protectedPlaceholder + ", customer Lena"))
		Expect(regions).To(HaveLen(1))
	})
})
