package pii

import (
	"context"
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// detect builds a single-detector []NERConfig that reports one entity
// over the whole input under the given group/action.
func oneShot(group string, action Action, start, end int) []NERConfig {
	return []NERConfig{{
		Detector:      &stubNERDetector{entities: []NEREntity{{Group: group, Start: start, End: end, Score: 1}}},
		EntityActions: map[string]Action{group: action},
	}}
}

var _ = Describe("RedactNER emission", func() {
	ctx := context.Background()

	It("masks with a [REDACTED:ner:GROUP] placeholder and records a hash prefix", func() {
		res, err := RedactNER(ctx, "Contact me at alice@example.com any time.", oneShot("EMAIL", ActionMask, 14, 31))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Masked).To(BeTrue())
		Expect(res.Blocked).To(BeFalse())
		Expect(res.Redacted).To(ContainSubstring("[REDACTED:ner:EMAIL]"))
		Expect(res.Redacted).NotTo(ContainSubstring("alice@example.com"))
		Expect(res.Spans).To(HaveLen(1))
		Expect(res.Spans[0].HashPrefix).NotTo(BeEmpty(), "hash prefix must be set so audits can dedupe leaks")
	})

	It("labels pattern-detector hits with the pattern source, not ner", func() {
		cfgs := []NERConfig{{
			Detector:      &stubNERDetector{entities: []NEREntity{{Group: "ANTHROPIC_KEY", Start: 4, End: 24, Score: 1}}},
			EntityActions: map[string]Action{"ANTHROPIC_KEY": ActionMask},
			Source:        SourcePattern,
		}}
		res, err := RedactNER(ctx, "use sk-ant-aaaaaaaaaaaaaaaa now", cfgs)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(ContainSubstring("[REDACTED:pattern:ANTHROPIC_KEY]"))
		Expect(res.Redacted).NotTo(ContainSubstring("[REDACTED:ner:"))
		Expect(res.Spans).To(HaveLen(1))
		Expect(res.Spans[0].Pattern).To(Equal("pattern:ANTHROPIC_KEY"))
	})

	It("block leaves the matched span intact and sets Blocked", func() {
		res, err := RedactNER(ctx, "token sk-abcdef here", oneShot("PASSWORD", ActionBlock, 6, 15))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Blocked).To(BeTrue())
		Expect(res.Redacted).To(ContainSubstring("sk-abcdef"), "block leaves the value intact for the caller to discard")
		Expect(res.Spans[0].Action).To(Equal(ActionBlock))
	})

	It("allow leaves text intact but still records the span", func() {
		res, err := RedactNER(ctx, "Hello Acme!", oneShot("ORG", ActionAllow, 6, 10))
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Masked).To(BeFalse())
		Expect(res.Blocked).To(BeFalse())
		Expect(res.Redacted).To(Equal("Hello Acme!"))
		Expect(res.Spans).To(HaveLen(1))
	})

	It("passes non-matching text through unchanged", func() {
		det := &stubNERDetector{} // no entities
		res, err := RedactNER(ctx, "no PII here, just words", []NERConfig{{Detector: det, DefaultAction: ActionMask}})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(Equal("no PII here, just words"))
		Expect(res.Spans).To(BeEmpty())
	})

	It("handles empty input without calling the detector", func() {
		det := &stubNERDetector{entities: []NEREntity{{Group: "X", Start: 0, End: 1, Score: 1}}}
		res, err := RedactNER(ctx, "", []NERConfig{{Detector: det, DefaultAction: ActionMask}})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(BeEmpty())
		Expect(res.Spans).To(BeEmpty())
		Expect(det.calls).To(Equal(0))
	})

	It("produces a stable hash prefix for the same matched value", func() {
		a, _ := RedactNER(ctx, "a@b.com", oneShot("EMAIL", ActionMask, 0, 7))
		b, _ := RedactNER(ctx, "hi a@b.com", oneShot("EMAIL", ActionMask, 3, 10))
		Expect(a.Spans).To(HaveLen(1))
		Expect(b.Spans).To(HaveLen(1))
		Expect(a.Spans[0].HashPrefix).To(Equal(b.Spans[0].HashPrefix), "same matched value must produce same hash prefix")
	})
})

// funcNERDetector computes entities from the text it is handed — used to
// prove the segment scan gives the detector the JOINED document, the way a
// context-sensitive encoder behaves.
type funcNERDetector struct {
	fn func(text string) ([]NEREntity, error)
}

func (f *funcNERDetector) Detect(_ context.Context, text string) ([]NEREntity, error) {
	return f.fn(text)
}

// pinAfterCard mimics the real encoder's context sensitivity: "4421" is a
// PIN only when "card" appears earlier in the same document (measured on
// privacy-filter-multilingual: alone it detects nothing, with the eliciting
// question it detects PIN).
func pinAfterCard(text string) ([]NEREntity, error) {
	i := strings.Index(text, "4421")
	if i < 0 || !strings.Contains(text[:i], "card") {
		return nil, nil
	}
	return []NEREntity{{Group: "PIN", Start: i, End: i + 4, Score: 0.9}}, nil
}

var _ = Describe("RedactNERSegments", func() {
	ctx := context.Background()
	maskCfg := func(d NERDetector) []NERConfig {
		return []NERConfig{{Detector: d, DefaultAction: ActionMask}}
	}

	It("scans segments as one document so context crosses messages", func() {
		det := &funcNERDetector{fn: pinAfterCard}

		// Scanned alone the digits are invisible...
		alone, err := RedactNER(ctx, "it is 4421 ok", maskCfg(det))
		Expect(err).NotTo(HaveOccurred())
		Expect(alone.Spans).To(BeEmpty())

		// ...as a segment after the eliciting question they are detected,
		// and the span maps back to the second segment with local offsets.
		res, err := RedactNERSegments(ctx,
			[]string{"What are the last four digits of your card?", "it is 4421 ok"},
			maskCfg(det))
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(HaveLen(2))
		Expect(res[0].Spans).To(BeEmpty())
		Expect(res[0].Redacted).To(Equal("What are the last four digits of your card?"))
		Expect(res[1].Spans).To(HaveLen(1))
		Expect(res[1].Spans[0].Start).To(Equal(6))
		Expect(res[1].Spans[0].End).To(Equal(10))
		Expect(res[1].Masked).To(BeTrue())
		Expect(res[1].Redacted).To(Equal("it is [REDACTED:ner:PIN] ok"))
	})

	It("splits a hit crossing a segment boundary, masking both fragments", func() {
		det := &funcNERDetector{fn: func(text string) ([]NEREntity, error) {
			i := strings.Index(text, "22 Baker")
			j := strings.Index(text, "Street")
			if i < 0 || j < 0 {
				return nil, nil
			}
			return []NEREntity{{Group: "STREET", Start: i, End: j + len("Street"), Score: 0.9}}, nil
		}}
		res, err := RedactNERSegments(ctx, []string{"22 Baker", "Street"}, maskCfg(det))
		Expect(err).NotTo(HaveOccurred())
		Expect(res[0].Redacted).To(Equal("[REDACTED:ner:STREET]"))
		Expect(res[1].Redacted).To(Equal("[REDACTED:ner:STREET]"))
	})

	It("returns best-effort results with the first detector error", func() {
		bad := NERConfig{Detector: &stubNERDetector{err: errors.New("backend down")}, DefaultAction: ActionMask}
		good := NERConfig{
			Detector:      &stubNERDetector{entities: []NEREntity{{Group: "PER", Start: 0, End: 5, Score: 0.9}}},
			DefaultAction: ActionMask,
		}
		res, err := RedactNERSegments(ctx, []string{"Alice", "rest"}, []NERConfig{bad, good})
		Expect(err).To(HaveOccurred())
		Expect(res[0].Spans).To(HaveLen(1), "healthy detector's hits still apply")
	})

	It("is a per-text no-op without detectors or texts", func() {
		res, err := RedactNERSegments(ctx, []string{"a", ""}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(HaveLen(2))
		Expect(res[0].Redacted).To(Equal("a"))
		Expect(res[1].Redacted).To(Equal(""))

		res, err = RedactNERSegments(ctx, nil, maskCfg(&stubNERDetector{}))
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(BeEmpty())
	})
})
