package backend

import (
	"context"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/config"

	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/store"
)

// VectorStore is the narrowed KNN store used by the router's embedding
// cache. Search returns the top-1 match (cosine similarity in [-1, 1])
// and the serialised payload, or ok=false on a clean miss.
type VectorStore interface {
	Search(ctx context.Context, vec []float32) (similarity float64, payload []byte, ok bool, err error)
	Insert(ctx context.Context, vec []float32, payload []byte) error
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

func (s *localVectorStore) Search(ctx context.Context, vec []float32) (float64, []byte, bool, error) {
	be, err := s.backend(ctx)
	if err != nil {
		return 0, nil, false, fmt.Errorf("vector store load: %w", err)
	}
	_, values, similarities, err := store.Find(ctx, be, vec, 1)
	if err != nil {
		// local-store's Find returns "existing length is -1" before
		// any keys are inserted. Surface that as a clean miss so the
		// cache layer treats it as an empty store and proceeds to
		// Insert rather than skipping.
		if strings.Contains(err.Error(), "existing length is -1") {
			return 0, nil, false, nil
		}
		return 0, nil, false, fmt.Errorf("vector store find: %w", err)
	}
	if len(values) == 0 || len(similarities) == 0 {
		return 0, nil, false, nil
	}
	return float64(similarities[0]), values[0], true, nil
}

func (s *localVectorStore) Insert(ctx context.Context, vec []float32, payload []byte) error {
	be, err := s.backend(ctx)
	if err != nil {
		return fmt.Errorf("vector store load: %w", err)
	}
	return store.SetSingle(ctx, be, vec, payload)
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
	sc := []model.Option{
		model.WithBackendString(backend),
		model.WithModelID(storeName),
		model.WithModel(storeName),
	}

	return sl.Load(sc...)
}
