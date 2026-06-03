package pii

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// stubNERDetector returns a fixed slice of entities and tracks call
// count so tests can assert the detector isn't called when text is empty.
type stubNERDetector struct {
	entities []NEREntity
	err      error
	calls    int
}

func (s *stubNERDetector) Detect(_ context.Context, _ string) ([]NEREntity, error) {
	s.calls++
	return s.entities, s.err
}

var _ = Describe("RedactNER", func() {
	It("no detectors is a no-op", func() {
		res, err := RedactNER(context.Background(), "ping me at alice@example.com", nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(Equal("ping me at alice@example.com"))
		Expect(res.Spans).To(BeEmpty())
	})

	It("applies entity actions", func() {
		det := &stubNERDetector{entities: []NEREntity{
			{Group: "PER", Start: 6, End: 11, Score: 0.95}, // "Alice" in "Hi I'm Alice today"
		}}
		res, err := RedactNER(context.Background(), "Hi I'm Alice today", []NERConfig{{
			Detector:      det,
			EntityActions: map[string]Action{"PER": ActionMask},
		}})
		Expect(err).NotTo(HaveOccurred())
		Expect(det.calls).To(Equal(1))
		Expect(res.Redacted).To(ContainSubstring("[REDACTED:ner:PER]"))
		Expect(res.Spans).To(HaveLen(1))
		Expect(res.Spans[0].Pattern).To(Equal("ner:PER"))
		Expect(res.Spans[0].Action).To(Equal(ActionMask))
	})

	It("filters below MinScore", func() {
		det := &stubNERDetector{entities: []NEREntity{
			{Group: "PER", Start: 0, End: 5, Score: 0.20},
		}}
		res, err := RedactNER(context.Background(), "Alice", []NERConfig{{
			Detector:      det,
			MinScore:      0.50,
			EntityActions: map[string]Action{"PER": ActionMask},
		}})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(Equal("Alice"), "low-confidence entity should be dropped")
	})

	It("default action applies to unconfigured groups", func() {
		det := &stubNERDetector{entities: []NEREntity{
			{Group: "ORG", Start: 7, End: 11, Score: 0.9}, // "Acme" in "Hello, Acme!"
		}}
		res, err := RedactNER(context.Background(), "Hello, Acme!", []NERConfig{{
			Detector:      det,
			DefaultAction: ActionMask,
		}})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(ContainSubstring("[REDACTED:ner:ORG]"), "DefaultAction should apply to ORG")
	})

	It("drops unconfigured groups with no default", func() {
		det := &stubNERDetector{entities: []NEREntity{
			{Group: "ORG", Start: 0, End: 4, Score: 0.9},
		}}
		res, err := RedactNER(context.Background(), "Acme", []NERConfig{{
			Detector:      det,
			EntityActions: map[string]Action{"PER": ActionMask}, // ORG is unconfigured
		}})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(Equal("Acme"))
		Expect(res.Spans).To(BeEmpty())
	})

	It("unions multiple detectors and keeps the stronger action on overlap", func() {
		// Detector A marks 0..10 as mask; detector B marks 5..15 as block.
		// After merge, the union 0..15 keeps the strongest action (block).
		detA := &stubNERDetector{entities: []NEREntity{{Group: "A", Start: 0, End: 10, Score: 0.9}}}
		detB := &stubNERDetector{entities: []NEREntity{{Group: "B", Start: 5, End: 15, Score: 0.9}}}
		text := "0123456789ABCDEF"
		res, err := RedactNER(context.Background(), text, []NERConfig{
			{Detector: detA, EntityActions: map[string]Action{"A": ActionMask}},
			{Detector: detB, EntityActions: map[string]Action{"B": ActionBlock}},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(detA.calls).To(Equal(1))
		Expect(detB.calls).To(Equal(1))
		Expect(res.Blocked).To(BeTrue(), "overlapping mask+block should set Blocked=true")
	})

	It("returns a best-effort result and the error when a detector fails (fail-closed contract)", func() {
		// One healthy detector, one failing. RedactNER returns the healthy
		// detector's hits AND the error, so the caller can fail closed.
		good := &stubNERDetector{entities: []NEREntity{{Group: "PER", Start: 0, End: 5, Score: 0.9}}}
		bad := &stubNERDetector{err: errors.New("backend offline")}
		res, err := RedactNER(context.Background(), "Alice", []NERConfig{
			{Detector: good, DefaultAction: ActionMask},
			{Detector: bad, DefaultAction: ActionMask},
		})
		Expect(err).To(HaveOccurred())
		Expect(res.Redacted).To(ContainSubstring("[REDACTED:ner:PER]"), "healthy detector's hits should still apply")
	})

	It("skips out-of-bounds offsets without panicking", func() {
		det := &stubNERDetector{entities: []NEREntity{
			{Group: "PER", Start: 0, End: 999, Score: 0.9},
			{Group: "PER", Start: -1, End: 3, Score: 0.9},
			{Group: "PER", Start: 5, End: 5, Score: 0.9}, // zero-length
		}}
		res, err := RedactNER(context.Background(), "Alice", []NERConfig{{
			Detector:      det,
			DefaultAction: ActionMask,
		}})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Redacted).To(Equal("Alice"))
		Expect(res.Spans).To(BeEmpty())
	})
})

var _ = Describe("NERConfigFromRaw", func() {
	det := &stubNERDetector{}

	It("defaults an empty default_action to mask", func() {
		cfg := NERConfigFromRaw(det, 0.4, "", nil)
		Expect(cfg.DefaultAction).To(Equal(ActionMask))
		Expect(cfg.MinScore).To(BeNumerically("~", 0.4, 1e-6))
	})

	It("passes through valid actions and drops invalid ones", func() {
		cfg := NERConfigFromRaw(det, 0, "block", map[string]string{
			"PASSWORD": "block",
			"EMAIL":    "mask",
			"BOGUS":    "nonsense", // dropped
		})
		Expect(cfg.DefaultAction).To(Equal(ActionBlock))
		Expect(cfg.EntityActions).To(HaveKeyWithValue("PASSWORD", ActionBlock))
		Expect(cfg.EntityActions).To(HaveKeyWithValue("EMAIL", ActionMask))
		Expect(cfg.EntityActions).NotTo(HaveKey("BOGUS"))
	})
})

var _ = Describe("NERConfig.ResolveAction", func() {
	It("prefers an explicit entity action over the default", func() {
		cfg := NERConfig{EntityActions: map[string]Action{"EMAIL": ActionBlock}, DefaultAction: ActionMask}
		a, ok := cfg.ResolveAction("EMAIL")
		Expect(ok).To(BeTrue())
		Expect(a).To(Equal(ActionBlock))
	})

	It("falls back to the default action", func() {
		cfg := NERConfig{DefaultAction: ActionMask}
		a, ok := cfg.ResolveAction("ANYTHING")
		Expect(ok).To(BeTrue())
		Expect(a).To(Equal(ActionMask))
	})

	It("ignores a group with no override and no default", func() {
		cfg := NERConfig{}
		_, ok := cfg.ResolveAction("ANYTHING")
		Expect(ok).To(BeFalse())
	})
})
