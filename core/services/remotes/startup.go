package remotes

import (
	"context"

	"github.com/mudler/xlog"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/advisorylock"
)

// StartupRefresh refreshes every remote once. A remote that fails is
// recorded in its status; nothing here blocks or fails the caller, so run it
// in a goroutine after startup.
//
// With a database (distributed mode) only one frontend runs a pass at a time:
// a frontend that finds the lock taken does nothing, because the pass that
// holds it covers every remote and the result is in the shared store.
// With a nil database (standalone) it runs directly.
func StartupRefresh(ctx context.Context, svc Service, db *gorm.DB) {
	if svc == nil {
		return
	}
	run := func() error {
		if failed := RefreshAll(ctx, svc); failed > 0 {
			xlog.Warn("remotes: the startup refresh did not finish for some remotes", "failed", failed)
		}
		return nil
	}
	if db == nil {
		_ = run()
		return
	}
	if _, err := advisorylock.TryWithLockCtx(ctx, db, advisorylock.KeyRemotesRefresh, run); err != nil {
		xlog.Warn("remotes: could not take the refresh lock", "error", err)
	}
}
