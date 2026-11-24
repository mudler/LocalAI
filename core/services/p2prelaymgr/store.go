package p2prelaymgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/mudler/LocalAI/pkg/atomicfile"
)

// Entry is what the manager persists about a relay. A claim code is never
// persisted.
type Entry struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	Addr  string `json:"addr"`
}

// Store persists the relay list. The server backs it with the p2p_relays
// setting; MemoryStore and FileStore are the others.
type Store interface {
	Load(ctx context.Context) ([]Entry, error)
	Save(ctx context.Context, entries []Entry) error
}

// MemoryStore keeps the list in memory.
type MemoryStore struct {
	mu      sync.Mutex
	entries []Entry
}

// NewMemoryStore returns a MemoryStore with the given entries.
func NewMemoryStore(entries ...Entry) *MemoryStore {
	return &MemoryStore{entries: slices.Clone(entries)}
}

// Load implements Store.
func (s *MemoryStore) Load(context.Context) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.entries), nil
}

// Save implements Store.
func (s *MemoryStore) Save(_ context.Context, entries []Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = slices.Clone(entries)
	return nil
}

// FileStore keeps the list in a JSON file, written with mode 0600.
type FileStore struct {
	path string
	mu   sync.Mutex
}

// NewFileStore returns a store backed by the file at path. A missing file is
// an empty list.
func NewFileStore(path string) *FileStore { return &FileStore{path: path} }

// Load implements Store.
func (s *FileStore) Load(context.Context) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("p2prelaymgr: read relays: %w", err)
	}
	var entries []Entry
	if err := json.Unmarshal(b, &entries); err != nil {
		return nil, fmt.Errorf("p2prelaymgr: parse %s: %w", s.path, err)
	}
	return entries, nil
}

// Save implements Store. It writes a temporary file and renames it, so a
// crash leaves the old list.
func (s *FileStore) Save(_ context.Context, entries []Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("p2prelaymgr: relays folder: %w", err)
	}
	if err := atomicfile.Write(s.path, b, 0o600); err != nil {
		return fmt.Errorf("p2prelaymgr: write relays: %w", err)
	}
	return nil
}
