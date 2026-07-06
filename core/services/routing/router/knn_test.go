package router_test

import (
	"context"
	"errors"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/services/routing/router"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// scriptedKNNStore returns a fixed neighbour list from SearchK,
// letting tests exercise the vote/gate math without a real store.
type scriptedKNNStore struct {
	neighbors []backend.Neighbor
	err       error
	lastK     int
}

func (s *scriptedKNNStore) SearchK(_ context.Context, _ []float32, k int) ([]backend.Neighbor, error) {
	s.lastK = k
	if s.err != nil {
		return nil, s.err
	}
	if len(s.neighbors) > k {
		return s.neighbors[:k], s.err
	}
	return s.neighbors, s.err
}

func (s *scriptedKNNStore) Search(_ context.Context, _ []float32) (float64, []byte, bool, error) {
	return 0, nil, false, errors.New("knn classifier must use SearchK")
}

func (s *scriptedKNNStore) Insert(_ context.Context, _ []float32, _ []byte) error {
	return errors.New("knn classifier must never insert")
}

func mustEntry(id string, labels ...string) []byte {
	b, err := router.EncodeCorpusEntry(id, labels)
	Expect(err).ToNot(HaveOccurred())
	return b
}

var _ = Describe("KNNClassifier", func() {
	var (
		embedder *fakeEmbedder
		probe    router.Probe
		ctx      context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		embedder = &fakeEmbedder{table: map[string][]float32{"prompt": {1, 0, 0}}}
		probe = router.Probe{Prompt: "prompt"}
	})

	classify := func(store *scriptedKNNStore, opts router.KNNClassifierOptions) router.Decision {
		c := router.NewKNNClassifier(embedder, store, opts)
		d, err := c.Classify(ctx, probe)
		Expect(err).ToNot(HaveOccurred())
		return d
	}

	It("computes similarity-weighted vote shares exactly", func() {
		// Hand-computed: usable sims 0.90 {code}, 0.85 {code,math},
		// 0.82 {math}; total = 2.57.
		//   share(code) = (0.90+0.85)/2.57 = 0.68093...
		//   share(math) = (0.85+0.82)/2.57 = 0.64980...
		// Both clear the 0.5 majority → both active; candidate matching
		// then requires a model labelled for both.
		store := &scriptedKNNStore{neighbors: []backend.Neighbor{
			{Similarity: 0.90, Payload: mustEntry("e1", "code")},
			{Similarity: 0.85, Payload: mustEntry("e2", "code", "math")},
			{Similarity: 0.82, Payload: mustEntry("e3", "math")},
		}}
		d := classify(store, router.KNNClassifierOptions{K: 3})
		Expect(d.Labels).To(Equal([]string{"code", "math"}))
		Expect(d.LabelScores).To(HaveLen(2))
		Expect(d.LabelScores[0].Label).To(Equal("code"))
		Expect(d.LabelScores[0].Score).To(BeNumerically("~", 1.75/2.57, 1e-9))
		Expect(d.LabelScores[1].Label).To(Equal("math"))
		Expect(d.LabelScores[1].Score).To(BeNumerically("~", 1.67/2.57, 1e-9))
		Expect(d.Score).To(BeNumerically("~", 1.75/2.57, 1e-9))
		Expect(d.NearestSimilarity).To(BeNumerically("~", 0.90, 1e-9))
		Expect(store.lastK).To(Equal(3))
		// The decision names every consulted neighbour so the log can be
		// joined back to corpus entries.
		Expect(d.Neighbors).To(HaveLen(3))
		Expect(d.Neighbors[0].ID).To(Equal("e1"))
		Expect(d.Neighbors[0].Similarity).To(BeNumerically("~", 0.90, 1e-9))
		Expect(d.Neighbors[1].ID).To(Equal("e2"))
		Expect(d.Neighbors[1].Labels).To(Equal([]string{"code", "math"}))
		Expect(d.Neighbors[2].ID).To(Equal("e3"))
	})

	It("does not activate a minority label", func() {
		// share(chat) = 0.81/2.57 < 0.5 → inactive, but still reported
		// in LabelScores so the decision log shows how close it came.
		store := &scriptedKNNStore{neighbors: []backend.Neighbor{
			{Similarity: 0.90, Payload: mustEntry("e1", "code")},
			{Similarity: 0.86, Payload: mustEntry("e2", "code")},
			{Similarity: 0.81, Payload: mustEntry("e3", "chat")},
		}}
		d := classify(store, router.KNNClassifierOptions{K: 3})
		Expect(d.Labels).To(Equal([]string{"code"}))
		Expect(d.LabelScores).To(HaveLen(2))
		Expect(d.LabelScores[1].Label).To(Equal("chat"))
		Expect(d.LabelScores[1].Score).To(BeNumerically("<", 0.5))
	})

	It("gates out-of-corpus probes to the fallback (empty labels)", func() {
		store := &scriptedKNNStore{neighbors: []backend.Neighbor{
			{Similarity: 0.55, Payload: mustEntry("e1", "code")},
			{Similarity: 0.40, Payload: mustEntry("e2", "math")},
		}}
		d := classify(store, router.KNNClassifierOptions{SimilarityThreshold: 0.80})
		Expect(d.Labels).To(BeEmpty())
		// The admin-facing epistemic signal: how far away the nearest
		// labelled experience was.
		Expect(d.NearestSimilarity).To(BeNumerically("~", 0.55, 1e-9))
		// Fallback decisions still name the sub-gate neighbours — that is
		// exactly what makes them diagnosable.
		Expect(d.Neighbors).To(HaveLen(2))
		Expect(d.Neighbors[0].ID).To(Equal("e1"))
		Expect(d.Neighbors[1].ID).To(Equal("e2"))
	})

	It("excludes sub-threshold neighbours from the vote", func() {
		// The 0.3-sim {chat} neighbour must not dilute the local
		// majority: with it, share(code) would be 0.9/1.2 = 0.75; the
		// vote must instead be over the single usable neighbour.
		store := &scriptedKNNStore{neighbors: []backend.Neighbor{
			{Similarity: 0.90, Payload: mustEntry("e1", "code")},
			{Similarity: 0.30, Payload: mustEntry("e2", "chat")},
		}}
		d := classify(store, router.KNNClassifierOptions{K: 2, SimilarityThreshold: 0.80})
		Expect(d.Labels).To(Equal([]string{"code"}))
		Expect(d.LabelScores).To(HaveLen(1))
		Expect(d.LabelScores[0].Score).To(BeNumerically("~", 1.0, 1e-9))
	})

	It("degenerates to nearest-entry labels at K=1", func() {
		store := &scriptedKNNStore{neighbors: []backend.Neighbor{
			{Similarity: 0.95, Payload: mustEntry("e1", "reasoning", "math")},
		}}
		d := classify(store, router.KNNClassifierOptions{K: 1})
		Expect(d.Labels).To(Equal([]string{"math", "reasoning"}))
		Expect(d.Score).To(BeNumerically("~", 1.0, 1e-9))
	})

	It("skips corrupt payloads and falls back when nothing can vote", func() {
		store := &scriptedKNNStore{neighbors: []backend.Neighbor{
			{Similarity: 0.90, Payload: []byte("not json")},
		}}
		d := classify(store, router.KNNClassifierOptions{})
		Expect(d.Labels).To(BeEmpty())
		Expect(d.NearestSimilarity).To(BeNumerically("~", 0.90, 1e-9))
		// A corrupt payload is still visible in the neighbour list — an
		// empty ID at a real similarity flags index corruption.
		Expect(d.Neighbors).To(HaveLen(1))
		Expect(d.Neighbors[0].ID).To(BeEmpty())
		Expect(d.Neighbors[0].Similarity).To(BeNumerically("~", 0.90, 1e-9))
	})

	It("returns the embed error so the middleware can fall back", func() {
		embedder.table = nil // unknown prompt → fakeEmbedder errors
		store := &scriptedKNNStore{}
		c := router.NewKNNClassifier(embedder, store, router.KNNClassifierOptions{})
		_, err := c.Classify(ctx, probe)
		Expect(err).To(HaveOccurred())
	})

	It("returns the store error so the middleware can fall back", func() {
		store := &scriptedKNNStore{err: errors.New("store offline")}
		c := router.NewKNNClassifier(embedder, store, router.KNNClassifierOptions{})
		_, err := c.Classify(ctx, probe)
		Expect(err).To(HaveOccurred())
	})

	It("rejects corpus entries without labels at encode time", func() {
		_, err := router.EncodeCorpusEntry("e1", nil)
		Expect(err).To(HaveOccurred())
	})

	It("rejects corpus entries without an id at encode time", func() {
		_, err := router.EncodeCorpusEntry("", []string{"code"})
		Expect(err).To(HaveOccurred())
	})

	It("derives stable, text-free entry ids", func() {
		id := router.EntryID("Is 18857 a prime number? Answer yes or no.")
		Expect(id).To(HaveLen(16))
		Expect(id).To(Equal(router.EntryID("Is 18857 a prime number? Answer yes or no.")))
		Expect(id).ToNot(Equal(router.EntryID("Is 18858 a prime number? Answer yes or no.")))
	})

	It("panics on missing embedder or store", func() {
		Expect(func() { router.NewKNNClassifier(nil, &scriptedKNNStore{}, router.KNNClassifierOptions{}) }).To(Panic())
		Expect(func() { router.NewKNNClassifier(embedder, nil, router.KNNClassifierOptions{}) }).To(Panic())
	})
})
