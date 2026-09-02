package cluster

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrNoConnection reports that no tunnel is recorded for the requested node, or
// that the claim a caller named is no longer the live one. Callers distinguish
// it from a transport failure to decide whether to relay through an owner, to
// answer "this worker is not connected here", or to retry.
var ErrNoConnection = errors.New("cluster: no connection recorded for node")

// isPostgres reports whether the gorm dialect is PostgreSQL. The connection
// fence is built out of PostgreSQL-only pieces (a sequence, ON CONFLICT
// RETURNING), and the single-binary path runs on SQLite. advisorylock has an
// identical private check; this one is copied rather than imported because it
// is a one-line comparison on gorm's own dialector name and cluster is
// deliberately a leaf package.
func isPostgres(db *gorm.DB) bool {
	return strings.Contains(db.Dialector.Name(), "postgres")
}

// epochSequence is the PostgreSQL sequence every claim draws its epoch from.
// A sequence rather than a per-row counter because a connection row does not
// outlive the deployment: a departure is purged once it is old enough, and with
// `epoch = epoch + 1` the numbering would restart at 1 for the next claim, so a
// replica that claimed, lost the worker, and claimed again could be handed an
// epoch it already held, and a delayed cleanup from the first claim would then
// match, and clear, the live one.
const epochSequence = "node_connection_epochs"

// NodeConnection records which frontend replica currently holds a worker's
// tunnel. There is at most one row per node: a worker holds exactly one link,
// and whoever wrote the row last owns it.
//
// Epoch is the fence. A worker whose link is silently broken reconnects and may
// land on another replica before the previous owner's socket has noticed, so
// for a while two replicas both believe they own it. Every claim draws a fresh,
// never-reused epoch, so the loser can be told apart from the winner by a number
// both of them hold, without either having to detect the broken socket first.
//
// There is deliberately no last-seen column here. Whether the owning replica is
// alive is answered by Instance.LastSeen, and whether a claim is still the live
// one is answered by the epoch; a second liveness clock for the same fact would
// only drift from the first.
type NodeConnection struct {
	NodeID string `gorm:"primaryKey;size:36" json:"node_id"`
	// Empty when nobody holds this tunnel. A departed row keeps the empty
	// string rather than NULL so there is one spelling of "held by nobody";
	// connectionIsHeld is the only place that spelling is written down.
	OwnerInstanceID string `gorm:"size:36;index;not null" json:"owner_instance_id"`
	Epoch           int64  `gorm:"not null" json:"epoch"`
	// No column DEFAULT: now() is PostgreSQL syntax and would reach the DDL,
	// which breaks AutoMigrate on the SQLite single-binary path. Claim writes
	// the database clock as an expression instead, the way Register does.
	ConnectedAt time.Time `gorm:"not null" json:"connected_at"`
	// DisconnectedAt is when the tunnel LEFT, and it exists because "gone for
	// thirty seconds" and "never here" have to be different answers.
	//
	// A released row used to be deleted, so a worker re-homing between replicas
	// was indistinguishable from one that had never dialled, and any grace
	// period built on top would have had nothing to measure from. It is NOT a
	// liveness clock for the owner: whether the OWNING replica is alive is
	// still Instance.LastSeen and nothing else. It ticks once, on departure.
	//
	// Null while the row is held, and stamped on the database clock by whoever
	// records the departure, so a row with an owner never also carries one.
	DisconnectedAt *time.Time `gorm:"index" json:"disconnected_at,omitempty"`
}

// connectionIsHeld is the one predicate that separates a row recording a tunnel
// somebody holds from a row recording a departure. Everything that asks that
// question asks it here, or asks for its negation: Owner and OwnerRow refuse a
// row it rejects, ReapStale clears only rows it accepts, and
// PurgeDepartedBefore deletes only rows it rejects. Two spellings of one fact
// drift, and the drift here would show up as a departed worker resolving to a
// replica that holds nothing.
//
// Deregister is the one write that does not use it, because it matches an owner
// id, which is narrower: a departed row carries no owner and so cannot match.
//
// The column is table-qualified because Owner reads it across a join, where an
// unqualified name is ambiguous; qualifying it costs the single-table readers
// nothing.
const connectionIsHeld = `node_connections.owner_instance_id <> ''`

// Migrate creates every table and sequence this package owns. It is the one
// call a caller has to remember: gorm's AutoMigrate models tables and columns
// but has no notion of a sequence, and the connection fence draws its epochs
// from one, so a caller that knew only about AutoMigrate would leave a schema
// that looks complete and cannot claim. Safe to call repeatedly.
//
// It does not take the migration advisory lock itself. The caller holds it
// across every table in the deployment, and taking a second one here would
// either nest inside that one or, worse, be the reason someone stops holding
// the outer one.
func Migrate(ctx context.Context, db *gorm.DB) error {
	if err := db.WithContext(ctx).AutoMigrate(&Instance{}, &NodeConnection{}); err != nil {
		return fmt.Errorf("migrating cluster tables: %w", err)
	}
	return ensureEpochSequence(ctx, db)
}

// ensureEpochSequence creates the sequence Claim draws epochs from. It lives
// here, beside the model that needs it, because gorm's AutoMigrate models
// tables and columns but has no notion of a sequence; the caller that owns the
// migration advisory lock calls Migrate so that concurrently starting replicas
// do not race on the DDL. It is safe to call repeatedly.
//
// The sequence is not attached as a column DEFAULT on purpose: AutoMigrate
// compares the struct's declared default against the one PostgreSQL reports
// (`nextval('...'::regclass)`), and a mismatch there makes every startup ALTER
// the column. Naming the sequence in the statement keeps the schema stable.
func ensureEpochSequence(ctx context.Context, db *gorm.DB) error {
	// CREATE SEQUENCE is PostgreSQL-only, and the same migration path runs
	// against SQLite in single-binary mode. Nothing there can claim a
	// connection (Claim refuses the dialect outright), so there is nothing to
	// create.
	if !isPostgres(db) {
		return nil
	}
	if err := db.WithContext(ctx).Exec(`CREATE SEQUENCE IF NOT EXISTS ` + epochSequence + ` AS bigint`).Error; err != nil {
		return fmt.Errorf("creating connection epoch sequence: %w", err)
	}
	return nil
}

// Claim records ownerID as the owner of nodeID's tunnel and returns the epoch
// of the claim.
//
// The epoch is UNIQUE and never reissued: no other claim, for this node or any
// other, is ever handed the same value. It is NOT ordered, and callers must not
// treat it as a version number. The sequence value on the insert path is drawn
// while the tuple is built, before the row lock, so a claim that inserts after
// a Release can be handed a number lower than one already issued elsewhere.
// Compare epochs for equality only; never compare them for order.
//
// Uniqueness is all the fence needs: Release matches owner and epoch exactly,
// so a stale claim's token cannot match a live claim's row whichever way the
// two numbers happen to compare.
//
// It is one statement on purpose. A read-then-write would let two replicas read
// the same epoch and hand out the same fence token, which is exactly the case
// the fence exists to rule out; PostgreSQL serializes concurrent
// INSERT ... ON CONFLICT DO UPDATE on the conflicting row, so the losing writers
// block until the winner commits and only then draw their own epoch, in the
// order they took the row lock.
func (r *Registry) Claim(ctx context.Context, nodeID, ownerID string) (int64, error) {
	// Refused rather than attempted on a dialect with no sequence. The
	// statement would fail anyway, but with a driver-level "no such function:
	// nextval" that reads like a missing migration; and a fence that cannot
	// issue a token must not look like one that did.
	if !isPostgres(r.db) {
		return 0, fmt.Errorf("claiming connection for node %q as %q: connection ownership requires PostgreSQL, this deployment runs on %q", nodeID, ownerID, r.db.Dialector.Name())
	}
	// connected_at is stamped by the database, never by this process, for the
	// same reason instance liveness is: it is compared across replicas, so it
	// has to be measured on the one clock every replica shares.
	nextEpoch := gorm.Expr("nextval('" + epochSequence + "')")
	values := map[string]any{
		"node_id":           nodeID,
		"owner_instance_id": ownerID,
		"epoch":             nextEpoch,
		"connected_at":      gorm.Expr("now()"),
	}
	if err := r.db.WithContext(ctx).Model(&NodeConnection{}).Clauses(
		clause.OnConflict{
			Columns: []clause.Column{{Name: "node_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				"owner_instance_id": ownerID,
				"epoch":             nextEpoch,
				"connected_at":      gorm.Expr("now()"),
				// In the SAME statement as the owner, so a reconnect is never
				// observed half-applied: a row that names a holder while still
				// carrying a departure would answer "held" and "gone" at once.
				"disconnected_at": nil,
			}),
		},
		clause.Returning{Columns: []clause.Column{{Name: "epoch"}}},
	).Create(values).Error; err != nil {
		return 0, fmt.Errorf("claiming connection for node %q as %q: %w", nodeID, ownerID, err)
	}
	// gorm scans RETURNING back over the map it was handed. If that ever stops
	// happening the entry is still the expression we passed in, and returning a
	// bogus epoch would hand out a fence token the database never issued.
	epoch, ok := values["epoch"].(int64)
	if !ok {
		return 0, fmt.Errorf("claiming connection for node %q as %q: epoch not returned by the database (got %T)", nodeID, ownerID, values["epoch"])
	}
	return epoch, nil
}

// OwnerRow returns the row recording which replica holds nodeID's tunnel, and
// the epoch of that claim, or ErrNoConnection when the node has no recorded
// connection.
//
// It answers "what does the table say", NOT "who holds this tunnel". The owner
// it names may be dead: a replica that dies stops heartbeating, and its rows
// survive until another replica's sweep removes them, which is up to
// InstanceLiveness plus one InstanceHeartbeat later.
//
// Anything that needs to know WHO owns a node in order to act on it, a dialer
// above all, wants Owner: it joins instances and treats a non-live owner as
// ErrNoConnection. Dialing what this function returns is dialing a process that
// may be gone.
//
// What is left for this one is observing the table as such, independently of
// liveness. Its callers today are this package's specs, including the one that
// holds the two reads apart, and the e2e cluster spec that watches ownership
// move between replicas. No production caller reads it, and the sweeper is not
// one: ReapStale finds orphans with a set difference in SQL.
//
// A row that records a departure is ErrNoConnection here too. The row survives
// so that something can decide how old the departure is; what it says about who
// holds the tunnel is nobody, and this read reports exactly that.
func (r *Registry) OwnerRow(ctx context.Context, nodeID string) (string, int64, error) {
	var conn NodeConnection
	err := r.db.WithContext(ctx).
		Where("node_id = ? AND "+connectionIsHeld, nodeID).First(&conn).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", 0, fmt.Errorf("looking up owner of node %q: %w", nodeID, ErrNoConnection)
	}
	if err != nil {
		return "", 0, fmt.Errorf("looking up owner of node %q: %w", nodeID, err)
	}
	return conn.OwnerInstanceID, conn.Epoch, nil
}

// Owner returns the replica that holds nodeID's tunnel AND is still live, with
// the epoch of that claim, or ErrNoConnection when there is no such replica.
//
// This is the read anything that ACTS on the answer must use. A connection row
// outlives its owner: a replica that dies stops heartbeating but its rows stay
// until a peer's sweep removes them, which is up to InstanceLiveness plus one
// InstanceHeartbeat later. For that whole window OwnerRow names a process that
// is gone, and a relay built on it would dial a corpse and report the worker as
// unreachable rather than as absent.
//
// A missing row, a departed row and a dead owner are one answer on purpose. All
// three mean "no live replica holds this worker's tunnel", which is what a
// caller decides on; they differ only in which sweep has already run, and that
// is the sweeper's business rather than the caller's.
//
// That answer is emphatically not "this worker is gone". How long ago the
// departure was recorded is what separates a worker re-homing between replicas
// from one that has left, and this read does not report it: a caller that has
// to tell those apart reads the departure, not this.
//
// One statement, joined, not a row read followed by an instance lookup: between
// two statements the owner can die, and the caller would act on an owner the
// second read would have rejected. The join makes the two facts one snapshot.
//
// The window is InstanceLiveness rather than a parameter, which is the window
// the membership loop sweeps with. A caller free to pick its own could keep
// relaying to a replica the sweeper has already declared dead, or give up on
// one the sweeper is still keeping.
func (r *Registry) Owner(ctx context.Context, nodeID string) (string, int64, error) {
	// Refused rather than attempted, for the reason Claim refuses: now() and
	// make_interval are PostgreSQL, so on the single-binary SQLite path this
	// would fail with "no such function: now", which reads as a missing
	// migration. It is deliberately not ErrNoConnection. A deployment with no
	// cluster has no answer to give about who owns a tunnel, and reporting
	// absence would let a caller conclude the worker is not connected.
	if !isPostgres(r.db) {
		return "", 0, fmt.Errorf("looking up live owner of node %q: connection ownership requires PostgreSQL, this deployment runs on %q", nodeID, r.db.Dialector.Name())
	}
	var conn NodeConnection
	err := r.db.WithContext(ctx).
		Model(&NodeConnection{}).
		// Not load-bearing: gorm already expands this model's own columns,
		// table-qualified, when a join is present and nothing was selected
		// (callbacks.BuildQuerySQL). Written out so the projection is a
		// property of this query rather than of that behaviour, since the join
		// is here to filter and the row scanned back must stay this table's.
		Select("node_connections.*").
		Joins("JOIN instances ON instances.id = node_connections.owner_instance_id AND "+instanceIsLive, InstanceLiveness.Seconds()).
		// The departure filter is this query's own, not the join's. An empty
		// owner matches no instance today only because no replica registers
		// under an empty id, which is an accident of who registers rather than
		// a property of ownership.
		Where("node_connections.node_id = ? AND "+connectionIsHeld, nodeID).
		Take(&conn).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", 0, fmt.Errorf("looking up live owner of node %q: %w", nodeID, ErrNoConnection)
	}
	if err != nil {
		return "", 0, fmt.Errorf("looking up live owner of node %q: %w", nodeID, err)
	}
	return conn.OwnerInstanceID, conn.Epoch, nil
}

// departure is the single assignment that turns a held row into a departed one.
// Release writes it for one claim and the membership sweep writes it for every
// claim a dead replica held, and the two must not disagree about what a
// departure looks like: a row cleared without a stamp, or stamped without being
// cleared, is a state every reader here has no answer for.
//
// The timestamp is the database's, never this process's, for the same reason
// instance liveness is: it is compared across replicas, so it has to be
// measured on the one clock they share.
func departure() map[string]any {
	return map[string]any{
		"owner_instance_id": "",
		"disconnected_at":   gorm.Expr("now()"),
	}
}

// Release drops the claim identified by ownerID and epoch, and records the
// departure: the row survives with no owner and a disconnected_at stamp.
//
// An UPDATE and not a DELETE, because the row is the only place a departure can
// be recorded. Deleting it made a worker re-homing between replicas look like
// one that had never connected, so nothing above could tell a two-second blip
// from a worker that is gone.
//
// Both ownerID and epoch are in the WHERE so a replica that has only just
// noticed its dead socket cannot touch the claim a later reconnect established
// elsewhere: it must not clear a live claim, and it must not stamp a departure
// onto one. A claim that is no longer the live one is reported as
// ErrNoConnection rather than silently ignored, because the caller learning it
// has been fenced out is the point.
func (r *Registry) Release(ctx context.Context, nodeID, ownerID string, epoch int64) error {
	// Refused rather than attempted, for the reason Claim refuses: the
	// departure is stamped with now(), which on the single-binary SQLite path
	// fails as a missing function and reads as a missing migration. It is
	// deliberately not ErrNoConnection: a deployment with no cluster holds no
	// claims, and reporting a fenced-out release would tell the caller it lost
	// one.
	if !isPostgres(r.db) {
		return fmt.Errorf("releasing connection for node %q held by %q: connection ownership requires PostgreSQL, this deployment runs on %q", nodeID, ownerID, r.db.Dialector.Name())
	}
	// gorm reports no error when a Where matches nothing, so the miss has to be
	// read off RowsAffected.
	res := r.db.WithContext(ctx).
		Model(&NodeConnection{}).
		Where("node_id = ? AND owner_instance_id = ? AND epoch = ?", nodeID, ownerID, epoch).
		Updates(departure())
	if res.Error != nil {
		return fmt.Errorf("releasing connection for node %q held by %q at epoch %d: %w", nodeID, ownerID, epoch, res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("releasing connection for node %q held by %q at epoch %d: %w", nodeID, ownerID, epoch, ErrNoConnection)
	}
	return nil
}

// PurgeDepartedBefore deletes the connection rows whose departure is older than
// olderThan, and returns how many went.
//
// Departures are kept so that absence can be decided, not kept forever. The
// retention has to outlast every window measured from a departure by enough
// that a purge can never turn a worker inside its reconnect grace into a worker
// that was never here; the caller passes a multiple of that grace, never the
// grace itself.
//
// A held row is never touched, whatever timestamp it carries: deleting one
// strands a worker that is connected at that moment. The age is measured by the
// database, like every other window here, so no replica's clock decides how old
// another replica's departure is.
func (r *Registry) PurgeDepartedBefore(ctx context.Context, olderThan time.Duration) (int64, error) {
	// Refused rather than attempted, for the reason Claim refuses: now() and
	// make_interval are PostgreSQL, and on the single-binary SQLite path this
	// would fail with "no such function: now", which reads as a missing
	// migration.
	if !isPostgres(r.db) {
		return 0, fmt.Errorf("purging departed connections: connection ownership requires PostgreSQL, this deployment runs on %q", r.db.Dialector.Name())
	}
	res := r.db.WithContext(ctx).
		Where("NOT ("+connectionIsHeld+") AND node_connections.disconnected_at < now() - make_interval(secs => ?)",
			olderThan.Seconds()).
		Delete(&NodeConnection{})
	if res.Error != nil {
		return 0, fmt.Errorf("purging departed connections: %w", res.Error)
	}
	return res.RowsAffected, nil
}
