// Package collections serves the knowledge-base collections of the server: one
// set for each user, kept on disk or in PostgreSQL by the LocalAGI collections
// backend. The chats search them, and /api/agents/collections manages them.
package collections

import (
	"cmp"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	lagi "github.com/mudler/LocalAGI/webui/collections"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/messaging"
)

// Service is the collections of a server.
type Service struct {
	appConfig *config.ApplicationConfig
	storage   userStorage
	stateDir  string
	apiURL    string
	apiKey    string
	bus       messaging.Broadcaster

	root lagi.Backend

	mu    sync.RWMutex
	cache map[string]lagi.Backend
}

// New builds the service. bus, when not nil, is the cluster's message bus: the
// root backend then shares its list of collections between frontends (with the
// PostgreSQL vector engine). The embedding calls go to the server's own API, so
// the caller starts the service after the API is listening.
func New(appConfig *config.ApplicationConfig, bus messaging.Broadcaster) (*Service, error) {
	cfg := appConfig.AgentPool
	stateDir := cmp.Or(cfg.StateDir, appConfig.DataPath, appConfig.DynamicConfigsDir, "agents")
	if err := os.MkdirAll(stateDir, 0o750); err != nil {
		return nil, fmt.Errorf("failed to create the collections state dir: %w", err)
	}
	apiURL := cfg.APIURL
	if apiURL == "" {
		_, port, err := net.SplitHostPort(appConfig.APIAddress)
		if err != nil {
			port = strings.TrimPrefix(appConfig.APIAddress, ":")
		}
		apiURL = "http://127.0.0.1:" + port
	}
	apiKey := cfg.APIKey
	if apiKey == "" && len(appConfig.ApiKeys) > 0 {
		apiKey = appConfig.ApiKeys[0]
	}
	s := &Service{appConfig: appConfig, storage: userStorage{baseDir: stateDir}, stateDir: stateDir, apiURL: apiURL, apiKey: apiKey, bus: bus,
		cache: map[string]lagi.Backend{}}
	dbPath := cfg.CollectionDBPath
	if dbPath == "" {
		dbPath = filepath.Join(stateDir, "collections")
	}
	rootCfg := s.config(dbPath, filepath.Join(stateDir, "assets"))
	backend, state := lagi.NewInProcessBackend(rootCfg)
	if bus != nil {
		backend = s.share(backend, state, rootCfg)
	}
	s.root = backend
	return s, nil
}

func (s *Service) config(collectionDBPath, fileAssets string) *lagi.Config {
	cfg := s.appConfig.AgentPool
	return &lagi.Config{
		LLMAPIURL: s.apiURL, LLMAPIKey: s.apiKey, LLMModel: cfg.DefaultModel,
		CollectionDBPath: collectionDBPath, FileAssets: fileAssets,
		VectorEngine: cfg.VectorEngine, EmbeddingModel: cfg.EmbeddingModel,
		MaxChunkingSize: cfg.MaxChunkingSize, ChunkOverlap: cfg.ChunkOverlap, DatabaseURL: cfg.DatabaseURL,
	}
}

// share makes the frontends agree on the list of collections. With the
// PostgreSQL vector engine the database is the shared registry; with any other
// engine there is none, and the backend is returned unchanged.
func (s *Service) share(backend lagi.Backend, state *lagi.State, cfg *lagi.Config) lagi.Backend {
	if cfg.VectorEngine != "postgres" || cfg.DatabaseURL == "" {
		xlog.Warn("Distributed mode without the postgres vector engine: each frontend keeps its own list of collections", "vectorEngine", cfg.VectorEngine)
		return backend
	}
	return newSharedCollections(backend, state, postgresCollectionRegistry{databaseURL: cfg.DatabaseURL}, s.bus)
}

// StateDir returns the directory the collections of the server live under.
func (s *Service) StateDir() string { return s.stateDir }

// Backend returns the collections backend of a user ("" is the root: a server
// with no users). It makes the user's directories on first use.
func (s *Service) Backend(userID string) (lagi.Backend, error) {
	if userID == "" {
		return s.root, nil
	}
	if err := ValidateUserID(userID); err != nil {
		return nil, err
	}
	s.mu.RLock()
	b, ok := s.cache[userID]
	s.mu.RUnlock()
	if ok {
		return b, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.cache[userID]; ok {
		return b, nil
	}
	if err := s.storage.ensure(userID); err != nil {
		return nil, err
	}
	b, _ = lagi.NewInProcessBackend(s.config(s.storage.CollectionsDir(userID), s.storage.AssetsDir(userID)))
	s.cache[userID] = b
	return b, nil
}

// UserIDs lists the users that have collections on disk, sorted.
func (s *Service) UserIDs() ([]string, error) { return s.storage.userDirs() }

// ListForUser lists the names of a user's collections.
func (s *Service) ListForUser(userID string) ([]string, error) {
	b, err := s.Backend(userID)
	if err != nil {
		return nil, err
	}
	return b.ListCollections()
}

// CreateForUser creates a collection.
func (s *Service) CreateForUser(userID, name string) error {
	b, err := s.Backend(userID)
	if err != nil {
		return err
	}
	return b.CreateCollection(name)
}

// UploadForUser adds a file to a collection and returns its key.
func (s *Service) UploadForUser(userID, collection, filename string, body io.Reader) (string, error) {
	b, err := s.Backend(userID)
	if err != nil {
		return "", err
	}
	return b.Upload(collection, filename, body)
}

// EntryExistsForUser tells whether a collection has an entry.
func (s *Service) EntryExistsForUser(userID, collection, entry string) bool {
	b, err := s.Backend(userID)
	return err == nil && b.EntryExists(collection, entry)
}

// ListEntriesForUser lists the entries of a collection.
func (s *Service) ListEntriesForUser(userID, collection string) ([]string, error) {
	b, err := s.Backend(userID)
	if err != nil {
		return nil, err
	}
	return b.ListEntries(collection)
}

// EntryContentForUser returns the text of an entry and its number of chunks.
func (s *Service) EntryContentForUser(userID, collection, entry string) (string, int, error) {
	b, err := s.Backend(userID)
	if err != nil {
		return "", 0, err
	}
	return b.GetEntryContent(collection, entry)
}

// SearchForUser searches a collection.
func (s *Service) SearchForUser(userID, collection, query string, maxResults int) ([]lagi.SearchResult, error) {
	b, err := s.Backend(userID)
	if err != nil {
		return nil, err
	}
	return b.Search(collection, query, maxResults)
}

// ResetForUser empties a collection.
func (s *Service) ResetForUser(userID, collection string) error {
	b, err := s.Backend(userID)
	if err != nil {
		return err
	}
	return b.Reset(collection)
}

// DeleteEntryForUser deletes an entry and returns the entries left.
func (s *Service) DeleteEntryForUser(userID, collection, entry string) ([]string, error) {
	b, err := s.Backend(userID)
	if err != nil {
		return nil, err
	}
	return b.DeleteEntry(collection, entry)
}

// AddSourceForUser makes a collection follow a URL.
func (s *Service) AddSourceForUser(userID, collection, url string, intervalMin int) error {
	b, err := s.Backend(userID)
	if err != nil {
		return err
	}
	return b.AddSource(collection, url, intervalMin)
}

// RemoveSourceForUser stops a collection following a URL.
func (s *Service) RemoveSourceForUser(userID, collection, url string) error {
	b, err := s.Backend(userID)
	if err != nil {
		return err
	}
	return b.RemoveSource(collection, url)
}

// ListSourcesForUser lists the URLs a collection follows.
func (s *Service) ListSourcesForUser(userID, collection string) ([]lagi.SourceInfo, error) {
	b, err := s.Backend(userID)
	if err != nil {
		return nil, err
	}
	return b.ListSources(collection)
}

// EntryFilePathForUser returns the file of an entry on disk.
func (s *Service) EntryFilePathForUser(userID, collection, entry string) (string, error) {
	b, err := s.Backend(userID)
	if err != nil {
		return "", err
	}
	return b.GetEntryFilePath(collection, entry)
}
