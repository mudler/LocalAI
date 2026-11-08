package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/atomicfile"
)

// jsonStore keeps the configs in memory and, with a directory, in the file
// coordinators.json (rewritten atomically after every change). The counters
// stay in memory: one process owns the directory, and a restart starts the
// counts again. A cluster uses GORM.
type jsonStore struct {
	mu       sync.Mutex
	path     string // empty: memory only
	byID     map[string]Config
	counters map[string]map[string]int
	audit    []AuditEntry // ascending ID
	nextID   int64
}

// NewJSON returns a Store. With an empty dir it keeps everything in memory.
func NewJSON(dir string) (Store, error) {
	s := &jsonStore{byID: map[string]Config{}, counters: map[string]map[string]int{}, nextID: 1}
	if dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s.path = filepath.Join(dir, "coordinators.json")
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var all []Config
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, fmt.Errorf("coordinator: read %s: %w", s.path, err)
	}
	for _, c := range all {
		s.byID[c.AgentID] = c.clone()
	}
	return s, nil
}

// save writes the file; the caller holds the lock.
func (s *jsonStore) save() error {
	if s.path == "" {
		return nil
	}
	all := make([]Config, 0, len(s.byID))
	for _, c := range s.byID {
		all = append(all, c)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].AgentID < all[j].AgentID })
	return atomicfile.WriteJSON(s.path, all, 0o600)
}

// mutate applies f to the stored config under the lock and saves; on a save
// failure the old value comes back.
func (s *jsonStore) mutate(agentID string, f func(c *Config) bool) (Config, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.byID[agentID]
	if !ok {
		return Config{}, false, ErrNotFound
	}
	c := old.clone()
	if !f(&c) {
		return c, false, nil
	}
	s.byID[agentID] = c
	if err := s.save(); err != nil {
		s.byID[agentID] = old
		return old.clone(), false, err
	}
	return c.clone(), true, nil
}

func (s *jsonStore) Put(_ context.Context, c Config) error {
	if c.AgentID == "" {
		return invalid("an agent ID is required")
	}
	if err := Validate(c); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, exists := s.byID[c.AgentID]
	if exists {
		c.ChatID, c.Paused, c.PausedBy, c.PausedAt = old.ChatID, old.Paused, old.PausedBy, old.PausedAt
		c.NextWake, c.LastWake, c.Created = old.NextWake, old.LastWake, old.Created
	}
	s.byID[c.AgentID] = c.clone()
	if err := s.save(); err != nil {
		if exists {
			s.byID[c.AgentID] = old
		} else {
			delete(s.byID, c.AgentID)
		}
		return err
	}
	return nil
}

func (s *jsonStore) Get(_ context.Context, agentID string) (Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.byID[agentID]
	if !ok {
		return Config{}, ErrNotFound
	}
	return c.clone(), nil
}

func (s *jsonStore) List(_ context.Context) ([]Config, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Config, 0, len(s.byID))
	for _, c := range s.byID {
		out = append(out, c.clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgentID < out[j].AgentID })
	return out, nil
}

func (s *jsonStore) Delete(_ context.Context, agentID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.byID[agentID]
	if !ok {
		return ErrNotFound
	}
	delete(s.byID, agentID)
	if err := s.save(); err != nil {
		s.byID[agentID] = old
		return err
	}
	delete(s.counters, agentID)
	return nil
}

func (s *jsonStore) SetChat(_ context.Context, agentID, chatID string) (string, error) {
	c, _, err := s.mutate(agentID, func(c *Config) bool {
		if c.ChatID != "" {
			return false
		}
		c.ChatID = chatID
		return true
	})
	return c.ChatID, err
}

func (s *jsonStore) ReplaceChat(_ context.Context, agentID, oldID, newID string) (string, error) {
	c, _, err := s.mutate(agentID, func(c *Config) bool {
		if c.ChatID != oldID {
			return false
		}
		c.ChatID = newID
		return true
	})
	return c.ChatID, err
}

func (s *jsonStore) SetPaused(_ context.Context, agentID string, paused bool, by string, at time.Time) (Config, bool, error) {
	return s.mutate(agentID, func(c *Config) bool {
		if c.Paused == paused {
			return false
		}
		c.Paused = paused
		if paused {
			c.PausedBy, c.PausedAt = by, at
		} else {
			c.PausedBy, c.PausedAt = "", time.Time{}
		}
		return true
	})
}

func (s *jsonStore) ClaimWake(_ context.Context, agentID string, now, next time.Time) (bool, error) {
	_, ok, err := s.mutate(agentID, func(c *Config) bool {
		if c.NextWake.After(now) {
			return false
		}
		c.NextWake, c.LastWake = next, now
		return true
	})
	return ok, err
}

func (s *jsonStore) Reserve(_ context.Context, agentID, bucket string, limit int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.counters[agentID]
	if m == nil {
		m = map[string]int{}
		s.counters[agentID] = m
	}
	if m[bucket] >= limit {
		return false, nil
	}
	m[bucket]++
	return true, nil
}

func (s *jsonStore) Used(_ context.Context, agentID, bucket string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counters[agentID][bucket], nil
}

func (s *jsonStore) AddAudit(_ context.Context, e AuditEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.At.IsZero() {
		e.At = time.Now()
	}
	e.At = e.At.UTC()
	e.ID = s.nextID
	s.nextID++
	s.audit = append(s.audit, e)
	return nil
}

func (s *jsonStore) Audit(_ context.Context, q AuditQuery) ([]AuditEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := q.limit()
	var out []AuditEntry
	for i := len(s.audit) - 1; i >= 0 && len(out) < limit; i-- {
		e := s.audit[i]
		switch {
		case q.Before > 0 && e.ID >= q.Before,
			q.CoordinatorID != "" && e.CoordinatorID != q.CoordinatorID,
			q.Principal != "" && e.Principal != q.Principal,
			q.Kind != "" && e.Kind != q.Kind,
			q.Outcome != "" && e.Outcome != q.Outcome,
			!q.Since.IsZero() && e.At.Before(q.Since),
			!q.Until.IsZero() && !e.At.Before(q.Until):
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func (s *jsonStore) Prune(_ context.Context, before time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.audit[:0]
	dropped := 0
	for _, e := range s.audit {
		if e.At.Before(before) {
			dropped++
			continue
		}
		kept = append(kept, e)
	}
	s.audit = kept
	for _, m := range s.counters {
		for b := range m {
			if end, ok := bucketEnd(b); ok && !end.After(before) {
				delete(m, b)
			}
		}
	}
	return dropped, nil
}
