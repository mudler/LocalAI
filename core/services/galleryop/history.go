package galleryop

import (
	"sync"
	"time"
)

// DefaultHistorySize bounds the in-memory record of finished operations. The
// Activity page is a "what just happened" view, not an audit log: 50 entries
// covers a full onboarding session and costs a few kilobytes. Durable history
// belongs in distributed.GalleryStore, which already persists terminal status.
const DefaultHistorySize = 50

// OpRecord is a finished operation. Field names match the JSON the Activity
// page consumes, so the handler can return the slice unwrapped.
type OpRecord struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	JobID      string    `json:"jobID"`
	IsBackend  bool      `json:"isBackend"`
	NodeID     string    `json:"nodeID,omitempty"`
	TaskType   string    `json:"taskType"`
	Outcome    string    `json:"outcome"`
	Error      string    `json:"error,omitempty"`
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
}

// Outcome values.
const (
	OutcomeCompleted = "completed"
	OutcomeFailed    = "failed"
	OutcomeCancelled = "cancelled"
)

// opHistory is a bounded, deduped ring of finished operations, oldest first.
// It knows nothing about jobs or statuses so it can be tested on its own.
type opHistory struct {
	mu      sync.Mutex
	records []OpRecord
	seen    map[string]struct{}
	limit   int
}

func newOpHistory(limit int) *opHistory {
	return &opHistory{
		records: make([]OpRecord, 0, limit),
		seen:    make(map[string]struct{}, limit),
		limit:   limit,
	}
}

// add appends rec unless its job ID was already recorded. Returns false when
// the record was a duplicate. The originating replica both evicts locally and
// receives its own NATS end broadcast, so without this every distributed
// operation would be recorded twice.
func (h *opHistory) add(rec OpRecord) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, dup := h.seen[rec.JobID]; dup {
		return false
	}
	h.seen[rec.JobID] = struct{}{}
	h.records = append(h.records, rec)

	// Drop the evicted record's job ID alongside it, so seen stays bounded by
	// limit rather than growing for the lifetime of the process. A duplicate
	// arriving 50 operations late would be re-added, which is both vanishingly
	// unlikely and harmless.
	for len(h.records) > h.limit {
		delete(h.seen, h.records[0].JobID)
		h.records = h.records[1:]
	}
	return true
}

// list returns a newest-first copy, so callers cannot mutate the ring and the
// page does not have to sort.
func (h *opHistory) list() []OpRecord {
	h.mu.Lock()
	defer h.mu.Unlock()

	out := make([]OpRecord, 0, len(h.records))
	for i := len(h.records) - 1; i >= 0; i-- {
		out = append(out, h.records[i])
	}
	return out
}

func (h *opHistory) clear() {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.records = h.records[:0]
	h.seen = make(map[string]struct{}, h.limit)
}
