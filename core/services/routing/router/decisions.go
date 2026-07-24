package router

import (
	"context"
	"sync"
	"time"
)

// Decision row written to the in-memory store. Mirrors the PIIEvent
// shape so the admin page can render the two side-by-side. Note:
// Prompt is NEVER stored — admins audit by Hash if they need to
// dedupe recurring routing patterns.
type DecisionRecord struct {
	ID                string  `json:"id"`
	CorrelationID     string  `json:"correlation_id"`
	UserID            string  `json:"user_id"`
	RouterModel       string  `json:"router_model"`    // The smart-router model name the client asked for.
	RequestedModel    string  `json:"requested_model"` // Same as RouterModel for now; reserved for chained routers.
	ServedModel       string  `json:"served_model"`    // The candidate the classifier picked.
	Classifier        string  `json:"classifier"`      // Classifier.Name(), e.g. "score".
	Label             string  `json:"label"`
	Score             float64 `json:"score"`
	LatencyMs         int64   `json:"latency_ms"`
	Cached            bool    `json:"cached"`                       // True when the decision came from the L2 embedding cache.
	CacheSimilarity   float64 `json:"cache_similarity,omitempty"`   // Cosine similarity of the cache hit, 0 when not cached.
	NearestSimilarity float64 `json:"nearest_similarity,omitempty"` // KNN classifier: similarity of the closest corpus entry, set even on fallback decisions. 0 for other classifiers.
	// LabelScores carries the full per-label score distribution so the
	// admin UI can show how close inactive labels got to the activation
	// threshold. Empty on cache hits (only the final label set is cached).
	LabelScores         []LabelScore `json:"label_scores,omitempty"`
	ActivationThreshold float64      `json:"activation_threshold,omitempty"`
	// Neighbors names the corpus entries a KNN decision consulted (content-
	// hash IDs, similarities, labels — never text). This is the join key
	// for platform-side per-region reliability accounting: decisions that
	// retrieve the same corpus entries belong to the same region.
	Neighbors []NeighborRef `json:"neighbors,omitempty"`
	// Source groups decisions by the entry point that produced them so
	// the admin page can split realtime / chat / anthropic streams. Empty
	// string is treated as "chat" for backward compatibility with rows
	// written before the field existed.
	Source    string    `json:"source,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Source values for DecisionRecord.Source. Kept as constants so callers
// don't drift on capitalisation.
const (
	SourceChat      = "chat"
	SourceAnthropic = "anthropic"
	SourceRealtime  = "realtime"
)

// DecisionStore persists routing decisions for the admin page and
// future drift checks. In-process by default so a no-auth box still
// gets a decision log; a future GORM impl can reuse the auth DB.
type DecisionStore interface {
	Record(ctx context.Context, r DecisionRecord) error
	List(ctx context.Context, q DecisionListQuery) ([]DecisionRecord, error)
	Count(ctx context.Context) (int, error)
	Close() error
}

// DecisionListQuery filters the decision log. Empty fields match all.
// Limit ≤ 0 picks a default cap.
type DecisionListQuery struct {
	CorrelationID string
	UserID        string
	RouterModel   string
	Source        string
	Limit         int
}

// NewMemoryDecisionStore returns a ring-buffer DecisionStore. capacity
// ≤ 0 picks 5_000 — same order of magnitude as PIIEvents but smaller
// because routing decisions correlate one-to-one with usage records;
// the existing UsageRecord log carries the bulk.
func NewMemoryDecisionStore(capacity int) DecisionStore {
	if capacity <= 0 {
		capacity = 5_000
	}
	return &memoryDecisionStore{
		ring: make([]DecisionRecord, capacity),
		cap:  capacity,
	}
}

type memoryDecisionStore struct {
	mu     sync.RWMutex
	ring   []DecisionRecord
	cap    int
	cursor int
	full   bool
}

func (s *memoryDecisionStore) Record(_ context.Context, r DecisionRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ring[s.cursor] = r
	s.cursor++
	if s.cursor == s.cap {
		s.cursor = 0
		s.full = true
	}
	return nil
}

func (s *memoryDecisionStore) List(_ context.Context, q DecisionListQuery) ([]DecisionRecord, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 1000
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]DecisionRecord, 0, limit)
	scan := func(r DecisionRecord) bool {
		if r.ID == "" {
			return false
		}
		if q.CorrelationID != "" && r.CorrelationID != q.CorrelationID {
			return false
		}
		if q.UserID != "" && r.UserID != q.UserID {
			return false
		}
		if q.RouterModel != "" && r.RouterModel != q.RouterModel {
			return false
		}
		if q.Source != "" {
			// Empty source on the row is treated as SourceChat for back-
			// compat with rows written before the field existed.
			rowSource := r.Source
			if rowSource == "" {
				rowSource = SourceChat
			}
			if rowSource != q.Source {
				return false
			}
		}
		out = append(out, r)
		return len(out) >= limit
	}
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

func (s *memoryDecisionStore) Count(_ context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.full {
		return s.cap, nil
	}
	return s.cursor, nil
}

func (s *memoryDecisionStore) Close() error { return nil }
