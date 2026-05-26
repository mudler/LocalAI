package pii

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// stubNERDetector returns a fixed slice of entities and tracks call
// count so tests can assert the detector isn't called when text is
// empty / no patterns / detector disabled.
type stubNERDetector struct {
	entities []NEREntity
	err      error
	calls    int
}

func (s *stubNERDetector) Detect(_ context.Context, _ string) ([]NEREntity, error) {
	s.calls++
	return s.entities, s.err
}

var _ = Describe("RedactWithNER", func() {
	It("nil detector is regex-only", func() {
		// When the NER tier is disabled (Detector == nil) the redactor
		// must behave exactly like the existing regex-only path — no
		// detector call, same Result shape, no error.
		r := NewRedactor([]Pattern{pickEmail()})
		res, err := r.RedactWithNER(context.Background(), "ping me at alice@example.com", nil, NERConfig{})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(ContainSubstring("[REDACTED:email]"), "regex tier should still run when Detector is nil")
	})

	It("applies entity actions", func() {
		det := &stubNERDetector{entities: []NEREntity{
			{Group: "PER", Start: 6, End: 11, Score: 0.95}, // "Alice" in "Hi I'm Alice today"
		}}
		r := NewRedactor(nil)
		res, err := r.RedactWithNER(context.Background(), "Hi I'm Alice today", nil, NERConfig{
			Detector:      det,
			EntityActions: map[string]Action{"PER": ActionMask},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(det.calls).To(Equal(1))
		Expect(res.Redacted).To(ContainSubstring("[REDACTED:ner:PER]"))
		Expect(res.Spans).To(HaveLen(1))
		Expect(res.Spans[0].Pattern).To(Equal("ner:PER"))
	})

	It("filters below MinScore", func() {
		det := &stubNERDetector{entities: []NEREntity{
			{Group: "PER", Start: 0, End: 5, Score: 0.20},
		}}
		r := NewRedactor(nil)
		res, err := r.RedactWithNER(context.Background(), "Alice", nil, NERConfig{
			Detector:      det,
			MinScore:      0.50,
			EntityActions: map[string]Action{"PER": ActionMask},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(Equal("Alice"), "low-confidence entity should be dropped")
	})

	It("default action applies to unconfigured groups", func() {
		det := &stubNERDetector{entities: []NEREntity{
			{Group: "ORG", Start: 7, End: 11, Score: 0.9}, // "Acme" in "Hello, Acme!"
		}}
		r := NewRedactor(nil)
		res, err := r.RedactWithNER(context.Background(), "Hello, Acme!", nil, NERConfig{
			Detector:      det,
			DefaultAction: ActionMask,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(ContainSubstring("[REDACTED:ner:ORG]"), "DefaultAction should apply to ORG")
	})

	It("drops unconfigured groups with no default", func() {
		// EntityActions has no entry for ORG and DefaultAction is empty —
		// the detected entity must be ignored entirely (no audit row, no
		// redaction).
		det := &stubNERDetector{entities: []NEREntity{
			{Group: "ORG", Start: 0, End: 4, Score: 0.9},
		}}
		r := NewRedactor(nil)
		res, err := r.RedactWithNER(context.Background(), "Acme", nil, NERConfig{
			Detector:      det,
			EntityActions: map[string]Action{"PER": ActionMask}, // ORG is unconfigured
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(Equal("Acme"))
		Expect(res.Spans).To(BeEmpty())
	})

	It("overlapping hits keep stronger action", func() {
		// Regex marks 0..10 as mask; NER marks 5..15 as block. After
		// merge, the union 0..15 keeps the strongest action (block).
		pat := Pattern{ID: "test", Action: ActionMask, regex: rangeRegex(0, 10)}
		r := NewRedactor([]Pattern{pat})
		det := &stubNERDetector{entities: []NEREntity{
			{Group: "PER", Start: 5, End: 15, Score: 0.9},
		}}
		text := "0123456789ABCDEF"
		res, err := r.RedactWithNER(context.Background(), text, nil, NERConfig{
			Detector:      det,
			EntityActions: map[string]Action{"PER": ActionBlock},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Blocked).To(BeTrue(), "overlapping mask+block should set Blocked=true")
	})

	It("detector error returns regex result and error", func() {
		// Fail-open: when the NER detector errors, the redactor still
		// returns regex-tier hits so an offline NER backend doesn't strip
		// the cheap protection. Caller can read the error and decide
		// whether to surface it.
		det := &stubNERDetector{err: errors.New("backend offline")}
		r := NewRedactor([]Pattern{pickEmail()})
		res, err := r.RedactWithNER(context.Background(), "ping alice@example.com", nil, NERConfig{
			Detector:      det,
			DefaultAction: ActionMask,
		})
		Expect(err).To(HaveOccurred(), "expected detector error to surface")
		Expect(res.Redacted).To(ContainSubstring("[REDACTED:email]"), "regex tier should still apply on NER failure")
	})

	It("out-of-bounds offsets are skipped", func() {
		// A misconfigured / buggy backend could return offsets past the
		// end of text. The redactor must not panic on slice OOB.
		det := &stubNERDetector{entities: []NEREntity{
			{Group: "PER", Start: 0, End: 999, Score: 0.9},
			{Group: "PER", Start: -1, End: 3, Score: 0.9},
			{Group: "PER", Start: 5, End: 5, Score: 0.9}, // zero-length
		}}
		r := NewRedactor(nil)
		res, err := r.RedactWithNER(context.Background(), "Alice", nil, NERConfig{
			Detector:      det,
			DefaultAction: ActionMask,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(Equal("Alice"))
		Expect(res.Spans).To(BeEmpty())
	})
})

// --- test helpers ---

// rangeMatcher is a deterministic regexpMatcher stub: it claims one
// fixed range regardless of input. Lets the overlap-merge test
// produce a known regex/NER intersection without depending on a real
// compiled regex.
type rangeMatcher struct{ start, end int }

func (m rangeMatcher) FindAllStringIndex(_ string, _ int) [][]int {
	return [][]int{{m.start, m.end}}
}

func rangeRegex(start, end int) regexpMatcher { return rangeMatcher{start: start, end: end} }

// pickEmail returns the compiled "email" pattern from DefaultPatterns
// — the NER tests use it as the regex tier's contribution.
func pickEmail() Pattern {
	for _, p := range DefaultPatterns() {
		if p.ID == "email" {
			compiled, err := Compile([]Pattern{p})
			ExpectWithOffset(1, err).NotTo(HaveOccurred(), "compile")
			return compiled[0]
		}
	}
	Fail("email pattern missing from DefaultPatterns")
	return Pattern{}
}

