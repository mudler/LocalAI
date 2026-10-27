package backend

import (
	"io"
	"net/http"
	"time"
)

// SearchResult is a single search result (content + metadata) for API responses.
type SearchResult struct {
	Content    string            `json:"content"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	ID         string            `json:"id,omitempty"`
	Similarity float32           `json:"similarity,omitempty"`
}

// SourceInfo is a single external source for a collection.
type SourceInfo struct {
	URL            string    `json:"url"`
	UpdateInterval int       `json:"update_interval"` // minutes
	LastUpdate     time.Time `json:"last_update"`
}

// Backend is the interface used by REST handlers for collection operations.
// It is implemented by in-process state (embedded) or by an HTTP client.
type Backend interface {
	ListCollections() ([]string, error)
	CreateCollection(name string) error
	Upload(collection, filename string, fileBody io.Reader) (string, error)
	ListEntries(collection string) ([]string, error)
	GetEntryContent(collection, entry string) (content string, chunkCount int, err error)
	Search(collection, query string, maxResults int) ([]SearchResult, error)
	Reset(collection string) error
	DeleteEntry(collection, entry string) (remainingEntries []string, err error)
	AddSource(collection, url string, intervalMin int) error
	RemoveSource(collection, url string) error
	ListSources(collection string) ([]SourceInfo, error)
	EntryExists(collection, entry string) bool
	// GetEntryFilePath returns the filesystem path of the stored file for the
	// given entry. This is used to serve the original uploaded binary file.
	GetEntryFilePath(collection, entry string) (string, error)
}

// CollectionModelSettings overrides models for one collection. Empty values use
// the default embedding model and disable reranking, respectively.
type CollectionModelSettings struct {
	EmbeddingModel string
	RerankerModel  string
}

// Config holds the configuration for the in-process collections backend.
type Config struct {
	// ModelSettings resolves models before an operation acquires backend locks.
	// Errors deny embedding and reranking operations; inspection and reset remain available.
	ModelSettings func(collectionName string) (CollectionModelSettings, error)
	LLMAPIURL     string
	LLMAPIKey     string
	// HTTPClient, when set, carries the embedding and rerank calls. The host
	// uses it to serve them inside its own process.
	HTTPClient       *http.Client
	LLMModel         string
	CollectionDBPath string
	FileAssets       string
	VectorEngine     string
	EmbeddingModel   string
	MaxChunkingSize  int
	ChunkOverlap     int
	DatabaseURL      string
}

// RAGDB is the minimal retrieval interface exposed by the in-process and
// backend-served collection adapters.
type RAGDB interface {
	Store(s string) error
	Reset() error
	Search(s string, similarEntries int) ([]string, error)
	Count() int
}

// KBCompactionClient is the interface the compaction logic needs from a
// collection: list, read, add and delete entries.
type KBCompactionClient interface {
	Collection() string
	ListEntries() ([]string, error)
	GetEntryContent(entry string) (content string, chunkCount int, err error)
	Store(filePath string) error
	DeleteEntry(entry string) error
}
