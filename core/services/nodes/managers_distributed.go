package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
	"github.com/nats-io/nats.go"
)

// DistributedModelManager wraps a local ModelManager and adds NATS fan-out
// for model deletion so worker nodes clean up stale files.
type DistributedModelManager struct {
	local   galleryop.ModelManager
	adapter *RemoteUnloaderAdapter
}

// NewDistributedModelManager creates a DistributedModelManager.
// Backend auto-install is disabled because the frontend node delegates
// inference to workers and never runs backends locally.
func NewDistributedModelManager(appConfig *config.ApplicationConfig, ml *model.ModelLoader, adapter *RemoteUnloaderAdapter) *DistributedModelManager {
	local := galleryop.NewLocalModelManager(appConfig, ml)
	local.SetAutoInstallBackend(false)
	return &DistributedModelManager{
		local:   local,
		adapter: adapter,
	}
}

func (d *DistributedModelManager) DeleteModel(name string) error {
	err := d.local.DeleteModel(name)
	// Best-effort: fan out model.delete to worker nodes
	if rcErr := d.adapter.DeleteModelFiles(name); rcErr != nil {
		xlog.Warn("Failed to propagate model file deletion to workers", "model", name, "error", rcErr)
	}
	return err
}

func (d *DistributedModelManager) InstallModel(ctx context.Context, op *galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig], progressCb galleryop.ProgressCallback) error {
	return d.local.InstallModel(ctx, op, progressCb)
}

// DistributedBackendManager wraps a local BackendManager and adds NATS fan-out
// for backend deletion so worker nodes clean up stale files.
type DistributedBackendManager struct {
	local            galleryop.BackendManager
	adapter          *RemoteUnloaderAdapter
	registry         *NodeRegistry
	backendGalleries []config.Gallery
	systemState      *system.SystemState
}

// NewDistributedBackendManager creates a DistributedBackendManager.
func NewDistributedBackendManager(appConfig *config.ApplicationConfig, ml *model.ModelLoader, adapter *RemoteUnloaderAdapter, registry *NodeRegistry) *DistributedBackendManager {
	return &DistributedBackendManager{
		local:            galleryop.NewLocalBackendManager(appConfig, ml),
		adapter:          adapter,
		registry:         registry,
		backendGalleries: appConfig.BackendGalleries,
		systemState:      appConfig.SystemState,
	}
}

// NodeOpStatus is the per-node outcome of a backend lifecycle operation.
// Returned as part of BackendOpResult so the frontend can surface exactly
// what happened on each worker instead of a single joined error string.
type NodeOpStatus struct {
	NodeID   string `json:"node_id"`
	NodeName string `json:"node_name"`
	Status   string `json:"status"` // "success" | "queued" | "error"
	Error    string `json:"error,omitempty"`
}

// BackendOpResult aggregates per-node outcomes.
type BackendOpResult struct {
	Nodes []NodeOpStatus `json:"nodes"`
}

// Err returns a non-nil error aggregating per-node hard failures
// (Status == "error"). Queued nodes (waiting for reconciler retry) are not
// failures — surfacing them as errors would mislead users about durable
// intent. Used by Install/Upgrade/Delete so reply.Success=false from
// workers reaches OpStatus.Error and the UI, instead of being silently
// dropped on the way up.
func (r BackendOpResult) Err() error {
	var failures []string
	for _, n := range r.Nodes {
		if n.Status == "error" {
			failures = append(failures, fmt.Sprintf("%s: %s", n.NodeName, n.Error))
		}
	}
	if len(failures) == 0 {
		return nil
	}
	return errors.New(strings.Join(failures, "; "))
}

// enqueueAndDrainBackendOp is the shared scaffolding for
// delete/install/upgrade. Every non-pending node gets a pending_backend_ops
// row (intent is durable even if the node is offline). Currently-healthy
// nodes get an immediate attempt; success deletes the row, failure records
// the error and leaves the row for the reconciler to retry.
//
// `apply` is the NATS round-trip for one node. Returning an error keeps the
// row in the queue and marks the per-node status as "error"; returning nil
// deletes the row and reports "success". For non-healthy nodes the status
// is "queued" — no attempt is made right now, reconciler will pick it up
// when the node returns.
func (d *DistributedBackendManager) enqueueAndDrainBackendOp(ctx context.Context, op, backend string, galleriesJSON []byte, apply func(node BackendNode) error) (BackendOpResult, error) {
	allNodes, err := d.registry.List(ctx)
	if err != nil {
		return BackendOpResult{}, err
	}

	result := BackendOpResult{Nodes: make([]NodeOpStatus, 0, len(allNodes))}
	for _, node := range allNodes {
		// Pending nodes haven't been approved yet — no intent to apply.
		if node.Status == StatusPending {
			continue
		}
		// Backend lifecycle ops only make sense on backend-type workers.
		// Agent workers don't subscribe to backend.install/delete/list, so
		// enqueueing for them guarantees a forever-retrying row that the
		// reconciler can never drain. Silently skip — they aren't consumers.
		if node.NodeType != "" && node.NodeType != NodeTypeBackend {
			continue
		}
		if err := d.registry.UpsertPendingBackendOp(ctx, node.ID, backend, op, galleriesJSON); err != nil {
			xlog.Warn("Failed to enqueue backend op", "op", op, "node", node.Name, "backend", backend, "error", err)
			result.Nodes = append(result.Nodes, NodeOpStatus{
				NodeID: node.ID, NodeName: node.Name, Status: "error",
				Error: fmt.Sprintf("enqueue failed: %v", err),
			})
			continue
		}

		if node.Status != StatusHealthy {
			// Intent is recorded; reconciler will retry when the node recovers.
			result.Nodes = append(result.Nodes, NodeOpStatus{
				NodeID: node.ID, NodeName: node.Name, Status: "queued",
				Error: fmt.Sprintf("node %s, will retry when healthy", node.Status),
			})
			continue
		}

		applyErr := apply(node)
		if applyErr == nil {
			// Find the row we just upserted and delete it; cheap but requires
			// a lookup since UpsertPendingBackendOp doesn't return the ID.
			if err := d.deletePendingRow(ctx, node.ID, backend, op); err != nil {
				xlog.Debug("Failed to clear pending backend op after success", "error", err)
			}
			result.Nodes = append(result.Nodes, NodeOpStatus{
				NodeID: node.ID, NodeName: node.Name, Status: "success",
			})
			continue
		}

		// Record failure for backoff. If it's an ErrNoResponders, the node's
		// gone AWOL — mark unhealthy so the router stops picking it too.
		errMsg := applyErr.Error()
		if errors.Is(applyErr, nats.ErrNoResponders) {
			xlog.Warn("No NATS responders for node, marking unhealthy", "node", node.Name, "nodeID", node.ID)
			d.registry.MarkUnhealthy(ctx, node.ID)
		}
		if id, err := d.findPendingRow(ctx, node.ID, backend, op); err == nil {
			_ = d.registry.RecordPendingBackendOpFailure(ctx, id, errMsg)
		}
		result.Nodes = append(result.Nodes, NodeOpStatus{
			NodeID: node.ID, NodeName: node.Name, Status: "error", Error: errMsg,
		})
	}
	return result, nil
}

// findPendingRow looks up the ID of a pending_backend_ops row by its
// composite key. Used to hand off to RecordPendingBackendOpFailure /
// DeletePendingBackendOp after UpsertPendingBackendOp upserts by the same
// composite key.
func (d *DistributedBackendManager) findPendingRow(ctx context.Context, nodeID, backend, op string) (uint, error) {
	var row PendingBackendOp
	if err := d.registry.db.WithContext(ctx).
		Where("node_id = ? AND backend = ? AND op = ?", nodeID, backend, op).
		First(&row).Error; err != nil {
		return 0, err
	}
	return row.ID, nil
}

// deletePendingRow removes the queue row keyed by (nodeID, backend, op).
func (d *DistributedBackendManager) deletePendingRow(ctx context.Context, nodeID, backend, op string) error {
	return d.registry.db.WithContext(ctx).
		Where("node_id = ? AND backend = ? AND op = ?", nodeID, backend, op).
		Delete(&PendingBackendOp{}).Error
}

// DeleteBackend fans out backend deletion to every known node. The previous
// implementation silently skipped non-healthy nodes, which meant zombies
// reappeared once those nodes returned. Now the intent is durable — see
// enqueueAndDrainBackendOp — and the reconciler catches up later.
func (d *DistributedBackendManager) DeleteBackend(name string) error {
	// Local delete first (frontend rarely has backends installed in
	// distributed mode, but the gallery operation still expects it; ignore
	// "not found" which is the common case).
	if err := d.local.DeleteBackend(name); err != nil {
		if !errors.Is(err, gallery.ErrBackendNotFound) {
			return err
		}
		xlog.Debug("Backend not found locally, will attempt deletion on workers", "backend", name)
	}

	ctx := context.Background()
	result, err := d.enqueueAndDrainBackendOp(ctx, OpBackendDelete, name, nil, func(node BackendNode) error {
		reply, err := d.adapter.DeleteBackend(node.ID, name)
		if err != nil {
			return err
		}
		if !reply.Success {
			return fmt.Errorf("delete failed: %s", reply.Error)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return result.Err()
}

// DeleteBackendDetailed is the per-node-result variant called by the HTTP
// handler so the UI can render a per-node status drawer. DeleteBackend still
// returns error-only for callers that don't care about node breakdown.
func (d *DistributedBackendManager) DeleteBackendDetailed(ctx context.Context, name string) (BackendOpResult, error) {
	if err := d.local.DeleteBackend(name); err != nil && !errors.Is(err, gallery.ErrBackendNotFound) {
		return BackendOpResult{}, err
	}
	return d.enqueueAndDrainBackendOp(ctx, OpBackendDelete, name, nil, func(node BackendNode) error {
		reply, err := d.adapter.DeleteBackend(node.ID, name)
		if err != nil {
			return err
		}
		if !reply.Success {
			return fmt.Errorf("delete failed: %s", reply.Error)
		}
		return nil
	})
}

// ListBackends aggregates installed backends from all worker nodes, preserving
// per-node attribution. Each SystemBackend.Nodes entry records which node has
// the backend and the version/digest it reports. The top-level Metadata is
// populated from the first node seen so single-node-minded callers still work.
//
// Pending/offline/draining nodes are skipped because they aren't expected to
// answer NATS requests; unhealthy nodes are still queried — ErrNoResponders
// then marks them unhealthy and the loop continues.
func (d *DistributedBackendManager) ListBackends() (gallery.SystemBackends, error) {
	result := make(gallery.SystemBackends)
	allNodes, err := d.registry.List(context.Background())
	if err != nil {
		return result, err
	}

	for _, node := range allNodes {
		if node.Status == StatusPending || node.Status == StatusOffline || node.Status == StatusDraining {
			continue
		}
		reply, err := d.adapter.ListBackends(node.ID)
		if err != nil {
			if errors.Is(err, nats.ErrNoResponders) {
				xlog.Warn("No NATS responders for node, marking unhealthy", "node", node.Name, "nodeID", node.ID)
				d.registry.MarkUnhealthy(context.Background(), node.ID)
				continue
			}
			xlog.Warn("Failed to list backends on worker", "node", node.Name, "error", err)
			continue
		}
		if reply.Error != "" {
			xlog.Warn("Worker returned error listing backends", "node", node.Name, "error", reply.Error)
			continue
		}
		for _, b := range reply.Backends {
			ref := gallery.NodeBackendRef{
				NodeID:      node.ID,
				NodeName:    node.Name,
				NodeStatus:  node.Status,
				Version:     b.Version,
				Digest:      b.Digest,
				URI:         b.URI,
				InstalledAt: b.InstalledAt,
			}
			entry, exists := result[b.Name]
			if !exists {
				entry = gallery.SystemBackend{
					Name:     b.Name,
					IsSystem: b.IsSystem,
					IsMeta:   b.IsMeta,
					Metadata: &gallery.BackendMetadata{
						Name:        b.Name,
						InstalledAt: b.InstalledAt,
						GalleryURL:  b.GalleryURL,
						Version:     b.Version,
						URI:         b.URI,
						Digest:      b.Digest,
					},
				}
			}
			entry.Nodes = append(entry.Nodes, ref)
			result[b.Name] = entry
		}
	}
	return result, nil
}

// InstallBackend fans out installation through the pending-ops queue so
// non-healthy nodes get retried when they come back instead of being silently
// skipped. Reply success from the NATS round-trip deletes the queue row;
// reply.Success==false is treated as an error so the row stays for retry.
func (d *DistributedBackendManager) InstallBackend(ctx context.Context, op *galleryop.ManagementOp[gallery.GalleryBackend, any], progressCb galleryop.ProgressCallback) error {
	galleriesJSON, _ := json.Marshal(op.Galleries)
	backendName := op.GalleryElementName

	result, err := d.enqueueAndDrainBackendOp(ctx, OpBackendInstall, backendName, galleriesJSON, func(node BackendNode) error {
		reply, err := d.adapter.InstallBackend(node.ID, backendName, "", string(galleriesJSON), op.ExternalURI, op.ExternalName, op.ExternalAlias)
		if err != nil {
			return err
		}
		if !reply.Success {
			return fmt.Errorf("install failed: %s", reply.Error)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return result.Err()
}

// UpgradeBackend reuses the install NATS subject (the worker re-downloads
// from the gallery). Same queue semantics as Install/Delete.
func (d *DistributedBackendManager) UpgradeBackend(ctx context.Context, name string, progressCb galleryop.ProgressCallback) error {
	galleriesJSON, _ := json.Marshal(d.backendGalleries)

	result, err := d.enqueueAndDrainBackendOp(ctx, OpBackendUpgrade, name, galleriesJSON, func(node BackendNode) error {
		reply, err := d.adapter.InstallBackend(node.ID, name, "", string(galleriesJSON), "", "", "")
		if err != nil {
			return err
		}
		if !reply.Success {
			return fmt.Errorf("upgrade failed: %s", reply.Error)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return result.Err()
}

// CheckUpgrades checks for available backend upgrades across the cluster.
//
// The previous implementation delegated to d.local, which called
// ListSystemBackends on the frontend — but in distributed mode the frontend
// has no backends installed locally, so the upgrade loop never ran and the UI
// never surfaced any upgrades. We now feed the cluster-wide aggregation
// (including per-node versions/digests) into gallery.CheckUpgradesAgainst so
// digest-based detection actually works and cluster drift is visible.
func (d *DistributedBackendManager) CheckUpgrades(ctx context.Context) (map[string]gallery.UpgradeInfo, error) {
	installed, err := d.ListBackends()
	if err != nil {
		return nil, err
	}
	// systemState is used by AvailableBackends (gallery paths + meta-backend
	// resolution). The `installed` argument is what the old code got wrong —
	// it used to come from the empty frontend filesystem.
	return gallery.CheckUpgradesAgainst(ctx, d.backendGalleries, d.systemState, installed)
}
