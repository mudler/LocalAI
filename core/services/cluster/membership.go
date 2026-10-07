package cluster

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

const (
	// InstanceHeartbeat is how often a replica refreshes its own row.
	InstanceHeartbeat = 5 * time.Second

	// InstanceLiveness is how long a replica may go without a heartbeat before
	// its peers treat it as gone: six heartbeats.
	//
	// The window is long on purpose. When a replica is declared dead, a
	// departure is recorded for every connection row it owned, and a worker
	// that is recorded as departed while its owner is only slow must be
	// re-homed for nothing. Waiting costs little: traffic for that worker is
	// retried and not lost.
	InstanceLiveness = 30 * time.Second

	// deregisterTimeout bounds the deregistration that Stop does. Shutdown must
	// not wait for a database for long.
	deregisterTimeout = 5 * time.Second

	// DepartedRetention is the lowest time that a connection row outlives the
	// tunnel it recorded before the sweep deletes it. DepartedRetentionFor is
	// what the sweep uses.
	//
	// It is a multiple of the liveness window and not the window itself. The
	// row stays so that the age of a departure can be answered at all. If the
	// retention were as short as the window that a reader compares the age
	// with, the sweep could delete the row under the reader, and a worker that
	// is dialling again would look like a worker that was never here.
	DepartedRetention = 10 * InstanceLiveness

	// departedRetentionGraceFactor is how many reconnect graces a departure is
	// kept for when the grace is the larger of the two values.
	departedRetentionGraceFactor = 5
)

// DepartedRetentionFor returns how long a departure must be kept, for the
// reconnect grace that the readers of the departure compare it with.
//
// The retention is derived and not fixed because two different people set the
// two windows. If an operator raises the grace above a fixed retention, the
// sweep deletes departures before the grace has passed. Presence then answers
// unknown for that worker for ever, and nothing reaps its rows.
func DepartedRetentionFor(grace time.Duration) time.Duration {
	// Check before the multiplication. A grace of more than about 58 years
	// overflows the nanoseconds of a Duration, and the product is then negative
	// or small, which brings back the defect that this function removes.
	if grace > time.Duration(math.MaxInt64)/departedRetentionGraceFactor {
		return time.Duration(math.MaxInt64)
	}
	if scaled := departedRetentionGraceFactor * grace; scaled > DepartedRetention {
		return scaled
	}
	return DepartedRetention
}

// Membership publishes this replica in the instances table and removes the
// replicas that have stopped answering.
//
// It is the only writer of the row of this replica and the only sweeper of the
// rows of the others. This keeps one fact on one clock: whether a replica is
// alive is answered by its last_seen and by nothing else.
type Membership struct {
	reg     *Registry
	id      string
	version string

	interval time.Duration
	liveness time.Duration

	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once

	// mu guards the fields below. started tells Stop whether there is a loop to
	// join. retention is how long a departure is kept before the sweep deletes
	// it, derived from the reconnect grace so that the sweep never outruns it.
	// The ready fields are what this replica last reported, so that a
	// registration after a sweep writes them back.
	mu          sync.Mutex
	started     bool
	retention   time.Duration
	readyEpoch  int64
	readyReason string
}

// NewMembership returns the membership loop for one replica.
func NewMembership(reg *Registry, id, version string) *Membership {
	return &Membership{
		reg:       reg,
		id:        id,
		version:   version,
		interval:  InstanceHeartbeat,
		liveness:  InstanceLiveness,
		retention: DepartedRetention,
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
}

// SetReconnectGrace tells the loop the window that Presence compares a
// departure with, so that the sweep keeps departures for longer than any reader
// needs them. Without it the sweep uses DepartedRetention, which is correct for
// every grace up to that value.
//
// It is safe on a nil receiver, like Stop.
func (m *Membership) SetReconnectGrace(grace time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.retention = DepartedRetentionFor(grace)
}

// ReportReady records in the instances table that this replica has built the
// carrier for the cluster carrier row at epoch, or, with a reason, that it
// could not. The loop writes the same values again whenever it has to register
// the replica again. The replica must be started.
func (m *Membership) ReportReady(ctx context.Context, epoch int64, reason string) error {
	if err := m.reg.ReportReady(ctx, m.id, epoch, reason); err != nil {
		return err
	}
	m.mu.Lock()
	m.readyEpoch, m.readyReason = epoch, reason
	m.mu.Unlock()
	return nil
}

// register writes the row of this replica with what it last reported.
func (m *Membership) register(ctx context.Context) error {
	m.mu.Lock()
	epoch, reason := m.readyEpoch, m.readyReason
	m.mu.Unlock()
	return m.reg.Register(ctx, m.id, m.version, epoch, reason)
}

// Start registers this replica, then heartbeats and sweeps in the background.
// The first registration is synchronous and its failure is returned: a replica
// that never reaches the table is invisible to its peers, and a background log
// line would hide that.
func (m *Membership) Start(ctx context.Context) error {
	if err := m.register(ctx); err != nil {
		return err
	}
	xlog.Info("Cluster instance registered", "id", m.id)
	m.mu.Lock()
	m.started = true
	m.mu.Unlock()
	go m.loop(ctx)
	return nil
}

// Stop ends the loop, waits for it, and removes the row of this replica.
//
// Deregistering makes a rolling restart quick for the others. A replica that
// only closes its sockets looks like one that crashed, and its peers go on
// dialling it for the whole liveness window. It cannot be more than best effort,
// because a killed process never gets here, so the sweep still exists.
//
// It is safe to call more than once, and on a Membership that was never started.
func (m *Membership) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	started := m.started
	m.mu.Unlock()
	if started {
		m.stopOnce.Do(func() { close(m.stop) })
		// Only a started Membership closes done. Waiting on one that never
		// started would block for ever.
		<-m.done
	}

	// This is not the context that Start received: that is the context of the
	// application, and it is usually cancelled by the time Stop runs.
	ctx, cancel := context.WithTimeout(context.Background(), deregisterTimeout)
	defer cancel()
	if err := m.reg.Deregister(ctx, m.id); err != nil {
		xlog.Warn("Deregistering this replica failed; peers drop it when its heartbeat ages out",
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

// tick refreshes the row of this replica and sweeps the dead ones.
//
// Every replica sweeps and there is no elected sweeper. The deletes are
// idempotent and cheap, and an elected sweeper is one more process that must be
// alive for the cluster to notice that another is not.
func (m *Membership) tick(ctx context.Context) {
	err := m.reg.Heartbeat(ctx, m.id)
	if errors.Is(err, ErrInstanceNotFound) {
		// Another replica swept this row while this process stalled for long
		// enough to look dead. Register again and do not only heartbeat: the
		// row has to be rebuilt.
		xlog.Warn("Cluster instance row was reaped, registering again", "id", m.id)
		if err := m.register(ctx); err != nil {
			xlog.Error("Registering the cluster instance again failed", "id", m.id, "error", err)
		}
	} else if err != nil {
		xlog.Warn("Cluster instance heartbeat failed", "id", m.id, "error", err)
	}

	instances, cleared, err := m.reg.ReapStale(ctx, m.id, m.liveness)
	if err != nil {
		xlog.Warn("Reaping stale cluster instances failed", "error", err)
		return
	}
	if instances > 0 || cleared > 0 {
		// Two counts because the sweep does two things: the instance rows are
		// deleted, and the connection rows stay and now record a departure.
		xlog.Info("Swept cluster state left by dead replicas",
			"instances_deleted", instances, "connections_departed", cleared)
	}

	// The sweep also owns the retention. A departure that nothing deletes is a
	// row for every worker that ever dialled this deployment.
	m.mu.Lock()
	retention := m.retention
	m.mu.Unlock()
	purged, err := m.reg.PurgeDepartedBefore(ctx, retention)
	if err != nil {
		xlog.Warn("Purging departed worker connections failed", "error", err)
		return
	}
	if purged > 0 {
		xlog.Info("Purged worker connections whose departure aged out", "connections", purged, "retention", retention)
	}
}

// Deregister removes one replica and records a departure for every connection
// that it owned.
//
// Both happen in one transaction, as in ReapStale. A replica that is gone owns
// nothing, and a connection row that still names it would send every reader to
// an owner that does not exist. This is the announced form of what the sweep
// does by inference, and the two must agree on what "gone" leaves behind.
//
// The connection rows are cleared and not deleted. A worker whose frontend shut
// down is about to dial the load balancer again, and without its row the
// seconds in between look like a worker that was never connected.
//
// The instances are changed first and the connections second. ReapStale takes
// the same order. The two run at the same time in the usual case, a replica
// shutting down while a peer sweeps it, and both lock the same two tables. With
// opposite orders each could wait for a row held by the other.
func (r *Registry) Deregister(ctx context.Context, id string) error {
	if err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// A row that another replica already swept is the normal result of a slow
		// shutdown and not an error, so RowsAffected is not checked.
		if err := tx.Where("id = ?", id).Delete(&Instance{}).Error; err != nil {
			return fmt.Errorf("deleting instance %q: %w", id, err)
		}
		// Held rows only. An empty id would match every departed row in the table
		// and reset the age of every departure.
		if err := tx.Model(&NodeConnection{}).
			Where("owner_instance_id = ? AND "+connectionIsHeld, id).
			Updates(departure()).Error; err != nil {
			return fmt.Errorf("recording departures for connections owned by %q: %w", id, err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("deregistering instance %q: %w", id, err)
	}
	return nil
}

// ReapStale deletes the replicas that have not heartbeated within the liveness
// window, and records a departure for every connection row whose owner is no
// longer among the survivors.
//
// It is one sweeper for both. A connection row is orphaned only when its owner
// dies, so the moment to clean up after it is the moment the death is decided.
// A second sweeper with its own schedule would lag behind this one or race it,
// and would need its own answer to "is that replica alive".
//
// The connection rows are cleared and not deleted, which is why the second
// result counts rows that were cleared. A worker whose owner died is dialling
// the load balancer again, and deleting its row would erase the departure that
// says how long ago that began.
//
// self is never reaped. This process can fail to heartbeat for longer than the
// window (a long stall, a database error) and still serve. Deleting its own row
// would mark as departed the workers that are connected to it at that moment.
// The protection goes one way: another replica can still reap a replica that
// stalls for long enough.
//
// It needs PostgreSQL, like Live, and the interval is measured on the clock of
// the database.
func (r *Registry) ReapStale(ctx context.Context, self string, within time.Duration) (instances int64, cleared int64, err error) {
	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// "Stale" is the negation of the live predicate and not a second
		// comparison, so both treat a row with a NULL last_seen the same way.
		res := tx.Where("id <> ? AND NOT ("+instanceIsLive+")", self, within.Seconds()).
			Delete(&Instance{})
		if res.Error != nil {
			return fmt.Errorf("deleting stale instances: %w", res.Error)
		}
		instances = res.RowsAffected

		// What survived the delete above is the live set. This needs no second
		// liveness rule, so it cannot disagree with the first.
		//
		// Held rows only. An empty owner is in no instance id, so a departed row
		// also matches the set difference, and clearing it again at every sweep
		// would move its departure forward and keep it from ever ageing out.
		res = tx.Model(&NodeConnection{}).
			Where("owner_instance_id NOT IN (SELECT id FROM instances) AND " + connectionIsHeld).
			Updates(departure())
		if res.Error != nil {
			return fmt.Errorf("recording departures for orphaned node connections: %w", res.Error)
		}
		cleared = res.RowsAffected
		return nil
	})
	if err != nil {
		return 0, 0, fmt.Errorf("reaping stale cluster state: %w", err)
	}
	return instances, cleared, nil
}
