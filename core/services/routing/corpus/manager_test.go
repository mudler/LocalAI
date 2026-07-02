package corpus_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/services/routing/corpus"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// countingEmbedder returns a deterministic vector per (model, text)
// and counts calls, so specs can assert when re-embedding happened vs
// when the cached vectors were reused.
type countingEmbedder struct {
	mu    sync.Mutex
	model float32 // baked into the vector so specs can tell models apart
	calls int
}

func (e *countingEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls += 1
	return []float32{float32(len(text)), e.model}, nil
}

// capturingStore records index mutations. Search/SearchK are
// irrelevant to the manager and return clean misses.
type capturingStore struct {
	mu       sync.Mutex
	payloads [][]byte
	batches  int
	deleted  [][]float32
}

func (s *capturingStore) Search(_ context.Context, _ []float32) (float64, []byte, bool, error) {
	return 0, nil, false, nil
}

func (s *capturingStore) SearchK(_ context.Context, _ []float32, _ int) ([]backend.Neighbor, error) {
	return nil, nil
}

func (s *capturingStore) Insert(_ context.Context, _ []float32, payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.payloads = append(s.payloads, payload)
	return nil
}

func (s *capturingStore) InsertBatch(_ context.Context, vecs [][]float32, payloads [][]byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.batches++
	s.payloads = append(s.payloads, payloads...)
	_ = vecs
	return nil
}

func (s *capturingStore) Delete(_ context.Context, vecs [][]float32) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, vecs...)
	return nil
}

var _ = Describe("corpus.Manager", func() {
	var (
		dir      string
		mgr      *corpus.Manager
		embedder *countingEmbedder
		store    *capturingStore
		ctx      context.Context
	)

	const storeName = "router-corpus-smart-router"

	seed := []corpus.Entry{
		{Text: "debug this Go null pointer", Labels: []string{"code-generation"}},
		{Text: "what is 12 * 42?", Labels: []string{"math-reasoning"}},
		{Text: "refactor this and explain the math", Labels: []string{"code-generation", "math-reasoning"}},
	}

	BeforeEach(func() {
		d, err := os.MkdirTemp("", "corpus-test-*")
		Expect(err).NotTo(HaveOccurred())
		dir = d
		mgr = corpus.NewManager(dir)
		embedder = &countingEmbedder{model: 1}
		store = &capturingStore{}
		ctx = context.Background()
	})

	AfterEach(func() {
		_ = os.RemoveAll(dir)
	})

	It("adds entries: embeds, persists, and indexes them", func() {
		added, skipped, err := mgr.Add(ctx, storeName, "embed-1", embedder, store, seed)
		Expect(err).NotTo(HaveOccurred())
		Expect(added).To(Equal(3))
		Expect(skipped).To(BeZero())
		Expect(embedder.calls).To(Equal(3))
		Expect(store.payloads).To(HaveLen(3))
		Expect(store.batches).To(Equal(1), "should use the batch fast path")

		// Payloads are the label sets the classifier votes over.
		Expect(string(store.payloads[0])).To(ContainSubstring("code-generation"))

		// Persisted on disk under a sanitised name.
		_, err = os.Stat(filepath.Join(dir, storeName+".jsonl"))
		Expect(err).NotTo(HaveOccurred())
	})

	It("skips duplicate texts instead of double-weighting them", func() {
		_, _, err := mgr.Add(ctx, storeName, "embed-1", embedder, store, seed)
		Expect(err).NotTo(HaveOccurred())
		added, skipped, err := mgr.Add(ctx, storeName, "embed-1", embedder, store, seed[:2])
		Expect(err).NotTo(HaveOccurred())
		Expect(added).To(BeZero())
		Expect(skipped).To(Equal(2))
	})

	It("rejects empty text and label-less entries", func() {
		_, _, err := mgr.Add(ctx, storeName, "embed-1", embedder, store, []corpus.Entry{{Text: " ", Labels: []string{"x"}}})
		Expect(err).To(HaveOccurred())
		_, _, err = mgr.Add(ctx, storeName, "embed-1", embedder, store, []corpus.Entry{{Text: "hello", Labels: nil}})
		Expect(err).To(HaveOccurred())
	})

	It("reloads a persisted corpus into a fresh index without re-embedding", func() {
		_, _, err := mgr.Add(ctx, storeName, "embed-1", embedder, store, seed)
		Expect(err).NotTo(HaveOccurred())

		// Simulate restart: fresh manager over the same dir, empty index.
		mgr2 := corpus.NewManager(dir)
		store2 := &capturingStore{}
		embedder2 := &countingEmbedder{model: 1}
		n, err := mgr2.EnsureLoaded(ctx, storeName, "embed-1", embedder2, store2)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(3))
		Expect(store2.payloads).To(HaveLen(3))
		Expect(embedder2.calls).To(BeZero(), "cached vectors must be reused")

		// Second call is a no-op — already synced this process.
		n, err = mgr2.EnsureLoaded(ctx, storeName, "embed-1", embedder2, store2)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeZero())
	})

	It("re-embeds entries recorded under a different embedding model", func() {
		_, _, err := mgr.Add(ctx, storeName, "embed-1", embedder, store, seed)
		Expect(err).NotTo(HaveOccurred())

		mgr2 := corpus.NewManager(dir)
		newEmbedder := &countingEmbedder{model: 2}
		store2 := &capturingStore{}
		n, err := mgr2.EnsureLoaded(ctx, storeName, "embed-2", newEmbedder, store2)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(3))
		Expect(newEmbedder.calls).To(Equal(3), "fingerprint mismatch must re-embed")

		// The rewrite is durable: a third manager loads under embed-2
		// without touching the embedder again.
		mgr3 := corpus.NewManager(dir)
		embedder3 := &countingEmbedder{model: 2}
		n, err = mgr3.EnsureLoaded(ctx, storeName, "embed-2", embedder3, &capturingStore{})
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(3))
		Expect(embedder3.calls).To(BeZero())
	})

	It("refuses to mix embedding models in a live index", func() {
		_, _, err := mgr.Add(ctx, storeName, "embed-1", embedder, store, seed)
		Expect(err).NotTo(HaveOccurred())
		_, err = mgr.EnsureLoaded(ctx, storeName, "embed-1", embedder, store)
		Expect(err).NotTo(HaveOccurred())

		_, err = mgr.EnsureLoaded(ctx, storeName, "embed-2", &countingEmbedder{model: 2}, store)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("restart"))
	})

	It("reports label counts and never texts", func() {
		_, _, err := mgr.Add(ctx, storeName, "embed-1", embedder, store, seed)
		Expect(err).NotTo(HaveOccurred())
		st, err := mgr.Stats(storeName)
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Total).To(Equal(3))
		Expect(st.LabelCounts).To(Equal(map[string]int{
			"code-generation": 2,
			"math-reasoning":  2,
		}))
		Expect(st.EmbeddingModels).To(Equal([]string{"embed-1"}))
	})

	It("clears the file and the live index", func() {
		_, _, err := mgr.Add(ctx, storeName, "embed-1", embedder, store, seed)
		Expect(err).NotTo(HaveOccurred())

		n, err := mgr.Clear(ctx, storeName, store)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(Equal(3))
		Expect(store.deleted).To(HaveLen(3))

		st, err := mgr.Stats(storeName)
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Total).To(BeZero())

		// And a load after clear indexes nothing.
		loaded, err := corpus.NewManager(dir).EnsureLoaded(ctx, storeName, "embed-1", embedder, &capturingStore{})
		Expect(err).NotTo(HaveOccurred())
		Expect(loaded).To(BeZero())
	})

	It("treats a missing file as an empty corpus", func() {
		n, err := mgr.EnsureLoaded(ctx, "never-seeded", "embed-1", embedder, store)
		Expect(err).NotTo(HaveOccurred())
		Expect(n).To(BeZero())
		st, err := mgr.Stats("never-seeded")
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Total).To(BeZero())
	})

	It("sanitises hostile store names into the corpus dir", func() {
		hostile := "../../etc/passwd"
		_, _, err := mgr.Add(ctx, hostile, "embed-1", embedder, store, seed[:1])
		Expect(err).NotTo(HaveOccurred())
		entries, err := os.ReadDir(dir)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(1))
		Expect(strings.Contains(entries[0].Name(), "/")).To(BeFalse())
	})
})
