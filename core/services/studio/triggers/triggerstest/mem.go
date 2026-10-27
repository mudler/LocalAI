package triggerstest

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/triggers"
)

// MemStore is a triggers.Store in memory.
type MemStore struct {
	mu       sync.Mutex
	triggers map[string]triggers.Trigger
	runs     []triggers.Run // in claim order
	cursor   int64
	hasCur   bool
	lastDue  map[string]time.Time
}

var _ triggers.Store = (*MemStore)(nil)

// NewMemStore returns an empty store.
func NewMemStore() *MemStore {
	return &MemStore{triggers: map[string]triggers.Trigger{}, lastDue: map[string]time.Time{}}
}

func utc(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.UTC()
}

func normTrigger(t triggers.Trigger) triggers.Trigger {
	t.Since = utc(t.Since)
	return t
}

func normRun(r triggers.Run) triggers.Run {
	r.Due, r.Started, r.Ended = utc(r.Due), utc(r.Started), utc(r.Ended)
	return r
}

// PutTrigger implements triggers.Store.
func (m *MemStore) PutTrigger(_ context.Context, t triggers.Trigger) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.triggers[t.ID] = normTrigger(t)
	return nil
}

// DeleteTrigger implements triggers.Store.
func (m *MemStore) DeleteTrigger(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.triggers, id)
	delete(m.lastDue, id)
	kept := m.runs[:0]
	for _, r := range m.runs {
		if r.TriggerID != id {
			kept = append(kept, r)
		}
	}
	m.runs = kept
	return nil
}

// GetTrigger implements triggers.Store.
func (m *MemStore) GetTrigger(_ context.Context, id string) (triggers.Trigger, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.triggers[id]
	if !ok {
		return triggers.Trigger{}, triggers.ErrNotFound
	}
	return t, nil
}

// ListTriggers implements triggers.Store.
func (m *MemStore) ListTriggers(_ context.Context, owner string) ([]triggers.Trigger, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []triggers.Trigger
	for _, t := range m.triggers {
		if t.Owner == owner {
			out = append(out, t)
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

// EnabledTriggers implements triggers.Store.
func (m *MemStore) EnabledTriggers(_ context.Context) ([]triggers.Trigger, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []triggers.Trigger
	for _, t := range m.triggers {
		if t.Enabled {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Claim implements triggers.Store.
func (m *MemStore) Claim(_ context.Context, r triggers.Run) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.runs {
		if x.TriggerID == r.TriggerID && x.DueKey == r.DueKey {
			return false, nil
		}
	}
	for _, x := range m.runs {
		if x.ID == r.ID {
			return false, fmt.Errorf("triggers: run %s already exists", r.ID)
		}
	}
	m.runs = append(m.runs, normRun(r))
	return true, nil
}

// GetRun implements triggers.Store.
func (m *MemStore) GetRun(_ context.Context, id string) (triggers.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.runs {
		if r.ID == id {
			return r, nil
		}
	}
	return triggers.Run{}, triggers.ErrNotFound
}

// SetRunStatus implements triggers.Store.
func (m *MemStore) SetRunStatus(_ context.Context, r triggers.Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, x := range m.runs {
		if x.ID == r.ID {
			x.Status, x.Reason, x.ChatID, x.Started, x.Ended = r.Status, r.Reason, r.ChatID, utc(r.Started), utc(r.Ended)
			m.runs[i] = x
			return nil
		}
	}
	return triggers.ErrNotFound
}

// ListRuns implements triggers.Store.
func (m *MemStore) ListRuns(_ context.Context, triggerID string, limit int) ([]triggers.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []triggers.Run
	for i := len(m.runs) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		if m.runs[i].TriggerID == triggerID {
			out = append(out, m.runs[i])
		}
	}
	return out, nil
}

func (m *MemStore) byStatus(s triggers.Status) []triggers.Run {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []triggers.Run
	for _, r := range m.runs {
		if r.Status == s {
			out = append(out, r)
		}
	}
	return out
}

// RunningRuns implements triggers.Store.
func (m *MemStore) RunningRuns(context.Context) ([]triggers.Run, error) {
	return m.byStatus(triggers.StatusRunning), nil
}

// QueuedRuns implements triggers.Store.
func (m *MemStore) QueuedRuns(context.Context) ([]triggers.Run, error) {
	return m.byStatus(triggers.StatusQueued), nil
}

// RunByChat implements triggers.Store.
func (m *MemStore) RunByChat(_ context.Context, chatID string) (triggers.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.runs {
		if chatID != "" && r.ChatID == chatID {
			return r, nil
		}
	}
	return triggers.Run{}, triggers.ErrNotFound
}

// CountRunsSince implements triggers.Store.
func (m *MemStore) CountRunsSince(_ context.Context, triggerID string, since time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, r := range m.runs {
		if r.TriggerID == triggerID && r.Status != triggers.StatusSkipped && !r.Due.Before(since) {
			n++
		}
	}
	return n, nil
}

// Cursor implements triggers.Store.
func (m *MemStore) Cursor(context.Context) (int64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cursor, m.hasCur, nil
}

// SetCursor implements triggers.Store.
func (m *MemStore) SetCursor(_ context.Context, rev int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cursor, m.hasCur = rev, true
	return nil
}

// LastDue implements triggers.Store.
func (m *MemStore) LastDue(_ context.Context, id string) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastDue[id], nil
}

// SetLastDue implements triggers.Store.
func (m *MemStore) SetLastDue(_ context.Context, id string, t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastDue[id] = utc(t)
	return nil
}
