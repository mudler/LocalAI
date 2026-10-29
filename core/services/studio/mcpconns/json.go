package mcpconns

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

// jsonStore keeps everything in memory and, with a directory, in
// mcp-connectors.json (mode 0600, rewritten atomically). One process owns the
// directory; a cluster uses the GORM store.
type jsonStore struct {
	mu   sync.Mutex
	path string
	all  map[string]Connector
}

// NewJSON returns a Store. With an empty dir it keeps everything in memory.
func NewJSON(dir string) (Store, error) {
	s := &jsonStore{all: map[string]Connector{}}
	if dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s.path = filepath.Join(dir, "mcp-connectors.json")
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.all); err != nil {
		return nil, fmt.Errorf("mcpconns: read %s: %w", s.path, err)
	}
	if s.all == nil {
		s.all = map[string]Connector{}
	}
	return s, nil
}

func (s *jsonStore) save() error {
	if s.path == "" {
		return nil
	}
	raw, err := json.Marshal(s.all)
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, raw, 0o600)
}

func (s *jsonStore) Put(_ context.Context, c Connector) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range s.all {
		if o.ID != c.ID && o.Owner == c.Owner && o.Name == c.Name {
			return ErrNameTaken
		}
	}
	old, had := s.all[c.ID]
	s.all[c.ID] = c.clone()
	if err := s.save(); err != nil {
		if had {
			s.all[c.ID] = old
		} else {
			delete(s.all, c.ID)
		}
		return err
	}
	return nil
}

func (s *jsonStore) Get(_ context.Context, id string) (Connector, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.all[id]
	if !ok {
		return Connector{}, ErrNotFound
	}
	return c.clone(), nil
}

func (s *jsonStore) List(_ context.Context, owner string) ([]Connector, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Connector
	for _, c := range s.all {
		if c.Owner == owner {
			out = append(out, c.clone())
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

func (s *jsonStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.all, id)
	return s.save()
}
