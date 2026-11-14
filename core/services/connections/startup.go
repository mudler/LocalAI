package connections

import (
	"context"

	"github.com/mudler/xlog"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/advisorylock"
)

// StartupRefresh refreshes every connection once. A remote that fails is
// recorded in its status; nothing here blocks or fails the caller, so run it
// in a goroutine after startup.
//
// With a database (distributed mode) only one frontend runs a pass at a time:
// a frontend that finds the lock taken does nothing, because the pass that
// holds it covers every connection and the result is in the shared store.
// With a nil database (standalone) it runs directly.
func StartupRefresh(ctx context.Context, svc Service, db *gorm.DB) {
	if svc == nil {
		return
	}
	run := func() error {
		if failed := RefreshAll(ctx, svc); failed > 0 {
			xlog.Warn("connections: the startup refresh did not finish for some connections", "failed", failed)
		}
		return nil
	}
	if db == nil {
		_ = run()
		return
	}
	if _, err := advisorylock.TryWithLockCtx(ctx, db, advisorylock.KeyConnectionsRefresh, run); err != nil {
		xlog.Warn("connections: could not take the refresh lock", "error", err)
	}
}
