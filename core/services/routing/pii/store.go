package pii

import (
	"context"
	"sync"
)

// EventStore persists PIIEvent records. Mirrors the StatsBackend
// abstraction in the billing package: in-process by default so a
// no-auth box still gets an event log; a future GORM-backed impl
// (when --auth is on) will reuse the auth DB.
type EventStore interface {
	Record(ctx context.Context, e PIIEvent) error
	List(ctx context.Context, q ListQuery) ([]PIIEvent, error)
	// Count returns the number of events currently stored. Used by
	// /api/middleware/status to surface a "recent_event_count" without
	// pulling the whole list (the dashboard polls this on a refresh).
	Count(ctx context.Context) (int, error)
	Close() error
}

// ListQuery filters the event log. CorrelationID, UserID, PatternID,
// Kind each scope the search; empty values match anything. Limit ≤ 0
// returns up to a default cap.
type ListQuery struct {
	CorrelationID string
	UserID        string
	PatternID     string
	Kind          EventKind
	Limit         int
}

// NewMemoryEventStore returns an in-memory ring-buffer event store.
// capacity ≤ 0 picks 10_000.
//
// Why a ring: PII events are noisy; a chatty deployment can produce
// thousands per minute. A bounded buffer keeps memory predictable,
// and the GORM impl (when added) handles long-term retention.
func NewMemoryEventStore(capacity int) EventStore {
	if capacity <= 0 {
		capacity = 10_000
	}
	return &memoryEventStore{
		ring: make([]PIIEvent, capacity),
		cap:  capacity,
	}
}

type memoryEventStore struct {
	mu     sync.RWMutex
	ring   []PIIEvent
	cap    int
	cursor int
	full   bool
}

func (s *memoryEventStore) Record(_ context.Context, e PIIEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ring[s.cursor] = e
	s.cursor++
	if s.cursor == s.cap {
		s.cursor = 0
		s.full = true
	}
	return nil
}

func (s *memoryEventStore) List(_ context.Context, q ListQuery) ([]PIIEvent, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 1000
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]PIIEvent, 0, limit)
	scan := func(e PIIEvent) bool {
		if e.ID == "" {
			return false // empty slot
		}
		if q.CorrelationID != "" && e.CorrelationID != q.CorrelationID {
			return false
		}
		if q.UserID != "" && e.UserID != q.UserID {
			return false
		}
		if q.PatternID != "" && e.PatternID != q.PatternID {
			return false
		}
		if q.Kind != "" && e.ResolvedKind() != q.Kind {
			return false
		}
		out = append(out, e)
		return len(out) >= limit
	}

	// Walk newest-first: cursor-1 down to 0, then cap-1 down to cursor
	// when the ring has wrapped.
	if s.full {
		for i := s.cursor - 1; i >= 0; i-- {
			if scan(s.ring[i]) {
				return out, nil
			}
		}
		for i := s.cap - 1; i >= s.cursor; i-- {
			if scan(s.ring[i]) {
				return out, nil
			}
		}
	} else {
		for i := s.cursor - 1; i >= 0; i-- {
			if scan(s.ring[i]) {
				return out, nil
			}
		}
	}
	return out, nil
}

func (s *memoryEventStore) Count(_ context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.full {
		return s.cap, nil
	}
	return s.cursor, nil
}

func (s *memoryEventStore) Close() error { return nil }
