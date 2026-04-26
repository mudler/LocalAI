// Package facerecognition provides a swappable backing store for face
// embeddings and the 1:N identification pipeline that sits on top of it.
//
// The current implementation (NewStoreRegistry) is backed by LocalAI's
// in-memory local-store gRPC backend. This is in-memory only — all
// registrations are lost when LocalAI restarts.
//
// TODO: add a persistent PostgreSQL/pgvector-backed implementation for
// production deployments. The Registry interface is explicitly designed
// so the swap is a constructor change in core/application, with zero
// HTTP-handler changes.
package facerecognition

import (
	"context"
	"errors"
	"time"
)

// Registry stores face embeddings keyed by an opaque ID and supports
// approximate similarity search. Implementations are expected to be
// safe for concurrent use.
type Registry interface {
	// Register stores a face embedding alongside its metadata.
	// Returns the stored metadata with ID and RegisteredAt populated.
	// The embedding length must match the registry's expected dimension.
	Register(ctx context.Context, embedding []float32, meta Metadata) (Metadata, error)

	// Identify returns up to topK matches for the probe embedding,
	// sorted by ascending distance (closest first).
	Identify(ctx context.Context, probe []float32, topK int) ([]Match, error)

	// Forget removes a previously-registered embedding by ID.
	// Returns ErrNotFound if the ID is unknown.
	Forget(ctx context.Context, id string) error
}

// Metadata is the user-supplied payload stored alongside a face embedding.
type Metadata struct {
	// ID is populated by the registry at Register time and should not be
	// set by the caller. It is echoed back in Match.Metadata.
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Labels       map[string]string `json:"labels,omitempty"`
	RegisteredAt time.Time         `json:"registered_at"`
}

// Match is a single result from Identify, ranked by similarity.
type Match struct {
	ID       string
	Metadata Metadata
	Distance float32 // 1 - cosine_similarity; lower = closer
}

// Sentinel errors; callers should compare with errors.Is.
var (
	ErrNotFound          = errors.New("facerecognition: id not found")
	ErrEmptyEmbedding    = errors.New("facerecognition: embedding is empty")
	ErrDimensionMismatch = errors.New("facerecognition: embedding dimension mismatch")
)
