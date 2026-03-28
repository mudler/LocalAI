package advisorylock

import (
	"context"
	"time"

	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// RunLeaderLoop runs fn on a fixed interval, guarded by a PostgreSQL advisory lock.
// Only one instance across the cluster executes fn at a time. If the lock is not
// acquired (another instance holds it), the tick is skipped.
// The loop stops when ctx is cancelled.
func RunLeaderLoop(ctx context.Context, db *gorm.DB, lockKey int64, interval time.Duration, fn func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, err := TryWithLock(db, lockKey, func() error {
				fn()
				return nil
			})
			if err != nil {
				xlog.Error("Leader loop advisory lock error", "key", lockKey, "error", err)
			}
		}
	}
}
