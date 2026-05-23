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
	"github.com/mudler/LocalAI/core/services/messaging"
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

// nodeProgressSink is the narrow interface DistributedBackendManager uses to
// publish per-node progress without dragging in the full *GalleryService.
// nil means "no sink, skip per-node writes" (used by single-node tests).
type nodeProgressSink interface {
	UpdateNodeProgress(opID, nodeID string, np galleryop.NodeProgress)
}

// DistributedBackendManager wraps a local BackendManager and adds NATS fan-out
// for backend deletion so worker nodes clean up stale files.
type DistributedBackendManager struct {
	local            galleryop.BackendManager
	adapter          *RemoteUnloaderAdapter
	registry         *NodeRegistry
	backendGalleries []config.Gallery
	systemState      *system.SystemState
	progressSink     nodeProgressSink
}

// NewDistributedBackendManager creates a DistributedBackendManager.
// progressSink may be nil to disable per-node OpStatus writes (single-node
// tests don't need it).
func NewDistributedBackendManager(appConfig *config.ApplicationConfig, ml *model.ModelLoader, adapter *RemoteUnloaderAdapter, registry *NodeRegistry, progressSink nodeProgressSink) *DistributedBackendManager {
	return &DistributedBackendManager{
		local:            galleryop.NewLocalBackendManager(appConfig, ml),
		adapter:          adapter,
		registry:         registry,
		backendGalleries: appConfig.BackendGalleries,
		systemState:      appConfig.SystemState,
		progressSink:     progressSink,
	}
}

// NodeOpStatus is the per-node outcome of a backend lifecycle operation.
// Returned as part of BackendOpResult so the frontend can surface exactly
// what happened on each worker instead of a single joined error string.
// Status holds one of the galleryop.NodeStatus* constants.
type NodeOpStatus struct {
	NodeID   string `json:"node_id"`
	NodeName string `json:"node_name"`
	Status   string `json:"status"`
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
		if n.Status == galleryop.NodeStatusError {
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
// targetNodeIDs is an optional allowlist: when non-nil, only nodes whose ID is
// in the set are visited. Used by UpgradeBackend to avoid asking nodes that
// never had the backend installed to "upgrade" it - such requests fail at the
// gallery (no platform variant) and would otherwise leave a forever-retrying
// pending_backend_ops row. nil means "fan out to every node" (Install/Delete).
//
// opID is the gallery operation identifier; when non-empty and progressSink is
// set, every per-node terminal status appended to BackendOpResult is also
// mirrored into the sink so the UI's per-node OpStatus.Nodes view stays in
// lockstep with the manager's view. opID may be empty for ops that aren't
// gallery-tracked (e.g. DeleteBackend's plain code path).
func (d *DistributedBackendManager) enqueueAndDrainBackendOp(ctx context.Context, opID, op, backend string, galleriesJSON []byte, targetNodeIDs map[string]bool, apply func(node BackendNode) error) (BackendOpResult, error) {
	allNodes, err := d.registry.List(ctx)
	if err != nil {
		return BackendOpResult{}, err
	}

	// emitNodeProgress is a small helper that funnels every NodeOpStatus we
	// append to result.Nodes into the per-node OpStatus sink (when configured
	// and opID is known). Keeping it inline avoids drift between the
	// BackendOpResult view and the sink view - they're written from the same
	// code path on the same terminal statuses.
	emitNodeProgress := func(node BackendNode, status, errMsg string) {
		if d.progressSink == nil || opID == "" {
			return
		}
		d.progressSink.UpdateNodeProgress(opID, node.ID, galleryop.NodeProgress{
			NodeID:   node.ID,
			NodeName: node.Name,
			Status:   status,
			Error:    errMsg,
		})
	}

	result := BackendOpResult{Nodes: make([]NodeOpStatus, 0, len(allNodes))}
	for _, node := range allNodes {
		// Pending nodes haven't been approved yet - no intent to apply.
		if node.Status == StatusPending {
			continue
		}
		// Backend lifecycle ops only make sense on backend-type workers.
		// Agent workers don't subscribe to backend.install/delete/list, so
		// enqueueing for them guarantees a forever-retrying row that the
		// reconciler can never drain. Silently skip - they aren't consumers.
		if node.NodeType != "" && node.NodeType != NodeTypeBackend {
			continue
		}
		if targetNodeIDs != nil && !targetNodeIDs[node.ID] {
			continue
		}
		if err := d.registry.UpsertPendingBackendOp(ctx, node.ID, backend, op, galleriesJSON); err != nil {
			xlog.Warn("Failed to enqueue backend op", "op", op, "node", node.Name, "backend", backend, "error", err)
			errMsg := fmt.Sprintf("enqueue failed: %v", err)
			result.Nodes = append(result.Nodes, NodeOpStatus{
				NodeID: node.ID, NodeName: node.Name, Status: galleryop.NodeStatusError,
				Error: errMsg,
			})
			emitNodeProgress(node, galleryop.NodeStatusError, errMsg)
			continue
		}

		if node.Status != StatusHealthy {
			// Intent is recorded; reconciler will retry when the node recovers.
			errMsg := fmt.Sprintf("node %s, will retry when healthy", node.Status)
			result.Nodes = append(result.Nodes, NodeOpStatus{
				NodeID: node.ID, NodeName: node.Name, Status: galleryop.NodeStatusQueued,
				Error: errMsg,
			})
			emitNodeProgress(node, galleryop.NodeStatusQueued, errMsg)
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
				NodeID: node.ID, NodeName: node.Name, Status: galleryop.NodeStatusSuccess,
			})
			emitNodeProgress(node, galleryop.NodeStatusSuccess, "")
			continue
		}

		// Record failure for backoff. If it's an ErrNoResponders, the node's
		// gone AWOL - mark unhealthy so the router stops picking it too.
		errMsg := applyErr.Error()

		// Worker-still-installing is a "soft" failure: the worker is most
		// likely still pulling the OCI image. Keep the row, push NextRetryAt
		// out so the reconciler does not immediately re-fire another install
		// while the worker is still busy, and report the in-progress state
		// to the caller. The next reconciler pass / backend.list confirms
		// the actual outcome.
		if errors.Is(applyErr, galleryop.ErrWorkerStillInstalling) {
			if id, err := d.findPendingRow(ctx, node.ID, backend, op); err == nil {
				_ = d.registry.RecordPendingBackendOpInFlight(ctx, id, errMsg, d.adapter.InstallTimeout())
			}
			result.Nodes = append(result.Nodes, NodeOpStatus{
				NodeID: node.ID, NodeName: node.Name, Status: galleryop.NodeStatusRunningOnWorker, Error: errMsg,
			})
			emitNodeProgress(node, galleryop.NodeStatusRunningOnWorker, errMsg)
			continue
		}

		if errors.Is(applyErr, nats.ErrNoResponders) {
			xlog.Warn("No NATS responders for node, marking unhealthy", "node", node.Name, "nodeID", node.ID)
			d.registry.MarkUnhealthy(ctx, node.ID)
		}
		if id, err := d.findPendingRow(ctx, node.ID, backend, op); err == nil {
			_ = d.registry.RecordPendingBackendOpFailure(ctx, id, errMsg)
		}
		result.Nodes = append(result.Nodes, NodeOpStatus{
			NodeID: node.ID, NodeName: node.Name, Status: galleryop.NodeStatusError, Error: errMsg,
		})
		emitNodeProgress(node, galleryop.NodeStatusError, errMsg)
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
	// Empty opID: plain DeleteBackend isn't gallery-tracked the same way as
	// Install/Upgrade (no progress dialog), so we skip the per-node sink
	// writes here. DeleteBackendDetailed is the HTTP path that surfaces
	// per-node results in its own response.
	result, err := d.enqueueAndDrainBackendOp(ctx, "", OpBackendDelete, name, nil, nil, func(node BackendNode) error {
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
	return d.enqueueAndDrainBackendOp(ctx, "", OpBackendDelete, name, nil, nil, func(node BackendNode) error {
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

	// Proactively clear pending_backend_ops install rows whose intent is now
	// satisfied: the backend is reported installed on its target node. Without
	// this, the row sits in the queue until next_retry_at expires (up to the
	// install timeout, default 15m) and the operator UI shows the install as
	// "still installing in background" for that whole window even though the
	// worker has actually been ready for minutes. We only clear install rows;
	// upgrade and delete rows have presence-based semantics that do NOT match
	// backend.list confirmation.
	d.clearSatisfiedInstallRows(context.Background(), result)
	return result, nil
}

// clearSatisfiedInstallRows removes pending_backend_ops install rows whose
// (nodeID, backend) pair now appears in the cluster-wide backend listing.
// Called by ListBackends after fan-out so the proactive clear sees every
// node's report. Best-effort: a DB failure is logged and the row stays for
// the reconciler to drain via its slower path.
func (d *DistributedBackendManager) clearSatisfiedInstallRows(ctx context.Context, backends gallery.SystemBackends) {
	rows, err := d.registry.ListPendingBackendOps(ctx)
	if err != nil {
		xlog.Debug("clearSatisfiedInstallRows: failed to list pending ops", "error", err)
		return
	}
	if len(rows) == 0 {
		return
	}
	// Build a (nodeID, backend) presence set from the listing.
	present := make(map[string]map[string]bool, len(backends))
	for name, b := range backends {
		for _, ref := range b.Nodes {
			if present[ref.NodeID] == nil {
				present[ref.NodeID] = make(map[string]bool)
			}
			present[ref.NodeID][name] = true
		}
	}
	for _, row := range rows {
		if row.Op != OpBackendInstall {
			continue
		}
		if !present[row.NodeID][row.Backend] {
			continue
		}
		if err := d.registry.DeletePendingBackendOp(ctx, row.ID); err != nil {
			xlog.Debug("clearSatisfiedInstallRows: delete failed",
				"id", row.ID, "node", row.NodeID, "backend", row.Backend, "error", err)
			continue
		}
		xlog.Info("Reconciler: pending install row satisfied by backend.list",
			"node", row.NodeID, "backend", row.Backend)
	}
}

// InstallBackend fans out installation through the pending-ops queue so
// non-healthy nodes get retried when they come back instead of being silently
// skipped. Reply success from the NATS round-trip deletes the queue row;
// reply.Success==false is treated as an error so the row stays for retry.
//
// When op.TargetNodeID is set, only that node is visited - the same allowlist
// path UpgradeBackend uses. Empty TargetNodeID preserves the original fan-out
// behavior so the periodic reconciler and /api/backends/install/:id keep
// working unchanged.
func (d *DistributedBackendManager) InstallBackend(ctx context.Context, op *galleryop.ManagementOp[gallery.GalleryBackend, any], progressCb galleryop.ProgressCallback) error {
	galleriesJSON, _ := json.Marshal(op.Galleries)
	backendName := op.GalleryElementName

	var targetNodeIDs map[string]bool
	if op.TargetNodeID != "" {
		targetNodeIDs = map[string]bool{op.TargetNodeID: true}
	}

	result, err := d.enqueueAndDrainBackendOp(ctx, op.ID, OpBackendInstall, backendName, galleriesJSON, targetNodeIDs, func(node BackendNode) error {
		// onProgress fans each BackendInstallProgressEvent into two
		// observers: the legacy single-bar progressCb (kept so callers
		// that only consume the aggregate view keep working) and the
		// per-node sink (so OpStatus.Nodes gets a "downloading" tick
		// per file/percentage with node attribution). Defined inside the
		// loop so each node captures its own node.Name into the closure.
		onProgress := func(ev messaging.BackendInstallProgressEvent) {
			if progressCb != nil {
				progressCb(ev.FileName, ev.Current, ev.Total, ev.Percentage)
			}
			if d.progressSink != nil && op.ID != "" {
				d.progressSink.UpdateNodeProgress(op.ID, ev.NodeID, galleryop.NodeProgress{
					NodeID:     ev.NodeID,
					NodeName:   node.Name,
					Status:     galleryop.NodeStatusDownloading,
					FileName:   ev.FileName,
					Current:    ev.Current,
					Total:      ev.Total,
					Percentage: ev.Percentage,
					Phase:      ev.Phase,
				})
			}
		}
		// nil-callback shortcut: when there is nothing to deliver to,
		// hand the adapter a nil onProgress so it skips the per-op NATS
		// subscription. Matches the pre-Phase-4 bridgeProgressCb semantics.
		var onProgressArg func(messaging.BackendInstallProgressEvent)
		if progressCb != nil || d.progressSink != nil {
			onProgressArg = onProgress
		}
		// Admin-driven backend install: not tied to a specific replica slot.
		// Pass replica 0 - the worker's processKey is "backend#0" when no
		// modelID is supplied, matching pre-PR4 behavior.
		reply, err := d.adapter.InstallBackend(node.ID, backendName, "", string(galleriesJSON), op.ExternalURI, op.ExternalName, op.ExternalAlias, 0, op.ID, onProgressArg)
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
	if hardErr := result.Err(); hardErr != nil {
		return hardErr
	}
	// No hard failures, but if at least one node reported running_on_worker,
	// surface a wrapped ErrWorkerStillInstalling so galleryop can render a
	// yellow in-progress state instead of green success. The reconciler
	// will confirm the actual outcome on its next pass via backend.list.
	for _, n := range result.Nodes {
		if n.Status == galleryop.NodeStatusRunningOnWorker {
			return fmt.Errorf("%w: %s", galleryop.ErrWorkerStillInstalling, summarizeRunningOnWorker(result.Nodes))
		}
	}
	return nil
}

// UpgradeBackend uses a separate NATS subject (backend.upgrade) so the slow
// force-reinstall path doesn't head-of-line-block routine model loads on
// the same worker. Only nodes that already report this backend as installed
// are targeted — fanning out to every node would ask workers to "upgrade"
// something they never had, which fails at the gallery (e.g. a darwin/arm64
// worker has no platform variant for a linux-only backend) and leaves a
// forever-retrying pending_backend_ops row.
//
// Rolling-update fallback: when a worker returns nats.ErrNoResponders on
// backend.upgrade, we try the legacy backend.install Force=true path so a
// new master + old worker still converges. Drop the fallback once every
// worker in the fleet is on 2026-05-08 or newer.
func (d *DistributedBackendManager) UpgradeBackend(ctx context.Context, name string, progressCb galleryop.ProgressCallback) error {
	galleriesJSON, _ := json.Marshal(d.backendGalleries)

	installed, err := d.ListBackends()
	if err != nil {
		return fmt.Errorf("failed to list cluster backends: %w", err)
	}
	entry, ok := installed[name]
	if !ok || len(entry.Nodes) == 0 {
		return fmt.Errorf("backend %q is not installed on any node", name)
	}
	targetNodeIDs := make(map[string]bool, len(entry.Nodes))
	for _, n := range entry.Nodes {
		targetNodeIDs[n.NodeID] = true
	}

	// Empty opID: the caller (galleryop) doesn't thread an op ID into
	// UpgradeBackend today, so we can't tag per-node sink writes with the
	// right OpStatus key. Until the upgrade path takes a ManagementOp the
	// way InstallBackend does, the sink stays no-op here.
	result, err := d.enqueueAndDrainBackendOp(ctx, "", OpBackendUpgrade, name, galleriesJSON, targetNodeIDs, func(node BackendNode) error {
		reply, err := d.adapter.UpgradeBackend(node.ID, name, string(galleriesJSON), "", "", "", 0)
		if err != nil {
			// Rolling-update fallback: an older worker doesn't know
			// backend.upgrade. Try the legacy install-with-force path.
			if errors.Is(err, nats.ErrNoResponders) {
				instReply, instErr := d.adapter.installWithForceFallback(node.ID, name, string(galleriesJSON), "", "", "", 0)
				if instErr != nil {
					return instErr
				}
				if !instReply.Success {
					return fmt.Errorf("upgrade (legacy fallback) failed: %s", instReply.Error)
				}
				return nil
			}
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
	if hardErr := result.Err(); hardErr != nil {
		return hardErr
	}
	// Same in-progress surfacing as InstallBackend: a long-running worker
	// upgrade that timed out the NATS round-trip must not be reported as
	// green success.
	for _, n := range result.Nodes {
		if n.Status == galleryop.NodeStatusRunningOnWorker {
			return fmt.Errorf("%w: %s", galleryop.ErrWorkerStillInstalling, summarizeRunningOnWorker(result.Nodes))
		}
	}
	return nil
}

// IsDistributed reports that installs from this manager fan out across the
// cluster. The HTTP layer reads this to gate hardware-specific installs on
// /api/backends/apply (which would otherwise silently land on every node).
func (d *DistributedBackendManager) IsDistributed() bool { return true }

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

// summarizeRunningOnWorker builds a short human-readable summary of which
// nodes are still installing in the background, for inclusion in the
// wrapped ErrWorkerStillInstalling error.
func summarizeRunningOnWorker(nodes []NodeOpStatus) string {
	var names []string
	for _, n := range nodes {
		if n.Status == galleryop.NodeStatusRunningOnWorker {
			names = append(names, n.NodeName)
		}
	}
	return strings.Join(names, ", ")
}
