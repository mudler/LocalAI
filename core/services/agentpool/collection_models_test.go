package agentpool

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/mudler/LocalAGI/core/state"
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type modelConfigBackend struct {
	AgentConfigBackend
	configs map[string]*state.AgentConfig
}

func (b *modelConfigBackend) GetConfig(userID, name string) *state.AgentConfig {
	return b.configs[userID+"/"+name]
}
func (b *modelConfigBackend) ListAgents(userID string) map[string]bool {
	names := map[string]bool{}
	for key, cfg := range b.configs {
		if key == userID+"/"+cfg.Name {
			names[cfg.Name] = true
		}
	}
	return names
}

var _ = Describe("knowledge-base models", func() {
	var svc *AgentPoolService
	var backend *modelConfigBackend
	BeforeEach(func() {
		backend = &modelConfigBackend{configs: map[string]*state.AgentConfig{
			"alice/Research": {Name: "Research", EmbeddingModel: "alice-embed", RerankerModel: "alice-rank"},
			"bob/Research":   {Name: "Research", EmbeddingModel: "bob-embed"},
		}}
		svc = &AgentPoolService{appConfig: &config.ApplicationConfig{}, configBackend: backend}
	})
	It("resolves namespaced and normalized names without crossing user boundaries", func() {
		resolve := svc.buildCollectionsConfig("", "", "", "").ModelSettings
		Expect(resolve).NotTo(BeNil())
		settings, err := resolve("alice:Research")
		Expect(err).NotTo(HaveOccurred())
		Expect(settings.EmbeddingModel).To(Equal("alice-embed"))
		settings, err = resolve("bob:research")
		Expect(err).NotTo(HaveOccurred())
		Expect(settings.EmbeddingModel).To(Equal("bob-embed"))
		settings, err = resolve("missing")
		Expect(err).NotTo(HaveOccurred())
		Expect(settings.EmbeddingModel).To(BeEmpty())
		backend.configs["alice/Research"].RerankerModel = "updated-rank"
		settings, err = resolve("alice:Research")
		Expect(err).NotTo(HaveOccurred())
		Expect(settings.RerankerModel).To(Equal("updated-rank"))
	})
	It("rejects ambiguous normalized names", func() {
		backend.configs["alice/RESEARCH"] = &state.AgentConfig{Name: "RESEARCH", EmbeddingModel: "other"}
		resolve := svc.buildCollectionsConfig("", "", "", "").ModelSettings
		Expect(resolve).NotTo(BeNil())
		_, err := resolve("alice:research")
		Expect(err).To(HaveOccurred())
	})
	It("strips only the owning user's namespace", func() {
		resolve := svc.collectionModelSettings("alice")
		for _, name := range []string{"Research", "alice:Research", "research"} {
			settings, err := resolve(name)
			Expect(err).NotTo(HaveOccurred())
			Expect(settings.EmbeddingModel).To(Equal("alice-embed"))
		}
		_, err := resolve("bob:Research")
		Expect(err).To(HaveOccurred())
	})
	It("uses per-user models for upload and search and refreshes reranking", func() {
		var mu sync.Mutex
		embeddings, rerankers := []string{}, []string{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct{ Model string }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if strings.HasSuffix(r.URL.Path, "/rerank") {
				rerankers = append(rerankers, body.Model)
				_, _ = w.Write([]byte(`{"results":[{"index":0,"relevance_score":0.9}]}`))
				return
			}
			embeddings = append(embeddings, body.Model)
			_, _ = w.Write([]byte(`{"data":[{"embedding":[1,0,0],"index":0}],"model":"test","usage":{}}`))
		}))
		DeferCleanup(server.Close)
		svc.appConfig.AgentPool.APIURL = server.URL + "/v1"
		svc.appConfig.AgentPool.VectorEngine = "chromem"
		svc.appConfig.AgentPool.EmbeddingModel = "default"
		svc.appConfig.AgentPool.MaxChunkingSize = 200
		manager := NewUserServicesManager(NewUserScopedStorage(GinkgoT().TempDir(), GinkgoT().TempDir()), svc.appConfig, nil, nil, nil)
		svc.SetUserServicesManager(manager)
		for _, user := range []string{"alice", "bob"} {
			coll, err := manager.GetCollections(user)
			Expect(err).NotTo(HaveOccurred())
			Expect(coll.CreateCollection("research")).To(Succeed())
			_, err = coll.Upload("research", "document.txt", strings.NewReader("a research document"))
			Expect(err).NotTo(HaveOccurred())
			results, err := coll.Search("research", "query", 1)
			Expect(err).NotTo(HaveOccurred())
			Expect(results).To(HaveLen(1))
		}
		backend.configs["alice/Research"].RerankerModel = "updated-rank"
		coll, err := manager.GetCollections("alice")
		Expect(err).NotTo(HaveOccurred())
		_, err = coll.Search("research", "query", 1)
		Expect(err).NotTo(HaveOccurred())
		mu.Lock()
		defer mu.Unlock()
		Expect(embeddings).To(Equal([]string{"alice-embed", "alice-embed", "bob-embed", "bob-embed", "alice-embed"}))
		Expect(rerankers).To(Equal([]string{"alice-rank", "updated-rank"}))
	})
})
