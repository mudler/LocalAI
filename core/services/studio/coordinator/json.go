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
	reports  map[string]Report
	repPath  string // coordinator-reports.json, empty: memory only
	byID     map[string]Config
	counters map[string]map[string]int
	audit    []AuditEntry // ascending ID
	nextID   int64
}

// NewJSON returns a Store. With an empty dir it keeps everything in memory.
func NewJSON(dir string) (Store, error) {
	s := &jsonStore{byID: map[string]Config{}, counters: map[string]map[string]int{}, reports: map[string]Report{}, nextID: 1}
	if dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s.path = filepath.Join(dir, "coordinators.json")
	s.repPath = filepath.Join(dir, "coordinator-reports.json")
	raw, err := os.ReadFile(s.path)
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return nil, err
	default:
		var all []Config
		if err := json.Unmarshal(raw, &all); err != nil {
			return nil, fmt.Errorf("coordinator: read %s: %w", s.path, err)
		}
		for _, c := range all {
			s.byID[c.AgentID] = c.clone()
		}
	}
	// The reports survive a restart, so a helper that ended while the process
	// was down is still reported.
	raw, err = os.ReadFile(s.repPath)
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return nil, err
	default:
		var reps []Report
		if err := json.Unmarshal(raw, &reps); err != nil {
			return nil, fmt.Errorf("coordinator: read %s: %w", s.repPath, err)
		}
		for _, r := range reps {
			s.reports[r.ChildID] = r
		}
	}
	return s, nil
}

// saveReports writes the reports file; the caller holds the lock.
func (s *jsonStore) saveReports() error {
	if s.repPath == "" {
		return nil
	}
	all := make([]Report, 0, len(s.reports))
	for _, r := range s.reports {
		all = append(all, r)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ChildID < all[j].ChildID })
	return atomicfile.WriteJSON(s.repPath, all, 0o600)
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
	dropped := false
	for id, r := range s.reports {
		if r.CoordinatorID == agentID {
			delete(s.reports, id)
			dropped = true
		}
	}
	if dropped {
		return s.saveReports()
	}
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
	if _, err := s.pruneReports(before); err != nil {
		return dropped, err
	}
	for _, m := range s.counters {
		for b := range m {
			if end, ok := bucketEnd(b); ok && !end.After(before) {
				delete(m, b)
			}
		}
	}
	return dropped, nil
}

func (s *jsonStore) AddReport(_ context.Context, r Report) error {
	if err := r.validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.reports[r.ChildID]; ok {
		return nil
	}
	r.Created, r.LeaseUntil, r.Done = r.Created.UTC(), time.Time{}, time.Time{}
	s.reports[r.ChildID] = r
	if err := s.saveReports(); err != nil {
		delete(s.reports, r.ChildID)
		return err
	}
	return nil
}

func (s *jsonStore) DueReports(_ context.Context, now time.Time, limit int) ([]Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Report
	for _, r := range s.reports {
		if r.Done.IsZero() && !r.LeaseUntil.After(now) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.Before(out[j].Created)
		}
		return out[i].ChildID < out[j].ChildID
	})
	if limit < 1 {
		limit = 1
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// updateReport applies f to an open report under the lock and saves; f says
// whether it changed the report.
func (s *jsonStore) updateReport(childID string, f func(r *Report) bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.reports[childID]
	if !ok || !old.Done.IsZero() {
		return false, nil
	}
	r := old
	if !f(&r) {
		return false, nil
	}
	s.reports[childID] = r
	if err := s.saveReports(); err != nil {
		s.reports[childID] = old
		return false, err
	}
	return true, nil
}

func (s *jsonStore) ClaimReport(_ context.Context, childID string, now, until time.Time) (bool, error) {
	return s.updateReport(childID, func(r *Report) bool {
		if r.LeaseUntil.After(now) {
			return false
		}
		r.LeaseUntil = until.UTC()
		return true
	})
}

func (s *jsonStore) FinishReport(_ context.Context, childID string, at time.Time) error {
	_, err := s.updateReport(childID, func(r *Report) bool {
		r.Done = at.UTC()
		return true
	})
	return err
}

func (s *jsonStore) ReleaseReport(_ context.Context, childID string) error {
	_, err := s.updateReport(childID, func(r *Report) bool {
		r.LeaseUntil = time.Time{}
		return true
	})
	return err
}

func (s *jsonStore) PruneReports(_ context.Context, before time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pruneReports(before)
}

// pruneReports drops the finished reports and the ones created before before;
// the caller holds the lock.
func (s *jsonStore) pruneReports(before time.Time) (int, error) {
	n := 0
	for id, r := range s.reports {
		if !r.Done.IsZero() || r.Created.Before(before) {
			delete(s.reports, id)
			n++
		}
	}
	if n == 0 {
		return 0, nil
	}
	return n, s.saveReports()
}

func (s *jsonStore) GetReport(_ context.Context, childID string) (Report, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.reports[childID]
	if !ok {
		return Report{}, ErrNotFound
	}
	return r, nil
}

func (s *jsonStore) PendingReports(_ context.Context, parentChat string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.reports {
		if r.ParentChat == parentChat && r.Done.IsZero() {
			n++
		}
	}
	return n, nil
}
