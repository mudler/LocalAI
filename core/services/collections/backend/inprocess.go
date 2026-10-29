package backend

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mudler/localrecall/rag"
	"github.com/mudler/localrecall/rag/sources"
	"github.com/mudler/xlog"
	"github.com/sashabaranov/go-openai"
	"golang.org/x/sync/singleflight"
)

// newVectorEngine creates the underlying RAG store for a collection.
// Returns nil + logs on failure (engine misconfiguration or transient embedding/DB
// outage). Callers must check for nil — RAG init failures must not crash the
// embedding application; LocalRecall used to os.Exit on these errors but now
// returns them, and we surface that to callers as a nil collection.
func newVectorEngine(
	vectorEngineType string,
	llmClient *openai.Client,
	apiURL, apiKey, collectionName, dbPath, fileAssets, embeddingModel, databaseURL string,
	maxChunkSize, chunkOverlap int,
) *rag.PersistentKB {
	var (
		kb  *rag.PersistentKB
		err error
	)
	switch vectorEngineType {
	case "chromem":
		xlog.Info("Chromem collection", "collectionName", collectionName, "dbPath", dbPath)
		kb, err = rag.NewPersistentChromeCollection(llmClient, collectionName, dbPath, fileAssets, embeddingModel, maxChunkSize, chunkOverlap)
	case "localai":
		xlog.Info("LocalAI collection", "collectionName", collectionName, "apiURL", apiURL)
		kb, err = rag.NewPersistentLocalAICollection(llmClient, apiURL, apiKey, collectionName, dbPath, fileAssets, embeddingModel, maxChunkSize, chunkOverlap)
	case "postgres":
		if databaseURL == "" {
			xlog.Error("DATABASE_URL is required for PostgreSQL engine")
			return nil
		}
		xlog.Info("PostgreSQL collection", "collectionName", collectionName)
		kb, err = rag.NewPersistentPostgresCollection(llmClient, collectionName, dbPath, fileAssets, embeddingModel, maxChunkSize, chunkOverlap, databaseURL)
	default:
		xlog.Error("Unknown vector engine", "engine", vectorEngineType)
		return nil
	}
	if err != nil {
		if vectorEngineType == "postgres" {
			// Driver errors can include credentials from malformed connection strings.
			xlog.Error("Failed to create vector engine collection",
				"engine", vectorEngineType, "collection", collectionName)
			return nil
		}
		xlog.Error("Failed to create vector engine collection",
			"engine", vectorEngineType, "collection", collectionName, "error", err)
		return nil
	}
	return kb
}

// backendInProcess implements Backend using in-process state.
type backendInProcess struct {
	// locksMu guards collectionLocks. Each entry serializes the operations
	// of one collection; see lockCollection.
	locksMu         sync.Mutex
	collectionLocks map[string]*sync.Mutex
	// sourcesMu guards the source history maps, which operations on
	// different collections update concurrently.
	sourcesMu        sync.Mutex
	resetWithSources map[string]bool
	hadSources       map[string]bool
	state            *State
	cfg              *Config
	openAIClient     *openai.Client
	// loading makes one caller open an unloaded collection while the others
	// wait for its answer. The engine opens outside state.Mu, because it may
	// call the embeddings API or the database.
	loading singleflight.Group
}

var _ Backend = (*backendInProcess)(nil)

// lookup returns the cached collection KB for name. If the cache holds a
// placeholder (nil entry — the engine init failed at startup, e.g. because
// the embedding service was momentarily unreachable when iterating over
// existing collections in NewInProcessBackend) it attempts to re-initialise
// the engine now so a transient outage doesn't permanently 404 a collection
// that still has data on disk / in the vector DB. Returns (nil, false) only
// when the collection isn't known at all, or when re-init still fails.
func (b *backendInProcess) lookup(name string) (*rag.PersistentKB, bool) {
	b.state.Mu.RLock()
	kb, exists := b.state.Collections[name]
	b.state.Mu.RUnlock()
	if !exists {
		return nil, false
	}
	if kb != nil {
		return kb, true
	}
	// Placeholder: collection is known on disk but its engine wrapper failed
	// to construct earlier. Open it again.
	kb, err := b.load(name, b.cfg.EmbeddingModel)
	if err != nil {
		xlog.Warn("Could not open the collection", "collection", name, "error", err)
		return nil, false
	}
	return kb, true
}

// load opens the engine of a collection that has none in the cache and
// installs it, unless another caller installed one first. It does not hold
// state.Mu while the engine opens, and concurrent calls for one name share one
// attempt.
func (b *backendInProcess) load(name, desired string) (*rag.PersistentKB, error) {
	v, err, _ := b.loading.Do(name, func() (any, error) {
		kb, err := b.construct(name, desired)
		if err != nil {
			return nil, err
		}
		b.state.Mu.Lock()
		defer b.state.Mu.Unlock()
		if cur := b.state.Collections[name]; cur != nil {
			return cur, nil
		}
		b.state.Collections[name] = kb
		b.state.SourceManager.RegisterCollection(name, kb)
		return kb, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*rag.PersistentKB), nil
}

// Close stops the loop that refreshes the external sources. The collections
// stay readable. It is safe to call twice.
func (b *backendInProcess) Close() { b.state.SourceManager.Stop() }

func (b *backendInProcess) ListCollections() ([]string, error) {
	return rag.ListAllCollections(b.cfg.CollectionDBPath), nil
}

func (b *backendInProcess) CreateCollection(name string) error {
	settings, err := b.settings(name)
	if err != nil {
		return err
	}
	return b.createCollection(name, settings)
}

func (b *backendInProcess) createCollection(name string, settings CollectionModelSettings) error {
	defer b.lockCollection(name)()
	_, err := b.writable(name, settings, true)
	return err
}

func (b *backendInProcess) Upload(ctx context.Context, collection, filename string, fileBody io.Reader) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	settings, err := b.settings(collection)
	if err != nil {
		return "", err
	}
	return b.upload(ctx, collection, filename, fileBody, settings)
}

func (b *backendInProcess) upload(ctx context.Context, collection, filename string, fileBody io.Reader, settings CollectionModelSettings) (string, error) {
	defer b.lockCollection(collection)()
	kb, err := b.writable(collection, settings, false)
	if err != nil {
		return "", err
	}
	// Write to a temp file; kb.Store will copy it into the correct UUID
	// subdirectory under the collection's asset dir.
	tmpDir, err := os.MkdirTemp("", "localagi-upload")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)
	tmpPath := filepath.Join(tmpDir, filepath.Base(filename))
	out, err := os.Create(tmpPath) // #nosec G304 -- tmpPath is inside a fresh temp directory and the name is reduced to its base
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, fileBody); err != nil {
		out.Close()
		return "", err
	}
	out.Close()
	now := time.Now().Format(time.RFC3339)
	return kb.Store(tmpPath, map[string]string{"created_at": now})
}

func (b *backendInProcess) ListEntries(collection string) ([]string, error) {
	kb, exists := b.lookup(collection)
	if !exists {
		return nil, fmt.Errorf("collection not found: %s", collection)
	}
	return kb.ListDocuments(), nil
}

func (b *backendInProcess) GetEntryContent(collection, entry string) (string, int, error) {
	kb, exists := b.lookup(collection)
	if !exists {
		return "", 0, fmt.Errorf("collection not found: %s", collection)
	}
	return kb.GetEntryFileContent(entry)
}

func (b *backendInProcess) Search(ctx context.Context, collection, query string, maxResults int) ([]SearchResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	settings, err := b.settings(collection)
	if err != nil {
		return nil, err
	}
	return b.search(ctx, collection, query, maxResults, settings)
}

func (b *backendInProcess) search(ctx context.Context, collection, query string, maxResults int, settings CollectionModelSettings) ([]SearchResult, error) {
	out, maxResults, err := b.vectorSearch(collection, query, maxResults, settings)
	if err != nil {
		return nil, err
	}
	// The rerank call can take up to its HTTP timeout, and it only needs the
	// candidates, so it runs without the collection lock.
	return b.rerank(ctx, settings.RerankerModel, query, out, maxResults)
}

// rerankCandidateFactor widens the vector search when a reranker is set, so
// the reranker can promote a relevant chunk that ranks just below the cutoff.
const rerankCandidateFactor = 4

// vectorSearch returns the candidates and the result count after defaults.
func (b *backendInProcess) vectorSearch(collection, query string, maxResults int, settings CollectionModelSettings) ([]SearchResult, int, error) {
	defer b.lockCollection(collection)()
	kb, err := b.writable(collection, settings, false)
	if err != nil {
		return nil, 0, err
	}
	if maxResults <= 0 {
		keys := kb.ListDocuments()
		if len(keys) >= 5 {
			maxResults = 5
		} else {
			maxResults = 1
		}
	}
	// Engines such as chromem reject a request for more results than the
	// collection holds, so never ask for more than the chunk count. An empty
	// collection has nothing to find.
	chunks := kb.Count()
	if chunks == 0 {
		return []SearchResult{}, maxResults, nil
	}
	candidates := min(maxResults, chunks)
	if settings.RerankerModel != "" {
		candidates = max(candidates, min(maxResults*rerankCandidateFactor, chunks))
	}
	results, err := kb.Search(query, candidates)
	if err != nil {
		return nil, 0, err
	}
	out := make([]SearchResult, 0, len(results))
	for _, r := range results {
		out = append(out, SearchResult{
			ID:         r.ID,
			Content:    r.Content,
			Metadata:   r.Metadata,
			Similarity: r.Similarity,
		})
	}
	return out, maxResults, nil
}

func (b *backendInProcess) Reset(collection string) error {
	defer b.lockCollection(collection)()
	kb, exists := b.lookup(collection)
	if !exists {
		return fmt.Errorf("collection not found: %s", collection)
	}
	sources := kb.GetExternalSources()
	b.rememberSources(collection, kb)
	if hadSources, _ := b.sourceHistory(collection); hadSources {
		// LocalRecall cannot cancel an in-flight source fetch. Keep the old
		// identity and block recreation until a restart drains those writers.
		b.markResetWithSources(collection)
		for _, source := range sources {
			if err := b.state.SourceManager.RemoveSource(collection, source.URL); err != nil {
				return err
			}
		}
	}
	if err := kb.Reset(); err != nil {
		return err
	}
	if _, resetWithSources := b.sourceHistory(collection); !resetWithSources {
		if err := os.Remove(b.identityPath(collection)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	b.state.Mu.Lock()
	delete(b.state.Collections, collection)
	b.state.Mu.Unlock()
	return nil
}

func (b *backendInProcess) DeleteEntry(collection, entry string) ([]string, error) {
	kb, exists := b.lookup(collection)
	if !exists {
		return nil, fmt.Errorf("collection not found: %s", collection)
	}
	if err := kb.RemoveEntry(entry); err != nil {
		return nil, err
	}
	keys := kb.ListDocuments()
	return keys, nil
}

func (b *backendInProcess) AddSource(collection, url string, intervalMin int) error {
	settings, err := b.settings(collection)
	if err != nil {
		return err
	}
	defer b.lockCollection(collection)()
	kb, err := b.writable(collection, settings, false)
	if err != nil {
		return err
	}
	b.state.SourceManager.RegisterCollection(collection, kb)
	// Even a partially failed add can leave persisted source metadata.
	b.markHadSources(collection)
	return b.state.SourceManager.AddSource(collection, url, time.Duration(intervalMin)*time.Minute)
}

func (b *backendInProcess) RemoveSource(collection, url string) error {
	defer b.lockCollection(collection)()
	if kb, exists := b.lookup(collection); exists {
		b.rememberSources(collection, kb)
	}
	return b.state.SourceManager.RemoveSource(collection, url)
}

func (b *backendInProcess) ListSources(collection string) ([]SourceInfo, error) {
	kb, exists := b.lookup(collection)
	if !exists {
		return nil, fmt.Errorf("collection not found: %s", collection)
	}
	srcs := kb.GetExternalSources()
	out := make([]SourceInfo, 0, len(srcs))
	for _, s := range srcs {
		out = append(out, SourceInfo{
			URL:            s.URL,
			UpdateInterval: int(s.UpdateInterval.Minutes()),
			LastUpdate:     s.LastUpdate,
		})
	}
	return out, nil
}

func (b *backendInProcess) GetEntryFilePath(collection, entry string) (string, error) {
	kb, exists := b.lookup(collection)
	if !exists {
		return "", fmt.Errorf("collection not found: %s", collection)
	}
	return kb.GetEntryFilePath(entry)
}

func (b *backendInProcess) EntryExists(collection, entry string) bool {
	kb, exists := b.lookup(collection)
	if !exists {
		return false
	}
	return kb.EntryExists(entry)
}

// NewInProcessBackend creates in-process state (load from disk, start sourceManager) and returns
// a Backend and the State.
func NewInProcessBackend(cfg *Config) (Backend, *State) {
	st := &State{
		Collections:   CollectionList{},
		SourceManager: rag.NewSourceManager(&sources.Config{}),
	}

	openaiConfig := openai.DefaultConfig(cfg.LLMAPIKey)
	openaiConfig.BaseURL = cfg.LLMAPIURL
	if cfg.HTTPClient != nil {
		openaiConfig.HTTPClient = cfg.HTTPClient
	}
	openAIClient := openai.NewClientWithConfig(openaiConfig)
	backend := &backendInProcess{state: st, cfg: cfg, openAIClient: openAIClient}
	st.backend = backend

	os.MkdirAll(cfg.CollectionDBPath, 0755) // #nosec G301 -- the collection directories are shared with the services that read them
	os.MkdirAll(cfg.FileAssets, 0755)       // #nosec G301 -- the collection directories are shared with the services that read them

	colls := rag.ListAllCollections(cfg.CollectionDBPath)
	for _, c := range colls {
		collection, _ := backend.construct(c, cfg.EmbeddingModel)
		// Register every on-disk collection — even when the engine wrapper
		// failed to construct (e.g. the embedding service was momentarily
		// unreachable). A nil entry marks "known on disk but not yet loaded";
		// backendInProcess.lookup will rehydrate lazily on first access so a
		// transient outage at boot doesn't permanently 404 collections whose
		// data is still on disk / in the vector DB.
		st.Collections[c] = collection
		if collection != nil {
			backend.rememberSources(c, collection)
			st.SourceManager.RegisterCollection(c, collection)
		}
	}

	st.EnsureCollection = func(name string) (*rag.PersistentKB, bool) {
		settings, err := backend.settings(name)
		if err != nil {
			return nil, false
		}
		defer backend.lockCollection(name)()
		collection, err := backend.writable(name, settings, true)
		return collection, err == nil
	}

	st.SourceManager.Start()

	return backend, st
}
