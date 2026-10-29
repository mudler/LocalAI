package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/mudler/LocalAI/pkg/atomicfile"
)

// jsonStore keeps the agents in memory and, with a directory, in the file
// agents.json (rewritten atomically after every change). One process owns the
// directory: the lock is in-process only, so a cluster uses the GORM store.
type jsonStore struct {
	mu   sync.Mutex
	path string // empty: memory only
	byID map[string]Agent
}

// NewJSON returns a Store. With an empty dir it keeps everything in memory.
// With a directory it creates it and loads what is there.
func NewJSON(dir string) (Store, error) {
	s := &jsonStore{byID: map[string]Agent{}}
	if dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s.path = filepath.Join(dir, "agents.json")
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var all []Agent
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, fmt.Errorf("agents: read %s: %w", s.path, err)
	}
	for _, a := range all {
		s.byID[a.ID] = a
	}
	return s, nil
}

func (s *jsonStore) save() error {
	if s.path == "" {
		return nil
	}
	all := make([]Agent, 0, len(s.byID))
	for _, a := range s.byID {
		all = append(all, a)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	raw, err := json.Marshal(all)
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, raw, 0o600)
}

func (s *jsonStore) Put(_ context.Context, a Agent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, exists := s.byID[a.ID]
	if exists && old.Principal != a.Principal {
		return ErrNotFound
	}
	key := NameKey(a.Name)
	for _, o := range s.byID {
		if o.ID != a.ID && o.Principal == a.Principal && NameKey(o.Name) == key {
			return ErrNameTaken
		}
	}
	if exists {
		a.Created = old.Created
	}
	s.byID[a.ID] = a.clone()
	if err := s.save(); err != nil {
		if exists {
			s.byID[a.ID] = old
		} else {
			delete(s.byID, a.ID)
		}
		return err
	}
	return nil
}

func (s *jsonStore) Get(_ context.Context, principal, id string) (Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.byID[id]
	if !ok || a.Principal != principal {
		return Agent{}, ErrNotFound
	}
	return a.clone(), nil
}

func (s *jsonStore) List(_ context.Context, principal string) ([]Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Agent
	for _, a := range s.byID {
		if a.Principal == principal {
			out = append(out, a.clone())
		}
	}
	sort.Slice(out, func(i, j int) bool {
		ki, kj := NameKey(out[i].Name), NameKey(out[j].Name)
		if ki != kj {
			return ki < kj
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s *jsonStore) ListAll(context.Context) ([]Agent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Agent, 0, len(s.byID))
	for _, a := range s.byID {
		out = append(out, a.clone())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Principal != out[j].Principal {
			return out[i].Principal < out[j].Principal
		}
		if ki, kj := NameKey(out[i].Name), NameKey(out[j].Name); ki != kj {
			return ki < kj
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s *jsonStore) Delete(_ context.Context, principal, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.byID[id]
	if !ok || a.Principal != principal {
		return ErrNotFound
	}
	delete(s.byID, id)
	if err := s.save(); err != nil {
		s.byID[id] = a
		return err
	}
	return nil
}
