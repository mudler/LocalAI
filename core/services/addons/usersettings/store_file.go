// SPDX-License-Identifier: MIT
package usersettings

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"sync"
)

// FileStore keeps the answers of the one local owner in a JSON file, for a
// deployment with authentication off (no account database). The file lives on
// one host. An empty path keeps them in memory.
type FileStore struct {
	path string
	mu   sync.Mutex
	// st maps user, then add-on, then key, to the value.
	st map[string]map[string]map[string]string
}

func NewFileStore(path string) (*FileStore, error) {
	s := &FileStore{path: path, st: map[string]map[string]map[string]string{}}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.st); err != nil {
		return nil, err
	}
	if s.st == nil {
		s.st = map[string]map[string]map[string]string{}
	}
	return s, nil
}

// save writes through a temporary file so a crash never leaves half a file.
func (s *FileStore) save() error {
	if s.path == "" {
		return nil
	}
	b, err := json.Marshal(s.st)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *FileStore) Get(_ context.Context, userID, addonID string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.st[userID][addonID]), nil
}

func (s *FileStore) Set(_ context.Context, userID, addonID, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.st[userID] == nil {
		s.st[userID] = map[string]map[string]string{}
	}
	if s.st[userID][addonID] == nil {
		s.st[userID][addonID] = map[string]string{}
	}
	prev, had := s.st[userID][addonID][key]
	s.st[userID][addonID][key] = value
	if err := s.save(); err != nil {
		if had {
			s.st[userID][addonID][key] = prev
		} else {
			delete(s.st[userID][addonID], key)
		}
		return err
	}
	return nil
}
