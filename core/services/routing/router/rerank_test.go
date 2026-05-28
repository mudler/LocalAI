package router

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/backend"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type stubReranker struct {
	results []backend.RerankResult
	err     error
	calls   int
	lastQ   string
	lastDs  []string
}

func (r *stubReranker) Rerank(_ context.Context, query string, documents []string) ([]backend.RerankResult, error) {
	r.calls++
	r.lastQ = query
	r.lastDs = append(r.lastDs[:0], documents...)
	if r.err != nil {
		return nil, r.err
	}
	return r.results, nil
}

var _ = Describe("RerankClassifier", func() {
	It("activates the single label whose description is most relevant", func() {
		// code-generation dominates; the other two fall below the
		// default 0.5 activation threshold.
		r := &stubReranker{results: []backend.RerankResult{
			{Index: 0, RelevanceScore: 0.92},
			{Index: 1, RelevanceScore: 0.10},
			{Index: 2, RelevanceScore: 0.05},
		}}
		c := NewRerankClassifier(testPolicies(), r, 0, 0)
		d, err := c.Classify(context.Background(), Probe{Prompt: "debug my null pointer"})
		Expect(err).NotTo(HaveOccurred())
		Expect(equalLabels(d.Labels, []string{"code-generation"})).To(BeTrue(), "got %v", d.Labels)
		Expect(d.Score).To(BeNumerically(">=", 0.9))
	})

	It("trims the query to the reranker context, keeping the newest turns", func() {
		r := &stubReranker{results: []backend.RerankResult{
			{Index: 0, RelevanceScore: 0.92},
			{Index: 1, RelevanceScore: 0.10},
			{Index: 2, RelevanceScore: 0.05},
		}}
		wordCount := func(s string) (int, error) { return len(strings.Fields(s)), nil }
		// budget = 60 − longest policy description − 16 margin; still well under
		// the ~120-word transcript, so the oldest turns drop.
		c := NewRerankClassifier(testPolicies(), r, 0, 0).WithTokenTrim(wordCount, 60)

		msgs := make([]string, 0, 31)
		for i := range 30 {
			msgs = append(msgs, fmt.Sprintf("OLDturn%d aaa bbb ccc", i))
		}
		msgs = append(msgs, "NEWESTTURN zzz")
		full := strings.Join(msgs, "\n")

		_, err := c.Classify(context.Background(), Probe{Prompt: full, Messages: msgs})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.lastQ).To(ContainSubstring("NEWESTTURN"), "newest turn must survive")
		Expect(r.lastQ).NotTo(ContainSubstring("OLDturn0 "), "oldest turns trimmed to fit context")
		Expect(r.lastQ).NotTo(Equal(full), "must not rerank the untrimmed prompt")
	})

	It("activates multiple labels when several descriptions clear threshold", func() {
		r := &stubReranker{results: []backend.RerankResult{
			{Index: 0, RelevanceScore: 0.85},
			{Index: 1, RelevanceScore: 0.10},
			{Index: 2, RelevanceScore: 0.75},
		}}
		c := NewRerankClassifier(testPolicies(), r, 0, 0)
		d, err := c.Classify(context.Background(), Probe{Prompt: "write code that solves this equation"})
		Expect(err).NotTo(HaveOccurred())
		Expect(sortedLabels(d)).To(Equal([]string{"code-generation", "math-reasoning"}))
	})

	It("falls back to argmax when no description clears threshold", func() {
		// All scores below 0.5 — defensively fall back to the top
		// label so the router always has something to route on.
		r := &stubReranker{results: []backend.RerankResult{
			{Index: 0, RelevanceScore: 0.30},
			{Index: 1, RelevanceScore: 0.10},
			{Index: 2, RelevanceScore: 0.20},
		}}
		c := NewRerankClassifier(testPolicies(), r, 0, 0)
		d, err := c.Classify(context.Background(), Probe{Prompt: "ambiguous"})
		Expect(err).NotTo(HaveOccurred())
		Expect(equalLabels(d.Labels, []string{"code-generation"})).To(BeTrue(), "got %v", d.Labels)
	})

	It("returns the reranker error verbatim", func() {
		r := &stubReranker{err: errors.New("backend down")}
		c := NewRerankClassifier(testPolicies(), r, 0, 0)
		_, err := c.Classify(context.Background(), Probe{Prompt: "anything"})
		Expect(err).To(MatchError(ContainSubstring("backend down")))
	})

	It("respects the configured activation threshold", func() {
		r := &stubReranker{results: []backend.RerankResult{
			{Index: 0, RelevanceScore: 0.40},
			{Index: 1, RelevanceScore: 0.10},
			{Index: 2, RelevanceScore: 0.45},
		}}
		// Threshold lowered to 0.35 — both 0.40 and 0.45 should activate.
		c := NewRerankClassifier(testPolicies(), r, 0, 0.35)
		d, err := c.Classify(context.Background(), Probe{Prompt: "borderline"})
		Expect(err).NotTo(HaveOccurred())
		Expect(sortedLabels(d)).To(Equal([]string{"code-generation", "math-reasoning"}))
	})

	It("caches by case-folded prompt", func() {
		r := &stubReranker{results: []backend.RerankResult{
			{Index: 0, RelevanceScore: 0.92},
			{Index: 1, RelevanceScore: 0.10},
			{Index: 2, RelevanceScore: 0.05},
		}}
		c := NewRerankClassifier(testPolicies(), r, 4, 0)
		_, _ = c.Classify(context.Background(), Probe{Prompt: "Debug my null pointer"})
		_, _ = c.Classify(context.Background(), Probe{Prompt: " debug MY null POINTER "})
		Expect(r.calls).To(Equal(1), "case+whitespace variants should hit the cache")
		Expect(c.CacheLen()).To(Equal(1))
	})

	It("scores against the policy descriptions, not the labels", func() {
		// The reranker library should be reranking *descriptions*
		// (natural English the model was trained on), not abstract
		// label slugs that wouldn't match any pretraining distribution.
		r := &stubReranker{results: []backend.RerankResult{
			{Index: 0, RelevanceScore: 0.9},
		}}
		c := NewRerankClassifier(testPolicies(), r, 0, 0)
		_, err := c.Classify(context.Background(), Probe{Prompt: "p"})
		Expect(err).NotTo(HaveOccurred())
		Expect(r.lastDs).To(Equal([]string{
			"writing, debugging, or explaining code",
			"small talk and general conversation",
			"arithmetic, equations, word problems",
		}))
	})
})
