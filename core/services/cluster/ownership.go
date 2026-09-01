package cluster

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrNoConnection reports that no tunnel is recorded for the requested node, or
// that the claim a caller named is no longer the live one. Callers distinguish
// it from a transport failure to decide whether to relay through an owner, to
// answer "this worker is not connected here", or to retry.
var ErrNoConnection = errors.New("cluster: no connection recorded for node")

// NodeConnection records which frontend replica currently holds a worker's
// tunnel. There is at most one row per node: a worker holds exactly one link,
// and whoever wrote the row last owns it.
//
// Epoch is the fence. A worker whose link is silently broken reconnects and may
// land on another replica before the previous owner's socket has noticed, so
// for a while two replicas both believe they own it. Every claim gets a higher
// epoch than any claim before it, so the loser can be told apart from the
// winner by a number both of them hold, without either having to detect the
// broken socket first.
type NodeConnection struct {
	NodeID          string    `gorm:"primaryKey;size:36" json:"node_id"`
	OwnerInstanceID string    `gorm:"size:36;index;not null" json:"owner_instance_id"`
	Epoch           int64     `gorm:"not null" json:"epoch"`
	ConnectedAt     time.Time `gorm:"not null;default:now()" json:"connected_at"`
	LastSeen        time.Time `gorm:"index;not null;default:now()" json:"last_seen"`
}

// Claim records ownerID as the owner of nodeID's tunnel and returns the new
// epoch, which is strictly greater than the epoch of every earlier claim on the
// same node.
//
// It is one statement on purpose. A read-then-write would let two replicas read
// the same epoch and hand out the same fence token, which is exactly the case
// the fence exists to rule out; PostgreSQL serializes concurrent
// INSERT ... ON CONFLICT DO UPDATE on the conflicting row, so the increment is
// computed by the database against the row version the winner just wrote.
func (r *Registry) Claim(ctx context.Context, nodeID, ownerID string) (int64, error) {
	// Timestamps are stamped by the database, never by this process, for the
	// same reason instance liveness is: they are compared across replicas, so
	// they have to be measured on the one clock every replica shares. On insert
	// that is the column default; on conflict it is the assignment below.
	conn := NodeConnection{NodeID: nodeID, OwnerInstanceID: ownerID, Epoch: 1}
	if err := r.db.WithContext(ctx).Clauses(
		clause.OnConflict{
			Columns: []clause.Column{{Name: "node_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				"owner_instance_id": ownerID,
				"epoch":             gorm.Expr(`"node_connections"."epoch" + 1`),
				"connected_at":      gorm.Expr("now()"),
				"last_seen":         gorm.Expr("now()"),
			}),
		},
		clause.Returning{Columns: []clause.Column{{Name: "epoch"}}},
	).Create(&conn).Error; err != nil {
		return 0, fmt.Errorf("claiming connection for node %q as %q: %w", nodeID, ownerID, err)
	}
	return conn.Epoch, nil
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
