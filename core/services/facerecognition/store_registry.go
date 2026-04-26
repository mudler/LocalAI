package facerecognition

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/store"
)

// StoreResolver resolves a named vector store to a gRPC backend. The
// HTTP handler layer wires this to backend.StoreBackend so the
// registry stays decoupled from the ModelLoader plumbing.
type StoreResolver func(ctx context.Context, storeName string) (grpc.Backend, error)

// NewStoreRegistry returns a Registry backed by LocalAI's generic
// StoresSet / StoresFind / StoresDelete gRPC surface.
//
// storeName selects which vector-store model to use (defaults to the
// local-store Go backend). `dim` is the expected embedding dimension;
// pass 0 to accept whatever dimension arrives (useful when the face
// backend exposes multiple recognizers of different sizes, e.g.
// ArcFace R50 at 512 vs SFace at 128). A non-zero dim is enforced at
// Register time and fails fast with ErrDimensionMismatch.
func NewStoreRegistry(resolve StoreResolver, storeName string, dim int) Registry {
	return &storeRegistry{
		resolve:   resolve,
		storeName: storeName,
		dim:       dim,
	}
}

type storeRegistry struct {
	resolve   StoreResolver
	storeName string
	dim       int

	// TODO(postgres): the local-store gRPC surface keys by embedding
	// vector and exposes no "list all" method, so we cannot delete by
	// ID without remembering the embedding. This in-memory index is
	// rebuilt on every Register and lost on restart — acceptable while
	// the only implementation is itself in-memory. A persistent
	// implementation must rebuild this index at startup.
	idIndex sync.Map // map[string][]float32
}

func (r *storeRegistry) Register(ctx context.Context, embedding []float32, meta Metadata) (Metadata, error) {
	if len(embedding) == 0 {
		return Metadata{}, ErrEmptyEmbedding
	}
	if r.dim != 0 && len(embedding) != r.dim {
		return Metadata{}, fmt.Errorf("%w: expected %d, got %d", ErrDimensionMismatch, r.dim, len(embedding))
	}

	backend, err := r.resolve(ctx, r.storeName)
	if err != nil {
		return Metadata{}, fmt.Errorf("facerecognition: resolve store: %w", err)
	}

	meta.ID = uuid.NewString()
	if meta.RegisteredAt.IsZero() {
		meta.RegisteredAt = time.Now().UTC()
	}

	payload, err := json.Marshal(meta)
	if err != nil {
		return Metadata{}, fmt.Errorf("facerecognition: marshal metadata: %w", err)
	}

	if err := store.SetSingle(ctx, backend, embedding, payload); err != nil {
		return Metadata{}, fmt.Errorf("facerecognition: set: %w", err)
	}

	// Retain a copy so Forget can look up the embedding by ID.
	embCopy := append([]float32(nil), embedding...)
	r.idIndex.Store(meta.ID, embCopy)
	return meta, nil
}

func (r *storeRegistry) Identify(ctx context.Context, probe []float32, topK int) ([]Match, error) {
	if len(probe) == 0 {
		return nil, ErrEmptyEmbedding
	}
	if r.dim != 0 && len(probe) != r.dim {
		return nil, fmt.Errorf("%w: expected %d, got %d", ErrDimensionMismatch, r.dim, len(probe))
	}
	if topK <= 0 {
		topK = 5
	}

	backend, err := r.resolve(ctx, r.storeName)
	if err != nil {
		return nil, fmt.Errorf("facerecognition: resolve store: %w", err)
	}

	_, values, similarities, err := store.Find(ctx, backend, probe, topK)
	if err != nil {
		return nil, fmt.Errorf("facerecognition: find: %w", err)
	}

	matches := make([]Match, 0, len(values))
	for i, raw := range values {
		var meta Metadata
		if err := json.Unmarshal(raw, &meta); err != nil {
			// Skip unreadable entries instead of failing the whole query —
			// the store may contain non-face records in shared deployments.
			continue
		}
		matches = append(matches, Match{
			ID:       meta.ID,
			Metadata: meta,
			Distance: 1 - similarities[i],
		})
	}

	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Distance < matches[j].Distance })
	return matches, nil
}

func (r *storeRegistry) Forget(ctx context.Context, id string) error {
	raw, ok := r.idIndex.Load(id)
	if !ok {
		return ErrNotFound
	}
	embedding := raw.([]float32)

	backend, err := r.resolve(ctx, r.storeName)
	if err != nil {
		return fmt.Errorf("facerecognition: resolve store: %w", err)
	}
	if err := store.DeleteSingle(ctx, backend, embedding); err != nil {
		return fmt.Errorf("facerecognition: delete: %w", err)
	}
	r.idIndex.Delete(id)
	return nil
}
