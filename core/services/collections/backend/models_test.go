package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mudler/localrecall/rag"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// fakeModelServer serves OpenAI-compatible embeddings and rerank responses and
// records what the backend asked for.
type fakeModelServer struct {
	mu          sync.Mutex
	models      []string
	reranked    bool
	rerankModel string
	rerankAuth  string
	// beforeEmbed and beforeRerank run before the response is written; tests
	// use them to hold a request open.
	beforeEmbed  func(body string)
	beforeRerank func(body string)
}

func (f *fakeModelServer) embeddingModels() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.models...)
}

func (f *fakeModelServer) lastModel() string {
	models := f.embeddingModels()
	Expect(models).ToNot(BeEmpty())
	return models[len(models)-1]
}

func (f *fakeModelServer) wasReranked() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reranked
}

func (f *fakeModelServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	body := string(raw)
	if r.URL.Path == "/v1/rerank" {
		var req struct{ Model string }
		_ = json.Unmarshal(raw, &req)
		f.mu.Lock()
		f.reranked = true
		f.rerankModel = req.Model
		f.rerankAuth = r.Header.Get("Authorization")
		hook := f.beforeRerank
		f.mu.Unlock()
		if hook != nil {
			hook(body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"index":0,"relevance_score":0.8}]}`))
		return
	}
	var req struct{ Model string }
	_ = json.Unmarshal(raw, &req)
	f.mu.Lock()
	f.models = append(f.models, req.Model)
	hook := f.beforeEmbed
	f.mu.Unlock()
	if hook != nil {
		hook(body)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"data":[{"embedding":[1,0,0],"index":0}],"model":"test","usage":{}}`))
}

func newModelBackend(selected *CollectionModelSettings) (*backendInProcess, *State, *fakeModelServer) {
	fake := &fakeModelServer{}
	server := httptest.NewServer(fake)
	DeferCleanup(server.Close)
	backend, st := NewInProcessBackend(&Config{
		LLMAPIURL:        server.URL + "/v1",
		LLMAPIKey:        "secret",
		CollectionDBPath: GinkgoT().TempDir(),
		FileAssets:       GinkgoT().TempDir(),
		VectorEngine:     "chromem",
		EmbeddingModel:   "default",
		MaxChunkingSize:  200,
		ModelSettings:    func(string) (CollectionModelSettings, error) { return *selected, nil },
	})
	DeferCleanup(st.SourceManager.Stop)
	return backend.(*backendInProcess), st, fake
}

// storeText creates the collection when needed and uploads text to it, as a
// REST client that adds a document does.
func storeText(b *backendInProcess, collection, text string) error {
	if err := b.CreateCollection(collection); err != nil {
		return err
	}
	_, err := b.Upload(context.Background(), collection, strings.ReplaceAll(text, " ", "-")+".txt", strings.NewReader(text))
	return err
}

var _ = Describe("Per-collection model settings", func() {
	It("embeds with the override, reranks, and requires a reset to switch models", func() {
		selected := CollectionModelSettings{EmbeddingModel: "custom", RerankerModel: "ranker"}
		b, _, fake := newModelBackend(&selected)
		Expect(storeText(b, "agent", "a source document")).To(Succeed())

		result, err := b.Search(context.Background(), "agent", "query", 1)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(HaveLen(1))
		Expect(result[0].Similarity).To(Equal(float32(0.8)))
		Expect(result[0].Metadata["source"]).ToNot(BeEmpty())
		Expect(fake.rerankModel).To(Equal("ranker"))
		Expect(fake.rerankAuth).To(Equal("Bearer secret"))
		Expect(fake.embeddingModels()).To(HaveEach("custom"))

		selected.EmbeddingModel = "changed"
		_, err = b.Search(context.Background(), "agent", "query", 1)
		Expect(err).To(MatchError(ContainSubstring("reset")))
		Expect(storeText(b, "agent", "must not be indexed")).ToNot(Succeed(), "mixed embeddings accepted")

		Expect(b.Reset("agent")).To(Succeed())
		Expect(storeText(b, "agent", "fresh document")).To(Succeed())
		Expect(fake.lastModel()).To(Equal("changed"))
	})

	It("keeps the identity file inside its directory", func() {
		b := &backendInProcess{cfg: &Config{CollectionDBPath: GinkgoT().TempDir()}}
		Expect(b.identityPath("../../outside")).To(HavePrefix(b.cfg.CollectionDBPath + "/embedding-models/"))
	})

	DescribeTable("keeps the embedding identity across restarts",
		func(legacy bool) {
			selected := CollectionModelSettings{}
			b, st, fake := newModelBackend(&selected)
			Expect(storeText(b, "agent", "persisted document")).To(Succeed())
			st.SourceManager.Stop()
			if legacy {
				Expect(os.Remove(b.identityPath("agent"))).To(Succeed())
			}

			selected.EmbeddingModel = "changed"
			restarted, nextState := NewInProcessBackend(b.cfg)
			defer nextState.SourceManager.Stop()
			_, err := restarted.Search(context.Background(), "agent", "query", 1)
			Expect(err).To(HaveOccurred(), "restart accepted changed embedding model")

			entries, err := restarted.ListEntries("agent")
			Expect(err).ToNot(HaveOccurred(), "mismatched collection must stay inspectable")
			Expect(entries).To(HaveLen(1))
			Expect(fake.embeddingModels()).To(HaveEach("default"))

			names, err := restarted.ListCollections()
			Expect(err).ToNot(HaveOccurred())
			Expect(names).To(HaveLen(1), "sidecar exposed as a collection")
			Expect(restarted.Reset("agent")).To(Succeed())
		},
		Entry("with an identity sidecar", false),
		Entry("legacy store without a sidecar", true),
	)

	It("requires a restart before recreating a reset collection that had sources", func() {
		selected := CollectionModelSettings{}
		b, st, _ := newModelBackend(&selected)
		Expect(b.CreateCollection("agent")).To(Succeed())
		kb := st.Collections["agent"]
		Expect(kb.AddExternalSource(&rag.ExternalSource{URL: "https://example.invalid/source", UpdateInterval: time.Hour})).To(Succeed())
		Expect(b.Reset("agent")).To(Succeed())

		selected.EmbeddingModel = "changed"
		Expect(b.CreateCollection("agent")).To(MatchError(ContainSubstring("restart")))
		model, err := b.storedModel("agent", "changed")
		Expect(err).ToNot(HaveOccurred())
		Expect(model).To(Equal("default"), "forgot identity while a source might still write")
	})

	DescribeTable("remembers removed sources and blocks a model switch",
		func(reset bool) {
			selected := CollectionModelSettings{}
			b, st, _ := newModelBackend(&selected)
			Expect(b.CreateCollection("agent")).To(Succeed())
			url := "https://example.invalid/source"
			// Seed persisted metadata without starting a network fetch. Removal
			// must remember that a source could already have an in-flight fetch.
			Expect(st.Collections["agent"].AddExternalSource(&rag.ExternalSource{URL: url, UpdateInterval: time.Hour})).To(Succeed())
			Expect(b.RemoveSource("agent", url)).To(Succeed())
			if reset {
				Expect(b.Reset("agent")).To(Succeed())
			}

			selected.EmbeddingModel = "changed"
			Expect(b.CreateCollection("agent")).ToNot(Succeed(), "removed source allowed a model switch while its fetch may still write")
			model, err := b.storedModel("agent", "changed")
			Expect(err).ToNot(HaveOccurred())
			Expect(model).To(Equal("default"))
		},
		Entry("without reset", false),
		Entry("after reset", true),
	)

	It("does not reuse the old engine after a failed model switch", func() {
		selected := CollectionModelSettings{}
		b, _, fake := newModelBackend(&selected)
		Expect(b.CreateCollection("agent")).To(Succeed())
		selected.EmbeddingModel = "changed"
		// Force the real constructor to fail after the new identity is saved.
		b.cfg.VectorEngine = "unavailable"
		Expect(b.CreateCollection("agent")).ToNot(Succeed())
		b.cfg.VectorEngine = "chromem"

		Expect(storeText(b, "agent", "retry after engine recovery")).To(Succeed())
		Expect(fake.lastModel()).To(Equal("changed"))
	})

	It("blocks inference when the model resolver fails but keeps inspection and reset", func() {
		selected := CollectionModelSettings{RerankerModel: "ranker"}
		b, st, fake := newModelBackend(&selected)
		// Store does not rerank, so nothing has been reranked yet.
		Expect(storeText(b, "agent", "existing document")).To(Succeed())
		before := len(fake.embeddingModels())

		denied := fmt.Errorf("model access denied")
		b.cfg.ModelSettings = func(string) (CollectionModelSettings, error) { return selected, denied }
		checks := map[string]func() error{
			"create": func() error { return b.CreateCollection("agent") },
			"upload": func() error {
				_, err := b.Upload(context.Background(), "agent", "file.txt", strings.NewReader("text"))
				return err
			},
			"search": func() error { _, err := b.Search(context.Background(), "agent", "query", 1); return err },
			"source": func() error { return b.AddSource("agent", "invalid://source", 60) },
		}
		for name, run := range checks {
			Expect(run()).To(BeIdenticalTo(denied), name)
		}
		_, ok := st.EnsureCollection("agent")
		Expect(ok).To(BeFalse(), "ensure ignored resolver failure")
		Expect(fake.embeddingModels()).To(HaveLen(before), "denied resolver emitted embedding requests")
		Expect(fake.wasReranked()).To(BeFalse(), "denied resolver emitted a rerank request")

		entries, err := b.ListEntries("agent")
		Expect(err).ToNot(HaveOccurred())
		Expect(entries).ToNot(BeEmpty())
		Expect(b.Reset("agent")).To(Succeed())
	})
})

// heldReader blocks its first Read until release is closed.
type heldReader struct {
	data     io.Reader
	entered  chan struct{}
	release  chan struct{}
	signaled bool
}

func (r *heldReader) Read(p []byte) (int, error) {
	if !r.signaled {
		r.signaled = true
		close(r.entered)
		<-r.release
	}
	return r.data.Read(p)
}

var _ = Describe("Collection operation locking", func() {
	const marker = "HOLD-THIS-EMBEDDING"

	var (
		selected CollectionModelSettings
		b        *backendInProcess
		fake     *fakeModelServer
		entered  chan struct{}
		release  chan struct{}
		once     sync.Once
	)

	// holdOn makes requests whose body contains marker wait until release.
	holdOn := func(body string) {
		if strings.Contains(body, marker) {
			once.Do(func() { close(entered) })
			<-release
		}
	}

	BeforeEach(func() {
		selected = CollectionModelSettings{}
		b, _, fake = newModelBackend(&selected)
		entered = make(chan struct{})
		release = make(chan struct{})
		once = sync.Once{}
		// Unblock anything still waiting if a spec fails early.
		DeferCleanup(func() {
			select {
			case <-release:
			default:
				close(release)
			}
		})
		Expect(b.CreateCollection("alpha")).To(Succeed())
		Expect(b.CreateCollection("beta")).To(Succeed())
		_, err := b.Upload(context.Background(), "beta", "beta.txt", strings.NewReader("beta document"))
		Expect(err).ToNot(HaveOccurred())
	})

	It("does not serialize a slow upload in one collection with a search in another", func() {
		fake.mu.Lock()
		fake.beforeEmbed = holdOn
		fake.mu.Unlock()

		uploadDone := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			_, err := b.Upload(context.Background(), "alpha", "alpha.txt", strings.NewReader(marker))
			uploadDone <- err
		}()
		Eventually(entered).Should(BeClosed())

		searchDone := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			_, err := b.Search(context.Background(), "beta", "query", 1)
			searchDone <- err
		}()
		Eventually(searchDone, 5*time.Second).Should(Receive(BeNil()), "search in beta waited for the upload in alpha")
		Consistently(uploadDone, 100*time.Millisecond).ShouldNot(Receive())

		close(release)
		Eventually(uploadDone, 5*time.Second).Should(Receive(BeNil()))
	})

	It("does not hold the collection lock during the rerank call", func() {
		selected.RerankerModel = "ranker"
		fake.mu.Lock()
		fake.beforeRerank = holdOn
		fake.mu.Unlock()

		searchDone := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			_, err := b.Search(context.Background(), "beta", marker, 1)
			searchDone <- err
		}()
		Eventually(entered).Should(BeClosed())

		// A model switch on beta needs the collection lock. It must fail
		// quickly (beta has documents) instead of waiting for the reranker.
		selected.EmbeddingModel = "changed"
		createDone := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			createDone <- b.CreateCollection("beta")
		}()
		Eventually(createDone, 5*time.Second).Should(Receive(MatchError(ContainSubstring("reset"))))

		close(release)
		Eventually(searchDone, 5*time.Second).Should(Receive(BeNil()))
	})

	It("still serializes a model switch with an in-flight upload in the same collection", func() {
		// The reader holds the upload after its model check and before
		// kb.Store, where only the collection lock protects the identity.
		uploadDone := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			_, err := b.Upload(context.Background(), "alpha", "alpha.txt", &heldReader{data: strings.NewReader("alpha document"), entered: entered, release: release})
			uploadDone <- err
		}()
		Eventually(entered).Should(BeClosed())

		// alpha is still empty. Without the collection lock the switch would
		// see no documents and swap the identity, and the upload would then
		// store vectors from the old model.
		selected.EmbeddingModel = "changed"
		switchDone := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			switchDone <- b.CreateCollection("alpha")
		}()
		Consistently(switchDone, 300*time.Millisecond).ShouldNot(Receive(), "model switch ran during an upload")

		close(release)
		Eventually(uploadDone, 5*time.Second).Should(Receive(BeNil()))
		Eventually(switchDone, 5*time.Second).Should(Receive(MatchError(ContainSubstring("reset"))))
		model, err := b.storedModel("alpha", "changed")
		Expect(err).ToNot(HaveOccurred())
		Expect(model).To(Equal("default"))
	})
})

var _ = Describe("Reranking", func() {
	serve := func(handler http.HandlerFunc) *backendInProcess {
		server := httptest.NewServer(handler)
		DeferCleanup(server.Close)
		return &backendInProcess{cfg: &Config{LLMAPIURL: server.URL, LLMAPIKey: "secret"}}
	}

	DescribeTable("rejects invalid rerank responses",
		func(response string) {
			b := serve(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(response)) })
			_, err := b.rerank(context.Background(), "ranker", "query", []SearchResult{{Content: "one"}}, 1)
			Expect(err).To(HaveOccurred())
		},
		Entry("index out of range", `{"results":[{"index":2,"relevance_score":1}]}`),
		Entry("duplicate index", `{"results":[{"index":0,"relevance_score":1},{"index":0,"relevance_score":0.5}]}`),
		Entry("infinite score", `{"results":[{"index":0,"relevance_score":1e50}]}`),
		Entry("missing results", `{}`),
		Entry("empty results", `{"results":[]}`),
	)

	It("orders results by score and keeps metadata", func() {
		b := serve(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"results":[{"index":0,"relevance_score":0.1},{"index":1,"relevance_score":0.9}]}`))
		})
		out, err := b.rerank(context.Background(), "ranker", "query", []SearchResult{{ID: "first", Content: "one"}, {ID: "second", Content: "two", Metadata: map[string]string{"source": "citation"}}}, 2)
		Expect(err).ToNot(HaveOccurred())
		Expect(out[0].ID).To(Equal("second"))
		Expect(out[0].Metadata).To(HaveKeyWithValue("source", "citation"))
	})

	It("does not follow redirects or hide HTTP errors", func() {
		var reached bool
		var mu sync.Mutex
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			reached = true
			mu.Unlock()
		}))
		DeferCleanup(target.Close)
		for _, status := range []int{http.StatusTemporaryRedirect, http.StatusInternalServerError} {
			b := serve(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", target.URL)
				w.WriteHeader(status)
			})
			_, err := b.rerank(context.Background(), "ranker", "query", []SearchResult{{Content: "one"}}, 1)
			Expect(err).To(HaveOccurred(), "rerank failure ignored for HTTP %d", status)
		}
		mu.Lock()
		defer mu.Unlock()
		Expect(reached).To(BeFalse(), "rerank request followed a credential-bearing redirect")
	})

	It("widens the vector search so the reranker sees more than the top hits", func() {
		selected := CollectionModelSettings{RerankerModel: "ranker"}
		b, _, fake := newModelBackend(&selected)
		Expect(b.CreateCollection("wide")).To(Succeed())
		for i := range 6 {
			_, err := b.Upload(context.Background(), "wide", fmt.Sprintf("doc-%d.txt", i), strings.NewReader(fmt.Sprintf("document %d", i)))
			Expect(err).ToNot(HaveOccurred())
		}
		documents := make(chan int, 1)
		fake.mu.Lock()
		fake.beforeRerank = func(body string) {
			var req struct{ Documents []string }
			_ = json.Unmarshal([]byte(body), &req)
			documents <- len(req.Documents)
		}
		fake.mu.Unlock()

		out, err := b.Search(context.Background(), "wide", "query", 1)
		Expect(err).ToNot(HaveOccurred())
		Expect(out).To(HaveLen(1))
		Expect(documents).To(Receive(Equal(rerankCandidateFactor)))
	})
})
