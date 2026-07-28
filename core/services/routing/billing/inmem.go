package billing

import (
	"context"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
)

// memoryBackend keeps the most recent N records in a ring buffer. It is
// the no-auth, no-DB fallback: a single user running LocalAI on a
// laptop still gets live aggregation against this buffer until the
// process exits. Records are not durable.
//
// Aggregation is computed by linear scan — fine because the ring is
// bounded (default 50_000 records) and aggregation is rare (UI dashboard
// poll, MCP tool calls). If the working set grows beyond what scan can
// service in <100ms, the operator should enable auth+DB.
type memoryBackend struct {
	mu     sync.RWMutex
	ring   []*auth.UsageRecord
	cap    int
	cursor int // next write position
	full   bool
}

// NewMemoryBackend returns a StatsBackend backed by an in-process ring
// buffer. capacity ≤ 0 uses 50_000.
func NewMemoryBackend(capacity int) StatsBackend {
	if capacity <= 0 {
		capacity = 50_000
	}
	return &memoryBackend{
		ring: make([]*auth.UsageRecord, capacity),
		cap:  capacity,
	}
}

func (b *memoryBackend) Record(_ context.Context, r *auth.UsageRecord) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ring[b.cursor] = r
	b.cursor++
	if b.cursor == b.cap {
		b.cursor = 0
		b.full = true
	}
	return nil
}

func (b *memoryBackend) Aggregate(_ context.Context, q AggregateQuery) ([]auth.UsageBucket, error) {
	since := periodStart(q.Period)
	bucketWidth := bucketWidthFor(q.Period)
	dateFmt := bucketFormatFor(q.Period)

	type aggKey struct {
		bucket   string
		model    string
		userID   string
		userName string
	}
	agg := make(map[aggKey]*auth.UsageBucket)

	b.mu.RLock()
	defer b.mu.RUnlock()

	scan := func(r *auth.UsageRecord) {
		if r == nil {
			return
		}
		if !since.IsZero() && r.CreatedAt.Before(since) {
			return
		}
		if q.UserID != "" && r.UserID != q.UserID {
			return
		}
		bucketTime := r.CreatedAt.Truncate(bucketWidth)
		key := aggKey{
			bucket:   bucketTime.Format(dateFmt),
			model:    r.Model,
			userID:   r.UserID,
			userName: r.UserName,
		}
		entry, ok := agg[key]
		if !ok {
			entry = &auth.UsageBucket{
				Bucket:   key.bucket,
				Model:    key.model,
				UserID:   key.userID,
				UserName: key.userName,
			}
			agg[key] = entry
		}
		entry.PromptTokens += r.PromptTokens
		entry.CompletionTokens += r.CompletionTokens
		entry.TotalTokens += r.TotalTokens
		entry.RequestCount++
	}

	if b.full {
		for _, r := range b.ring {
			scan(r)
		}
	} else {
		for i := 0; i < b.cursor; i++ {
			scan(b.ring[i])
		}
	}

	out := make([]auth.UsageBucket, 0, len(agg))
	for _, v := range agg {
		out = append(out, *v)
	}
	return out, nil
}

func (b *memoryBackend) Close() error { return nil }

// periodStart returns the lower bound of the time window for the
// given period. Mirrors auth.periodToWindow but without GORM
// dialector concerns.
func periodStart(period string) time.Time {
	now := time.Now()
	switch period {
	case "day":
		return now.Add(-24 * time.Hour)
	case "week":
		return now.Add(-7 * 24 * time.Hour)
	case "all":
		return time.Time{}
	default: // "month"
		return now.Add(-30 * 24 * time.Hour)
	}
}

func bucketWidthFor(period string) time.Duration {
	switch period {
	case "day":
		return time.Hour
	case "all":
		return 30 * 24 * time.Hour
	default: // week, month
		return 24 * time.Hour
	}
}

func bucketFormatFor(period string) string {
	switch period {
	case "day":
		return "2006-01-02 15:00"
	case "all":
		return "2006-01"
	default:
		return "2006-01-02"
	}
}
