package triggers

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

// jsonStore keeps triggers, runs and the Runner's state in memory and, with a
// directory, in the file triggers.json (rewritten atomically after every
// change). One process owns the directory: the lock is in-process only, so a
// cluster uses the GORM store.
type jsonStore struct {
	mu   sync.Mutex
	path string // empty: memory only
	d    jsonData
}

type jsonData struct {
	Triggers map[string]Trigger   `json:"triggers"`
	Runs     []Run                `json:"runs"` // in claim order
	Cursor   int64                `json:"cursor"`
	HasCur   bool                 `json:"has_cursor"`
	LastDue  map[string]time.Time `json:"last_due"`
}

// NewJSON returns a Store. With an empty dir it keeps everything in memory.
func NewJSON(dir string) (Store, error) {
	s := &jsonStore{d: jsonData{Triggers: map[string]Trigger{}, LastDue: map[string]time.Time{}}}
	if dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s.path = filepath.Join(dir, "triggers.json")
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &s.d); err != nil {
		return nil, fmt.Errorf("triggers: read %s: %w", s.path, err)
	}
	if s.d.Triggers == nil {
		s.d.Triggers = map[string]Trigger{}
	}
	if s.d.LastDue == nil {
		s.d.LastDue = map[string]time.Time{}
	}
	return s, nil
}

func (s *jsonStore) save() error {
	if s.path == "" {
		return nil
	}
	raw, err := json.Marshal(s.d)
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, raw, 0o600)
}

func utc(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.UTC()
}

func normTrigger(t Trigger) Trigger { t.Since = utc(t.Since); return t }

func normRun(r Run) Run {
	r.Due, r.Started, r.Ended = utc(r.Due), utc(r.Started), utc(r.Ended)
	return r
}

func (s *jsonStore) PutTrigger(_ context.Context, t Trigger) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, had := s.d.Triggers[t.ID]
	s.d.Triggers[t.ID] = normTrigger(t)
	if err := s.save(); err != nil {
		if had {
			s.d.Triggers[t.ID] = old
		} else {
			delete(s.d.Triggers, t.ID)
		}
		return err
	}
	return nil
}

func (s *jsonStore) DeleteTrigger(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.d
	old := jsonData{Triggers: map[string]Trigger{}, LastDue: map[string]time.Time{}, Runs: append([]Run(nil), d.Runs...), Cursor: d.Cursor, HasCur: d.HasCur}
	for k, v := range d.Triggers {
		old.Triggers[k] = v
	}
	for k, v := range d.LastDue {
		old.LastDue[k] = v
	}
	delete(s.d.Triggers, id)
	delete(s.d.LastDue, id)
	kept := s.d.Runs[:0:0]
	for _, r := range s.d.Runs {
		if r.TriggerID != id {
			kept = append(kept, r)
		}
	}
	s.d.Runs = kept
	if err := s.save(); err != nil {
		s.d = old
		return err
	}
	return nil
}

func (s *jsonStore) GetTrigger(_ context.Context, id string) (Trigger, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.d.Triggers[id]
	if !ok {
		return Trigger{}, ErrNotFound
	}
	return t, nil
}

func (s *jsonStore) triggersWhere(keep func(Trigger) bool, less func(a, b Trigger) bool) []Trigger {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Trigger
	for _, t := range s.d.Triggers {
		if keep(t) {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

func (s *jsonStore) ListTriggers(_ context.Context, owner string) ([]Trigger, error) {
	return s.triggersWhere(func(t Trigger) bool { return t.Owner == owner }, func(a, b Trigger) bool {
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	}), nil
}

func (s *jsonStore) AllTriggers(context.Context) ([]Trigger, error) {
	return s.triggersWhere(func(Trigger) bool { return true }, func(a, b Trigger) bool {
		if a.Owner != b.Owner {
			return a.Owner < b.Owner
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	}), nil
}

func (s *jsonStore) EnabledTriggers(context.Context) ([]Trigger, error) {
	return s.triggersWhere(func(t Trigger) bool { return t.Enabled }, func(a, b Trigger) bool { return a.ID < b.ID }), nil
}

func (s *jsonStore) Claim(_ context.Context, r Run) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.d.Runs {
		if x.TriggerID == r.TriggerID && x.DueKey == r.DueKey {
			return false, nil
		}
	}
	for _, x := range s.d.Runs {
		if x.ID == r.ID {
			return false, fmt.Errorf("triggers: run %s already exists", r.ID)
		}
	}
	s.d.Runs = append(s.d.Runs, normRun(r))
	if err := s.save(); err != nil {
		s.d.Runs = s.d.Runs[:len(s.d.Runs)-1]
		return false, err
	}
	return true, nil
}

func (s *jsonStore) GetRun(_ context.Context, id string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.d.Runs {
		if r.ID == id {
			return r, nil
		}
	}
	return Run{}, ErrNotFound
}

func (s *jsonStore) SetRunStatus(_ context.Context, r Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.d.Runs {
		if x.ID != r.ID {
			continue
		}
		prev := x
		x.Status, x.Reason, x.ChatID, x.Started, x.Ended = r.Status, r.Reason, r.ChatID, utc(r.Started), utc(r.Ended)
		s.d.Runs[i] = x
		if err := s.save(); err != nil {
			s.d.Runs[i] = prev
			return err
		}
		return nil
	}
	return ErrNotFound
}

func (s *jsonStore) ListRuns(_ context.Context, triggerID string, limit int) ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Run
	for i := len(s.d.Runs) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		if s.d.Runs[i].TriggerID == triggerID {
			out = append(out, s.d.Runs[i])
		}
	}
	return out, nil
}

func (s *jsonStore) PruneRuns(_ context.Context, triggerID string, p Retention) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	drop := make([]bool, len(s.d.Runs))
	n, finished := 0, 0
	for i := len(s.d.Runs) - 1; i >= 0; i-- { // newest claim first
		r := s.d.Runs[i]
		if r.TriggerID != triggerID || !r.Status.Final() {
			continue
		}
		finished++
		if p.expires(r.Due) || (p.Keep > 0 && finished > p.Keep && !p.protects(r.Due)) {
			drop[i] = true
			n++
		}
	}
	if n == 0 {
		return 0, nil
	}
	prev := s.d.Runs
	kept := make([]Run, 0, len(prev)-n)
	for i, r := range prev {
		if !drop[i] {
			kept = append(kept, r)
		}
	}
	s.d.Runs = kept
	if err := s.save(); err != nil {
		s.d.Runs = prev
		return 0, err
	}
	return n, nil
}

func (s *jsonStore) byStatus(st Status) []Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Run
	for _, r := range s.d.Runs {
		if r.Status == st {
			out = append(out, r)
		}
	}
	return out
}

func (s *jsonStore) RunningRuns(context.Context) ([]Run, error) {
	return s.byStatus(StatusRunning), nil
}
func (s *jsonStore) QueuedRuns(context.Context) ([]Run, error) { return s.byStatus(StatusQueued), nil }

func (s *jsonStore) RunByChat(_ context.Context, chatID string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.d.Runs {
		if chatID != "" && r.ChatID == chatID {
			return r, nil
		}
	}
	return Run{}, ErrNotFound
}

func (s *jsonStore) CountRunsSince(_ context.Context, triggerID string, since time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.d.Runs {
		if r.TriggerID == triggerID && r.Status != StatusSkipped && !r.Due.Before(since) {
			n++
		}
	}
	return n, nil
}

func (s *jsonStore) Cursor(context.Context) (int64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.d.Cursor, s.d.HasCur, nil
}

func (s *jsonStore) SetCursor(_ context.Context, rev int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prevRev, prevHas := s.d.Cursor, s.d.HasCur
	s.d.Cursor, s.d.HasCur = rev, true
	if err := s.save(); err != nil {
		s.d.Cursor, s.d.HasCur = prevRev, prevHas
		return err
	}
	return nil
}

func (s *jsonStore) LastDue(_ context.Context, id string) (time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.d.LastDue[id], nil
}

func (s *jsonStore) SetLastDue(_ context.Context, id string, t time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, had := s.d.LastDue[id]
	s.d.LastDue[id] = utc(t)
	if err := s.save(); err != nil {
		if had {
			s.d.LastDue[id] = prev
		} else {
			delete(s.d.LastDue, id)
		}
		return err
	}
	return nil
}
