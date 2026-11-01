// SPDX-License-Identifier: MIT
package application

import (
	"context"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/services/advisorylock"
)

// studioLeader returns the function with which a chat background host (the
// triggers, the connectors) asks whether this frontend leads. The lock is kept
// until Shutdown closes it, so a follower can take over at once and does not
// wait for this session to time out on the database.
func (a *Application) studioLeader(key int64, name string) func(context.Context) bool {
	lock := advisorylock.NewHeldLock(a.authDB, key)
	a.studioLocks = append(a.studioLocks, lock)
	return func(ctx context.Context) bool {
		if lock.Held() {
			return lock.Verify(ctx)
		}
		ok, err := lock.TryAcquire(ctx)
		if err != nil {
			xlog.Warn(name+": could not try the leader lock", "error", err)
		}
		return ok
	}
}

// closeStudioLocks gives up the leader locks of the chat hosts for good. It
// runs after the hosts are closed, because a host that is still ticking would
// take a released lock back.
func (a *Application) closeStudioLocks() {
	for _, l := range a.studioLocks {
		l.Close()
	}
	a.studioLocks = nil
}
