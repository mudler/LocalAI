package router

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/backend"
)

// RerankClassifier scores each policy description against the prompt
// via a reranker model and activates the labels whose relevance clears
// an absolute threshold. Robust when policy labels are abstract
// relative to user prompts — the description is the natural English
// the reranker was trained on.
type RerankClassifier struct {
	reranker            backend.Reranker
	activationThreshold float64
	// labels[i] is the policy label corresponding to documents[i] —
	// both are scattered indices into the reranker's input order.
	// Materialised once at construction so Classify never allocates
	// them per call.
	labels    []string
	documents []string
	cache     *labelSetCache

	// budget trims the query to the reranker model's context minus the
	// longest policy description (paired with the query per rerank call);
	// nil reranks Probe.Prompt as built by the caller.
	budget *lazyBudget
}

// defaultRerankActivationThreshold is the relevance floor a label
// must clear to be considered active. Reranker scores live in [0, 1]
// for cross-encoder / ColBERT heads; 0.5 picks "more positive than
// not on this label."
const defaultRerankActivationThreshold = 0.5

func NewRerankClassifier(policies []ScorePolicy, reranker backend.Reranker, cacheCap int, activationThreshold float64) *RerankClassifier {
	if len(policies) == 0 {
		panic("router/rerank: at least one policy is required")
	}
	if reranker == nil {
		panic("router/rerank: reranker is required (configure router.classifier_model)")
	}
	for _, p := range policies {
		if p.Label == "" {
			panic("router/rerank: policy has empty label")
		}
		if p.Description == "" {
			panic(fmt.Sprintf("router/rerank: policy %q has no description", p.Label))
		}
	}
	if activationThreshold <= 0 {
		activationThreshold = defaultRerankActivationThreshold
	}
	labels := make([]string, len(policies))
	docs := make([]string, len(policies))
	for i, p := range policies {
		labels[i] = p.Label
		docs[i] = p.Description
	}
	return &RerankClassifier{
		reranker:            reranker,
		activationThreshold: activationThreshold,
		labels:              labels,
		documents:           docs,
		cache:               newLabelSetCache(cacheCap),
	}
}

// WithTokenTrim wires the reranker model's own tokenizer and context so the
// query is trimmed to the most recent turns that fit alongside the longest
// policy description. nil tokenizer / non-positive context leaves trimming
// off. Returns the receiver for chaining at construction.
func (c *RerankClassifier) WithTokenTrim(tokenize func(string) (int, error), maxContextTokens int) *RerankClassifier {
	c.budget = &lazyBudget{tokenize: tokenize, maxContext: maxContextTokens, extras: c.documents}
	return c
}

func (c *RerankClassifier) Name() string { return ClassifierColbert }

func (c *RerankClassifier) Classify(ctx context.Context, p Probe) (Decision, error) {
	start := time.Now()
	query := trimmedProbeText(p, c.budget, identityRender)
	key := cacheKey(query)
	if hit, ok := c.cache.get(key); ok {
		return Decision{Labels: hit, Score: 1.0, Latency: time.Since(start)}, nil
	}

	results, err := c.reranker.Rerank(ctx, query, c.documents)
	if err != nil {
		return errDecision(start, fmt.Errorf("rerank classify: %w", err))
	}

	// The reranker may return fewer-than-N entries (top_n filtering)
	// or reorder them by score. Scatter back into input order so
	// threshold + argmax don't depend on result ordering.
	scores := make([]float64, len(c.labels))
	for _, r := range results {
		if r.Index < 0 || r.Index >= len(scores) {
			continue
		}
		scores[r.Index] = float64(r.RelevanceScore)
	}

	active, bestIdx := selectActive(scores, c.labels, c.activationThreshold)
	c.cache.put(key, active)
	labelScores := NewLabelScores(c.labels, scores)
	return Decision{
		Labels:              active,
		Score:               scores[bestIdx],
		Latency:             time.Since(start),
		LabelScores:         labelScores,
		ActivationThreshold: c.activationThreshold,
	}, nil
}

func (c *RerankClassifier) CacheLen() int { return c.cache.len() }
