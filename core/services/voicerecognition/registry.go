// Package voicerecognition provides a swappable backing store for
// speaker embeddings and the 1:N identification pipeline on top of it.
//
// Mirrors the facerecognition package — the audio analog. The current
// implementation (NewStoreRegistry) is backed by LocalAI's in-memory
// local-store gRPC backend, so all registrations are lost on restart.
//
// TODO: share a persistent pgvector-backed implementation with
// facerecognition once the first one lands. The Registry interface
// here is intentionally identical in shape, so a shared generic
// biometric registry can replace both without HTTP-handler churn.
package voicerecognition

import (
	"context"
	"errors"
	"time"
)

// Registry stores speaker embeddings keyed by an opaque ID and
// supports approximate similarity search. Implementations are expected
// to be safe for concurrent use.
type Registry interface {
	// Register stores a speaker embedding alongside its metadata.
	// Returns the stored metadata with ID and RegisteredAt populated.
	Register(ctx context.Context, embedding []float32, meta Metadata) (Metadata, error)

	// Identify returns up to topK matches for the probe embedding,
	// sorted by ascending distance (closest first).
	Identify(ctx context.Context, probe []float32, topK int) ([]Match, error)

	// Forget removes a previously-registered embedding by ID.
	// Returns ErrNotFound if the ID is unknown.
	Forget(ctx context.Context, id string) error

	// List returns every registered voice with its embedding, oldest first
	// (ties broken by ID). The embeddings are copies. The store registry
	// answers from its in-process index, so it lists what this process
	// registered since it started, which is also everything the in-memory
	// store holds.
	List(ctx context.Context) ([]Entry, error)
}

// Entry is a registered voice together with its embedding, as returned by List.
type Entry struct {
	Metadata  Metadata
	Embedding []float32
}

// Metadata is the user-supplied payload stored alongside a speaker embedding.
type Metadata struct {
	// ID is populated by the registry at Register time; callers must not set it.
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Labels       map[string]string `json:"labels,omitempty"`
	RegisteredAt time.Time         `json:"registered_at"`
	// Model names the speaker encoder that produced the embedding (the voice
	// backend's model name, by default the GGUF file name). Empty for voices
	// registered before this field existed.
	Model string `json:"model,omitempty"`
	// EncoderFamily is the embedding space of the encoder ("voicedetect:<arch>:<name>:<dim>"),
	// recorded when the encoder reports it (portable enrollment from speaker
	// profiles). Empty when unknown, and for voices registered before it existed.
	// Model then holds the weights identity ("sha256:<hex>") for the same voices.
	EncoderFamily string `json:"encoder_family,omitempty"`
}

// Match is a single result from Identify, ranked by similarity.
type Match struct {
	ID       string
	Metadata Metadata
	Distance float32 // 1 - cosine_similarity; lower = closer
}

// Sentinel errors; callers should compare with errors.Is.
var (
	ErrNotFound          = errors.New("voicerecognition: id not found")
	ErrEmptyEmbedding    = errors.New("voicerecognition: embedding is empty")
	ErrDimensionMismatch = errors.New("voicerecognition: embedding dimension mismatch")
)
