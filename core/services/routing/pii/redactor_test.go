package pii

import (
	"context"

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
