package projects

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/pkg/atomicfile"
)

// jsonStore keeps everything in memory and, with a directory, in projects.json
// (mode 0600, rewritten atomically). One process owns the directory; a cluster
// uses the GORM store.
type jsonStore struct {
	mu   sync.Mutex
	path string
	all  map[string]Project
}

// NewJSON returns a Store. With an empty dir it keeps everything in memory.
func NewJSON(dir string) (Store, error) {
	s := &jsonStore{all: map[string]Project{}}
	if dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s.path = filepath.Join(dir, "projects.json")
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.all); err != nil {
		return nil, fmt.Errorf("projects: read %s: %w", s.path, err)
	}
	if s.all == nil {
		s.all = map[string]Project{}
	}
	return s, nil
}

func (s *jsonStore) save() error {
	if s.path == "" {
		return nil
	}
	return atomicfile.WriteJSON(s.path, s.all, 0o600)
}

func (s *jsonStore) Put(_ context.Context, p Project) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p.Slug = strings.ToLower(strings.TrimSpace(p.Slug))
	if p.Slug == "" {
		return fmt.Errorf("%w: slug is empty", ErrInvalid)
	}
	if o, ok := s.all[p.ID]; ok && o.Owner != p.Owner {
		return errIDInUse
	}
	for _, o := range s.all {
		if o.ID != p.ID && o.Owner == p.Owner && strings.EqualFold(o.Slug, p.Slug) {
			return ErrNameTaken
		}
	}
	old, had := s.all[p.ID]
	s.all[p.ID] = p
	if err := s.save(); err != nil {
		if had {
			s.all[p.ID] = old
		} else {
			delete(s.all, p.ID)
		}
		return err
	}
	return nil
}

func (s *jsonStore) Get(_ context.Context, owner, id string) (Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.all[id]
	if !ok || p.Owner != owner {
		return Project{}, ErrNotFound
	}
	return p, nil
}

func (s *jsonStore) List(_ context.Context, owner string) ([]Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Project, 0)
	for _, p := range s.all {
		if p.Owner == owner {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s *jsonStore) Delete(_ context.Context, owner, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.all[id]
	if !ok || p.Owner != owner {
		return nil
	}
	delete(s.all, id)
	if err := s.save(); err != nil {
		s.all[id] = p
		return err
	}
	return nil
}
