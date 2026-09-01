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
// A sequence rather than a per-row counter because a released row is deleted:
// with `epoch = epoch + 1` the numbering restarts at 1 for the next claim, so a
// replica that claimed, lost the worker, and claimed again could be handed an
// epoch it already held, and a delayed cleanup from the first claim would then
// match, and delete, the live one.
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
	NodeID          string `gorm:"primaryKey;size:36" json:"node_id"`
	OwnerInstanceID string `gorm:"size:36;index;not null" json:"owner_instance_id"`
	Epoch           int64  `gorm:"not null" json:"epoch"`
	// No column DEFAULT: now() is PostgreSQL syntax and would reach the DDL,
	// which breaks AutoMigrate on the SQLite single-binary path. Claim writes
	// the database clock as an expression instead, the way Register does.
	ConnectedAt time.Time `gorm:"not null" json:"connected_at"`
}

// EnsureEpochSequence creates the sequence Claim draws epochs from. It lives
// here, beside the model that needs it, because gorm's AutoMigrate models
// tables and columns but has no notion of a sequence; the caller that owns the
// migration advisory lock calls it so that concurrently starting replicas do
// not race on the DDL. It is safe to call repeatedly.
//
// The sequence is not attached as a column DEFAULT on purpose: AutoMigrate
// compares the struct's declared default against the one PostgreSQL reports
// (`nextval('...'::regclass)`), and a mismatch there makes every startup ALTER
// the column. Naming the sequence in the statement keeps the schema stable.
func EnsureEpochSequence(ctx context.Context, db *gorm.DB) error {
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

// Owner returns the replica that holds nodeID's tunnel and the epoch of that
// claim, or ErrNoConnection when the node has no recorded connection.
func (r *Registry) Owner(ctx context.Context, nodeID string) (string, int64, error) {
	var conn NodeConnection
	err := r.db.WithContext(ctx).Where("node_id = ?", nodeID).First(&conn).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", 0, fmt.Errorf("looking up owner of node %q: %w", nodeID, ErrNoConnection)
	}
	if err != nil {
		return "", 0, fmt.Errorf("looking up owner of node %q: %w", nodeID, err)
	}
	return conn.OwnerInstanceID, conn.Epoch, nil
}

// Release drops the claim identified by ownerID and epoch. Both are in the
// WHERE so a replica that has only just noticed its dead socket cannot delete
// the claim a later reconnect established elsewhere: the row it is trying to
// clean up no longer exists, and deleting the live one would strand a worker
// that is in fact connected. A claim that is no longer the live one is reported
// as ErrNoConnection rather than silently ignored, because the caller learning
// it has been fenced out is the point.
func (r *Registry) Release(ctx context.Context, nodeID, ownerID string, epoch int64) error {
	// gorm reports no error when a Where matches nothing, so the miss has to be
	// read off RowsAffected.
	res := r.db.WithContext(ctx).
		Where("node_id = ? AND owner_instance_id = ? AND epoch = ?", nodeID, ownerID, epoch).
		Delete(&NodeConnection{})
	if res.Error != nil {
		return fmt.Errorf("releasing connection for node %q held by %q at epoch %d: %w", nodeID, ownerID, epoch, res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("releasing connection for node %q held by %q at epoch %d: %w", nodeID, ownerID, epoch, ErrNoConnection)
	}
	return nil
}
