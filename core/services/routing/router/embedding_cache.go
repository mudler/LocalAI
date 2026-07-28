package router

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/xlog"
)

// EmbeddingCacheStats reports per-classifier cache hit/miss/error
// counts. Surfaced through /api/router/cache/stats and the Routing tab
// so admins can see whether the cache is paying off.
//
// Hits + NearMisses + Misses equals the total number of Search calls
// that succeeded (no embedder/store error). NearMisses are kept
// separate from Misses because their similarity is observable —
// lowering similarity_threshold turns near-misses into hits without
// growing the cache, so the ratio tells admins how much room is left
// in the current threshold.
type EmbeddingCacheStats struct {
	Hits           uint64 `json:"hits"`
	Misses         uint64 `json:"misses"`         // empty store or no similar key
	NearMisses     uint64 `json:"near_misses"`    // store returned a key but below similarity_threshold
	LowConfidence  uint64 `json:"low_confidence"` // decisions we deliberately did not cache
	EmbedderErrors uint64 `json:"embedder_errors"`
	StoreErrors    uint64 `json:"store_errors"`

	// SimilarityBuckets is a 10-bin histogram of the cosine
	// similarities the store reported for any successful Search (hits
	// and near-misses combined). Index i covers similarity [i/10,
	// (i+1)/10). Counts are non-decreasing across the classifier's
	// lifetime; reset via process restart.
	SimilarityBuckets [10]uint64 `json:"similarity_buckets"`
}

// EmbeddingCacheClassifier wraps an inner Classifier with an
// embedding-similarity cache. On Classify it first embeds the probe,
// asks the vector store for the nearest past decision, and returns
// it if similarity passes the configured threshold. Misses fall
// through to the inner classifier, and high-confidence outcomes are
// inserted into the store for future hits.
//
// Failure modes — embedder error, store error — degrade to the inner
// classifier's result. Routing never fails because of cache plumbing.
type EmbeddingCacheClassifier struct {
	inner               Classifier
	embedder            backend.Embedder
	store               backend.VectorStore
	similarityThreshold float64
	confidenceThreshold float64

	// budget trims the conversation to the embedder model's own context
	// before embedding; nil embeds Probe.Prompt as built by the caller.
	budget *lazyBudget

	hits           atomic.Uint64
	misses         atomic.Uint64
	nearMisses     atomic.Uint64
	lowConfidence  atomic.Uint64
	embedderErrors atomic.Uint64
	storeErrors    atomic.Uint64
	simBuckets     [10]atomic.Uint64
}

// Default thresholds. Re-tune per (embedding model, corpus) — the
// admin histogram on the Routing tab shows where the cosine
// distribution actually sits.
const (
	defaultEmbeddingSimilarity = 0.80
	defaultEmbeddingConfidence = 0.60
)

// NewEmbeddingCacheClassifier wraps inner with an embedding-similarity
// cache. Panics on misconfiguration (nil inner / embedder / store) —
// same fail-fast posture as the score classifier.
//
// Zero threshold picks the package default (defaultEmbeddingSimilarity
// / defaultEmbeddingConfidence).
func NewEmbeddingCacheClassifier(inner Classifier, embedder backend.Embedder, store backend.VectorStore, similarityThreshold, confidenceThreshold float64) *EmbeddingCacheClassifier {
	if inner == nil {
		panic("router/embedding_cache: inner classifier is required")
	}
	if embedder == nil {
		panic("router/embedding_cache: embedder is required")
	}
	if store == nil {
		panic("router/embedding_cache: vector store is required")
	}
	if similarityThreshold <= 0 {
		similarityThreshold = defaultEmbeddingSimilarity
	}
	if confidenceThreshold <= 0 {
		confidenceThreshold = defaultEmbeddingConfidence
	}
	return &EmbeddingCacheClassifier{
		inner:               inner,
		embedder:            embedder,
		store:               store,
		similarityThreshold: similarityThreshold,
		confidenceThreshold: confidenceThreshold,
	}
}

// WithTokenTrim wires the embedder model's own tokenizer and context so the
// probe embeds the most recent turns that fit instead of a caller-chosen size.
// nil tokenizer / non-positive context leaves trimming off. Returns the
// receiver for chaining at construction.
func (c *EmbeddingCacheClassifier) WithTokenTrim(tokenize func(string) (int, error), maxContextTokens int) *EmbeddingCacheClassifier {
	c.budget = &lazyBudget{tokenize: tokenize, maxContext: maxContextTokens}
	return c
}

// Name is the inner classifier's name — the decision-log "classifier"
// field should reflect *what* made the decision, not the caching
// transport. Cache hits set Decision.Cached separately so admins can
// still distinguish a cached lookup from a fresh run.
func (c *EmbeddingCacheClassifier) Name() string {
	return c.inner.Name()
}

// Stats returns a snapshot of the cache counters.
func (c *EmbeddingCacheClassifier) Stats() EmbeddingCacheStats {
	s := EmbeddingCacheStats{
		Hits:           c.hits.Load(),
		Misses:         c.misses.Load(),
		NearMisses:     c.nearMisses.Load(),
		LowConfidence:  c.lowConfidence.Load(),
		EmbedderErrors: c.embedderErrors.Load(),
		StoreErrors:    c.storeErrors.Load(),
	}
	for i := range c.simBuckets {
		s.SimilarityBuckets[i] = c.simBuckets[i].Load()
	}
	return s
}

func (c *EmbeddingCacheClassifier) Classify(ctx context.Context, p Probe) (Decision, error) {
	start := time.Now()

	vec, err := c.embedder.Embed(ctx, trimmedProbeText(p, c.budget, identityRender))
	if err != nil {
		c.embedderErrors.Add(1)
		xlog.Warn("router: embedding cache embed failed", "error", err)
		// Embedder failure — fall through to the inner classifier so
		// routing still happens. The miss is not a hard error.
		return c.inner.Classify(ctx, p)
	}

	sim, payload, hit, err := c.store.Search(ctx, vec)
	if err != nil {
		c.storeErrors.Add(1)
		xlog.Warn("router: embedding cache store.Search failed", "error", err, "vec_dim", len(vec))
		return c.inner.Classify(ctx, p)
	}
	if hit {
		// Bin the similarity once, regardless of threshold outcome.
		// Admins read this back to see where the cosine distribution
		// sits relative to the configured similarity_threshold.
		c.recordSimilarity(sim)
		if sim >= c.similarityThreshold {
			if cached, ok := decodeCachedDecision(payload); ok {
				c.hits.Add(1)
				cached.Cached = true
				cached.CacheSimilarity = sim
				cached.Latency = time.Since(start)
				return cached, nil
			}
			// Payload corrupt — treat as miss and overwrite on the next
			// confident decision.
			c.misses.Add(1)
		} else {
			c.nearMisses.Add(1)
		}
	} else {
		c.misses.Add(1)
	}
	decision, err := c.inner.Classify(ctx, p)
	if err != nil {
		return decision, err
	}

	// Don't poison the cache with uncertain decisions. The score
	// classifier's softmax can put the top label as low as 1/N in
	// pathological cases; only store outcomes where the model is
	// clearly committed.
	if decision.Score < c.confidenceThreshold {
		c.lowConfidence.Add(1)
		return decision, nil
	}

	payload, encodeErr := encodeCachedDecision(decision)
	if encodeErr != nil {
		// Encoding can't realistically fail for the Decision type but
		// guard so a future field doesn't break routing silently.
		return decision, nil
	}
	if insertErr := c.store.Insert(ctx, vec, payload); insertErr != nil {
		c.storeErrors.Add(1)
		xlog.Warn("router: embedding cache store.Insert failed", "error", insertErr, "vec_dim", len(vec))
		// Insert failure is non-fatal — the decision is still good
		// for this request, only the future-hit benefit is lost.
	}
	return decision, nil
}

// recordSimilarity increments the histogram bucket covering the given
// cosine similarity. The store occasionally returns sim slightly above
// 1.0 due to floating-point error on exact matches; we clamp to the
// top bin to keep the histogram bounded.
func (c *EmbeddingCacheClassifier) recordSimilarity(sim float64) {
	bucket := max(0, min(9, int(sim*10)))
	c.simBuckets[bucket].Add(1)
}

// cachedDecision is the on-disk shape stored in the vector backend.
// Kept separate from Decision so transient fields (Latency, Cached,
// CacheSimilarity) don't get serialized — they're per-call, not
// per-prompt.
type cachedDecision struct {
	Labels []string `json:"labels"`
	Score  float64  `json:"score"`
}

func encodeCachedDecision(d Decision) ([]byte, error) {
	return json.Marshal(cachedDecision{Labels: append([]string(nil), d.Labels...), Score: d.Score})
}

func decodeCachedDecision(b []byte) (Decision, bool) {
	var cd cachedDecision
	if err := json.Unmarshal(b, &cd); err != nil {
		return Decision{}, false
	}
	if len(cd.Labels) == 0 {
		return Decision{}, false
	}
	return Decision{Labels: cd.Labels, Score: cd.Score}, true
}
