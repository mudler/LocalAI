// SPDX-License-Identifier: MIT

package cluster

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

const (
	// InstanceHeartbeat is how often a replica refreshes its own row.
	InstanceHeartbeat = 5 * time.Second

	// InstanceLiveness is how long a replica may go without a heartbeat before
	// its peers treat it as gone: six consecutive misses.
	//
	// The window is generous on purpose. Declaring a replica dead deletes the
	// connection rows it owned, and a worker whose row is deleted while its
	// owner is merely slow has to be re-homed for nothing. The cost of waiting
	// is bounded and symmetric: traffic for that worker is retried, not lost.
	InstanceLiveness = 30 * time.Second

	// deregisterTimeout bounds the deregistration Stop performs. Shutdown is
	// not the place to wait on a database.
	deregisterTimeout = 5 * time.Second
)

// Membership publishes this replica's address and keeps the instances table
// free of replicas that have stopped answering.
//
// It is the only writer of this replica's row and the only sweeper of anyone
// else's, which is what keeps one fact on one clock: whether a replica is
// alive is answered by its last_seen and by nothing else.
type Membership struct {
	reg     *Registry
	id      string
	addr    string
	version string

	interval time.Duration
	liveness time.Duration

	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once

	// mu guards started, which tells Stop whether there is a loop to join, and
	// tunnels, which SetTunnels may write while the loop is already reading it.
	mu      sync.Mutex
	started bool
	tunnels *TunnelRegistry
}

// NewMembership returns the membership loop for one replica. The address is
// what peers will dial, so it must be reachable from another host, not the
// address this process binds.
func NewMembership(reg *Registry, id, addr, version string) *Membership {
	return &Membership{
		reg:      reg,
		id:       id,
		addr:     addr,
		version:  version,
		interval: InstanceHeartbeat,
		liveness: InstanceLiveness,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// SetTunnels gives the loop the registry holding this replica's worker tunnels,
// so it can re-claim them after its rows have been swept. A Membership without
// one still heartbeats and sweeps; it simply has nothing to re-claim, which is
// the single-binary case and the case of a replica that accepts no tunnels.
//
// It is a setter rather than a constructor argument because the tunnel registry
// is what the tunnel endpoint is built on, and that is wired after membership
// is already running.
//
// Safe on a nil receiver, like Stop. This package deliberately produces a nil
// *Membership (core/application/distributed.go leaves it nil when no
// peer-reachable address can be derived), so a setter that panicked on one
// would be a trap for the next caller rather than an impossibility.
func (m *Membership) SetTunnels(t *TunnelRegistry) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tunnels = t
}

// Start registers this replica and begins heartbeating and sweeping. The first
// registration is synchronous and its failure is returned: a replica whose
// address never reaches the table is invisible to its peers, and starting
// anyway would hide that behind a background log line.
func (m *Membership) Start(ctx context.Context) error {
	if err := m.reg.Register(ctx, m.id, m.addr, m.version); err != nil {
		return err
	}
	xlog.Info("Cluster instance registered", "id", m.id, "addr", m.addr)
	m.mu.Lock()
	m.started = true
	m.mu.Unlock()
	go m.loop(ctx)
	return nil
}

// Stop ends the loop, waits for it, and removes this replica's row.
//
// Deregistering is what makes a rolling restart quick for everyone else: a
// replica that just closes its sockets is indistinguishable from one that
// crashed, so its peers keep dialling it for the whole liveness window. It is
// best-effort by nature (a killed process never gets here), which is why the
// sweeper still exists.
//
// Safe to call more than once, and on a Membership that was never started.
func (m *Membership) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	started := m.started
	m.mu.Unlock()
	if started {
		m.stopOnce.Do(func() { close(m.stop) })
		// Only a started Membership ever closes done. Waiting on one that was
		// never started, or whose Start failed, would block forever.
		<-m.done
	}

	// Deliberately NOT the context Start was given: that one is the
	// application's, and by the time anything calls Stop it has usually been
	// cancelled already, so deregistering on it would fail every time. The
	// bound is here instead, because shutdown must not hang on a database that
	// went away before the process using it.
	ctx, cancel := context.WithTimeout(context.Background(), deregisterTimeout)
	defer cancel()
	if err := m.reg.Deregister(ctx, m.id); err != nil {
		xlog.Warn("Deregistering this replica failed; peers will drop it when its heartbeat ages out",
			"id", m.id, "within", m.liveness, "error", err)
		return
	}
	xlog.Info("Cluster instance deregistered", "id", m.id)
}

func (m *Membership) loop(ctx context.Context) {
	defer close(m.done)

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.tick(ctx)
		}
	}
}

// tick refreshes this replica's row and sweeps the dead.
//
// Every replica sweeps, rather than one elected sweeper. The deletes are
// idempotent and cheap, and an elected sweeper is one more thing that has to be
// alive for the cluster to notice that something is not.
func (m *Membership) tick(ctx context.Context) {
	err := m.reg.Heartbeat(ctx, m.id)
	if errors.Is(err, ErrInstanceNotFound) {
		// Another replica swept this row while this process was stalled long
		// enough to look dead. Re-register rather than heartbeat: a heartbeat
		// carries no address, so the row has to be rebuilt from scratch.
		//
		// Register rebuilds the instance row ONLY. The sweep that removed it
		// removed the connections this replica owned in the same transaction,
		// so the tunnels still held here have to be claimed again or this
		// replica serves workers that, as far as every other replica can see,
		// are connected nowhere.
		xlog.Warn("Cluster instance row was reaped, re-registering", "id", m.id)
		if err := m.reg.Register(ctx, m.id, m.addr, m.version); err == nil {
			m.reclaimTunnels(ctx)
		} else {
			// Re-claiming is skipped and only re-claiming: a claim written now
			// would name an instance row that does not exist, and the very next
			// sweep deletes it as an orphan. The sweep below still runs, because
			// what it removes is other replicas, and this replica failing to
			// rebuild its own row is no reason to stop reaping theirs.
			xlog.Error("Re-registering cluster instance failed", "id", m.id, "error", err)
		}
	} else if err != nil {
		xlog.Warn("Cluster instance heartbeat failed", "id", m.id, "error", err)
	}

	instances, connections, err := m.reg.ReapStale(ctx, m.id, m.liveness)
	if err != nil {
		xlog.Warn("Reaping stale cluster instances failed", "error", err)
		return
	}
	if instances > 0 || connections > 0 {
		xlog.Info("Reaped cluster state left by dead replicas", "instances", instances, "connections", connections)
	}
}

// reclaimTunnels re-writes a claim for every worker tunnel this replica still
// holds, after the sweep that deleted them. It is separate from tick only so
// the lock around the registry reference is not held across the database work.
func (m *Membership) reclaimTunnels(ctx context.Context) {
	m.mu.Lock()
	tunnels := m.tunnels
	m.mu.Unlock()
	if tunnels == nil {
		return
	}

	reclaimed, err := tunnels.Reclaim(ctx)
	if err != nil {
		// Logged rather than returned, and the loop keeps running: the next
		// heartbeat fails the same way if the row is still missing, so the
		// re-claim is retried. A worker whose claim never lands is reachable
		// only through the replica it is connected to, which is this one.
		xlog.Error("Re-claiming worker tunnels after this replica was reaped failed", "id", m.id, "error", err)
	}
	if reclaimed > 0 {
		xlog.Info("Re-claimed worker tunnels after this replica was reaped", "id", m.id, "tunnels", reclaimed)
	}
}

// Deregister removes one replica and the connections it owned.
//
// It deletes both, in one transaction, for the same reason ReapStale does: a
// replica that is gone owns nothing, and leaving its connection rows behind
// would point every reader at an owner that no longer exists. This is the
// announced form of what the sweeper does by inference, and the two must not
// disagree about what "gone" removes.
//
// Instances first, then connections, which is deliberate and is the same order
// ReapStale takes. The two paths run concurrently in the ordinary case, a
// replica shutting down while a peer is sweeping it, and each locks the same
// two tables; opposite orders would let each hold the row the other is waiting
// for. PostgreSQL breaks such a cycle by aborting one side, so the cost is a
// failed shutdown rather than lost data, but an inversion that costs nothing to
// remove should not be left in.
func (r *Registry) Deregister(ctx context.Context, id string) error {
	if err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// No RowsAffected check: deregistering a row another replica already
		// swept is the normal outcome of a slow shutdown, not an error.
		if err := tx.Where("id = ?", id).Delete(&Instance{}).Error; err != nil {
			return fmt.Errorf("deleting instance %q: %w", id, err)
		}
		if err := tx.Where("owner_instance_id = ?", id).Delete(&NodeConnection{}).Error; err != nil {
			return fmt.Errorf("deleting connections owned by %q: %w", id, err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("deregistering instance %q: %w", id, err)
	}
	return nil
}

// ReapStale deletes the replicas that have not heartbeated within the liveness
// window, and the connection rows whose owner is no longer among the survivors.
//
// The two deletes are one sweeper on purpose. A connection row is only ever
// orphaned by its owner dying, so the moment that is decided is the moment to
// clean up after it; a second sweeper with its own schedule would either lag
// this one or race it, and would need its own answer to "is that replica
// alive", which is the one fact this table already owns.
//
// self is never reaped. This process may fail to heartbeat for longer than the
// window (a long stall, a database blip) and still be serving: deleting its own
// row would then delete the connections of workers that are, at that moment,
// connected to it.
//
// That protection is one-sided. A replica that stalls long enough is reaped BY
// ANOTHER replica, taking its connection rows with it, and Register rebuilds
// the instance row and nothing else. What restores the rest is the re-claim in
// tick, which writes a fresh claim for every tunnel the tunnel registry still
// holds; until it runs, this replica holds sockets the table records nobody
// holding.
//
// PostgreSQL only, like Live: distributed mode requires it, and the interval
// arithmetic is measured on the database's clock because liveness is compared
// across replicas.
//
// Instances are deleted before connections, and Deregister takes the same order
// on purpose, so the two paths cannot deadlock against each other. Here the
// order is also forced: the connection delete asks which instance rows survived,
// so it has to run second.
func (r *Registry) ReapStale(ctx context.Context, self string, within time.Duration) (instances int64, connections int64, err error) {
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Negated rather than spelled as its own comparison: "stale" has to be
		// exactly "not live", including how each treats a row whose last_seen
		// is NULL, and a hand-written complement is a second definition that
		// only looks like the first.
		res := tx.Where("id <> ? AND NOT ("+instanceIsLive+")", self, within.Seconds()).
			Delete(&Instance{})
		if res.Error != nil {
			return fmt.Errorf("deleting stale instances: %w", res.Error)
		}
		instances = res.RowsAffected

		// Whatever survived the delete above is the live set, so this needs no
		// second liveness rule and cannot disagree with the first one.
		res = tx.Where("owner_instance_id NOT IN (SELECT id FROM instances)").
			Delete(&NodeConnection{})
		if res.Error != nil {
			return fmt.Errorf("deleting orphaned node connections: %w", res.Error)
		}
		connections = res.RowsAffected
		return nil
	})
	if err != nil {
		return 0, 0, fmt.Errorf("reaping stale cluster state: %w", err)
	}
	return instances, connections, nil
}
