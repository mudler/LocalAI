package cluster_test

import (
	"context"
	"errors"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"gorm.io/gorm"
)

// ownerRow reads the connection table as it is, without the liveness join that
// Registry.Owner applies. It returns cluster.ErrNoConnection for a node with no
// held row.
func ownerRow(ctx context.Context, db *gorm.DB, nodeID string) (string, int64, error) {
	var conn cluster.NodeConnection
	err := db.WithContext(ctx).Where("node_id = ? AND owner_instance_id <> ''", nodeID).First(&conn).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", 0, cluster.ErrNoConnection
	}
	if err != nil {
		return "", 0, err
	}
	return conn.OwnerInstanceID, conn.Epoch, nil
}

// liveInstances reads the replicas that count as alive within the window, by the
// same SQL that the claim queue joins on.
func liveInstances(ctx context.Context, db *gorm.DB, within time.Duration) ([]cluster.Instance, error) {
	var out []cluster.Instance
	err := db.WithContext(ctx).
		Raw("SELECT * FROM instances WHERE id IN ("+cluster.LiveInstanceIDsSQL+") ORDER BY id", within.Seconds()).
		Scan(&out).Error
	return out, err
}
