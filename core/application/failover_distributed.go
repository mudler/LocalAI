package application

import (
	"context"

	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/failover"
	"github.com/mudler/LocalAI/core/services/failover/distsync"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/xlog"
)

// failoverLeaderGate elects the frontend that probes failover targets and
// decides chains, so N frontends do not probe every target N times or
// disagree on which target is active.
//
// Leadership is sticky: the leader keeps the lock across ticks and loses it
// only when its database session dies or it shuts down. A lock taken per tick
// would pass between frontends on almost every tick, and each change of
// leader redelivers the warm set and republishes all state. A lock error
// counts as "not leader": skipping one tick is safe, two leaders are not.
func failoverLeaderGate(l *advisorylock.HeldLock) failover.LeaderGate {
	return func(ctx context.Context, fn func()) bool {
		if !l.Held() || !l.Verify(ctx) {
			ok, err := l.TryAcquire(ctx)
			if err != nil {
				xlog.Warn("failover: could not take the prober leader lock", "error", err)
				return false
			}
			if !ok {
				return false
			}
		}
		fn()
		return true
	}
}

// failoverPinnedResolver adds warm failover targets to the config-pinned
// models, so the SmartRouter and ReplicaReconciler keep them loaded on the
// workers the same way the local watchdog keeps them loaded in standalone mode.
type failoverPinnedResolver struct {
	base nodes.PinnedModelResolver
	fm   *failover.Manager
}

func (r *failoverPinnedResolver) GetPinnedModelNames() []string {
	var pinned []string
	if r.base != nil {
		pinned = r.base.GetPinnedModelNames()
	}
	return failover.MergePinned(pinned, r.fm.WarmTargets())
}

// startFailoverDistributed makes the failover manager cluster-aware: one
// frontend (the advisory-lock holder) probes and decides, and pins, target
// health and chain state are shared over NATS. A sync failure is logged and
// the manager keeps probing on its own, as in standalone mode.
func (a *Application) startFailoverDistributed(ctx context.Context) {
	db := a.distributedDB()
	pins, err := distsync.NewPinStore(db)
	if err != nil {
		xlog.Error("failover: pins will not persist, could not prepare the pin store", "error", err)
		pins = nil // distsync.New treats a nil store as "no durable pins"
	}
	s, err := distsync.New(ctx, a.distributed.Nats, pins, a.failoverManager)
	if err != nil {
		xlog.Error("failover: state will not be shared between frontends", "error", err)
		return
	}
	a.failoverSync = s
	// Gate only once state is shared: a follower learns target health and
	// chain decisions solely from the leader's publishes, so gating without
	// the sync would leave every follower's chains frozen.
	a.failoverLock = advisorylock.NewHeldLock(db, advisorylock.KeyFailoverProber)
	a.failoverManager.SetLeaderGate(failoverLeaderGate(a.failoverLock))
}

// stopFailoverDistributed detaches the manager from the sync before closing
// it, so nothing publishes into closed maps, and gives up leadership at once
// instead of making another frontend wait for this session to time out.
func (a *Application) stopFailoverDistributed() {
	if a.failoverSync != nil {
		a.failoverManager.SetStateSync(nil)
		if err := a.failoverSync.Close(); err != nil {
			xlog.Warn("failover: closing state sync", "error", err)
		}
	}
	if a.failoverLock != nil {
		// Close, not Release: Run may still be ticking and would take the
		// lock straight back.
		a.failoverLock.Close()
	}
}
