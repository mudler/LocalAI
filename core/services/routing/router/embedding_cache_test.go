package router_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/routing/router"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// capturingEmbedder records the text it was last asked to embed and returns a
// fixed vector, so a test can assert what the cache fed the embedder.
type capturingEmbedder struct {
	mu       sync.Mutex
	lastText string
}

func (e *capturingEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastText = text
	return []float32{1, 2, 3}, nil
}

// fakeEmbedder returns a vector keyed by a lookup table; this lets the
// test exercise hit/miss control without depending on a real model.
type fakeEmbedder struct {
	mu       sync.Mutex
	table    map[string][]float32
	failOnce bool
}

func (e *fakeEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.failOnce {
		e.failOnce = false
		return nil, errors.New("embedder offline")
	}
	v, ok := e.table[text]
	if !ok {
		return nil, errors.New("no embedding for: " + text)
	}
	return v, nil
}

// memVectorStore is an in-memory KNN store with exact-vector hits, used
// to exercise the cache layer without a real local-store backend.
// Similarity is 1.0 for an exact match (after vector quantisation), 0.5
// for "close" (configured via the second-arg suffix), 0.0 otherwise.
type memVectorStore struct {
	mu      sync.Mutex
	entries []memEntry
	failOps int // remaining Search calls to fail before returning miss
}

type memEntry struct {
	vec     []float32
	payload []byte
}

func (s *memVectorStore) Search(_ context.Context, vec []float32) (float64, []byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failOps > 0 {
		s.failOps--
		return 0, nil, false, errors.New("store offline")
	}
	for _, e := range s.entries {
		if vecEqual(e.vec, vec) {
			return 1.0, e.payload, true, nil
		}
	}
	// "close" hit if the leading element matches but the rest doesn't —
	// lets a test simulate sim=0.8 without floating-point fragility.
	for _, e := range s.entries {
		if len(vec) > 0 && len(e.vec) > 0 && vec[0] == e.vec[0] {
			return 0.80, e.payload, true, nil
		}
	}
	return 0, nil, false, nil
}

func (s *memVectorStore) Insert(_ context.Context, vec []float32, payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, memEntry{vec: append([]float32(nil), vec...), payload: append([]byte(nil), payload...)})
	return nil
}

func vecEqual(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// stubInner is a Classifier that records call count and returns a
// pre-programmed Decision.
type stubInner struct {
	name     string
	decision router.Decision
	err      error
	calls    int
}

func (s *stubInner) Classify(_ context.Context, _ router.Probe) (router.Decision, error) {
	s.calls++
	if s.err != nil {
		return router.Decision{}, s.err
	}
	return s.decision, nil
}

func (s *stubInner) Name() string { return s.name }

var _ = Describe("EmbeddingCache", func() {
	ctx := context.Background()

	Context("miss then hit on exact prompt", func() {
		It("populates the cache and serves the second call", func() {
			embedder := &fakeEmbedder{table: map[string][]float32{
				"how do I exit vim": {1, 2, 3},
			}}
			store := &memVectorStore{}
			inner := &stubInner{
				name:     "score",
				decision: router.Decision{Labels: []string{"code-generation"}, Score: 0.9},
			}
			cache := router.NewEmbeddingCacheClassifier(inner, embedder, store, 0.92, 0.6)

			// First call → miss, inner runs, decision stored.
			d, err := cache.Classify(ctx, router.Probe{Prompt: "how do I exit vim"})
			Expect(err).NotTo(HaveOccurred(), "first classify")
			Expect(d.Cached).To(BeFalse(), "first call should be a miss")
			Expect(inner.calls).To(Equal(1))

			// Second call with the same prompt → hit, inner NOT called again.
			d, err = cache.Classify(ctx, router.Probe{Prompt: "how do I exit vim"})
			Expect(err).NotTo(HaveOccurred(), "second classify")
			Expect(d.Cached).To(BeTrue(), "second call should be a cache hit")
			Expect(d.CacheSimilarity).To(Equal(1.0))
			Expect(inner.calls).To(Equal(1), "inner ran on a hit")
			Expect(d.Labels).To(Equal([]string{"code-generation"}))

			stats := cache.Stats()
			Expect(stats.Hits).To(Equal(uint64(1)))
			Expect(stats.Misses).To(Equal(uint64(1)))
			// Second call had sim=1.0 (exact match), so the top bucket
			// should have one count.
			Expect(stats.SimilarityBuckets[9]).To(Equal(uint64(1)), "SimilarityBuckets[9] should be 1 (sim=1.0 hit)")
		})
	})

	Context("similarity below threshold", func() {
		It("counts as a near-miss", func() {
			// Two distinct prompts that produce vectors sharing only the
			// first element — memVectorStore reports similarity 0.80, below
			// the 0.92 threshold.
			embedder := &fakeEmbedder{table: map[string][]float32{
				"first prompt":  {1, 1, 1},
				"second prompt": {1, 9, 9},
			}}
			store := &memVectorStore{}
			inner := &stubInner{
				name:     "score",
				decision: router.Decision{Labels: []string{"math-reasoning"}, Score: 0.95},
			}
			cache := router.NewEmbeddingCacheClassifier(inner, embedder, store, 0.92, 0.6)

			_, _ = cache.Classify(ctx, router.Probe{Prompt: "first prompt"})
			d, err := cache.Classify(ctx, router.Probe{Prompt: "second prompt"})
			Expect(err).NotTo(HaveOccurred(), "classify")
			Expect(d.Cached).To(BeFalse(), "0.80 sim below 0.92 threshold should not hit")
			Expect(inner.calls).To(Equal(2), "inner should have run twice")
			stats := cache.Stats()
			Expect(stats.NearMisses).To(Equal(uint64(1)), "NearMisses (sim=0.80 below 0.92 threshold)")
			// Second call hit at sim=0.80 → bucket [0.8, 0.9) = index 8.
			// First call missed cleanly (empty store) → no bucket.
			Expect(stats.SimilarityBuckets[8]).To(Equal(uint64(1)), "SimilarityBuckets[8] (sim=0.80 near-miss)")
		})
	})

	Context("low confidence decisions", func() {
		It("are not cached", func() {
			embedder := &fakeEmbedder{table: map[string][]float32{
				"ambiguous": {7, 7, 7},
			}}
			store := &memVectorStore{}
			// Score 0.4 < confidenceThreshold 0.6 → don't cache.
			inner := &stubInner{
				name:     "score",
				decision: router.Decision{Labels: []string{"casual-chat"}, Score: 0.4},
			}
			cache := router.NewEmbeddingCacheClassifier(inner, embedder, store, 0.92, 0.6)

			_, _ = cache.Classify(ctx, router.Probe{Prompt: "ambiguous"})
			_, _ = cache.Classify(ctx, router.Probe{Prompt: "ambiguous"})

			Expect(inner.calls).To(Equal(2), "second call should also miss")
			stats := cache.Stats()
			Expect(stats.LowConfidence).To(Equal(uint64(2)))
			Expect(stats.Hits).To(Equal(uint64(0)))
		})
	})

	Context("embedder error", func() {
		It("degrades to inner classifier", func() {
			embedder := &fakeEmbedder{
				table:    map[string][]float32{"p": {1}},
				failOnce: true,
			}
			store := &memVectorStore{}
			inner := &stubInner{
				name:     "score",
				decision: router.Decision{Labels: []string{"x"}, Score: 0.99},
			}
			cache := router.NewEmbeddingCacheClassifier(inner, embedder, store, 0.92, 0.6)

			d, err := cache.Classify(ctx, router.Probe{Prompt: "p"})
			Expect(err).NotTo(HaveOccurred(), "classify")
			Expect(d.Cached).To(BeFalse(), "embedder error should not produce a cache hit")
			Expect(inner.calls).To(Equal(1), "inner should have run once via fallthrough")
			stats := cache.Stats()
			Expect(stats.EmbedderErrors).To(Equal(uint64(1)))
		})
	})

	Context("store error", func() {
		It("degrades to inner classifier", func() {
			embedder := &fakeEmbedder{table: map[string][]float32{"p": {1}}}
			store := &memVectorStore{failOps: 1}
			inner := &stubInner{
				name:     "score",
				decision: router.Decision{Labels: []string{"x"}, Score: 0.99},
			}
			cache := router.NewEmbeddingCacheClassifier(inner, embedder, store, 0.92, 0.6)

			_, _ = cache.Classify(ctx, router.Probe{Prompt: "p"})
			stats := cache.Stats()
			Expect(stats.StoreErrors).To(Equal(uint64(1)))
		})
	})

	Context("Name", func() {
		It("returns inner classifier name", func() {
			embedder := &fakeEmbedder{}
			store := &memVectorStore{}
			inner := &stubInner{name: "score"}
			cache := router.NewEmbeddingCacheClassifier(inner, embedder, store, 0, 0)
			Expect(cache.Name()).To(Equal("score"))
		})
	})

	Context("inner error", func() {
		It("propagates", func() {
			embedder := &fakeEmbedder{table: map[string][]float32{"p": {1}}}
			store := &memVectorStore{}
			inner := &stubInner{name: "score", err: errors.New("classifier blew up")}
			cache := router.NewEmbeddingCacheClassifier(inner, embedder, store, 0.92, 0.6)

			_, err := cache.Classify(ctx, router.Probe{Prompt: "p"})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("classifier blew up"))
		})
	})

	Context("default thresholds", func() {
		It("apply for zero values", func() {
			embedder := &fakeEmbedder{table: map[string][]float32{"p": {1}}}
			store := &memVectorStore{}
			inner := &stubInner{name: "score", decision: router.Decision{Labels: []string{"y"}, Score: 0.7}}
			// thresholds=0 → defaults (0.92 / 0.60). 0.7 > 0.60 so should
			// cache, and a re-call hits at sim=1.0 > 0.92.
			cache := router.NewEmbeddingCacheClassifier(inner, embedder, store, 0, 0)
			_, _ = cache.Classify(ctx, router.Probe{Prompt: "p"})
			d, _ := cache.Classify(ctx, router.Probe{Prompt: "p"})
			Expect(d.Cached).To(BeTrue(), "expected hit with default thresholds")
		})
	})

	Context("corrupt payload", func() {
		It("is treated as miss", func() {
			embedder := &fakeEmbedder{table: map[string][]float32{"p": {1}}}
			store := &memVectorStore{}
			// Pre-poison the store with garbage that decodes to an empty
			// label slice — Search will hit but the payload decoder must
			// reject it, falling through to the inner classifier.
			garbage, _ := json.Marshal(map[string]any{"labels": []string{}, "score": 1.0})
			_ = store.Insert(ctx, []float32{1}, garbage)
			inner := &stubInner{name: "score", decision: router.Decision{Labels: []string{"ok"}, Score: 0.8}}
			cache := router.NewEmbeddingCacheClassifier(inner, embedder, store, 0.5, 0.5)
			d, err := cache.Classify(ctx, router.Probe{Prompt: "p"})
			Expect(err).NotTo(HaveOccurred(), "classify")
			Expect(d.Cached).To(BeFalse(), "corrupt payload should not surface as a hit")
			Expect(inner.calls).To(Equal(1), "inner should have run via fallthrough")
		})
	})
})

var _ = Describe("EmbeddingCache WithTokenTrim", func() {
	ctx := context.Background()
	wordCount := func(s string) (int, error) { return len(strings.Fields(s)), nil }

	It("embeds the most recent turns that fit the embedder context, not the full prompt", func() {
		emb := &capturingEmbedder{}
		store := &memVectorStore{}
		inner := &stubInner{name: "score", decision: router.Decision{Labels: []string{"x"}, Score: 0.1}}
		// context_size 50 → budget 50−16 margin ≈ 34 tokens, far under the
		// ~120-word transcript below, so the oldest turns must be dropped.
		cache := router.NewEmbeddingCacheClassifier(inner, emb, store, 0.92, 0.6).
			WithTokenTrim(wordCount, 50)

		msgs := make([]string, 0, 31)
		for i := range 30 {
			msgs = append(msgs, fmt.Sprintf("OLDturn%d filler filler filler", i))
		}
		msgs = append(msgs, "NEWESTTURN final words here")
		full := strings.Join(msgs, "\n")

		_, err := cache.Classify(ctx, router.Probe{Prompt: full, Messages: msgs})
		Expect(err).NotTo(HaveOccurred())
		Expect(emb.lastText).To(ContainSubstring("NEWESTTURN"), "newest turn must survive")
		Expect(emb.lastText).NotTo(ContainSubstring("OLDturn0 "), "oldest turns trimmed to fit context")
		Expect(emb.lastText).NotTo(Equal(full), "must not embed the untrimmed prompt")
	})

	It("embeds Probe.Prompt unchanged when no trim is wired", func() {
		emb := &capturingEmbedder{}
		store := &memVectorStore{}
		inner := &stubInner{name: "score", decision: router.Decision{Labels: []string{"x"}, Score: 0.1}}
		cache := router.NewEmbeddingCacheClassifier(inner, emb, store, 0.92, 0.6)

		_, err := cache.Classify(ctx, router.Probe{Prompt: "PROMPTASIS", Messages: []string{"ignored-no-tokenizer"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(emb.lastText).To(Equal("PROMPTASIS"))
	})
})

var _ = Describe("EmbeddingCache latency", func() {
	It("is populated on hits", func() {
		embedder := &fakeEmbedder{table: map[string][]float32{"p": {1}}}
		store := &memVectorStore{}
		inner := &stubInner{name: "score", decision: router.Decision{Labels: []string{"x"}, Score: 0.9, Latency: time.Millisecond}}
		cache := router.NewEmbeddingCacheClassifier(inner, embedder, store, 0.92, 0.6)

		_, _ = cache.Classify(context.Background(), router.Probe{Prompt: "p"})
		d, _ := cache.Classify(context.Background(), router.Probe{Prompt: "p"})
		Expect(d.Cached).To(BeTrue(), "expected hit")
		// On a hit, Latency reflects the cache-lookup time, NOT the original
		// classifier latency stored in the payload.
		Expect(d.Latency).To(BeNumerically("<", time.Second), "Latency unreasonably high for an in-memory hit")
	})
})
