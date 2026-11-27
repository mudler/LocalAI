package nodes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Draining a node takes it out of placement without dropping the models it
// serves. The node gets StatusDraining at once, so no new replica is placed on
// it (placement selects healthy nodes only). Its loaded replicas stay routable
// as a fallback while the reconciler places replacements on other nodes. A
// replica on the draining node is removed when the model no longer needs it
// there, or when the drain deadline passes. See reconciler_drain.go.
//
// All drain state lives in the database (the status and drain_deadline
// columns of backend_nodes), so every frontend sees the same drain.

// DefaultDrainTimeout is how long a drain waits for replacements before it
// removes the replicas that are still on the node.
const DefaultDrainTimeout = 10 * time.Minute

// DrainOptions controls one drain request.
type DrainOptions struct {
	// Force removes the node's replicas at once, as drain did before
	// replacements were placed first.
	Force bool
	// Timeout is the time after which the replicas still on the node are
	// removed. Zero uses the registry default (SetDrainTimeout).
	Timeout time.Duration
}

// DrainResult describes the drain that DrainNode started or found.
type DrainResult struct {
	// Immediate is true when the node's replicas were removed at once: the
	// drain was forced, or the node was not reachable (unhealthy or offline),
	// so there was nothing to wait for.
	Immediate bool
	// Deadline is when the replicas still on the node are removed. Nil for an
	// immediate drain.
	Deadline *time.Time
	// PreviousStatus is the status the node had before the drain.
	PreviousStatus string
}

// ErrInvalidDrainTimeout is a drain timeout that is not a positive duration.
var ErrInvalidDrainTimeout = errors.New("timeout must be a positive duration such as 10m")

// ParseDrainTimeout reads the deadline a drain request names, such as 10m.
// An empty value is zero: the registry default applies.
func ParseDrainTimeout(value string) (time.Duration, error) {
	if value == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, ErrInvalidDrainTimeout
	}
	return d, nil
}

// SetDrainTimeout sets the default drain deadline. Zero or less restores
// DefaultDrainTimeout.
func (r *NodeRegistry) SetDrainTimeout(d time.Duration) {
	r.drainTimeout.Store(int64(max(d, 0)))
}

// DrainTimeout returns the default drain deadline.
func (r *NodeRegistry) DrainTimeout() time.Duration {
	if d := time.Duration(r.drainTimeout.Load()); d > 0 {
		return d
	}
	return DefaultDrainTimeout
}

// DrainNode marks a node as draining. A healthy node keeps its replicas
// until replacements load elsewhere or the deadline passes. A forced drain, or
// a drain of a node that is unhealthy or offline, removes the replicas at
// once. A pending node cannot drain: it was never approved and serves nothing.
//
// Draining a node that already drains is not an error. A second normal drain
// keeps the first deadline; a forced one removes the replicas now.
func (r *NodeRegistry) DrainNode(ctx context.Context, nodeID string, opts DrainOptions) (DrainResult, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = r.DrainTimeout()
	}
	var res DrainResult
	var removedModels []string
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var node BackendNode
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "status", "drain_deadline").
			First(&node, "id = ?", nodeID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("node %s: %w", nodeID, ErrNodeNotFound)
			}
			return fmt.Errorf("reading node %s: %w", nodeID, err)
		}
		if node.Status == StatusPending {
			return fmt.Errorf("node %s has status %s and cannot drain: %w", nodeID, node.Status, ErrNodeStatusConflict)
		}
		res.PreviousStatus = node.Status
		res.Immediate = opts.Force || node.Status == StatusUnhealthy || node.Status == StatusOffline

		updates := map[string]any{"status": StatusDraining}
		switch {
		case res.Immediate:
			updates["drain_deadline"] = nil
		case node.Status == StatusDraining && node.DrainDeadline != nil:
			res.Deadline = node.DrainDeadline
		default:
			// The deadline comes from the database clock, the clock the
			// reconciler compares it with, so frontends whose clocks
			// disagree still end the drain at the same time.
			updates["drain_deadline"] = r.dbAfter(timeout)
		}
		if err := tx.Model(&BackendNode{}).Where("id = ?", nodeID).Updates(updates).Error; err != nil {
			return fmt.Errorf("marking node %s draining: %w", nodeID, err)
		}
		if !res.Immediate {
			if res.Deadline == nil {
				var stored BackendNode
				if err := tx.Select("drain_deadline").First(&stored, "id = ?", nodeID).Error; err != nil {
					return fmt.Errorf("reading the drain deadline of node %s: %w", nodeID, err)
				}
				res.Deadline = stored.DrainDeadline
			}
			return nil
		}
		removedModels = r.nodeModelNames(ctx, tx, nodeID)
		return tx.Where("node_id = ?", nodeID).Delete(&NodeModel{}).Error
	})
	if err != nil {
		return DrainResult{}, err
	}
	for _, m := range removedModels {
		r.fireReplicaRemoved(m, nodeID, -1)
	}
	return res, nil
}

// MarkDraining drains a node at once: it removes the node's replicas in the
// same transaction. It is DrainNode with Force.
func (r *NodeRegistry) MarkDraining(ctx context.Context, nodeID string) error {
	_, err := r.DrainNode(ctx, nodeID, DrainOptions{Force: true})
	return err
}

// ResumeNode transitions a draining node back to healthy and cancels its
// drain. It does not overwrite pending approval or a concurrent health change.
func (r *NodeRegistry) ResumeNode(ctx context.Context, nodeID string) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := transitionStatus(tx, nodeID, StatusDraining, StatusHealthy); err != nil {
			return err
		}
		return tx.Model(&BackendNode{}).Where("id = ?", nodeID).Update("drain_deadline", nil).Error
	})
	if err != nil {
		return err
	}
	// The node is capacity again: let the reconciler retry rules it gave up on.
	_ = r.ClearAllUnsatisfiable(ctx)
	return nil
}

// ListDrainingNodes returns the backend nodes that drain.
func (r *NodeRegistry) ListDrainingNodes(ctx context.Context) ([]BackendNode, error) {
	var nodes []BackendNode
	if err := r.db.WithContext(ctx).
		Where("status = ? AND node_type = ?", StatusDraining, NodeTypeBackend).
		Find(&nodes).Error; err != nil {
		return nil, fmt.Errorf("listing draining nodes: %w", err)
	}
	return nodes, nil
}

// DrainingNodesPastDeadline returns the IDs of the draining nodes whose drain
// deadline has passed by the database clock.
func (r *NodeRegistry) DrainingNodesPastDeadline(ctx context.Context) (map[string]bool, error) {
	var ids []string
	if err := r.db.WithContext(ctx).Model(&BackendNode{}).
		Where("status = ? AND node_type = ? AND drain_deadline IS NOT NULL AND drain_deadline <= ?",
			StatusDraining, NodeTypeBackend, r.dbNow()).
		Pluck("id", &ids).Error; err != nil {
		return nil, fmt.Errorf("listing draining nodes past their deadline: %w", err)
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// ReleaseDrainingNode removes every replica row of a draining node. The drain
// deadline passed, or the node stopped answering, so there is nothing left to
// wait for. It does nothing for a node that does not drain, so a resume that
// lands first wins. It returns the rows it removed.
func (r *NodeRegistry) ReleaseDrainingNode(ctx context.Context, nodeID string) ([]NodeModel, error) {
	var removed []NodeModel
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var node BackendNode
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id", "status").First(&node, "id = ?", nodeID).Error; err != nil {
			return err
		}
		if node.Status != StatusDraining {
			return nil
		}
		if err := tx.Where("node_id = ?", nodeID).Find(&removed).Error; err != nil {
			return err
		}
		return tx.Where("node_id = ?", nodeID).Delete(&NodeModel{}).Error
	})
	if err != nil {
		return nil, fmt.Errorf("releasing the replicas of draining node %s: %w", nodeID, err)
	}
	seen := map[string]bool{}
	for _, nm := range removed {
		if !seen[nm.ModelName] {
			seen[nm.ModelName] = true
			r.fireReplicaRemoved(nm.ModelName, nodeID, -1)
		}
	}
	return removed, nil
}

// notOnDrainingNodes limits a node_models query to rows on nodes that do not
// drain. A replica on a draining node is leaving: it serves until a
// replacement loads, but it must not count toward a model's replicas.
func notOnDrainingNodes(db *gorm.DB) *gorm.DB {
	return db.Where("node_models.node_id NOT IN (SELECT id FROM backend_nodes WHERE status = ?)", StatusDraining)
}

// CountStayingReplicas counts the loaded replicas of modelName on nodes that
// match selector and do not drain.
func (r *NodeRegistry) CountStayingReplicas(ctx context.Context, modelName string, selector map[string]string) (int64, error) {
	var count int64
	err := currentModelRevision(r.db.WithContext(ctx).Model(&NodeModel{})).
		Scopes(onNodesMatching(selector), notOnDrainingNodes).
		Where("node_models.model_name = ? AND node_models.state = ?", modelName, "loaded").
		Count(&count).Error
	return count, err
}

// CountHealthyReplicas counts the loaded replicas of modelName on healthy
// nodes that match selector. A draining replica may leave only when this
// count is high enough: a replica on an unhealthy node does not take
// requests, so counting it could leave the model with nothing routable.
// reconcileModel keeps counting with CountStayingReplicas, so a short health
// blip does not place extra replicas.
func (r *NodeRegistry) CountHealthyReplicas(ctx context.Context, modelName string, selector map[string]string) (int64, error) {
	var count int64
	err := currentModelRevision(r.db.WithContext(ctx).Model(&NodeModel{})).
		Scopes(onNodesMatching(selector)).
		Where("node_models.node_id IN (SELECT id FROM backend_nodes WHERE status = ?)", StatusHealthy).
		Where("node_models.model_name = ? AND node_models.state = ?", modelName, "loaded").
		Count(&count).Error
	return count, err
}

// RemoveDrainedReplica removes the replica row nm of a draining node, like
// RemoveIdleNodeModel, and only while the node still drains: a node resumed
// after the pass read it keeps its replica.
func (r *NodeRegistry) RemoveDrainedReplica(ctx context.Context, nm NodeModel) (bool, error) {
	res := r.db.WithContext(ctx).
		Where("id = ? AND state = ? AND in_flight = 0 AND last_used <= ?", nm.ID, "loaded", nm.LastUsed).
		Where("node_id IN (SELECT id FROM backend_nodes WHERE status = ?)", StatusDraining).
		Delete(&NodeModel{})
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		return false, nil
	}
	r.fireReplicaRemoved(nm.ModelName, nm.NodeID, nm.ReplicaIndex)
	return true, nil
}
