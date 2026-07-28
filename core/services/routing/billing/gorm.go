package billing

import (
	"context"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// gormBackend writes UsageRecord rows to a GORM-backed database (the
// existing auth DB when --auth is enabled). It batches inserts every
// flushInterval to amortize round-trips; pre-routing-module middleware
// did the same with a private batcher — we keep the same cadence.
type gormBackend struct {
	db            *gorm.DB
	flushInterval time.Duration
	maxPending    int

	mu      sync.Mutex
	pending []*auth.UsageRecord

	stopCh chan struct{}
	doneCh chan struct{}
}

// NewGormBackend constructs a StatsBackend that persists records to db.
// The returned backend launches a background flush goroutine; call
// Close() to stop it. flushInterval ≤ 0 picks the prior 5s default;
// maxPending ≤ 0 picks 5000.
func NewGormBackend(db *gorm.DB, flushInterval time.Duration, maxPending int) StatsBackend {
	if flushInterval <= 0 {
		flushInterval = 5 * time.Second
	}
	if maxPending <= 0 {
		maxPending = 5000
	}
	b := &gormBackend{
		db:            db,
		flushInterval: flushInterval,
		maxPending:    maxPending,
		stopCh:        make(chan struct{}),
		doneCh:        make(chan struct{}),
	}
	go b.run()
	return b
}

func (b *gormBackend) Record(_ context.Context, r *auth.UsageRecord) error {
	b.mu.Lock()
	b.pending = append(b.pending, r)
	b.mu.Unlock()
	return nil
}

func (b *gormBackend) Aggregate(_ context.Context, q AggregateQuery) ([]auth.UsageBucket, error) {
	if q.UserID == "" {
		return auth.GetAllUsage(b.db, q.Period, "")
	}
	return auth.GetUserUsage(b.db, q.UserID, q.Period)
}

func (b *gormBackend) Close() error {
	select {
	case <-b.stopCh:
		// already stopped
	default:
		close(b.stopCh)
	}
	<-b.doneCh
	return nil
}

func (b *gormBackend) run() {
	defer close(b.doneCh)
	ticker := time.NewTicker(b.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-b.stopCh:
			b.flush()
			return
		case <-ticker.C:
			b.flush()
		}
	}
}

func (b *gormBackend) flush() {
	b.mu.Lock()
	batch := b.pending
	b.pending = nil
	b.mu.Unlock()

	if len(batch) == 0 {
		return
	}

	if err := b.db.Create(&batch).Error; err != nil {
		xlog.Error("failed to flush usage batch", "count", len(batch), "error", err)
		// Re-queue with a cap to avoid unbounded growth on persistent DB
		// failure (matches the prior behavior in core/http/middleware/usage.go).
		b.mu.Lock()
		if len(b.pending) < b.maxPending {
			b.pending = append(batch, b.pending...)
		}
		b.mu.Unlock()
	}
}
