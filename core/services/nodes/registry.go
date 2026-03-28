package nodes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/services/advisorylock"
	"github.com/mudler/LocalAI/core/services/messaging"
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
	NodeType      string    `gorm:"size:32;default:backend" json:"node_type"` // backend, agent
	Address       string    `gorm:"size:255" json:"address"`             // host:port for gRPC
	HTTPAddress   string    `gorm:"size:255" json:"http_address"`        // host:port for HTTP file transfer
	Status        string    `gorm:"size:32;default:registering" json:"status"` // registering, healthy, unhealthy, draining, pending
	TokenHash     string    `gorm:"size:64" json:"-"`                    // SHA-256 of registration token
	TotalVRAM     uint64    `gorm:"column:total_vram" json:"total_vram"`           // Total GPU VRAM in bytes
	AvailableVRAM uint64    `gorm:"column:available_vram" json:"available_vram"`   // Available GPU VRAM in bytes
	TotalRAM      uint64    `gorm:"column:total_ram" json:"total_ram"`             // Total system RAM in bytes (fallback when no GPU)
	AvailableRAM  uint64    `gorm:"column:available_ram" json:"available_ram"`     // Available system RAM in bytes
	GPUVendor     string    `gorm:"column:gpu_vendor;size:32" json:"gpu_vendor"`   // nvidia, amd, intel, vulkan, unknown
	APIKeyID      string    `gorm:"size:36" json:"-"`                    // auto-provisioned API key ID (for cleanup)
	AuthUserID    string    `gorm:"size:36" json:"-"`                    // auto-provisioned user ID (for cleanup)
	LastHeartbeat time.Time `gorm:"column:last_heartbeat" json:"last_heartbeat"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
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
	ColAvailableVRAM = "available_vram"
	ColTotalVRAM     = "total_vram"
	ColAvailableRAM  = "available_ram"
	ColGPUVendor     = "gpu_vendor"
	ColLastHeartbeat = "last_heartbeat"
)

// NodeModel tracks which models are loaded on which nodes.
type NodeModel struct {
	ID        string    `gorm:"primaryKey;size:36" json:"id"`
	NodeID    string    `gorm:"index;size:36" json:"node_id"`
	ModelName string    `gorm:"index;size:255" json:"model_name"`
	Address   string    `gorm:"size:255" json:"address"`           // gRPC address for this model's backend process
	State     string    `gorm:"size:32;default:idle" json:"state"` // loading, loaded, unloading, idle
	InFlight  int       `json:"in_flight"`                         // number of active requests
	LastUsed  time.Time `json:"last_used"`
	LoadingBy string    `gorm:"size:36" json:"loading_by,omitempty"` // frontend ID that triggered loading
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NodeRegistry manages backend node registration and lookup in PostgreSQL.
type NodeRegistry struct {
	db *gorm.DB
}

// NewNodeRegistry creates a NodeRegistry and auto-migrates the schema.
// Uses a PostgreSQL advisory lock to prevent concurrent migration races
// when multiple instances (frontend + workers) start at the same time.
func NewNodeRegistry(db *gorm.DB) (*NodeRegistry, error) {
	if err := advisorylock.WithLock(db, messaging.AdvisoryLockSchemaMigrate, func() error {
		return db.AutoMigrate(&BackendNode{}, &NodeModel{})
	}); err != nil {
		return nil, fmt.Errorf("migrating node tables: %w", err)
	}
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
		if err := r.db.WithContext(ctx).Model(&existing).Updates(node).Error; err != nil {
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
	return nil
}

// MarkOffline sets a node to offline status and clears its model records.
// Used on graceful shutdown — preserves the node row so re-registration
// can restore the previous approval status.
func (r *NodeRegistry) MarkOffline(ctx context.Context, nodeID string) error {
	result := r.db.WithContext(ctx).Model(&BackendNode{}).Where("id = ?", nodeID).Update("status", StatusOffline)
	if result.Error != nil {
		return fmt.Errorf("marking node %s offline: %w", nodeID, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("node %s not found", nodeID)
	}
	// Clear model records — node is shutting down
	if err := r.db.WithContext(ctx).Where("node_id = ?", nodeID).Delete(&NodeModel{}).Error; err != nil {
		xlog.Warn("Failed to clear model records on offline", "node", nodeID, "error", err)
	}
	return nil
}

// FindNodeWithVRAM returns healthy nodes with at least minBytes available VRAM,
// ordered idle-first then least-loaded.
func (r *NodeRegistry) FindNodeWithVRAM(ctx context.Context, minBytes uint64) (*BackendNode, error) {
	db := r.db.WithContext(ctx)

	loadedModels := db.Model(&NodeModel{}).
		Select("node_id").
		Where("state = ?", "loaded").
		Group("node_id")

	subquery := db.Model(&NodeModel{}).
		Select("node_id, COALESCE(SUM(in_flight), 0) as total_inflight").
		Group("node_id")

	// Try idle nodes with enough VRAM first
	var node BackendNode
	err := db.Where("status = ? AND node_type = ? AND available_vram >= ? AND id NOT IN (?)", StatusHealthy, NodeTypeBackend, minBytes, loadedModels).
		First(&node).Error
	if err == nil {
		return &node, nil
	}

	// Fall back to least-loaded nodes with enough VRAM
	err = db.Where("status = ? AND node_type = ? AND available_vram >= ?", StatusHealthy, NodeTypeBackend, minBytes).
		Joins("LEFT JOIN (?) AS load ON load.node_id = backend_nodes.id", subquery).
		Order("COALESCE(load.total_inflight, 0) ASC").
		First(&node).Error
	if err != nil {
		return nil, fmt.Errorf("no healthy nodes with %d bytes available VRAM: %w", minBytes, err)
	}
	return &node, nil
}

// Deregister removes a backend node, its model associations, and any auto-provisioned auth credentials.
func (r *NodeRegistry) Deregister(ctx context.Context, nodeID string) error {
	db := r.db.WithContext(ctx)

	// Read the node first to get auth references for cleanup
	var node BackendNode
	if err := db.Where("id = ?", nodeID).First(&node).Error; err != nil {
		return fmt.Errorf("node %s not found: %w", nodeID, err)
	}

	tx := db.Begin()
	if err := tx.Where("node_id = ?", nodeID).Delete(&NodeModel{}).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("deleting node models for %s: %w", nodeID, err)
	}
	if err := tx.Where("id = ?", nodeID).Delete(&BackendNode{}).Error; err != nil {
		tx.Rollback()
		return fmt.Errorf("deleting node %s: %w", nodeID, err)
	}

	// Clean up auto-provisioned auth user (cascades to API keys via FK)
	if node.AuthUserID != "" {
		tx.Exec("SAVEPOINT auth_cleanup")
		if err := tx.Exec("DELETE FROM users WHERE id = ?", node.AuthUserID).Error; err != nil {
			tx.Exec("ROLLBACK TO SAVEPOINT auth_cleanup")
			xlog.Warn("Failed to clean up agent worker user", "node", node.Name, "userID", node.AuthUserID, "error", err)
		}
	}

	return tx.Commit().Error
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
	return r.db.WithContext(ctx).Model(&BackendNode{}).Where("id = ?", nodeID).
		Update("status", StatusUnhealthy).Error
}

// MarkDraining sets a node status to draining (no new requests).
func (r *NodeRegistry) MarkDraining(ctx context.Context, nodeID string) error {
	return r.db.WithContext(ctx).Model(&BackendNode{}).Where("id = ?", nodeID).
		Update("status", StatusDraining).Error
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

// SetNodeModel records that a model is loaded on a node.
func (r *NodeRegistry) SetNodeModel(ctx context.Context, nodeID, modelName, state string, address ...string) error {
	addr := ""
	if len(address) > 0 {
		addr = address[0]
	}
	nm := &NodeModel{
		ID:        uuid.New().String(),
		NodeID:    nodeID,
		ModelName: modelName,
		Address:   addr,
		State:     state,
		LastUsed:  time.Now(),
	}
	result := r.db.WithContext(ctx).Where("node_id = ? AND model_name = ?", nodeID, modelName).
		Assign(nm).FirstOrCreate(nm)
	return result.Error
}

// RemoveNodeModel removes a model association from a node.
func (r *NodeRegistry) RemoveNodeModel(ctx context.Context, nodeID, modelName string) error {
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
func (r *NodeRegistry) FindAndLockNodeWithModel(ctx context.Context, modelName string) (*BackendNode, *NodeModel, error) {
	var nm NodeModel
	var node BackendNode

	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("model_name = ? AND state = ?", modelName, "loaded").
			Order("in_flight ASC").
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

// TouchNodeModel updates the last_used timestamp for LRU tracking.
func (r *NodeRegistry) TouchNodeModel(ctx context.Context, nodeID, modelName string) {
	r.db.WithContext(ctx).Model(&NodeModel{}).Where("node_id = ? AND model_name = ?", nodeID, modelName).
		Update("last_used", time.Now())
}

// GetNodeModel returns the NodeModel record for a specific node+model combination.
func (r *NodeRegistry) GetNodeModel(ctx context.Context, nodeID, modelName string) (*NodeModel, error) {
	var nm NodeModel
	err := r.db.WithContext(ctx).Where("node_id = ? AND model_name = ?", nodeID, modelName).First(&nm).Error
	if err != nil {
		return nil, err
	}
	return &nm, nil
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
		Order("COALESCE(load.total_inflight, 0) ASC").
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
		First(&node).Error
	if err != nil {
		return nil, err
	}
	return &node, nil
}

// IncrementInFlight atomically increments the in-flight counter for a model on a node.
func (r *NodeRegistry) IncrementInFlight(ctx context.Context, nodeID, modelName string) error {
	return r.db.WithContext(ctx).Model(&NodeModel{}).
		Where("node_id = ? AND model_name = ?", nodeID, modelName).
		Updates(map[string]any{
			"in_flight": gorm.Expr("in_flight + 1"),
			"last_used": time.Now(),
		}).Error
}

// DecrementInFlight atomically decrements the in-flight counter for a model on a node.
func (r *NodeRegistry) DecrementInFlight(ctx context.Context, nodeID, modelName string) error {
	return r.db.WithContext(ctx).Model(&NodeModel{}).
		Where("node_id = ? AND model_name = ? AND in_flight > 0", nodeID, modelName).
		UpdateColumn("in_flight", gorm.Expr("in_flight - 1")).Error
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
