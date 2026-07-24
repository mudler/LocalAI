package backend

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/trace"

	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/store"
)

// VectorStore is the narrowed KNN store used by the router's embedding
// cache and the KNN classifier. Search returns the top-1 match (cosine
// similarity in [-1, 1]) and the serialised payload, or ok=false on a
// clean miss. SearchK returns up to k nearest neighbours ordered by
// descending similarity; an empty slice is a clean miss.
type VectorStore interface {
	Search(ctx context.Context, vec []float32) (similarity float64, payload []byte, ok bool, err error)
	SearchK(ctx context.Context, vec []float32, k int) ([]Neighbor, error)
	Insert(ctx context.Context, vec []float32, payload []byte) error
}

// Neighbor is one SearchK result — the stored payload and its cosine
// similarity to the query vector.
type Neighbor struct {
	Similarity float64
	Payload    []byte
}

// NewVectorStore returns a VectorStore backed by the local-store
// gRPC backend, namespaced by storeName so two routers don't collide.
func NewVectorStore(loader *model.ModelLoader, appConfig *config.ApplicationConfig, storeName string) VectorStore {
	if storeName == "" {
		return nil
	}
	return &localVectorStore{loader: loader, appConfig: appConfig, storeName: storeName}
}

type localVectorStore struct {
	loader    *model.ModelLoader
	appConfig *config.ApplicationConfig
	storeName string
}

func (s *localVectorStore) backend(_ context.Context) (grpc.Backend, error) {
	return StoreBackend(s.loader, s.appConfig, s.storeName, "")
}

// Search is the top-1 special case of SearchK; delegating keeps the
// backend-load/Find/trace plumbing in one place (SearchK records the
// identically-shaped trace, so /api/backend-traces sees no difference).
func (s *localVectorStore) Search(ctx context.Context, vec []float32) (float64, []byte, bool, error) {
	neighbors, err := s.SearchK(ctx, vec, 1)
	if err != nil || len(neighbors) == 0 {
		return 0, nil, false, err
	}
	return neighbors[0].Similarity, neighbors[0].Payload, true, nil
}

func (s *localVectorStore) SearchK(ctx context.Context, vec []float32, k int) (neighbors []Neighbor, err error) {
	start := time.Now()
	outcome := "hit"
	sim := 0.0
	defer func() {
		s.recordTrace(start, "search", len(vec), sim, outcome, err)
	}()
	be, berr := s.backend(ctx)
	if berr != nil {
		outcome = "backend_load_error"
		return nil, fmt.Errorf("vector store load: %w", berr)
	}
	_, values, similarities, ferr := store.Find(ctx, be, vec, k)
	if ferr != nil {
		outcome = "find_error"
		return nil, fmt.Errorf("vector store find: %w", ferr)
	}
	if len(values) == 0 {
		outcome = "miss"
		return nil, nil
	}
	neighbors = make([]Neighbor, 0, len(values))
	for i, v := range values {
		neighbors = append(neighbors, Neighbor{Similarity: float64(similarities[i]), Payload: v})
	}
	sim = neighbors[0].Similarity
	return neighbors, nil
}

func (s *localVectorStore) Insert(ctx context.Context, vec []float32, payload []byte) (err error) {
	start := time.Now()
	outcome := "ok"
	defer func() {
		s.recordTrace(start, "insert", len(vec), 0, outcome, err)
	}()
	be, berr := s.backend(ctx)
	if berr != nil {
		outcome = "backend_load_error"
		return fmt.Errorf("vector store load: %w", berr)
	}
	if serr := store.SetSingle(ctx, be, vec, payload); serr != nil {
		outcome = "insert_error"
		return serr
	}
	return nil
}

// InsertBatch upserts many vectors in one gRPC round-trip. Not part of
// the VectorStore interface — the corpus manager type-asserts for it
// and falls back to per-entry Insert on stores that lack it.
func (s *localVectorStore) InsertBatch(ctx context.Context, vecs [][]float32, payloads [][]byte) (err error) {
	start := time.Now()
	outcome := "ok"
	dim := 0
	if len(vecs) > 0 {
		dim = len(vecs[0])
	}
	defer func() {
		s.recordTrace(start, "insert_batch", dim, 0, outcome, err)
	}()
	be, berr := s.backend(ctx)
	if berr != nil {
		outcome = "backend_load_error"
		return fmt.Errorf("vector store load: %w", berr)
	}
	if serr := store.SetCols(ctx, be, vecs, payloads); serr != nil {
		outcome = "insert_error"
		return serr
	}
	return nil
}

// Delete removes vectors by key. Optional capability like InsertBatch;
// used by the corpus manager's Clear so a wiped corpus also leaves the
// live index.
func (s *localVectorStore) Delete(ctx context.Context, vecs [][]float32) (err error) {
	start := time.Now()
	outcome := "ok"
	dim := 0
	if len(vecs) > 0 {
		dim = len(vecs[0])
	}
	defer func() {
		s.recordTrace(start, "delete", dim, 0, outcome, err)
	}()
	be, berr := s.backend(ctx)
	if berr != nil {
		outcome = "backend_load_error"
		return fmt.Errorf("vector store load: %w", berr)
	}
	if serr := store.DeleteCols(ctx, be, vecs); serr != nil {
		outcome = "delete_error"
		return serr
	}
	return nil
}

// recordTrace surfaces vector-store calls in /api/backend-traces, including
// the backend-load-failure path that otherwise vanishes into an xlog.Warn.
// modelName uses the store namespace (e.g. "router-cache-smart-router") so
// admins can tell which router's cache misbehaved; the backend is always
// "local-store" and can't disambiguate.
func (s *localVectorStore) recordTrace(start time.Time, op string, vecDim int, sim float64, outcome string, err error) {
	if s.appConfig == nil || !s.appConfig.EnableTracing {
		return
	}
	trace.InitBackendTracingIfEnabled(s.appConfig.TracingMaxItems, s.appConfig.TracingMaxBodyBytes)
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	summary := op + " " + outcome
	if op == "search" && outcome == "hit" {
		summary = fmt.Sprintf("search hit (sim=%.3f)", sim)
	}
	data := map[string]any{
		"op":         op,
		"outcome":    outcome,
		"vector_dim": vecDim,
	}
	// Only include similarity for a real neighbor — miss/empty_store would
	// otherwise render "similarity: 0" and read as a measured value.
	if op == "search" && outcome == "hit" {
		data["similarity"] = sim
	}
	trace.RecordBackendTrace(trace.BackendTrace{
		Timestamp: start,
		Duration:  time.Since(start),
		Type:      trace.BackendTraceVectorStore,
		ModelName: s.storeName,
		Backend:   model.LocalStoreBackend,
		Summary:   summary,
		Error:     errStr,
		Data:      data,
	})
}

func StoreBackend(sl *model.ModelLoader, appConfig *config.ApplicationConfig, storeName string, backend string) (grpc.Backend, error) {
	if backend == "" {
		backend = model.LocalStoreBackend
	}
	// ModelLoader caches backend processes by `modelID`, not by the `model`
	// passed via WithModel. Without a distinct modelID, every StoreBackend
	// call collapses to the same `modelID=""` cache slot — face (512-D) and
	// voice (192-D) biometrics would then share the same local-store process
	// and the second enrollment would fail with
	//   Try to add key with length N when existing length is M
	// Use the store namespace as modelID so each namespace gets its own
	// process instance and its own in-memory Store{}.
	//
	// The model name sent over gRPC carries store.NamespacePrefix so the
	// backend can tell a genuine store load from the greedy autoload
	// probing it with LLM model names; local-store refuses names without
	// the prefix (core and backend ship from the same release, so the
	// convention upgrades in lockstep).
	sc := []model.Option{
		model.WithBackendString(backend),
		model.WithModelID(storeName),
		model.WithModel(store.NamespacePrefix + storeName),
	}

	return sl.Load(sc...)
}
