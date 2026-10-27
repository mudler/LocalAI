package backend

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mudler/localrecall/rag"
)

func (b *backendInProcess) settings(name string) (CollectionModelSettings, error) {
	var settings CollectionModelSettings
	if b.cfg.ModelSettings != nil {
		var err error
		settings, err = b.cfg.ModelSettings(name)
		if err != nil {
			return CollectionModelSettings{}, err
		}
	}
	if settings.EmbeddingModel == "" {
		settings.EmbeddingModel = b.cfg.EmbeddingModel
	}
	return settings, nil
}

func (b *backendInProcess) identityPath(name string) string {
	return filepath.Join(b.cfg.CollectionDBPath, "embedding-models", fmt.Sprintf("%x.json", sha256.Sum256([]byte(name))))
}

// Existing stores without a sidecar predate overrides and use the pool default.
// Always open them with that identity: constructors can re-embed on dimension changes.
func (b *backendInProcess) storedModel(name, desired string) (string, error) {
	data, err := os.ReadFile(b.identityPath(name))
	if err == nil {
		var model string
		if err := json.Unmarshal(data, &model); err != nil {
			return "", fmt.Errorf("read embedding model for %s: %w", name, err)
		}
		return model, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(b.cfg.CollectionDBPath, "collection-"+name+".json")); err == nil {
		return b.cfg.EmbeddingModel, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return desired, nil
}

func (b *backendInProcess) saveModel(name, model string) error {
	path := b.identityPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, _ := json.Marshal(model)
	tmp, err := os.CreateTemp(filepath.Dir(path), ".model-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (b *backendInProcess) construct(name, desired string) (*rag.PersistentKB, error) {
	model, err := b.storedModel(name, desired)
	if err != nil {
		return nil, err
	}
	// Persist before construction, including legacy identities, so retries and
	// restarts cannot reinterpret a populated store using a new override.
	if err = b.saveModel(name, model); err != nil {
		return nil, err
	}
	kb := newVectorEngine(b.cfg.VectorEngine, b.openAIClient, b.cfg.LLMAPIURL, b.cfg.LLMAPIKey, name, b.cfg.CollectionDBPath, b.cfg.FileAssets, model, b.cfg.DatabaseURL, b.cfg.MaxChunkingSize, b.cfg.ChunkOverlap)
	if kb == nil {
		return nil, fmt.Errorf("unsupported or misconfigured vector engine")
	}
	return kb, nil
}

// lockCollection serializes the operations of one collection that must see a
// stable embedding identity: the model check and swap in writable, Reset, and
// the Store or Search that follows the check. The lock is per collection
// because hosts such as LocalAI share one backend between all agents, and a
// long upload in one collection must not stall searches in the others.
// PersistentKB already serializes Store and Search in a single collection.
// Call it as: defer b.lockCollection(name)().
func (b *backendInProcess) lockCollection(name string) func() {
	b.locksMu.Lock()
	if b.collectionLocks == nil {
		b.collectionLocks = map[string]*sync.Mutex{}
	}
	mu, ok := b.collectionLocks[name]
	if !ok {
		mu = &sync.Mutex{}
		b.collectionLocks[name] = mu
	}
	b.locksMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

func (b *backendInProcess) markHadSources(name string) {
	b.sourcesMu.Lock()
	defer b.sourcesMu.Unlock()
	if b.hadSources == nil {
		b.hadSources = map[string]bool{}
	}
	b.hadSources[name] = true
}

func (b *backendInProcess) markResetWithSources(name string) {
	b.sourcesMu.Lock()
	defer b.sourcesMu.Unlock()
	if b.resetWithSources == nil {
		b.resetWithSources = map[string]bool{}
	}
	b.resetWithSources[name] = true
}

func (b *backendInProcess) sourceHistory(name string) (hadSources, resetWithSources bool) {
	b.sourcesMu.Lock()
	defer b.sourcesMu.Unlock()
	return b.hadSources[name], b.resetWithSources[name]
}

// Removing a source cannot cancel an active fetch, so its history must outlive
// the current list of configured sources.
func (b *backendInProcess) rememberSources(name string, kb *rag.PersistentKB) {
	if len(kb.GetExternalSources()) > 0 {
		b.markHadSources(name)
	}
}

// Called with the collection lock held. Reads and resets deliberately use lookup
// instead, so a mismatched collection can still be inspected and cleared.
func (b *backendInProcess) writable(name string, settings CollectionModelSettings, create bool) (*rag.PersistentKB, error) {
	if _, resetWithSources := b.sourceHistory(name); resetWithSources {
		return nil, fmt.Errorf("collection %s had external sources; restart the service after reset before recreating it", name)
	}
	kb, exists := b.lookup(name)
	if !exists {
		if !create {
			return nil, fmt.Errorf("collection not found: %s", name)
		}
		var err error
		kb, err = b.construct(name, settings.EmbeddingModel)
		if err != nil {
			return nil, err
		}
		b.state.Mu.Lock()
		b.state.Collections[name] = kb
		b.state.SourceManager.RegisterCollection(name, kb)
		b.state.Mu.Unlock()
	}
	b.rememberSources(name, kb)
	model, err := b.storedModel(name, settings.EmbeddingModel)
	if err != nil {
		return nil, err
	}
	if model == settings.EmbeddingModel {
		return kb, nil
	}
	if hadSources, _ := b.sourceHistory(name); hadSources {
		return nil, fmt.Errorf("collection %s had external sources; reset and restart the service before switching embedding models", name)
	}
	if kb.Count() > 0 || len(kb.ListDocuments()) > 0 {
		return nil, fmt.Errorf("collection %s uses embedding model %q; reset the collection before switching to %q", name, model, settings.EmbeddingModel)
	}
	if err := kb.Reset(); err != nil {
		return nil, err
	}
	// Invalidate before changing identity. Holding the cache lock prevents
	// readers from rehydrating between invalidation and the identity write.
	// Failures leave a placeholder that lookup retries with the saved model.
	b.state.Mu.Lock()
	defer b.state.Mu.Unlock()
	b.state.Collections[name] = nil
	if err := b.saveModel(name, settings.EmbeddingModel); err != nil {
		return nil, err
	}
	replacement, err := b.construct(name, settings.EmbeddingModel)
	if err != nil {
		return nil, err
	}
	b.state.Collections[name] = replacement
	b.state.SourceManager.RegisterCollection(name, replacement)
	return replacement, nil
}

func (b *backendInProcess) rerank(model, query string, results []SearchResult, maxResults int) ([]SearchResult, error) {
	if model == "" || len(results) == 0 {
		return results, nil
	}
	docs := make([]string, len(results))
	for i, r := range results {
		docs[i] = r.Content
	}
	payload, err := json.Marshal(map[string]any{"model": model, "query": query, "documents": docs, "top_n": maxResults})
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(b.cfg.LLMAPIURL, "/")
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	req, err := http.NewRequest(http.MethodPost, base+"/rerank", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if b.cfg.LLMAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+b.cfg.LLMAPIKey)
	}
	client := &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rerank request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rerank request failed with HTTP %d", response.StatusCode)
	}
	var body struct {
		Results []struct {
			Index *int     `json:"index"`
			Score *float32 `json:"relevance_score"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("invalid rerank response: %w", err)
	}
	if len(body.Results) == 0 {
		return nil, fmt.Errorf("rerank returned no results")
	}
	seen := map[int]bool{}
	out := make([]SearchResult, 0, len(body.Results))
	for _, result := range body.Results {
		if result.Index == nil || *result.Index < 0 || *result.Index >= len(results) || seen[*result.Index] || result.Score == nil || math.IsNaN(float64(*result.Score)) || math.IsInf(float64(*result.Score), 0) {
			return nil, fmt.Errorf("invalid rerank result")
		}
		index := *result.Index
		seen[index] = true
		item := results[index]
		item.Similarity = *result.Score
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Similarity > out[j].Similarity })
	if maxResults > 0 && len(out) > maxResults {
		out = out[:maxResults]
	}
	return out, nil
}
