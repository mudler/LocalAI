package distsync

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/mudler/LocalAI/core/services/failover"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/syncstate"
	"github.com/mudler/xlog"
)

// compile-time assertions.
var (
	_ syncstate.Store[string, PinRecord] = (*PinStore)(nil)
	_ failover.StateSync                 = (*Sync)(nil)
)

// pinReconcileInterval is how often the pins map re-reads the DB.
const pinReconcileInterval = 30 * time.Second

// Sync is a failover.StateSync backed by three syncstate.SyncedMaps:
//
//   - failover.pins keeps the durable source of truth: Store-backed (when a
//     PinStore is given) so a late joiner hydrates every pin from the DB on
//     Start, not just from whatever peers happen to broadcast afterwards,
//     and re-reads it every pinReconcileInterval to repair a missed delta.
//   - failover.targets and failover.chains are ephemeral live-health state,
//     NATS-only with no Store and no Reconcile: with neither set, a Reconcile
//     tick's hydrate is a no-op (nothing durable to pull from), so it could
//     never help a late joiner catch up anyway. A late joiner instead catches
//     up from the leader's periodic Republish (see failover.Manager.Republish).
type Sync struct {
	pins    *syncstate.SyncedMap[string, PinRecord]
	targets *syncstate.SyncedMap[string, failover.TargetSnapshot]
	chains  *syncstate.SyncedMap[string, failover.ChainSnapshot]
}

// New builds and starts the three maps, then attaches the result to m via
// SetStateSync so any already-durable pins hydrate onto m immediately.
func New(ctx context.Context, nats messaging.Broadcaster, pins syncstate.Store[string, PinRecord], m *failover.Manager) (*Sync, error) {
	s := &Sync{}

	// pins is already typed as the Store interface (the brief fixes this
	// signature), so a caller holding a nil *PinStore (e.g. standalone mode,
	// no DB configured) and passing it straight through boxes it into a
	// non-nil interface wrapping a nil pointer - "pins != nil" alone would
	// not catch that, and the SyncedMap would then try to hydrate/write
	// through a nil *gorm.DB. isNilStore catches both that and a literal nil
	// argument, the same defense finetune/service.go gets for free by taking
	// a concrete *distributed.FineTuneStore and nil-checking before boxing it.
	var pinStore syncstate.Store[string, PinRecord]
	if !isNilStore(pins) {
		pinStore = pins
	}

	// A delta dropped without a reconnect would leave this map stale until
	// the next reconnect; re-reading the DB repairs it, and the manager's
	// periodic ReconcilePins carries the repair into the chains.
	var reconcile time.Duration
	if pinStore != nil {
		reconcile = pinReconcileInterval
	}
	s.pins = syncstate.New(syncstate.Config[string, PinRecord]{
		Name:      "failover.pins",
		Key:       func(p PinRecord) string { return p.Chain },
		Nats:      nats,
		Store:     pinStore,
		Reconcile: reconcile,
		OnApply: func(op string, chain string, v PinRecord) {
			if op == "delete" {
				m.ApplyPin(chain, "")
				return
			}
			m.ApplyPin(chain, v.Target)
		},
	})
	if err := s.pins.Start(ctx); err != nil {
		return nil, fmt.Errorf("distsync: starting pins map: %w", err)
	}

	s.targets = syncstate.New(syncstate.Config[string, failover.TargetSnapshot]{
		Name: "failover.targets",
		Key:  func(t failover.TargetSnapshot) string { return t.Target },
		Nats: nats,
		OnApply: func(_ string, _ string, v failover.TargetSnapshot) {
			m.ApplyTarget(v)
		},
	})
	if err := s.targets.Start(ctx); err != nil {
		_ = s.pins.Close()
		return nil, fmt.Errorf("distsync: starting targets map: %w", err)
	}

	s.chains = syncstate.New(syncstate.Config[string, failover.ChainSnapshot]{
		Name: "failover.chains",
		Key:  func(c failover.ChainSnapshot) string { return c.Chain },
		Nats: nats,
		OnApply: func(_ string, _ string, v failover.ChainSnapshot) {
			m.ApplyChain(v)
		},
	})
	if err := s.chains.Start(ctx); err != nil {
		_ = s.targets.Close()
		_ = s.pins.Close()
		return nil, fmt.Errorf("distsync: starting chains map: %w", err)
	}

	m.SetStateSync(s)
	// The pins map re-hydrates from the DB on reconnect without OnApply, so
	// hand the manager the result. Registered after the map's own callback,
	// which runs first.
	if r, ok := nats.(interface{ OnReconnect(func()) }); ok {
		r.OnReconnect(m.ReconcilePins)
	}
	return s, nil
}

// isNilStore reports whether pins is nil - either a literal nil argument, or
// the classic Go footgun of a non-nil interface value wrapping a nil
// pointer (e.g. a nil *PinStore passed in directly). Both must disable the
// durable Store the same way, so syncstate hydrates from nothing rather than
// panicking on a nil *gorm.DB the first time it dereferences it.
func isNilStore(pins syncstate.Store[string, PinRecord]) bool {
	if pins == nil {
		return true
	}
	v := reflect.ValueOf(pins)
	return v.Kind() == reflect.Ptr && v.IsNil()
}

// Close releases all three maps' subscriptions and background workers.
func (s *Sync) Close() error {
	return errors.Join(s.chains.Close(), s.targets.Close(), s.pins.Close())
}

// PublishTarget shares a target health transition. StateSync's methods
// return no error to the manager, and a publish must never block it (the
// manager calls this outside its lock precisely so a synchronous NATS echo
// is safe) - so a failure here is logged and dropped; a missed publish
// self-heals on the leader's next Republish.
func (s *Sync) PublishTarget(t failover.TargetSnapshot) {
	if err := s.targets.Set(context.Background(), t); err != nil {
		xlog.Warn("distsync: publishing target state failed", "target", t.Target, "error", err)
	}
}

// PublishChain shares the leader's decision for a chain.
func (s *Sync) PublishChain(c failover.ChainSnapshot) {
	if err := s.chains.Set(context.Background(), c); err != nil {
		xlog.Warn("distsync: publishing chain state failed", "chain", c.Chain, "error", err)
	}
}

// SetPin durably persists and broadcasts a pin.
func (s *Sync) SetPin(chain, target string) error {
	return s.pins.Set(context.Background(), PinRecord{Chain: chain, Target: target, UpdatedAt: time.Now()})
}

// ClearPin durably removes and broadcasts a pin's removal.
func (s *Sync) ClearPin(chain string) error {
	return s.pins.Delete(context.Background(), chain)
}

// Pins returns every known pin (chain -> target), for Manager.SetStateSync's
// hydrate-on-attach.
func (s *Sync) Pins() map[string]string {
	out := make(map[string]string)
	for chain, rec := range s.pins.Snapshot() {
		out[chain] = rec.Target
	}
	return out
}
