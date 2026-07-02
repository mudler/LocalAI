package router

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/mudler/LocalAI/core/backend"
)

// KNNClassifier routes by nearest-neighbour vote over a curated,
// labelled corpus of example prompts. It is the first-class form of
// what EmbeddingCacheClassifier does opportunistically: instead of
// caching another classifier's decisions, the corpus is seeded and
// curated explicitly (via the router corpus API), each entry carrying
// the policy labels a matching prompt should activate.
//
// Classify embeds the probe, fetches the K nearest corpus entries, and
// activates every label whose similarity-weighted vote share clears
// VoteThreshold. Neighbours below SimilarityThreshold are discarded
// first — that cutoff is the epistemic gate: a probe dissimilar from
// *all* labelled experience is undecidable by construction, so the
// classifier returns an empty label set and the middleware falls back
// to cfg.Router.Fallback (the assumed-best model) rather than guessing.
//
// The classifier never inserts into the corpus on its own. Routing
// outcomes only become corpus entries through explicit curation — a
// mislabelled exemplar poisons every future neighbourhood around it,
// so growth is an admin decision, not a side effect.
type KNNClassifier struct {
	embedder            backend.Embedder
	store               backend.VectorStore
	k                   int
	similarityThreshold float64
	voteThreshold       float64

	// budget trims the conversation to the embedder model's own context
	// before embedding; nil embeds Probe.Prompt as built by the caller.
	budget *lazyBudget
}

// Defaults. K=3 keeps a weighted majority meaningful on small corpora
// while tolerating one mislabelled neighbour; the similarity default
// matches the embedding cache so one threshold intuition serves both.
// VoteThreshold 0.5 means a label activates on a weighted majority —
// with K=1 this degenerates to "the nearest entry's labels", the same
// contract the embedding cache implements.
const (
	defaultKNNK          = 3
	defaultKNNSimilarity = 0.80
	defaultKNNVote       = 0.5
)

// KNNClassifierOptions carries the tunables; zero values pick the
// package defaults above.
type KNNClassifierOptions struct {
	K                   int
	SimilarityThreshold float64
	VoteThreshold       float64
}

// NewKNNClassifier builds a KNN classifier over the given embedder and
// vector store. Panics on nil embedder/store — same fail-fast posture
// as the other classifiers; buildClassifier validates config before
// construction.
func NewKNNClassifier(embedder backend.Embedder, store backend.VectorStore, opts KNNClassifierOptions) *KNNClassifier {
	if embedder == nil {
		panic("router/knn: embedder is required")
	}
	if store == nil {
		panic("router/knn: vector store is required")
	}
	if opts.K <= 0 {
		opts.K = defaultKNNK
	}
	if opts.SimilarityThreshold <= 0 {
		opts.SimilarityThreshold = defaultKNNSimilarity
	}
	if opts.VoteThreshold <= 0 {
		opts.VoteThreshold = defaultKNNVote
	}
	return &KNNClassifier{
		embedder:            embedder,
		store:               store,
		k:                   opts.K,
		similarityThreshold: opts.SimilarityThreshold,
		voteThreshold:       opts.VoteThreshold,
	}
}

// WithTokenTrim wires the embedder model's own tokenizer and context so
// the probe embeds the most recent turns that fit instead of a
// caller-chosen size. nil tokenizer / non-positive context leaves
// trimming off. Returns the receiver for chaining at construction.
func (c *KNNClassifier) WithTokenTrim(tokenize func(string) (int, error), maxContextTokens int) *KNNClassifier {
	c.budget = &lazyBudget{tokenize: tokenize, maxContext: maxContextTokens}
	return c
}

func (c *KNNClassifier) Name() string { return ClassifierKNN }

func (c *KNNClassifier) Classify(ctx context.Context, p Probe) (Decision, error) {
	start := time.Now()

	vec, err := c.embedder.Embed(ctx, trimmedProbeText(p, c.budget, identityRender))
	if err != nil {
		return errDecision(start, fmt.Errorf("knn classifier embed: %w", err))
	}
	neighbors, err := c.store.SearchK(ctx, vec, c.k)
	if err != nil {
		return errDecision(start, fmt.Errorf("knn classifier search: %w", err))
	}

	// Epistemic gate: only neighbours the probe is genuinely close to
	// may vote. Keeping sub-threshold neighbours out of the vote (rather
	// than merely gating on the best one) stops far-away corpus regions
	// from diluting a clear local majority.
	best := 0.0
	usable := neighbors[:0]
	for _, n := range neighbors {
		if n.Similarity > best {
			best = n.Similarity
		}
		if n.Similarity >= c.similarityThreshold {
			usable = append(usable, n)
		}
	}
	if len(usable) == 0 {
		// Out of corpus range — empty label set routes to the fallback
		// via MatchCandidate's empty-active-set contract. Surfacing the
		// best similarity in the decision log tells the admin whether
		// the corpus needs entries near this probe or the threshold is
		// simply too tight.
		return Decision{
			NearestSimilarity:   best,
			ActivationThreshold: c.voteThreshold,
			Latency:             time.Since(start),
		}, nil
	}

	votes := map[string]float64{}
	total := 0.0
	for _, n := range usable {
		entry, ok := decodeCorpusEntry(n.Payload)
		if !ok {
			// A corrupt payload can't vote; it still counted toward K.
			continue
		}
		total += n.Similarity
		for _, l := range entry.Labels {
			votes[l] += n.Similarity
		}
	}
	if total == 0 {
		return Decision{
			NearestSimilarity:   best,
			ActivationThreshold: c.voteThreshold,
			Latency:             time.Since(start),
		}, nil
	}

	// Vote shares in descending order; ties broken lexicographically so
	// the decision log is deterministic.
	labels := make([]string, 0, len(votes))
	for l := range votes {
		labels = append(labels, l)
	}
	sort.Slice(labels, func(i, j int) bool {
		if votes[labels[i]] != votes[labels[j]] {
			return votes[labels[i]] > votes[labels[j]]
		}
		return labels[i] < labels[j]
	})
	scores := make([]float64, len(labels))
	active := []string{}
	for i, l := range labels {
		scores[i] = votes[l] / total
		if scores[i] >= c.voteThreshold {
			active = append(active, l)
		}
	}

	d := Decision{
		Labels:              active,
		ActivationThreshold: c.voteThreshold,
		LabelScores:         NewLabelScores(labels, scores),
		NearestSimilarity:   best,
		Latency:             time.Since(start),
	}
	if len(active) > 0 {
		d.Score = votes[active[0]] / total
	}
	return d, nil
}

// corpusEntry is the stored shape of one labelled exemplar. Kept
// deliberately minimal: the vector key lives in the store, the text
// lives only in the corpus file (never returned by inspection APIs),
// so the store payload is just the label set.
type corpusEntry struct {
	Labels []string `json:"labels"`
}

// EncodeCorpusEntry serialises the labels of one corpus exemplar into
// the vector-store payload shape Classify votes over. Exported for the
// corpus loader/API in core, which owns insertion.
func EncodeCorpusEntry(labels []string) ([]byte, error) {
	if len(labels) == 0 {
		return nil, fmt.Errorf("corpus entry needs at least one label")
	}
	return json.Marshal(corpusEntry{Labels: labels})
}

func decodeCorpusEntry(b []byte) (corpusEntry, bool) {
	var e corpusEntry
	if err := json.Unmarshal(b, &e); err != nil || len(e.Labels) == 0 {
		return corpusEntry{}, false
	}
	return e, true
}
