// SPDX-License-Identifier: MIT
package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// The functions in this file are the trusted, local face of the source store:
// they are for `local-ai library` and for startup code, which run as the owner
// of the data directory and take no actor. The server reads the same file, so a
// change made here is what the server sees at its next start.

// ErrExists is returned when a source with the same id already exists.
var ErrExists = errors.New("a library source with this id already exists")

// StorePath is where the owner keeps the sources, under the data path.
func StorePath(dataPath string) string {
	return filepath.Join(dataPath, "addons", "sources.json")
}

// OpenStore opens the source store for the owner of the data directory. A
// missing file is a fresh store. The directory is created with owner-only
// access, as the add-on owner creates it.
func OpenStore(path string) (*Sources, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("library store: %w", err)
	}
	s, err := NewSources(path, func(context.Context, contract.Actor) error { return nil }, CredentialResolver{})
	if err != nil {
		return nil, fmt.Errorf("library store %s: %w (the file must be a regular file readable by its owner only)", path, err)
	}
	return s, nil
}

// Entries returns every record, tombstones included, ordered by kind and id.
func (s *Sources) Entries() []Source {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Source, 0, len(s.sources))
	for _, v := range s.sources {
		if v.Kind == "" {
			v.Kind = KindAddon
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// AddUser adds a source the user owns. The id must be free, tombstones
// included: a removed default is brought back with Enable, not by adding it
// again.
func (s *Sources) AddUser(src Source) error {
	src.Default, src.Removed = false, false
	if src.Kind == "" {
		src.Kind = KindAddon
	}
	if !validSource(src) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sources[src.ID]; ok {
		return fmt.Errorf("%w: %q", ErrExists, src.ID)
	}
	next := map[string]Source{src.ID: src}
	for id, v := range s.sources {
		next[id] = v
	}
	if err := s.persist(next); err != nil {
		return err
	}
	s.sources = next
	s.fresh = false
	return nil
}

// RemoveSource deletes a user source. A default is not deleted: it becomes a
// tombstone, so it does not come back at the next start.
func (s *Sources) RemoveSource(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.sources[id]
	if !ok || old.Removed {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	next, err := s.apply(Source{ID: id, Removed: true})
	if err != nil {
		return err
	}
	if err = s.persist(next); err != nil {
		return err
	}
	s.sources = next
	delete(s.cache, id)
	return nil
}

// SetEnabled turns a source on or off. Turning on a tombstone brings the
// default back.
func (s *Sources) SetEnabled(id string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.sources[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	if old.Removed && !enabled {
		return nil
	}
	old.Enabled, old.Removed = enabled, false
	next := map[string]Source{}
	for k, v := range s.sources {
		next[k] = v
	}
	next[id] = old
	if err := s.persist(next); err != nil {
		return err
	}
	s.sources = next
	delete(s.cache, id)
	return nil
}
