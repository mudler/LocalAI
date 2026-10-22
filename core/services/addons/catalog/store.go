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

// SetMirrors replaces the mirrors of a source. An empty list clears them. A
// removed default has no mirrors to change.
func (s *Sources) SetMirrors(id string, mirrors []string) error {
	if err := ValidateMirrors(mirrors); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.sources[id]
	if !ok || old.Removed {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	if len(mirrors) == 0 {
		mirrors = nil
	}
	old.Mirrors = mirrors
	next := map[string]Source{}
	for k, v := range s.sources {
		next[k] = v
	}
	next[id] = old
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

// SetOfficial turns the shipped default sources of every kind on or off as a
// group. A suspended default is not read, but its record, its own enabled
// state and its tombstone stay as they are, so turning the switch back on
// restores exactly what was there. Sources the user added are not affected.
// It reports whether any default changed.
func (s *Sources) SetOfficial(on bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.officialOff = !on
	changed := false
	next := make(map[string]Source, len(s.sources))
	for id, v := range s.sources {
		if v.Default && v.Suspended != !on {
			v.Suspended = !on
			changed = true
			delete(s.cache, id)
		}
		next[id] = v
	}
	s.sources = next
	return changed
}

// Official reports whether the shipped defaults are in use.
func (s *Sources) Official() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.officialOff
}

// Conflicts maps a source id to the message that explains how its entries
// clash with another source: for two sources of the user that list one entry,
// the same message as the refusal ConflictError gives; for a shipped default
// whose entry a source of the user also lists, a note that the default loses
// that entry. Only sources the add-on catalog reads and has fetched can
// conflict, so gallery-format sources never appear.
func (s *Sources) Conflicts() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	keys := make([]entryKey, 0)
	own := s.entryOwners()
	for k := range own {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].kind != keys[j].kind {
			return keys[i].kind < keys[j].kind
		}
		return keys[i].id < keys[j].id
	})
	for _, k := range keys {
		o := own[k]
		if len(o.user) >= 2 {
			msg := (&ConflictError{Kind: k.kind, ID: k.id, First: o.user[0], Second: o.user[1]}).Error()
			for _, id := range o.user {
				if _, ok := out[id]; !ok {
					out[id] = msg
				}
			}
		} else if len(o.user) == 1 {
			for _, id := range o.shipped {
				if _, ok := out[id]; !ok {
					out[id] = fmt.Sprintf("the %s %q is provided by your source %q, so this default source is ignored for it", k.kind, k.id, o.user[0])
				}
			}
		}
	}
	return out
}
