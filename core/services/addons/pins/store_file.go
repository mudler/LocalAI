// SPDX-License-Identifier: MIT
package pins

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// FileStore keeps the pins of the one local owner in a JSON file, for a
// deployment with authentication off (no account database). The behaviour is
// per instance: the file lives on one host.
type FileStore struct {
	path string
	mu   sync.Mutex
	st   fileState
}

type fileState struct {
	Pins     map[string][]Pin     `json:"pins"`
	SeedRuns map[string]time.Time `json:"seedRuns"`
}

func NewFileStore(path string) (*FileStore, error) {
	s := &FileStore{path: path, st: fileState{Pins: map[string][]Pin{}, SeedRuns: map[string]time.Time{}}}
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.st); err != nil {
		return nil, err
	}
	if s.st.Pins == nil {
		s.st.Pins = map[string][]Pin{}
	}
	if s.st.SeedRuns == nil {
		s.st.SeedRuns = map[string]time.Time{}
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

func (s *FileStore) List(_ context.Context, userID string) ([]Pin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.st.Pins[userID]), nil
}

func (s *FileStore) Replace(_ context.Context, userID string, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Pins[userID] = rebuild(s.st.Pins[userID], userID, ids)
	return s.save()
}

// rebuild lays ids out in order and carries the missing mark of an id that
// was already pinned.
func rebuild(old []Pin, userID string, ids []string) []Pin {
	marks := map[string]*time.Time{}
	for _, p := range old {
		marks[p.AddonID] = p.MissingSince
	}
	out := make([]Pin, 0, len(ids))
	for i, id := range ids {
		out = append(out, Pin{UserID: userID, AddonID: id, Position: i, MissingSince: marks[id]})
	}
	return out
}

func (s *FileStore) SeedExisting(_ context.Context, userIDs []string, ids []string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, done := s.st.SeedRuns[UpgradeSeedName]; done {
		return false, nil
	}
	for _, userID := range userIDs {
		have := map[string]bool{}
		list := s.st.Pins[userID]
		for _, p := range list {
			have[p.AddonID] = true
		}
		for _, id := range ids {
			if !have[id] {
				list = append(list, Pin{UserID: userID, AddonID: id, Position: len(list)})
			}
		}
		s.st.Pins[userID] = list
	}
	s.st.SeedRuns[UpgradeSeedName] = time.Now().UTC()
	return true, s.save()
}

func (s *FileStore) Distinct(_ context.Context) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	out := []string{}
	for _, list := range s.st.Pins {
		for _, p := range list {
			if !seen[p.AddonID] {
				seen[p.AddonID] = true
				out = append(out, p.AddonID)
			}
		}
	}
	return out, nil
}

func (s *FileStore) MarkMissing(_ context.Context, ids []string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for u, list := range s.st.Pins {
		for i := range list {
			if list[i].MissingSince == nil && slices.Contains(ids, list[i].AddonID) {
				t := now
				list[i].MissingSince = &t
			}
		}
		s.st.Pins[u] = list
	}
	return s.save()
}

func (s *FileStore) ClearMissing(_ context.Context, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for u, list := range s.st.Pins {
		for i := range list {
			if slices.Contains(ids, list[i].AddonID) {
				list[i].MissingSince = nil
			}
		}
		s.st.Pins[u] = list
	}
	return s.save()
}

func (s *FileStore) DeleteMissingBefore(_ context.Context, cutoff time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for u, list := range s.st.Pins {
		kept := list[:0]
		for _, p := range list {
			if p.MissingSince != nil && p.MissingSince.Before(cutoff) {
				n++
				continue
			}
			kept = append(kept, p)
		}
		for i := range kept {
			kept[i].Position = i
		}
		s.st.Pins[u] = kept
	}
	return n, s.save()
}
