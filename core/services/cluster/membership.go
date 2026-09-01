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

// Start registers this replica and begins heartbeating and sweeping. The first
// registration is synchronous and its failure is returned: a replica whose
// address never reaches the table is invisible to its peers, and starting
// anyway would hide that behind a background log line.
func (m *Membership) Start(ctx context.Context) error {
	if err := m.reg.Register(ctx, m.id, m.addr, m.version); err != nil {
		return err
	}
	xlog.Info("Cluster instance registered", "id", m.id, "addr", m.addr)
	go m.loop(ctx)
	return nil
}

// Stop ends the loop and waits for it. Safe to call more than once.
func (m *Membership) Stop() {
	if m == nil {
		return
	}
	m.stopOnce.Do(func() { close(m.stop) })
	<-m.done
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
		xlog.Warn("Cluster instance row was reaped, re-registering", "id", m.id)
		if err := m.reg.Register(ctx, m.id, m.addr, m.version); err != nil {
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
// connected to it. The stall is recovered by the re-register in tick instead.
//
// PostgreSQL only, like Live: distributed mode requires it, and the interval
// arithmetic is measured on the database's clock because liveness is compared
// across replicas.
func (r *Registry) ReapStale(ctx context.Context, self string, within time.Duration) (instances int64, connections int64, err error) {
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Where("id <> ? AND last_seen <= now() - make_interval(secs => ?)", self, within.Seconds()).
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
