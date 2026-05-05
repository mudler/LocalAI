package nodes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// BackendNode represents a remote worker node.
// Workers are generic — they don't have a fixed backend type.
// The SmartRouter dynamically installs backends via NATS backend.install events.
type BackendNode struct {
	ID            string    `gorm:"primaryKey;size:36" json:"id"`
	Name          string    `gorm:"uniqueIndex;size:255" json:"name"`
	NodeType      string    `gorm:"size:32;default:backend" json:"node_type"`    // backend, agent
	Address       string    `gorm:"size:255" json:"address"`                     // host:port for gRPC
	HTTPAddress   string    `gorm:"size:255" json:"http_address"`                // host:port for HTTP file transfer
	Status        string    `gorm:"size:32;default:registering" json:"status"`   // registering, healthy, unhealthy, draining, pending
	TokenHash     string    `gorm:"size:64" json:"-"`                            // SHA-256 of registration token
	TotalVRAM     uint64    `gorm:"column:total_vram" json:"total_vram"`         // Total GPU VRAM in bytes
	AvailableVRAM uint64    `gorm:"column:available_vram" json:"available_vram"` // Available GPU VRAM in bytes
	// ReservedVRAM is a soft, in-tick reservation deducted by the scheduler when
	// it picks this node to load a model. Workers reset it back to 0 on each
	// heartbeat (the worker is the source of truth for actual free VRAM); the
	// reservation is only here to keep two scheduling decisions within the
	// same heartbeat window from over-committing the same node.
	ReservedVRAM        uint64    `gorm:"column:reserved_vram;default:0" json:"reserved_vram"`
	TotalRAM            uint64    `gorm:"column:total_ram" json:"total_ram"`         // Total system RAM in bytes (fallback when no GPU)
	AvailableRAM        uint64    `gorm:"column:available_ram" json:"available_ram"` // Available system RAM in bytes
	GPUVendor           string    `gorm:"column:gpu_vendor;size:32" json:"gpu_vendor"` // nvidia, amd, intel, vulkan, unknown
	// MaxReplicasPerModel caps how many replicas of any one model can run on
	// this node concurrently. Default 1 preserves the historical "one
	// (node, model)" assumption; set higher (via worker --max-replicas-per-model)
	// to allow stacking replicas on a fat node.
	MaxReplicasPerModel int `gorm:"column:max_replicas_per_model;default:1" json:"max_replicas_per_model"`
	// MaxReplicasPerModelManuallySet flags the value above as a UI-set
	// admin override. When true, the worker's CLI value is ignored on
	// re-registration so the override survives worker restarts. Cleared
	// by an explicit "reset to worker default" action.
	MaxReplicasPerModelManuallySet bool `gorm:"column:max_replicas_per_model_manually_set;default:false" json:"max_replicas_per_model_manually_set"`
	APIKeyID            string    `gorm:"size:36" json:"-"` // auto-provisioned API key ID (for cleanup)
	AuthUserID          string    `gorm:"size:36" json:"-"` // auto-provisioned user ID (for cleanup)
	LastHeartbeat       time.Time `gorm:"column:last_heartbeat" json:"last_heartbeat"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

const (
	NodeTypeBackend = "backend"
	NodeTypeAgent   = "agent"

	StatusHealthy   = "healthy"
	StatusPending   = "pending"
	StatusOffline   = "offline"
	StatusDraining  = "draining"
	StatusUnhealthy = "unhealthy"

	// Column names (must match gorm:"column:" tags on BackendNode)
	ColAvailableVRAM       = "available_vram"
	ColTotalVRAM           = "total_vram"
	ColReservedVRAM        = "reserved_vram"
	ColAvailableRAM        = "available_ram"
	ColGPUVendor           = "gpu_vendor"
	ColLastHeartbeat       = "last_heartbeat"
	ColMaxReplicasPerModel = "max_replicas_per_model"
)

// NodeModel tracks which models are loaded on which nodes.
//
// Multiple replicas of the same model on the same node are allowed; each
// replica has its own ReplicaIndex (0..MaxReplicasPerModel-1), its own
// gRPC Address (each replica is a separate worker process on its own port),
// and its own InFlight counter.
type NodeModel struct {
	ID           string `gorm:"primaryKey;size:36" json:"id"`
	NodeID       string `gorm:"index;size:36" json:"node_id"`
	ModelName    string `gorm:"index;size:255" json:"model_name"`
	ReplicaIndex int    `gorm:"column:replica_index;default:0;index" json:"replica_index"`
	Address      string `gorm:"size:255" json:"address"`           // gRPC address for this replica's backend process
	State        string `gorm:"size:32;default:idle" json:"state"` // loading, loaded, unloading, idle
	InFlight     int    `json:"in_flight"`                         // number of active requests on this replica
	LastUsed     time.Time `json:"last_used"`
	LoadingBy     string    `gorm:"size:36" json:"loading_by,omitempty"`     // frontend ID that triggered loading
	BackendType   string    `gorm:"size:128" json:"backend_type,omitempty"`  // e.g. "llama-cpp"; used by reconciler to replicate loads
	ModelOptsBlob []byte    `gorm:"type:bytea" json:"-"`                     // serialized pb.ModelOptions for replica scale-ups
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// NodeLabel is a key-value label on a node (like K8s labels).
type NodeLabel struct {
	ID     string `gorm:"primaryKey;size:36" json:"id"`
	NodeID string `gorm:"uniqueIndex:idx_node_label;size:36" json:"node_id"`
	Key    string `gorm:"uniqueIndex:idx_node_label;size:128" json:"key"`
	Value  string `gorm:"size:255" json:"value"`
}

// ModelSchedulingConfig defines how a model should be scheduled across the cluster.
// All fields are optional:
//   - NodeSelector only → constrain nodes, single replica
//   - MinReplicas/MaxReplicas only → auto-scale on any node
//   - Both → auto-scale on matching nodes
//   - Neither → no-op (default behavior)
//
// Auto-scaling is enabled when MinReplicas > 0 or MaxReplicas > 0.
type ModelSchedulingConfig struct {
	ID           string `gorm:"primaryKey;size:36" json:"id"`
	ModelName    string `gorm:"uniqueIndex;size:255" json:"model_name"`
	NodeSelector string `gorm:"type:text" json:"node_selector,omitempty"` // JSON {"key":"value",...}
	MinReplicas  int    `gorm:"default:0" json:"min_replicas"`
	MaxReplicas  int    `gorm:"default:0" json:"max_replicas"`
	// UnsatisfiableUntil is set by the reconciler when no candidate node has
	// free capacity for this model; while in the future, the reconciler skips
	// scale-up attempts for this model. Cleared on cluster events that could
	// change capacity (new node registers, node approved, labels change,
	// max-replicas-per-model changes) or when the cooldown expires.
	UnsatisfiableUntil *time.Time `gorm:"column:unsatisfiable_until" json:"unsatisfiable_until,omitempty"`
	// UnsatisfiableTicks is hysteresis: incremented each tick capacity==0,
	// promoted to UnsatisfiableUntil once it crosses a small threshold to
	// avoid one-tick flaps. Reset on any successful scale-up.
	UnsatisfiableTicks int       `gorm:"column:unsatisfiable_ticks;default:0" json:"unsatisfiable_ticks"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// NodeWithExtras extends BackendNode with computed fields for list views.
type NodeWithExtras struct {
	BackendNode
	ModelCount    int               `json:"model_count"`
	InFlightCount int               `json:"in_flight_count"`
	Labels        map[string]string `json:"labels,omitempty"`
}

// PendingBackendOp is a durable intent for a backend lifecycle operation
// (delete/install/upgrade) that needs to eventually apply on a specific node.
//
// Without this table, a backend delete against an offline node silently
// dropped: the frontend skipped the node, the node came back later with the
// backend still installed, and the operator saw a zombie. Now the intent is
// recorded regardless of node status; the state reconciler drains the queue
// whenever a node is healthy and removes the row on success. Reissuing the
// same operation while a row exists updates NextRetryAt instead of stacking
// duplicates (see the unique index).
type PendingBackendOp struct {
	ID          uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	NodeID      string    `gorm:"index;size:36;not null;uniqueIndex:idx_pending_backend_op,priority:1" json:"node_id"`
	Backend     string    `gorm:"index;size:255;not null;uniqueIndex:idx_pending_backend_op,priority:2" json:"backend"`
	Op          string    `gorm:"size:16;not null;uniqueIndex:idx_pending_backend_op,priority:3" json:"op"`
	Galleries   []byte    `gorm:"type:bytea" json:"-"` // serialized JSON for install/upgrade retries
	Attempts    int       `gorm:"default:0" json:"attempts"`
	LastError   string    `gorm:"type:text" json:"last_error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	NextRetryAt time.Time `gorm:"index" json:"next_retry_at"`
}

// Op constants mirror the operation names used by DistributedBackendManager
// so callers don't repeat stringly-typed values.
const (
	OpBackendDelete  = "delete"
	OpBackendInstall = "install"
	OpBackendUpgrade = "upgrade"
)

// NodeRegistry manages backend node registration and lookup in PostgreSQL.
type NodeRegistry struct {
	db *gorm.DB
}

// NewNodeRegistry creates a NodeRegistry and auto-migrates the schema.
// Uses a PostgreSQL advisory lock to prevent concurrent migration races
// when multiple instances (frontend + workers) start at the same time.
func NewNodeRegistry(db *gorm.DB) (*NodeRegistry, error) {
	if err := advisorylock.WithLockCtx(context.Background(), db, advisorylock.KeySchemaMigrate, func() error {
		return db.AutoMigrate(&BackendNode{}, &NodeModel{}, &NodeLabel{}, &ModelSchedulingConfig{}, &PendingBackendOp{})
	}); err != nil {
		return nil, fmt.Errorf("migrating node tables: %w", err)
	}

	// One-shot cleanup of queue rows that can never drain: ops targeted at
	// agent workers (wrong subscription set), at non-existent nodes, or with
	// an empty backend name. The guard in enqueueAndDrainBackendOp prevents
	// new ones from being written, but rows persisted by earlier versions
	// keep the reconciler busy retrying a permanently-failing NATS request
	// every 30s. Guarded by the same migration advisory lock so only one
	// frontend runs it.
	_ = advisorylock.WithLockCtx(context.Background(), db, advisorylock.KeySchemaMigrate, func() error {
		res := db.Exec(`
			DELETE FROM pending_backend_ops
			WHERE backend = ''
			   OR node_id NOT IN (SELECT id FROM backend_nodes WHERE node_type = ? OR node_type = '')
		`, NodeTypeBackend)
		if res.Error != nil {
			xlog.Warn("Failed to prune malformed pending_backend_ops rows", "error", res.Error)
			return res.Error
		}
		if res.RowsAffected > 0 {
			xlog.Info("Pruned pending_backend_ops rows (wrong node type or empty backend)", "count", res.RowsAffected)
		}
		return nil
	})

	return &NodeRegistry{db: db}, nil
}

// Register adds or updates a backend node.
// If autoApprove is true, the node goes directly to "healthy" status.
// If false, new nodes start in "pending" status and must be approved by an admin.
// On re-registration (same name), previously approved nodes return to "healthy";
// nodes that were never approved stay in "pending".
func (r *NodeRegistry) Register(ctx context.Context, node *BackendNode, autoApprove bool) error {
	node.LastHeartbeat = time.Now()

	// Try to find existing node by name
	var existing BackendNode
	err := r.db.WithContext(ctx).Where("name = ?", node.Name).First(&existing).Error
	if err == nil {
		// Re-registration (node restart): preserve ID, respect approval history
		node.ID = existing.ID
		if autoApprove || existing.Status != StatusPending {
			// Auto-approve enabled, or node was previously approved — restore healthy
			node.Status = StatusHealthy
		} else {
			// Node was never approved — keep pending
			node.Status = StatusPending
		}
		// Preserve admin overrides from re-registration. Without this,
		// every worker restart silently reverts the UI-set value back to
		// the worker's CLI flag (default 1) — a footgun for operators who
		// configure capacity from the UI without touching the worker flag.
		updateDB := r.db.WithContext(ctx).Model(&existing)
		if existing.MaxReplicasPerModelManuallySet {
			updateDB = updateDB.Omit("max_replicas_per_model", "max_replicas_per_model_manually_set")
			// Reflect the persisted value back so the caller sees what the
			// scheduler will actually use.
			node.MaxReplicasPerModel = existing.MaxReplicasPerModel
			node.MaxReplicasPerModelManuallySet = true
		}
		if err := updateDB.Updates(node).Error; err != nil {
			return fmt.Errorf("updating node %s: %w", node.Name, err)
		}
		// Preserve auth references from existing record.
		// GORM Updates(struct) skips zero-value fields, so the DB retains
		// the old auth_user_id/api_key_id but the caller's struct is empty.
		// Copy them back so the caller can revoke old credentials on re-registration.
		if node.AuthUserID == "" {
			node.AuthUserID = existing.AuthUserID
		}
		if node.APIKeyID == "" {
			node.APIKeyID = existing.APIKeyID
		}
		// Clear stale model records — the node restarted and has nothing loaded
		if err := r.db.WithContext(ctx).Where("node_id = ?", existing.ID).Delete(&NodeModel{}).Error; err != nil {
			xlog.Warn("Failed to clear stale model records on re-register", "node", node.Name, "error", err)
		}
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		// Create new node
		if node.ID == "" {
			node.ID = uuid.New().String()
		}
		if autoApprove {
			node.Status = StatusHealthy
		} else {
			node.Status = StatusPending
		}
		if err := r.db.WithContext(ctx).Create(node).Error; err != nil {
			return fmt.Errorf("creating node %s: %w", node.Name, err)
		}
	} else {
		return fmt.Errorf("looking up node %s: %w", node.Name, err)
	}

	xlog.Info("Node registered", "name", node.Name, "address", node.Address, "status", node.Status)
	// Cluster capacity may have changed: a new healthy node, a returning
	// node, or one with different MaxReplicasPerModel. Wake any configs the
	// reconciler put in cooldown — the next tick will re-flag if still
	// unsatisfiable. Best-effort; logged but non-fatal.
	if err := r.ClearAllUnsatisfiable(ctx); err != nil {
		xlog.Warn("Failed to clear unsatisfiable scheduling flags on register", "error", err)
	}
	return nil
}

// UpdateAuthRefs stores the auto-provisioned user and API key IDs on a node.
func (r *NodeRegistry) UpdateAuthRefs(ctx context.Context, nodeID, authUserID, apiKeyID string) error {
	return r.db.WithContext(ctx).Model(&BackendNode{}).Where("id = ?", nodeID).Updates(map[string]any{
		"auth_user_id": authUserID,
		"api_key_id":   apiKeyID,
	}).Error
}

// ApproveNode sets a pending node's status to healthy.
func (r *NodeRegistry) ApproveNode(ctx context.Context, nodeID string) error {
	result := r.db.WithContext(ctx).Model(&BackendNode{}).
		Where("id = ? AND status = ?", nodeID, StatusPending).
		Update("status", StatusHealthy)
	if result.Error != nil {
		return fmt.Errorf("approving node %s: %w", nodeID, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("node %s not found or not in pending status", nodeID)
	}
	// pending → healthy adds cluster capacity; clear any cooldown flags so
	// the next reconciler tick can use the new node.
	if err := r.ClearAllUnsatisfiable(ctx); err != nil {
		xlog.Warn("Failed to clear unsatisfiable scheduling flags on approve", "error", err)
	}
	return nil
}

// setStatus updates a node's status column in the database.
func (r *NodeRegistry) setStatus(ctx context.Context, nodeID, status string) error {
	result := r.db.WithContext(ctx).Model(&BackendNode{}).
		Where("id = ?", nodeID).Update("status", status)
	if result.Error != nil {
		return fmt.Errorf("setting node %s to %s: %w", nodeID, status, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("node %s not found", nodeID)
	}
	return nil
}

// MarkOffline sets a node to offline status and clears its model records.
// Used on graceful shutdown — preserves the node row so re-registration
// can restore the previous approval status.
func (r *NodeRegistry) MarkOffline(ctx context.Context, nodeID string) error {
	if err := r.setStatus(ctx, nodeID, StatusOffline); err != nil {
		return err
	}
	// Clear model records — node is shutting down
	if err := r.db.WithContext(ctx).Where("node_id = ?", nodeID).Delete(&NodeModel{}).Error; err != nil {
		xlog.Warn("Failed to clear model records on offline", "node", nodeID, "error", err)
	}
	return nil
}

// FindNodeWithVRAM returns healthy nodes with at least minBytes effectively-
// available VRAM (available_vram - reserved_vram), ordered idle-first then
// least-loaded. The reserved_vram subtraction is the in-tick soft reservation
// that prevents two scheduling decisions in the same heartbeat window from
// over-committing the same node.
func (r *NodeRegistry) FindNodeWithVRAM(ctx context.Context, minBytes uint64) (*BackendNode, error) {
	db := r.db.WithContext(ctx)

	loadedModels := db.Model(&NodeModel{}).
		Select("node_id").
		Where("state = ?", "loaded").
		Group("node_id")

	subquery := db.Model(&NodeModel{}).
		Select("node_id, COALESCE(SUM(in_flight), 0) as total_inflight").
		Group("node_id")

	// Try idle nodes with enough effectively-free VRAM first, prefer the one
	// with most free VRAM (after deducting the in-tick reservation).
	var node BackendNode
	err := db.Where("status = ? AND node_type = ? AND (available_vram - reserved_vram) >= ? AND id NOT IN (?)",
		StatusHealthy, NodeTypeBackend, minBytes, loadedModels).
		Order("(available_vram - reserved_vram) DESC").
		First(&node).Error
	if err == nil {
		return &node, nil
	}

	// Fall back to least-loaded nodes with enough effectively-free VRAM
	err = db.Where("status = ? AND node_type = ? AND (available_vram - reserved_vram) >= ?",
		StatusHealthy, NodeTypeBackend, minBytes).
		Joins("LEFT JOIN (?) AS load ON load.node_id = backend_nodes.id", subquery).
		Order("COALESCE(load.total_inflight, 0) ASC, (backend_nodes.available_vram - backend_nodes.reserved_vram) DESC").
		First(&node).Error
	if err != nil {
		return nil, fmt.Errorf("no healthy nodes with %d bytes available VRAM: %w", minBytes, err)
	}
	return &node, nil
}

// ErrInsufficientVRAM signals that ReserveVRAM could not deduct the requested
// amount because the node's effectively-free VRAM has dropped below it
// (raced with another scheduler tick or with a heartbeat reset).
var ErrInsufficientVRAM = errors.New("insufficient effectively-free VRAM on node")

// ReserveVRAM atomically deducts `bytes` from the node's effectively-free
// VRAM (available_vram - reserved_vram). The UPDATE's WHERE clause does the
// admission check inside the database so two concurrent scheduling ticks
// can't both succeed when only one fits — whichever lands first reserves
// the slot, the other gets ErrInsufficientVRAM and falls through to the
// next candidate node.
//
// `bytes` may be 0 (e.g. when the model size estimator declines), in which
// case ReserveVRAM is a no-op — leaving accounting alone is preferable to
// reserving 0 (which would still bump no rows but is conceptually wrong).
//
// Worker heartbeats reset reserved_vram to 0 because the worker is the
// authoritative source for actual free VRAM. This is what makes the
// "soft" in soft-reservation: it's only honored within one heartbeat
// window; longer-term accounting comes from the worker's own readings.
func (r *NodeRegistry) ReserveVRAM(ctx context.Context, nodeID string, bytes uint64) error {
	if bytes == 0 {
		return nil
	}
	res := r.db.WithContext(ctx).Model(&BackendNode{}).
		Where("id = ? AND (available_vram - reserved_vram) >= ?", nodeID, bytes).
		UpdateColumn(ColReservedVRAM, gorm.Expr("reserved_vram + ?", bytes))
	if res.Error != nil {
		return fmt.Errorf("reserving %d bytes on node %s: %w", bytes, nodeID, res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrInsufficientVRAM
	}
	return nil
}

// ReleaseVRAM returns previously-reserved bytes to the pool. Called from the
// scheduler's deferred rollback path when LoadModel fails after a successful
// reservation, so the failed in-flight reservation doesn't linger until the
// next heartbeat.
//
// Guarded by `reserved_vram >= bytes` so a duplicate Release can't underflow
// past zero (the column is uint64 — wrap-around would be catastrophic for
// scheduler decisions).
func (r *NodeRegistry) ReleaseVRAM(ctx context.Context, nodeID string, bytes uint64) error {
	if bytes == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Model(&BackendNode{}).
		Where("id = ? AND reserved_vram >= ?", nodeID, bytes).
		UpdateColumn(ColReservedVRAM, gorm.Expr("reserved_vram - ?", bytes)).Error
}

// Deregister removes a backend node, its model associations, and any auto-provisioned auth credentials.
func (r *NodeRegistry) Deregister(ctx context.Context, nodeID string) error {
	db := r.db.WithContext(ctx)

	var node BackendNode
	if err := db.Where("id = ?", nodeID).First(&node).Error; err != nil {
		return fmt.Errorf("node %s not found: %w", nodeID, err)
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("node_id = ?", nodeID).Delete(&NodeModel{}).Error; err != nil {
			return fmt.Errorf("deleting node models for %s: %w", nodeID, err)
		}
		if err := tx.Where("id = ?", nodeID).Delete(&BackendNode{}).Error; err != nil {
			return fmt.Errorf("deleting node %s: %w", nodeID, err)
		}
		// Clean up auto-provisioned auth user (cascades to API keys via FK)
		if node.AuthUserID != "" {
			if err := tx.Exec("DELETE FROM users WHERE id = ?", node.AuthUserID).Error; err != nil {
				xlog.Warn("Failed to clean up agent worker user", "node", node.Name, "userID", node.AuthUserID, "error", err)
				// non-fatal: don't rollback the whole deregistration for auth cleanup
			}
		}
		return nil
	})
}

// HeartbeatUpdate contains optional fields to update on heartbeat.
type HeartbeatUpdate struct {
	AvailableVRAM *uint64 `json:"available_vram,omitempty"`
	TotalVRAM     *uint64 `json:"total_vram,omitempty"`
	AvailableRAM  *uint64 `json:"available_ram,omitempty"`
	GPUVendor     string  `json:"gpu_vendor,omitempty"`
}

// Heartbeat updates the heartbeat timestamp and status for a node.
// Nodes in "pending" or "offline" status stay in their current status —
// they must be approved or re-register respectively.
func (r *NodeRegistry) Heartbeat(ctx context.Context, nodeID string, update *HeartbeatUpdate) error {
	db := r.db.WithContext(ctx)

	updates := map[string]any{
		ColLastHeartbeat: time.Now(),
	}

	if update != nil {
		if update.AvailableVRAM != nil {
			updates[ColAvailableVRAM] = *update.AvailableVRAM
			// The worker is the source of truth for actual free VRAM.
			// Whenever it sends us a fresh reading, the in-tick soft
			// reservation is no longer needed — clear it. (See ReserveVRAM.)
			updates[ColReservedVRAM] = uint64(0)
		}
		if update.TotalVRAM != nil {
			updates[ColTotalVRAM] = *update.TotalVRAM
		}
		if update.AvailableRAM != nil {
			updates[ColAvailableRAM] = *update.AvailableRAM
		}
		if update.GPUVendor != "" {
			updates[ColGPUVendor] = update.GPUVendor
		}
	}

	// Only update all fields (including status promotion) for active nodes.
	// Pending and offline nodes must go through approval or re-registration.
	result := db.Model(&BackendNode{}).
		Where("id = ? AND status NOT IN ?", nodeID, []string{StatusPending, StatusOffline}).
		Updates(updates)
	if result.Error != nil {
		return fmt.Errorf("heartbeat for %s: %w", nodeID, result.Error)
	}
	if result.RowsAffected == 0 {
		// May be pending or offline — still update heartbeat timestamp
		result = db.Model(&BackendNode{}).Where("id = ?", nodeID).Update(ColLastHeartbeat, time.Now())
		if result.Error != nil {
			return fmt.Errorf("heartbeat for %s: %w", nodeID, result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("node %s not found", nodeID)
		}
	}
	return nil
}

// List returns all registered nodes.
func (r *NodeRegistry) List(ctx context.Context) ([]BackendNode, error) {
	var nodes []BackendNode
	if err := r.db.WithContext(ctx).Order("name").Find(&nodes).Error; err != nil {
		return nil, fmt.Errorf("listing nodes: %w", err)
	}
	return nodes, nil
}

// Get returns a single node by ID.
func (r *NodeRegistry) Get(ctx context.Context, nodeID string) (*BackendNode, error) {
	var node BackendNode
	if err := r.db.WithContext(ctx).First(&node, "id = ?", nodeID).Error; err != nil {
		return nil, fmt.Errorf("getting node %s: %w", nodeID, err)
	}
	return &node, nil
}

// GetByName returns a single node by name.
func (r *NodeRegistry) GetByName(ctx context.Context, name string) (*BackendNode, error) {
	var node BackendNode
	if err := r.db.WithContext(ctx).First(&node, "name = ?", name).Error; err != nil {
		return nil, fmt.Errorf("getting node by name %s: %w", name, err)
	}
	return &node, nil
}

// MarkUnhealthy sets a node status to unhealthy.
func (r *NodeRegistry) MarkUnhealthy(ctx context.Context, nodeID string) error {
	return r.setStatus(ctx, nodeID, StatusUnhealthy)
}

// MarkHealthy sets a node status to healthy.
func (r *NodeRegistry) MarkHealthy(ctx context.Context, nodeID string) error {
	return r.setStatus(ctx, nodeID, StatusHealthy)
}

// MarkDraining sets a node status to draining (no new requests).
func (r *NodeRegistry) MarkDraining(ctx context.Context, nodeID string) error {
	return r.setStatus(ctx, nodeID, StatusDraining)
}

// FindStaleNodes returns nodes that haven't sent a heartbeat within the given threshold.
// Excludes unhealthy, offline, and pending nodes since they're not actively participating.
func (r *NodeRegistry) FindStaleNodes(ctx context.Context, threshold time.Duration) ([]BackendNode, error) {
	var nodes []BackendNode
	cutoff := time.Now().Add(-threshold)
	if err := r.db.WithContext(ctx).Where("last_heartbeat < ? AND status NOT IN ?", cutoff,
		[]string{StatusUnhealthy, StatusOffline, StatusPending}).
		Find(&nodes).Error; err != nil {
		return nil, fmt.Errorf("finding stale nodes: %w", err)
	}
	return nodes, nil
}

// --- NodeModel operations ---

// SetNodeModel records that a replica of a model is loaded on a node.
// replicaIndex identifies which slot on the node this replica occupies
// (0..MaxReplicasPerModel-1). Pass 0 for single-replica scheduling.
func (r *NodeRegistry) SetNodeModel(ctx context.Context, nodeID, modelName string, replicaIndex int, state, address string, initialInFlight int) error {
	now := time.Now()
	// Use Attrs for creation-only fields (ID) and Assign for update-only fields.
	// Attrs is applied only when creating a new record. Assign is applied on
	// both create and update. This prevents overwriting the primary key on
	// subsequent calls for the same (node, model, replica_index).
	var nm NodeModel
	result := r.db.WithContext(ctx).Where("node_id = ? AND model_name = ? AND replica_index = ?", nodeID, modelName, replicaIndex).
		Attrs(NodeModel{ID: uuid.New().String(), NodeID: nodeID, ModelName: modelName, ReplicaIndex: replicaIndex}).
		Assign(map[string]any{"address": address, "state": state, "last_used": now, "in_flight": initialInFlight}).
		FirstOrCreate(&nm)
	return result.Error
}

// SetNodeModelLoadInfo stores the backend type and serialized model options on
// an existing NodeModel record. This metadata is used by the reconciler to
// replicate model loads during scale-up.
func (r *NodeRegistry) SetNodeModelLoadInfo(ctx context.Context, nodeID, modelName string, replicaIndex int, backendType string, optsBlob []byte) error {
	return r.db.WithContext(ctx).Model(&NodeModel{}).
		Where("node_id = ? AND model_name = ? AND replica_index = ?", nodeID, modelName, replicaIndex).
		Updates(map[string]any{"backend_type": backendType, "model_opts_blob": optsBlob}).Error
}

// GetModelLoadInfo retrieves the stored backend type and serialized model
// options from any existing loaded replica. Returns gorm.ErrRecordNotFound
// if no replica has stored options.
func (r *NodeRegistry) GetModelLoadInfo(ctx context.Context, modelName string) (backendType string, optsBlob []byte, err error) {
	var nm NodeModel
	err = r.db.WithContext(ctx).
		Where("model_name = ? AND state = ? AND model_opts_blob IS NOT NULL", modelName, "loaded").
		First(&nm).Error
	if err != nil {
		return "", nil, err
	}
	return nm.BackendType, nm.ModelOptsBlob, nil
}

// RemoveNodeModel removes a single replica of a model from a node.
// replicaIndex must match the row to delete; passing 0 for single-replica
// scheduling preserves historical behavior. Removing siblings requires
// separate calls per index — there is no "remove all replicas" shortcut here
// to keep the contract explicit (probeLoadedModels and scaleDownIdle iterate
// per-row and must not orphan healthy siblings).
func (r *NodeRegistry) RemoveNodeModel(ctx context.Context, nodeID, modelName string, replicaIndex int) error {
	return r.db.WithContext(ctx).Where("node_id = ? AND model_name = ? AND replica_index = ?", nodeID, modelName, replicaIndex).
		Delete(&NodeModel{}).Error
}

// RemoveAllNodeModelReplicas removes every replica of modelName on nodeID.
// Used by callers (e.g. node deregistration, full backend stop) that genuinely
// want to clear all replicas, not just one.
func (r *NodeRegistry) RemoveAllNodeModelReplicas(ctx context.Context, nodeID, modelName string) error {
	return r.db.WithContext(ctx).Where("node_id = ? AND model_name = ?", nodeID, modelName).
		Delete(&NodeModel{}).Error
}

// FindNodesWithModel returns nodes that have the given model loaded.
func (r *NodeRegistry) FindNodesWithModel(ctx context.Context, modelName string) ([]BackendNode, error) {
	var nodes []BackendNode
	if err := r.db.WithContext(ctx).Joins("JOIN node_models ON node_models.node_id = backend_nodes.id").
		Where("node_models.model_name = ? AND node_models.state = ? AND backend_nodes.status = ?",
			modelName, "loaded", StatusHealthy).
		Order("node_models.in_flight ASC").
		Find(&nodes).Error; err != nil {
		return nil, fmt.Errorf("finding nodes with model %s: %w", modelName, err)
	}
	return nodes, nil
}

// FindAndLockNodeWithModel atomically finds the least-loaded node with the given
// model loaded and increments its in-flight counter within a single transaction.
// The SELECT FOR UPDATE row lock prevents concurrent eviction from removing the
// NodeModel row between the find and increment operations.
//
// When candidateNodeIDs is non-empty, only nodes in that set are considered.
// Pass nil (or empty) to consider any node. This lets callers pre-filter by
// NodeSelector so a cached replica on a now-excluded node isn't picked over a
// matching replica elsewhere — the selector-mismatch fall-through path used to
// trigger an eviction-busy loop when both sides had the model loaded.
func (r *NodeRegistry) FindAndLockNodeWithModel(ctx context.Context, modelName string, candidateNodeIDs []string) (*BackendNode, *NodeModel, error) {
	var nm NodeModel
	var node BackendNode

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Order by in_flight ASC (least busy replica), then by available_vram DESC
		// (prefer nodes with more free VRAM to spread load across the cluster).
		q := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Joins("JOIN backend_nodes ON backend_nodes.id = node_models.node_id").
			Where("node_models.model_name = ? AND node_models.state = ?", modelName, "loaded")
		if len(candidateNodeIDs) > 0 {
			q = q.Where("node_models.node_id IN ?", candidateNodeIDs)
		}
		if err := q.
			Order("node_models.in_flight ASC, backend_nodes.available_vram DESC").
			First(&nm).Error; err != nil {
			return err
		}

		if err := tx.Model(&nm).Updates(map[string]any{
			"in_flight": gorm.Expr("in_flight + 1"),
			"last_used": time.Now(),
		}).Error; err != nil {
			return err
		}

		if err := tx.Where("id = ? AND status = ?", nm.NodeID, StatusHealthy).
			First(&node).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return nil, nil, err
	}
	return &node, &nm, nil
}

// TouchNodeModel updates the last_used timestamp for LRU tracking on a single
// replica row.
func (r *NodeRegistry) TouchNodeModel(ctx context.Context, nodeID, modelName string, replicaIndex int) {
	r.db.WithContext(ctx).Model(&NodeModel{}).
		Where("node_id = ? AND model_name = ? AND replica_index = ?", nodeID, modelName, replicaIndex).
		Update("last_used", time.Now())
}

// GetNodeModel returns the NodeModel record for a specific (node, model, replica_index) combination.
func (r *NodeRegistry) GetNodeModel(ctx context.Context, nodeID, modelName string, replicaIndex int) (*NodeModel, error) {
	var nm NodeModel
	err := r.db.WithContext(ctx).
		Where("node_id = ? AND model_name = ? AND replica_index = ?", nodeID, modelName, replicaIndex).
		First(&nm).Error
	if err != nil {
		return nil, err
	}
	return &nm, nil
}

// CountReplicasOnNode returns how many replicas of modelName are currently
// recorded for nodeID (across all states). Used by NextFreeReplicaIndex and
// by capacity checks.
func (r *NodeRegistry) CountReplicasOnNode(ctx context.Context, nodeID, modelName string) (int, error) {
	var count int64
	if err := r.db.WithContext(ctx).Model(&NodeModel{}).
		Where("node_id = ? AND model_name = ?", nodeID, modelName).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
}

// ErrNoFreeSlot is returned by NextFreeReplicaIndex when the node already has
// MaxReplicasPerModel replicas of this model and cannot host another.
var ErrNoFreeSlot = errors.New("no free replica slot on node")

// NextFreeReplicaIndex returns the lowest replica_index in [0, maxSlots) that
// is not currently occupied by a row for (nodeID, modelName). Returns
// ErrNoFreeSlot if every index is taken.
//
// Allocating the lowest free index (rather than always appending) keeps slot
// numbers compact across scale-down/scale-up cycles, which matches the worker
// supervisor's port-recycling behavior in core/cli/worker.go (freePorts).
func (r *NodeRegistry) NextFreeReplicaIndex(ctx context.Context, nodeID, modelName string, maxSlots int) (int, error) {
	if maxSlots <= 0 {
		return 0, ErrNoFreeSlot
	}
	var taken []int
	if err := r.db.WithContext(ctx).Model(&NodeModel{}).
		Where("node_id = ? AND model_name = ?", nodeID, modelName).
		Pluck("replica_index", &taken).Error; err != nil {
		return 0, err
	}
	occupied := make(map[int]struct{}, len(taken))
	for _, idx := range taken {
		occupied[idx] = struct{}{}
	}
	for idx := 0; idx < maxSlots; idx++ {
		if _, ok := occupied[idx]; !ok {
			return idx, nil
		}
	}
	return 0, ErrNoFreeSlot
}

// FindLeastLoadedNode returns the healthy node with the fewest in-flight requests.
func (r *NodeRegistry) FindLeastLoadedNode(ctx context.Context) (*BackendNode, error) {
	db := r.db.WithContext(ctx)

	var node BackendNode
	query := db.Where("status = ? AND node_type = ?", StatusHealthy, NodeTypeBackend)
	// Order by total in-flight across all models on the node
	subquery := db.Model(&NodeModel{}).
		Select("node_id, COALESCE(SUM(in_flight), 0) as total_inflight").
		Group("node_id")

	err := query.Joins("LEFT JOIN (?) AS load ON load.node_id = backend_nodes.id", subquery).
		Order("COALESCE(load.total_inflight, 0) ASC, backend_nodes.available_vram DESC").
		First(&node).Error
	if err != nil {
		return nil, fmt.Errorf("finding least loaded node: %w", err)
	}
	return &node, nil
}

// FindIdleNode returns a healthy node with zero in-flight requests and zero loaded models.
// Used by the scheduler to prefer truly idle nodes for new backend assignments.
func (r *NodeRegistry) FindIdleNode(ctx context.Context) (*BackendNode, error) {
	db := r.db.WithContext(ctx)

	var node BackendNode
	loadedModels := db.Model(&NodeModel{}).
		Select("node_id").
		Where("state = ?", "loaded").
		Group("node_id")
	err := db.Where("status = ? AND node_type = ? AND id NOT IN (?)", StatusHealthy, NodeTypeBackend, loadedModels).
		Order("available_vram DESC").
		First(&node).Error
	if err != nil {
		return nil, err
	}
	return &node, nil
}

// IncrementInFlight atomically increments the in-flight counter on a single replica row.
func (r *NodeRegistry) IncrementInFlight(ctx context.Context, nodeID, modelName string, replicaIndex int) error {
	result := r.db.WithContext(ctx).Model(&NodeModel{}).
		Where("node_id = ? AND model_name = ? AND replica_index = ?", nodeID, modelName, replicaIndex).
		Updates(map[string]any{
			"in_flight": gorm.Expr("in_flight + 1"),
			"last_used": time.Now(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("node model %s/%s replica %d not found", nodeID, modelName, replicaIndex)
	}
	return nil
}

// DecrementInFlight atomically decrements the in-flight counter on a single replica row.
// Guarded by `in_flight > 0` so that double-decrements don't go negative.
func (r *NodeRegistry) DecrementInFlight(ctx context.Context, nodeID, modelName string, replicaIndex int) error {
	result := r.db.WithContext(ctx).Model(&NodeModel{}).
		Where("node_id = ? AND model_name = ? AND replica_index = ? AND in_flight > 0", nodeID, modelName, replicaIndex).
		UpdateColumn("in_flight", gorm.Expr("in_flight - 1"))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		xlog.Warn("DecrementInFlight: no matching row or already zero", "node", nodeID, "model", modelName, "replica", replicaIndex)
	}
	return nil
}

// GetNodeModels returns all models loaded on a given node.
func (r *NodeRegistry) GetNodeModels(ctx context.Context, nodeID string) ([]NodeModel, error) {
	var models []NodeModel
	if err := r.db.WithContext(ctx).Where("node_id = ?", nodeID).Find(&models).Error; err != nil {
		return nil, fmt.Errorf("getting models for node %s: %w", nodeID, err)
	}
	return models, nil
}

// ListAllLoadedModels returns all models that are loaded on healthy nodes.
// Used by DistributedModelStore.Range() to discover models not in local cache.
func (r *NodeRegistry) ListAllLoadedModels(ctx context.Context) ([]NodeModel, error) {
	var models []NodeModel
	err := r.db.WithContext(ctx).Joins("JOIN backend_nodes ON backend_nodes.id = node_models.node_id").
		Where("node_models.state = ? AND backend_nodes.status = ?", "loaded", StatusHealthy).
		Find(&models).Error
	if err != nil {
		return nil, fmt.Errorf("listing all loaded models: %w", err)
	}
	return models, nil
}

// FindNodeForModel returns the first healthy node that has the given model loaded.
// Returns the node and true if found, nil and false otherwise.
func (r *NodeRegistry) FindNodeForModel(ctx context.Context, modelName string) (*BackendNode, bool) {
	nodes, err := r.FindNodesWithModel(ctx, modelName)
	if err != nil || len(nodes) == 0 {
		return nil, false
	}
	return &nodes[0], true
}

// FindLRUModel returns the least-recently-used model on a node.
func (r *NodeRegistry) FindLRUModel(ctx context.Context, nodeID string) (*NodeModel, error) {
	var nm NodeModel
	err := r.db.WithContext(ctx).Where("node_id = ? AND state = ? AND in_flight = 0", nodeID, "loaded").
		Order("last_used ASC").First(&nm).Error
	if err != nil {
		return nil, fmt.Errorf("finding LRU model on node %s: %w", nodeID, err)
	}
	return &nm, nil
}

// FindGlobalLRUModelWithZeroInFlight returns the least-recently-used model
// across all healthy backend nodes that has zero in-flight requests.
// Used by the router for preemptive eviction when no node has free VRAM.
func (r *NodeRegistry) FindGlobalLRUModelWithZeroInFlight(ctx context.Context) (*NodeModel, error) {
	var nm NodeModel
	err := r.db.WithContext(ctx).Joins("JOIN backend_nodes ON backend_nodes.id = node_models.node_id").
		Where("node_models.state = ? AND node_models.in_flight = 0 AND backend_nodes.status = ? AND backend_nodes.node_type = ?",
			"loaded", StatusHealthy, NodeTypeBackend).
		Order("node_models.last_used ASC").
		First(&nm).Error
	if err != nil {
		return nil, fmt.Errorf("no evictable model found: %w", err)
	}
	return &nm, nil
}

// --- NodeLabel operations ---

// SetNodeLabel upserts a single label on a node.
//
// A label change can change which models match a NodeSelector, so any
// scheduling cooldown flag is cleared as a side effect — the next reconciler
// tick will re-flag if the new label set still doesn't satisfy capacity.
func (r *NodeRegistry) SetNodeLabel(ctx context.Context, nodeID, key, value string) error {
	label := NodeLabel{
		ID:     uuid.New().String(),
		NodeID: nodeID,
		Key:    key,
		Value:  value,
	}
	if err := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "node_id"}, {Name: "key"}},
			DoUpdates: clause.AssignmentColumns([]string{"value"}),
		}).
		Create(&label).Error; err != nil {
		return err
	}
	if err := r.ClearAllUnsatisfiable(ctx); err != nil {
		xlog.Warn("Failed to clear unsatisfiable scheduling flags on SetNodeLabel", "error", err)
	}
	return nil
}

// SetNodeLabels replaces all labels for a node with the given map.
func (r *NodeRegistry) SetNodeLabels(ctx context.Context, nodeID string, labels map[string]string) error {
	if err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("node_id = ?", nodeID).Delete(&NodeLabel{}).Error; err != nil {
			return err
		}
		for k, v := range labels {
			label := NodeLabel{ID: uuid.New().String(), NodeID: nodeID, Key: k, Value: v}
			if err := tx.Create(&label).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := r.ClearAllUnsatisfiable(ctx); err != nil {
		xlog.Warn("Failed to clear unsatisfiable scheduling flags on SetNodeLabels", "error", err)
	}
	return nil
}

// RemoveNodeLabel removes a single label from a node.
func (r *NodeRegistry) RemoveNodeLabel(ctx context.Context, nodeID, key string) error {
	if err := r.db.WithContext(ctx).Where("node_id = ? AND key = ?", nodeID, key).Delete(&NodeLabel{}).Error; err != nil {
		return err
	}
	if err := r.ClearAllUnsatisfiable(ctx); err != nil {
		xlog.Warn("Failed to clear unsatisfiable scheduling flags on RemoveNodeLabel", "error", err)
	}
	return nil
}

// GetNodeLabels returns all labels for a node.
func (r *NodeRegistry) GetNodeLabels(ctx context.Context, nodeID string) ([]NodeLabel, error) {
	var labels []NodeLabel
	err := r.db.WithContext(ctx).Where("node_id = ?", nodeID).Find(&labels).Error
	return labels, err
}

// GetAllNodeLabelsMap returns all labels grouped by node ID.
func (r *NodeRegistry) GetAllNodeLabelsMap(ctx context.Context) (map[string]map[string]string, error) {
	var labels []NodeLabel
	if err := r.db.WithContext(ctx).Find(&labels).Error; err != nil {
		return nil, err
	}
	result := make(map[string]map[string]string)
	for _, l := range labels {
		if result[l.NodeID] == nil {
			result[l.NodeID] = make(map[string]string)
		}
		result[l.NodeID][l.Key] = l.Value
	}
	return result, nil
}

// --- Selector-based queries ---

// FindNodesBySelector returns healthy backend nodes matching ALL key-value pairs in the selector.
func (r *NodeRegistry) FindNodesBySelector(ctx context.Context, selector map[string]string) ([]BackendNode, error) {
	if len(selector) == 0 {
		// Empty selector matches all healthy backend nodes
		var nodes []BackendNode
		err := r.db.WithContext(ctx).Where("status = ? AND node_type = ?", StatusHealthy, NodeTypeBackend).Find(&nodes).Error
		return nodes, err
	}

	db := r.db.WithContext(ctx).Where("status = ? AND node_type = ?", StatusHealthy, NodeTypeBackend)
	for k, v := range selector {
		db = db.Where("EXISTS (SELECT 1 FROM node_labels WHERE node_labels.node_id = backend_nodes.id AND node_labels.key = ? AND node_labels.value = ?)", k, v)
	}

	var nodes []BackendNode
	err := db.Find(&nodes).Error
	return nodes, err
}

// FindNodeWithVRAMFromSet is like FindNodeWithVRAM but restricted to the given node IDs.
func (r *NodeRegistry) FindNodeWithVRAMFromSet(ctx context.Context, minBytes uint64, nodeIDs []string) (*BackendNode, error) {
	db := r.db.WithContext(ctx)

	loadedModels := db.Model(&NodeModel{}).
		Select("node_id").
		Where("state = ?", "loaded").
		Group("node_id")

	subquery := db.Model(&NodeModel{}).
		Select("node_id, COALESCE(SUM(in_flight), 0) as total_inflight").
		Group("node_id")

	// Try idle nodes with enough effectively-free VRAM first.
	var node BackendNode
	err := db.Where("status = ? AND node_type = ? AND (available_vram - reserved_vram) >= ? AND id NOT IN (?) AND id IN ?",
		StatusHealthy, NodeTypeBackend, minBytes, loadedModels, nodeIDs).
		Order("(available_vram - reserved_vram) DESC").
		First(&node).Error
	if err == nil {
		return &node, nil
	}

	// Fall back to least-loaded nodes with enough effectively-free VRAM
	err = db.Where("status = ? AND node_type = ? AND (available_vram - reserved_vram) >= ? AND backend_nodes.id IN ?",
		StatusHealthy, NodeTypeBackend, minBytes, nodeIDs).
		Joins("LEFT JOIN (?) AS load ON load.node_id = backend_nodes.id", subquery).
		Order("COALESCE(load.total_inflight, 0) ASC, (backend_nodes.available_vram - backend_nodes.reserved_vram) DESC").
		First(&node).Error
	if err != nil {
		return nil, fmt.Errorf("no healthy nodes in set with %d bytes available VRAM: %w", minBytes, err)
	}
	return &node, nil
}

// FindIdleNodeFromSet is like FindIdleNode but restricted to the given node IDs.
func (r *NodeRegistry) FindIdleNodeFromSet(ctx context.Context, nodeIDs []string) (*BackendNode, error) {
	db := r.db.WithContext(ctx)

	var node BackendNode
	loadedModels := db.Model(&NodeModel{}).
		Select("node_id").
		Where("state = ?", "loaded").
		Group("node_id")
	err := db.Where("status = ? AND node_type = ? AND id NOT IN (?) AND id IN ?", StatusHealthy, NodeTypeBackend, loadedModels, nodeIDs).
		Order("available_vram DESC").
		First(&node).Error
	if err != nil {
		return nil, err
	}
	return &node, nil
}

// FindLeastLoadedNodeFromSet is like FindLeastLoadedNode but restricted to the given node IDs.
func (r *NodeRegistry) FindLeastLoadedNodeFromSet(ctx context.Context, nodeIDs []string) (*BackendNode, error) {
	db := r.db.WithContext(ctx)

	var node BackendNode
	query := db.Where("status = ? AND node_type = ? AND backend_nodes.id IN ?", StatusHealthy, NodeTypeBackend, nodeIDs)
	// Order by total in-flight across all models on the node
	subquery := db.Model(&NodeModel{}).
		Select("node_id, COALESCE(SUM(in_flight), 0) as total_inflight").
		Group("node_id")

	err := query.Joins("LEFT JOIN (?) AS load ON load.node_id = backend_nodes.id", subquery).
		Order("COALESCE(load.total_inflight, 0) ASC, backend_nodes.available_vram DESC").
		First(&node).Error
	if err != nil {
		return nil, fmt.Errorf("finding least loaded node in set: %w", err)
	}
	return &node, nil
}

// --- ModelSchedulingConfig operations ---

// SetModelScheduling creates or updates a scheduling config for a model.
func (r *NodeRegistry) SetModelScheduling(ctx context.Context, config *ModelSchedulingConfig) error {
	if config.ID == "" {
		config.ID = uuid.New().String()
	}
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "model_name"}},
			DoUpdates: clause.AssignmentColumns([]string{"node_selector", "min_replicas", "max_replicas", "updated_at"}),
		}).
		Create(config).Error
}

// GetModelScheduling returns the scheduling config for a model, or nil if none exists.
func (r *NodeRegistry) GetModelScheduling(ctx context.Context, modelName string) (*ModelSchedulingConfig, error) {
	var config ModelSchedulingConfig
	err := r.db.WithContext(ctx).Where("model_name = ?", modelName).First(&config).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &config, nil
}

// ListModelSchedulings returns all scheduling configs.
func (r *NodeRegistry) ListModelSchedulings(ctx context.Context) ([]ModelSchedulingConfig, error) {
	var configs []ModelSchedulingConfig
	err := r.db.WithContext(ctx).Order("model_name ASC").Find(&configs).Error
	return configs, err
}

// ListAutoScalingConfigs returns scheduling configs where auto-scaling is enabled.
func (r *NodeRegistry) ListAutoScalingConfigs(ctx context.Context) ([]ModelSchedulingConfig, error) {
	var configs []ModelSchedulingConfig
	err := r.db.WithContext(ctx).Where("min_replicas > 0 OR max_replicas > 0").Find(&configs).Error
	return configs, err
}

// DeleteModelScheduling removes a scheduling config by model name.
func (r *NodeRegistry) DeleteModelScheduling(ctx context.Context, modelName string) error {
	return r.db.WithContext(ctx).Where("model_name = ?", modelName).Delete(&ModelSchedulingConfig{}).Error
}

// CountLoadedReplicas returns the number of loaded replicas for a model.
func (r *NodeRegistry) CountLoadedReplicas(ctx context.Context, modelName string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&NodeModel{}).Where("model_name = ? AND state = ?", modelName, "loaded").Count(&count).Error
	return count, err
}

// FindNodesWithFreeSlot returns healthy backend nodes that have at least one
// free replica slot for modelName (i.e. count(node_models.*) for this model
// is strictly less than the node's MaxReplicasPerModel cap). When
// candidateNodeIDs is non-empty, only those nodes are considered.
//
// This is the candidate-pool used by SmartRouter.scheduleNewModel — without
// it, the scheduler would happily pick the same node for replica #2 even
// when that node already hosts replica #1, re-creating the original flap.
func (r *NodeRegistry) FindNodesWithFreeSlot(ctx context.Context, modelName string, candidateNodeIDs []string) ([]BackendNode, error) {
	q := r.db.WithContext(ctx).Model(&BackendNode{}).
		Where("status = ? AND node_type = ?", StatusHealthy, NodeTypeBackend)
	if len(candidateNodeIDs) > 0 {
		q = q.Where("id IN ?", candidateNodeIDs)
	}
	// Subquery: per-node count of loaded+loading replicas of this model.
	// We count any non-removed row (state != deleted) so a load in progress
	// counts against the cap and a second concurrent scale-up can't overshoot.
	subq := r.db.Model(&NodeModel{}).
		Select("node_id, COUNT(*) as cnt").
		Where("model_name = ?", modelName).
		Group("node_id")

	var out []BackendNode
	err := q.Joins("LEFT JOIN (?) AS rc ON rc.node_id = backend_nodes.id", subq).
		Where("COALESCE(rc.cnt, 0) < backend_nodes.max_replicas_per_model").
		Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("finding nodes with free slot for %s: %w", modelName, err)
	}
	return out, nil
}

// ClusterCapacityForModel returns the total free replica capacity for
// modelName across the candidate node set: Σ (max_replicas_per_model −
// current_replicas[n,m]). When candidateNodeIDs is empty all healthy backend
// nodes are considered.
//
// The reconciler uses this to bound MinReplicas at what the cluster can
// actually host, preventing the "scale-up forever" loop from #9XXX where a
// MinReplicas=2 with one worker × one slot churned the model every 30s.
func (r *NodeRegistry) ClusterCapacityForModel(ctx context.Context, modelName string, candidateNodeIDs []string) (int, error) {
	q := r.db.WithContext(ctx).Model(&BackendNode{}).
		Where("status = ? AND node_type = ?", StatusHealthy, NodeTypeBackend)
	if len(candidateNodeIDs) > 0 {
		q = q.Where("id IN ?", candidateNodeIDs)
	}
	subq := r.db.Model(&NodeModel{}).
		Select("node_id, COUNT(*) as cnt").
		Where("model_name = ?", modelName).
		Group("node_id")

	var nodes []struct {
		MaxReplicasPerModel int
		Loaded              int
	}
	err := q.Select("backend_nodes.max_replicas_per_model AS max_replicas_per_model, COALESCE(rc.cnt, 0) AS loaded").
		Joins("LEFT JOIN (?) AS rc ON rc.node_id = backend_nodes.id", subq).
		Scan(&nodes).Error
	if err != nil {
		return 0, fmt.Errorf("computing cluster capacity for %s: %w", modelName, err)
	}
	total := 0
	for _, n := range nodes {
		free := n.MaxReplicasPerModel - n.Loaded
		if free > 0 {
			total += free
		}
	}
	return total, nil
}

// BumpUnsatisfiableTicks increments the per-config hysteresis counter when
// the reconciler tries to scale up but cluster capacity is exhausted.
// Returns the new value.
func (r *NodeRegistry) BumpUnsatisfiableTicks(ctx context.Context, modelName string) (int, error) {
	res := r.db.WithContext(ctx).Model(&ModelSchedulingConfig{}).
		Where("model_name = ?", modelName).
		UpdateColumn("unsatisfiable_ticks", gorm.Expr("unsatisfiable_ticks + 1"))
	if res.Error != nil {
		return 0, res.Error
	}
	var cfg ModelSchedulingConfig
	if err := r.db.WithContext(ctx).Where("model_name = ?", modelName).First(&cfg).Error; err != nil {
		return 0, err
	}
	return cfg.UnsatisfiableTicks, nil
}

// MarkUnsatisfiable sets UnsatisfiableUntil to a future time, so the
// reconciler skips scale-up attempts for this model until the cooldown
// expires (or a cluster event clears the flag — see ClearAllUnsatisfiable).
func (r *NodeRegistry) MarkUnsatisfiable(ctx context.Context, modelName string, until time.Time) error {
	return r.db.WithContext(ctx).Model(&ModelSchedulingConfig{}).
		Where("model_name = ?", modelName).
		Update("unsatisfiable_until", until).Error
}

// ClearUnsatisfiable resets both the cooldown timestamp and the hysteresis
// counter for a single model. Called on a successful scale-up so the next
// transient capacity dip starts the hysteresis from zero.
func (r *NodeRegistry) ClearUnsatisfiable(ctx context.Context, modelName string) error {
	return r.db.WithContext(ctx).Model(&ModelSchedulingConfig{}).
		Where("model_name = ?", modelName).
		Updates(map[string]any{
			"unsatisfiable_until": gorm.Expr("NULL"),
			"unsatisfiable_ticks": 0,
		}).Error
}

// UpdateMaxReplicasPerModel sets a node's per-model replica cap as an admin
// override (sticky across worker restarts) and refreshes the mirrored
// `node.replica-slots` auto-label so selectors reflect the new value.
// Capacity may have just changed, so cooldown flags are cleared too — the
// next reconciler tick will re-flag if still unsatisfiable.
//
// The override is preserved on worker re-registration (see Register). To
// hand control back to the worker flag, call ResetMaxReplicasPerModel.
func (r *NodeRegistry) UpdateMaxReplicasPerModel(ctx context.Context, nodeID string, n int) error {
	if n < 1 {
		return fmt.Errorf("max_replicas_per_model must be >= 1, got %d", n)
	}
	res := r.db.WithContext(ctx).Model(&BackendNode{}).
		Where("id = ?", nodeID).
		Updates(map[string]any{
			ColMaxReplicasPerModel:    n,
			"max_replicas_per_model_manually_set": true,
		})
	if res.Error != nil {
		return fmt.Errorf("updating max_replicas_per_model on %s: %w", nodeID, res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("node %s not found", nodeID)
	}
	// Keep the auto-label in sync so existing AND-selectors keep matching.
	if err := r.SetNodeLabel(ctx, nodeID, "node.replica-slots", fmt.Sprintf("%d", n)); err != nil {
		xlog.Warn("Failed to refresh node.replica-slots label", "node", nodeID, "error", err)
	}
	if err := r.ClearAllUnsatisfiable(ctx); err != nil {
		xlog.Warn("Failed to clear unsatisfiable scheduling flags after capacity update", "error", err)
	}
	return nil
}

// ResetMaxReplicasPerModel clears the admin override flag so the next worker
// re-registration is allowed to update the value again. The current value is
// left in place — the worker will overwrite it on its next register call.
//
// This is the "Reset to worker default" affordance in the UI: it doesn't
// require knowing what the worker flag is set to (the worker tells us on
// re-register), it just hands ownership back.
func (r *NodeRegistry) ResetMaxReplicasPerModel(ctx context.Context, nodeID string) error {
	res := r.db.WithContext(ctx).Model(&BackendNode{}).
		Where("id = ?", nodeID).
		Update("max_replicas_per_model_manually_set", false)
	if res.Error != nil {
		return fmt.Errorf("clearing max_replicas_per_model override on %s: %w", nodeID, res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("node %s not found", nodeID)
	}
	return nil
}

// ClearAllUnsatisfiable clears the cooldown flag on every scheduling config.
// Called from cluster-events that could plausibly increase capacity (new
// node registers, node approves pending→healthy, node labels change,
// MaxReplicasPerModel changes). The reconciler's own loop will re-flag any
// config whose target is still unsatisfiable, so over-clearing is cheap and
// correct.
func (r *NodeRegistry) ClearAllUnsatisfiable(ctx context.Context) error {
	return r.db.WithContext(ctx).Model(&ModelSchedulingConfig{}).
		Where("unsatisfiable_until IS NOT NULL OR unsatisfiable_ticks > 0").
		Updates(map[string]any{
			"unsatisfiable_until": gorm.Expr("NULL"),
			"unsatisfiable_ticks": 0,
		}).Error
}

// --- Composite queries ---

// ListWithExtras returns all nodes with model counts and labels.
func (r *NodeRegistry) ListWithExtras(ctx context.Context) ([]NodeWithExtras, error) {
	// Get all nodes
	var nodes []BackendNode
	if err := r.db.WithContext(ctx).Order("name ASC").Find(&nodes).Error; err != nil {
		return nil, err
	}

	// Get model counts per node
	type modelCount struct {
		NodeID string
		Count  int
	}
	var counts []modelCount
	if err := r.db.WithContext(ctx).Model(&NodeModel{}).
		Select("node_id, COUNT(*) as count").
		Where("state = ?", "loaded").
		Group("node_id").
		Find(&counts).Error; err != nil {
		xlog.Warn("ListWithExtras: failed to get model counts", "error", err)
	}

	countMap := make(map[string]int)
	for _, c := range counts {
		countMap[c.NodeID] = c.Count
	}

	// Get in-flight counts per node
	type inFlightCount struct {
		NodeID string
		Total  int
	}
	var inFlights []inFlightCount
	if err := r.db.WithContext(ctx).Model(&NodeModel{}).
		Select("node_id, COALESCE(SUM(in_flight), 0) as total").
		Where("state IN ?", []string{"loaded", "unloading"}).
		Group("node_id").
		Find(&inFlights).Error; err != nil {
		xlog.Warn("ListWithExtras: failed to get in-flight counts", "error", err)
	}

	inFlightMap := make(map[string]int)
	for _, f := range inFlights {
		inFlightMap[f.NodeID] = f.Total
	}

	// Get all labels
	labelsMap, err := r.GetAllNodeLabelsMap(ctx)
	if err != nil {
		xlog.Warn("ListWithExtras: failed to get labels", "error", err)
	}

	// Build result
	result := make([]NodeWithExtras, len(nodes))
	for i, n := range nodes {
		result[i] = NodeWithExtras{
			BackendNode:   n,
			ModelCount:    countMap[n.ID],
			InFlightCount: inFlightMap[n.ID],
			Labels:        labelsMap[n.ID],
		}
	}
	return result, nil
}

// ApplyAutoLabels sets automatic labels based on node hardware info.
func (r *NodeRegistry) ApplyAutoLabels(ctx context.Context, nodeID string, node *BackendNode) {
	if node.GPUVendor != "" {
		_ = r.SetNodeLabel(ctx, nodeID, "gpu.vendor", node.GPUVendor)
	}
	if node.TotalVRAM > 0 {
		gb := node.TotalVRAM / (1024 * 1024 * 1024)
		var bucket string
		switch {
		case gb >= 80:
			bucket = "80GB+"
		case gb >= 48:
			bucket = "48GB"
		case gb >= 24:
			bucket = "24GB"
		case gb >= 16:
			bucket = "16GB"
		case gb >= 8:
			bucket = "8GB"
		default:
			bucket = fmt.Sprintf("%dGB", gb)
		}
		_ = r.SetNodeLabel(ctx, nodeID, "gpu.vram", bucket)
	}
	if node.Name != "" {
		_ = r.SetNodeLabel(ctx, nodeID, "node.name", node.Name)
	}
	// Mirror the typed MaxReplicasPerModel field as a label so the existing
	// AND-selector machinery in ModelSchedulingConfig can target high-capacity
	// nodes (e.g. {"node.replica-slots": "4"}). Always set it (default 1) so
	// selectors don't have to special-case missing labels.
	slots := node.MaxReplicasPerModel
	if slots < 1 {
		slots = 1
	}
	_ = r.SetNodeLabel(ctx, nodeID, "node.replica-slots", fmt.Sprintf("%d", slots))
}

// UpsertPendingBackendOp records or refreshes a pending backend operation for
// a node. If a row already exists for (nodeID, backend, op) we keep its
// Attempts/LastError but reset NextRetryAt to now, so reissuing the same
// delete/upgrade nudges it to the front of the queue instead of stacking a
// duplicate intent.
func (r *NodeRegistry) UpsertPendingBackendOp(ctx context.Context, nodeID, backend, op string, galleries []byte) error {
	row := PendingBackendOp{
		NodeID:      nodeID,
		Backend:     backend,
		Op:          op,
		Galleries:   galleries,
		NextRetryAt: time.Now(),
	}
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "node_id"}, {Name: "backend"}, {Name: "op"}},
		DoUpdates: clause.AssignmentColumns([]string{"galleries", "next_retry_at"}),
	}).Create(&row).Error
}

// ListDuePendingBackendOps returns queued ops whose NextRetryAt has passed
// AND whose node is currently healthy. The reconciler drains this list; we
// filter by node status in the query so a tick doesn't hammer NATS for
// nodes that obviously can't answer.
func (r *NodeRegistry) ListDuePendingBackendOps(ctx context.Context) ([]PendingBackendOp, error) {
	var ops []PendingBackendOp
	err := r.db.WithContext(ctx).
		Joins("JOIN backend_nodes ON backend_nodes.id = pending_backend_ops.node_id").
		Where("pending_backend_ops.next_retry_at <= ? AND backend_nodes.status = ?", time.Now(), StatusHealthy).
		Order("pending_backend_ops.next_retry_at ASC").
		Find(&ops).Error
	if err != nil {
		return nil, fmt.Errorf("listing due pending backend ops: %w", err)
	}
	return ops, nil
}

// ListPendingBackendOps returns every queued row (for the UI "pending on N
// nodes" chip and the pre-delete ConfirmDialog).
func (r *NodeRegistry) ListPendingBackendOps(ctx context.Context) ([]PendingBackendOp, error) {
	var ops []PendingBackendOp
	if err := r.db.WithContext(ctx).Order("backend ASC, created_at ASC").Find(&ops).Error; err != nil {
		return nil, fmt.Errorf("listing pending backend ops: %w", err)
	}
	return ops, nil
}

// DeletePendingBackendOp removes a queue row — called after the op succeeds.
func (r *NodeRegistry) DeletePendingBackendOp(ctx context.Context, id uint) error {
	if err := r.db.WithContext(ctx).Delete(&PendingBackendOp{}, id).Error; err != nil {
		return fmt.Errorf("deleting pending backend op %d: %w", id, err)
	}
	return nil
}

// RecordPendingBackendOpFailure bumps Attempts, captures the error, and
// pushes NextRetryAt out with exponential backoff capped at 15 minutes.
func (r *NodeRegistry) RecordPendingBackendOpFailure(ctx context.Context, id uint, errMsg string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row PendingBackendOp
		if err := tx.First(&row, id).Error; err != nil {
			return err
		}
		row.Attempts++
		row.LastError = errMsg
		row.NextRetryAt = time.Now().Add(backoffForAttempt(row.Attempts))
		return tx.Save(&row).Error
	})
}

// backoffForAttempt is exponential from 30s doubling up to a 15m cap. The
// reconciler tick is 30s so anything shorter would just re-fire immediately.
func backoffForAttempt(attempts int) time.Duration {
	const cap = 15 * time.Minute
	base := 30 * time.Second
	shift := attempts - 1
	if shift < 0 {
		shift = 0
	}
	if shift > 10 { // 2^10 * 30s already exceeds the cap
		shift = 10
	}
	d := base << shift
	if d > cap {
		return cap
	}
	return d
}

// CountPendingBackendOpsByBackend returns a map of backend name to the count
// of pending rows. Used to decorate Manage → Backends with a "pending on N
// nodes" chip without exposing the full queue.
func (r *NodeRegistry) CountPendingBackendOpsByBackend(ctx context.Context) (map[string]int, error) {
	type row struct {
		Backend string
		Count   int
	}
	var rows []row
	err := r.db.WithContext(ctx).Model(&PendingBackendOp{}).
		Select("backend, COUNT(*) as count").
		Group("backend").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("counting pending backend ops: %w", err)
	}
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[r.Backend] = r.Count
	}
	return out, nil
}
