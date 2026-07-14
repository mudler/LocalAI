package galleryop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
)

type GalleryService struct {
	appConfig *config.ApplicationConfig
	sync.Mutex
	ModelGalleryChannel   chan ManagementOp[gallery.GalleryModel, gallery.ModelConfig]
	BackendGalleryChannel chan ManagementOp[gallery.GalleryBackend, any]

	modelLoader    *model.ModelLoader
	modelManager   ModelManager
	backendManager BackendManager
	statuses       map[string]*OpStatus
	cancellations  map[string]context.CancelCauseFunc
	pausedOps      map[string]*PausedModelOp
	rateLimiters   map[string]*downloader.DynamicRateLimiter

	// Distributed mode (nil when not in distributed mode).
	// natsClient is the wider MessagingClient (Publisher + subscribe methods)
	// when wired by the distributed startup path; broadcastSubs holds the
	// progress + cancel subscriptions opened by SubscribeBroadcasts.
	natsClient    messaging.MessagingClient
	galleryStore  *distributed.GalleryStore
	broadcastSubs []messaging.Subscription

	// OnBackendOpCompleted is fired after every successful install/upgrade/delete
	// on the backend channel. The Application wires this to UpgradeChecker.TriggerCheck
	// so `/api/backends/upgrades` stops surfacing a backend as upgradeable the moment
	// the worker finishes — previously the cache only refreshed on the 6-hour tick,
	// making manual upgrades look like they failed even when they hadn't.
	//
	// In distributed mode the same hook also fires on peer replicas via the
	// SubjectCacheInvalidateBackends subscriber, so every replica's
	// UpgradeChecker stays in sync.
	OnBackendOpCompleted func()

	// OnModelsChanged is fired on peer replicas when SOMEONE else publishes
	// SubjectCacheInvalidateModels. The Application wires this to
	// ModelConfigLoader.LoadModelConfigsFromPath so a chat completion that
	// load-balances onto this replica can find the just-installed model.
	// The originating replica reloads inline (models.go) so it does not need
	// the hook.
	OnModelsChanged func(messaging.CacheInvalidateEvent)
}

func NewGalleryService(appConfig *config.ApplicationConfig, ml *model.ModelLoader) *GalleryService {
	return &GalleryService{
		appConfig:             appConfig,
		ModelGalleryChannel:   make(chan ManagementOp[gallery.GalleryModel, gallery.ModelConfig]),
		BackendGalleryChannel: make(chan ManagementOp[gallery.GalleryBackend, any]),
		modelLoader:           ml,
		modelManager:          NewLocalModelManager(appConfig, ml),
		backendManager:        NewLocalBackendManager(appConfig, ml),
		statuses:              make(map[string]*OpStatus),
		cancellations:         make(map[string]context.CancelCauseFunc),
		rateLimiters:          make(map[string]*downloader.DynamicRateLimiter),
	}
}

// SetModelManager replaces the model manager (e.g. with a distributed implementation).
func (g *GalleryService) SetModelManager(m ModelManager) {
	g.Lock()
	defer g.Unlock()
	g.modelManager = m
}

// SetBackendManager replaces the backend manager (e.g. with a distributed implementation).
func (g *GalleryService) SetBackendManager(b BackendManager) {
	g.Lock()
	defer g.Unlock()
	g.backendManager = b
}

// BackendManager returns the current backend manager. Callers like the
// periodic upgrade checker need this so they run CheckUpgrades through the
// distributed implementation (which asks workers) instead of the frontend's
// local filesystem — the latter is always empty in distributed deployments.
func (g *GalleryService) BackendManager() BackendManager {
	g.Lock()
	defer g.Unlock()
	return g.backendManager
}

// SetNATSClient sets the NATS client for distributed progress publishing.
// Accepting the wider MessagingClient (vs. plain Publisher) lets
// SubscribeBroadcasts wire the wildcard subscriptions that keep peer
// replicas' statuses + cancellations in sync.
func (g *GalleryService) SetNATSClient(nc messaging.MessagingClient) {
	g.Lock()
	defer g.Unlock()
	g.natsClient = nc
}

// SetGalleryStore sets the PostgreSQL gallery store for distributed persistence.
func (g *GalleryService) SetGalleryStore(s *distributed.GalleryStore) {
	g.Lock()
	defer g.Unlock()
	g.galleryStore = s
}

// ListBackends returns installed backends via the backend manager.
// In standalone mode this checks the local filesystem; in distributed mode
// it aggregates from all healthy worker nodes.
func (g *GalleryService) ListBackends() (gallery.SystemBackends, error) {
	g.Lock()
	mgr := g.backendManager
	g.Unlock()
	return mgr.ListBackends()
}

// DeleteBackend delegates backend deletion to the backend manager, which in distributed
// mode fans out the deletion to worker nodes via NATS.
func (g *GalleryService) DeleteBackend(name string) error {
	g.Lock()
	mgr := g.backendManager
	g.Unlock()
	return mgr.DeleteBackend(name)
}

func (g *GalleryService) UpdateStatus(s string, op *OpStatus) {
	g.Lock()
	// Preserve any per-node entries already accumulated by UpdateNodeProgress:
	// the legacy progressCb path (used by the Phase 2 install bridge) calls
	// UpdateStatus with a fresh *OpStatus on every tick, which would otherwise
	// wipe the Nodes slice and leave the UI flickering between one node and
	// another. If the caller explicitly populates Nodes on the incoming op,
	// that wins; an empty Nodes slice on the incoming op is treated as "no
	// new per-node data" and the previous Nodes are carried forward.
	if op != nil && len(op.Nodes) == 0 {
		if prev := g.statuses[s]; prev != nil && len(prev.Nodes) > 0 {
			op.Nodes = prev.Nodes
		}
	}
	g.statuses[s] = op
	store := g.galleryStore
	nc := g.natsClient
	g.Unlock()

	// I/O happens after Unlock. The NATS broadcast loops back into our own
	// wildcard subscriber (mergeStatus), which would deadlock on this mutex
	// if we still held it. Holding the lock across a PostgreSQL round-trip
	// would also stall every concurrent reader on each progress tick.
	if store != nil && op != nil {
		if op.Processed {
			status, errMsg := "completed", ""
			if op.Error != nil {
				status = "failed"
				errMsg = op.Error.Error()
			}
			if op.Cancelled {
				status = "cancelled"
			}
			if err := store.UpdateStatus(s, status, errMsg); err != nil {
				xlog.Warn("Failed to persist gallery operation status", "op_id", s, "error", err)
			}
		} else {
			if err := store.UpdateProgress(s, op.Progress, op.Message, op.DownloadedFileSize, op.Cancellable); err != nil {
				xlog.Warn("Failed to persist gallery operation progress", "op_id", s, "error", err)
			}
		}
	}

	// Publish progress to NATS in distributed mode. The payload wraps the
	// OpStatus with the opID so peer replicas reading the wildcard subject
	// don't need to parse it back out of the NATS subject string.
	if nc != nil {
		if err := nc.Publish(messaging.SubjectGalleryProgress(s), GalleryProgressEvent{
			JobID:  s,
			Status: op,
		}); err != nil {
			xlog.Warn("Failed to broadcast gallery progress", "op_id", s, "error", err)
		}
	}
}

// publishCacheInvalidate broadcasts a cache invalidation event so peer
// replicas refresh whatever in-memory state mirrors disk. No-op when
// natsClient is not wired (standalone mode).
func (g *GalleryService) publishCacheInvalidate(subject string, evt messaging.CacheInvalidateEvent) {
	g.Lock()
	nc := g.natsClient
	g.Unlock()
	if nc == nil {
		return
	}
	if err := nc.Publish(subject, evt); err != nil {
		xlog.Warn("Failed to broadcast cache invalidation", "subject", subject, "error", err)
	}
}

// BroadcastModelsChanged notifies peer replicas that a model config was
// created, edited, or removed out-of-band of the gallery install/delete
// channel (e.g. the admin /models/edit, /models/import and
// /models/toggle-state endpoints, which write the YAML and reload only the
// local in-memory loader). Peers receive it via OnModelsChanged and refresh
// their own ModelConfigLoader so a request load-balanced to any replica sees
// the same config. No-op in standalone mode (no NATS client).
//
// op is "install" for a create/edit (the element must be (re)loaded from
// disk) or "delete" for a removal (the element must be pruned from memory,
// which a reload-from-path cannot do because the loader is additive).
func (g *GalleryService) BroadcastModelsChanged(element, op string) {
	g.publishCacheInvalidate(messaging.SubjectCacheInvalidateModels, messaging.CacheInvalidateEvent{
		Element: element,
		Op:      op,
	})
}

// mergeStatus is the broadcast-side merge: it updates the in-memory map from
// a peer's GalleryProgressEvent without re-publishing to NATS or re-writing
// to PostgreSQL. UpdateStatus is the local-write entry point and does both;
// mergeStatus is what the wildcard subscriber calls. Splitting them avoids
// an echo loop (replica publishes → its own subscriber receives → mergeStatus
// silently re-applies → no second publish).
func (g *GalleryService) mergeStatus(opID string, op *OpStatus) {
	if op == nil {
		return
	}
	g.Lock()
	defer g.Unlock()
	if len(op.Nodes) == 0 {
		if prev := g.statuses[opID]; prev != nil && len(prev.Nodes) > 0 {
			op.Nodes = prev.Nodes
		}
	}
	g.statuses[opID] = op
}

// UpdateNodeProgress merges a per-node progress tick into OpStatus.Nodes,
// keyed by nodeID, and mirrors the latest values into the aggregate
// Progress / FileName / DownloadedFileSize / TotalFileSize / Message
// fields so the legacy single-bar OperationsBar view keeps working
// unchanged alongside the new per-node breakdown.
//
// We deliberately do NOT delegate the aggregate mirror to UpdateStatus
// here: UpdateStatus overwrites the entire OpStatus, which would clobber
// the Nodes slice we just merged into. Doing the merge + mirror under a
// single lock keeps both views consistent and concurrent-safe.
func (g *GalleryService) UpdateNodeProgress(opID, nodeID string, np NodeProgress) {
	g.Lock()
	defer g.Unlock()
	status := g.statuses[opID]
	if status == nil {
		status = &OpStatus{}
		g.statuses[opID] = status
	}
	merged := false
	for i := range status.Nodes {
		if status.Nodes[i].NodeID == nodeID {
			status.Nodes[i] = np
			merged = true
			break
		}
	}
	if !merged {
		status.Nodes = append(status.Nodes, np)
	}

	// Mirror the latest tick into the legacy aggregate fields so the
	// existing single-bar UI keeps rendering meaningful progress.
	status.FileName = np.FileName
	status.Progress = np.Percentage
	status.DownloadedFileSize = np.Current
	status.TotalFileSize = np.Total
	if np.Phase != "" {
		status.Message = np.Phase
	}
}

func (g *GalleryService) GetStatus(s string) *OpStatus {
	g.Lock()
	defer g.Unlock()

	return g.statuses[s]
}

func (g *GalleryService) GetAllStatus() map[string]*OpStatus {
	g.Lock()
	defer g.Unlock()

	return g.statuses
}

// ReapStaleOperations marks abandoned in-progress operations (pending/
// downloading/processing) older than `age` as failed, so an op orphaned by a
// replica that died mid-flight does not linger as "processing" forever. The
// store's CleanStale runs once on startup; this exposes it for periodic
// invocation (a post-startup orphan is otherwise not reaped until the next
// restart). No-op when no gallery store is wired. Returns rows reaped.
func (g *GalleryService) ReapStaleOperations(age time.Duration) (int64, error) {
	g.Lock()
	store := g.galleryStore
	g.Unlock()
	if store == nil {
		return 0, nil
	}
	n, err := store.CleanStale(age)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		xlog.Info("Reaped stale gallery operations", "count", n)
	}
	return n, nil
}

// CancelOperation cancels an in-progress operation by its ID.
//
// In distributed mode the UI's cancel click may land on a different replica
// than the one running the operation. We still publish the cancel event in
// that case — the peer holding the cancellation func picks it up via the
// SubjectGalleryCancelWildcard subscriber and runs it locally. The caller
// gets a non-error reply so the UI shows the cancel as accepted.
func (g *GalleryService) CancelOperation(id string) error {
	g.Lock()

	if status, ok := g.statuses[id]; ok && status.Cancelled {
		g.Unlock()
		return fmt.Errorf("operation %q is already cancelled", id)
	}

	cancelCause, localExists := g.cancellations[id]
	if localExists {
		delete(g.cancellations, id)
	}

	nc := g.natsClient
	store := g.galleryStore

	if !localExists && nc == nil {
		g.Unlock()
		return fmt.Errorf("operation %q not found or already completed", id)
	}

	if status, ok := g.statuses[id]; ok {
		status.Cancelled = true
		status.Processed = true
		status.Message = "cancelled"
	} else {
		g.statuses[id] = &OpStatus{
			Cancelled:   true,
			Processed:   true,
			Message:     "cancelled",
			Cancellable: false,
		}
	}
	g.Unlock()

	// Persist the terminal status so the cancel survives a restart. Without
	// this the row stays in its active state and re-hydrates straight back into
	// processingBackends on the next replica boot — the UI spins again on an op
	// the admin already cancelled. The peer that broadcasts wins the write; a
	// no-op when standalone (store nil).
	if store != nil {
		if err := store.Cancel(id); err != nil {
			xlog.Warn("Failed to persist gallery operation cancellation", "op_id", id, "error", err)
		}
	}

	// I/O and user-provided callback after Unlock — the cancel-wildcard
	// subscriber loops back into applyCancel on this same replica, which
	// would otherwise deadlock on g.Mutex.
	if cancelCause != nil {
		cancelCause(downloader.ErrUserCancelled)
	}
	if nc != nil {
		if err := nc.Publish(messaging.SubjectGalleryCancel(id), GalleryCancelEvent{JobID: id}); err != nil {
			xlog.Warn("Failed to broadcast gallery cancel", "op_id", id, "error", err)
		}
	}

	return nil
}

// applyCancel is the broadcast-side counterpart to CancelOperation. The
// wildcard subscriber calls it when a peer publishes a cancel event:
// run the local cancel func if we have one (no echo via NATS), and reflect
// the cancellation in the local statuses map. Idempotent: a replica that
// already cancelled this op locally treats the inbound event as a no-op.
func (g *GalleryService) applyCancel(id string) {
	g.Lock()
	cancelCause, hasCancel := g.cancellations[id]
	if hasCancel {
		delete(g.cancellations, id)
	}
	if status, ok := g.statuses[id]; ok {
		if status.Cancelled {
			g.Unlock()
			return
		}
		status.Cancelled = true
		status.Processed = true
		status.Message = "cancelled"
	} else {
		g.statuses[id] = &OpStatus{
			Cancelled:   true,
			Processed:   true,
			Message:     "cancelled",
			Cancellable: false,
		}
	}
	g.Unlock()

	// Invoke the cancel func after Unlock so a callback that touches
	// GalleryService doesn't re-enter the mutex.
	if hasCancel {
		cancelCause(downloader.ErrUserCancelled)
	}
}

// newUserCancellableContext returns a child context whose CancelCauseFunc
// can be called with either downloader.ErrUserCancelled (discards .partial)
// or downloader.ErrUserPaused (preserves .partial for later resume). This
// lets the download layer distinguish deliberate user actions from incidental
// cancellations such as process shutdown.
func newUserCancellableContext(parent context.Context) (context.Context, context.CancelCauseFunc) {
	ctx, cancelCause := context.WithCancelCause(parent)
	return ctx, cancelCause
}

// storeCancellation stores a cancellation function for an operation
func (g *GalleryService) storeCancellation(id string, cancelFunc context.CancelCauseFunc) {
	g.Lock()
	defer g.Unlock()
	g.cancellations[id] = cancelFunc
}

// StoreCancellation is a public method to store a cancellation function for an operation
// This allows cancellation functions to be stored immediately when operations are created,
// enabling cancellation of queued operations that haven't started processing yet.
func (g *GalleryService) StoreCancellation(id string, cancelFunc context.CancelCauseFunc) {
	g.storeCancellation(id, cancelFunc)
}

// removeCancellation removes a cancellation function when operation completes
func (g *GalleryService) removeCancellation(id string) {
	g.Lock()
	defer g.Unlock()
	delete(g.cancellations, id)
}

// storePausedOp saves the paused operation metadata for a later Resume call.
func (g *GalleryService) storePausedOp(id string, op *PausedModelOp) {
	g.Lock()
	defer g.Unlock()
	if g.pausedOps == nil {
		g.pausedOps = make(map[string]*PausedModelOp)
	}
	g.pausedOps[id] = op
}

// removePausedOp deletes the stored paused operation metadata.
func (g *GalleryService) removePausedOp(id string) {
	g.Lock()
	defer g.Unlock()
	delete(g.pausedOps, id)
}

// getPausedOpLocked retrieves the paused operation metadata without locking.
// Caller must hold g.Lock().
func (g *GalleryService) getPausedOpLocked(id string) *PausedModelOp {
	if g.pausedOps == nil {
		return nil
	}
	return g.pausedOps[id]
}

// storeRateLimiter stores a rate limiter for an active download operation.
func (g *GalleryService) storeRateLimiter(id string, rl *downloader.DynamicRateLimiter) {
	g.Lock()
	defer g.Unlock()
	g.rateLimiters[id] = rl
}

// removeRateLimiter removes the rate limiter for a completed operation.
func (g *GalleryService) removeRateLimiter(id string) {
	g.Lock()
	defer g.Unlock()
	delete(g.rateLimiters, id)
}

// PauseAllOperations pauses every active (non-paused, non-cancelled) model
// download. Each operation's context is cancelled with ErrUserPaused so the
// download layer preserves the .partial file. Broadcast is NOT sent for
// individual ops — the callers sees a single API response and the result is
// the same set of paused statuses regardless of which replica it hits.
func (g *GalleryService) PauseAllOperations() error {
	g.Lock()
	ids := make([]string, 0, len(g.cancellations))
	for id := range g.cancellations {
		if status, ok := g.statuses[id]; ok {
			if status.Paused || (status.Processed && status.Cancelled) {
				continue
			}
		}
		ids = append(ids, id)
	}
	g.Unlock()

	var errs []error
	for _, id := range ids {
		if err := g.PauseOperation(id); err != nil {
			errs = append(errs, fmt.Errorf("op %q: %w", id, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("failed to pause %d/%d operations: %v", len(errs), len(ids), errors.Join(errs...))
	}
	return nil
}

// ResumeAllOperations resumes every paused model download by re-queuing
// each stored PausedModelOp. Operations that were paused before a restart
// (recovered from sidecar files) are included.
func (g *GalleryService) ResumeAllOperations() error {
	g.Lock()
	ids := make([]string, 0, len(g.pausedOps))
	for id, p := range g.pausedOps {
		if p == nil {
			continue
		}
		ids = append(ids, id)
	}
	g.Unlock()

	var errs []error
	for _, id := range ids {
		if err := g.ResumeOperation(id); err != nil {
			errs = append(errs, fmt.Errorf("op %q: %w", id, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("failed to resume %d/%d operations: %v", len(errs), len(ids), errors.Join(errs...))
	}
	return nil
}

// SetOperationRateLimit overrides the download rate limit for an active
// operation. A value <= 0 removes the limit (unlimited). The format
// convenience (e.g. "2mb", "500kb") should be pre-parsed by the caller.
func (g *GalleryService) SetOperationRateLimit(id string, bytesPerSec int64) error {
	g.Lock()
	defer g.Unlock()
	rl, ok := g.rateLimiters[id]
	if !ok {
		return fmt.Errorf("operation %q not found or does not support rate limiting", id)
	}
	rl.SetRate(bytesPerSec)
	return nil
}

// autoResumePausedDownloads scans the models directory for .partial.json
// sidecar files (written when a download was paused) and re-queues the
// corresponding model operations. This provides crash-resilience: even if
// the process restarted, paused downloads are automatically resumed.
func (g *GalleryService) autoResumePausedDownloads(systemState *system.SystemState) {
	modelsPath := systemState.Model.ModelsPath
	sidecarFiles, err := filepath.Glob(filepath.Join(modelsPath, "*.partial.json"))
	if err != nil {
		xlog.Warn("Failed to scan for download sidecar files", "path", modelsPath, "error", err)
		return
	}
	for _, scPath := range sidecarFiles {
		sc, err := downloader.ReadPartialSidecar(scPath)
		if err != nil {
			xlog.Warn("Failed to read download sidecar for auto-resume, removing", "file", scPath, "error", err)
			_ = os.Remove(scPath)
			continue
		}
		if sc.ModelID == "" {
			xlog.Warn("Download sidecar missing model_id, removing", "file", scPath)
			_ = os.Remove(scPath)
			continue
		}

		opID := uuid.New().String()
		// Reconstruct a minimal GalleryModel. The gallery layer will re-fetch
		// the full model config from the configured galleries by name.
		req := gallery.GalleryModel{
			Metadata: gallery.Metadata{
				Name: sc.ModelID,
			},
		}

		g.ModelGalleryChannel <- ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
			ID:                 opID,
			GalleryElementName: sc.ModelID,
			Req:                req,
			Galleries:          g.appConfig.Galleries,
			BackendGalleries:   g.appConfig.BackendGalleries,
		}
		xlog.Info("Auto-resumed paused download", "model", sc.ModelID, "op_id", opID)
	}
}

// PauseOperation pauses an in-progress model download. The download layer
// preserves the .partial file so a subsequent ResumeOperation can continue
// from the saved byte offset. In distributed mode the pause event is
// broadcast so the peer replica holding the cancellation func can apply it.
func (g *GalleryService) PauseOperation(id string) error {
	g.Lock()

	if status, ok := g.statuses[id]; ok && status.Paused {
		g.Unlock()
		return fmt.Errorf("operation %q is already paused", id)
	}
	if status, ok := g.statuses[id]; ok && status.Processed && status.Cancelled {
		g.Unlock()
		return fmt.Errorf("operation %q is already cancelled, cannot pause", id)
	}

	cancelCause, localExists := g.cancellations[id]
	if !localExists {
		// The cancel func may live on a different replica in distributed mode.
		nc := g.natsClient
		if nc == nil {
			g.Unlock()
			return fmt.Errorf("operation %q not found or already completed", id)
		}
		// Broadcast to the peer that holds the cancel func.
		g.Unlock()
		if err := nc.Publish(messaging.SubjectGalleryPause(id), GalleryPauseEvent{JobID: id}); err != nil {
			return fmt.Errorf("failed to broadcast pause for operation %q: %w", id, err)
		}
		return nil
	}

	delete(g.cancellations, id)

	if status, ok := g.statuses[id]; ok {
		status.Paused = true
		status.Message = "paused"
	} else {
		g.statuses[id] = &OpStatus{
			Paused:      true,
			Message:     "paused",
			Cancellable: true,
		}
	}
	g.Unlock()

	// Cancel the context with ErrUserPaused so the download layer preserves
	// the .partial file.
	cancelCause(downloader.ErrUserPaused)

	// Broadcast the pause event so peer replicas update their status maps.
	nc := g.natsClient
	if nc != nil {
		if err := nc.Publish(messaging.SubjectGalleryPause(id), GalleryPauseEvent{JobID: id}); err != nil {
			xlog.Warn("Failed to broadcast gallery pause", "op_id", id, "error", err)
		}
	}

	return nil
}

// ResumeOperation resumes a previously paused model download. It re-creates
// the download context and pushes a fresh ManagementOp to the model channel,
// where the existing .partial file will be picked up automatically via Range.
func (g *GalleryService) ResumeOperation(id string) error {
	g.Lock()
	status, statusExists := g.statuses[id]
	if !statusExists || !status.Paused {
		g.Unlock()
		return fmt.Errorf("operation %q is not paused", id)
	}

	pausedOp := g.getPausedOpLocked(id)
	if pausedOp == nil {
		g.Unlock()
		return fmt.Errorf("no paused operation metadata found for %q", id)
	}

	// Remove the paused op metadata so a second Resume fails cleanly.
	delete(g.pausedOps, id)

	// Reset the status: paused → downloading.
	status.Paused = false
	status.Processed = false
	status.Message = "resuming download"
	g.Unlock()

	// Push a new ManagementOp to the model channel. Start() will create a
	// fresh context with newUserCancellableContext so the user can still
	// cancel the resumed download.
	g.ModelGalleryChannel <- ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
		ID:                 id,
		GalleryElementName: pausedOp.GalleryElementName,
		Req:                pausedOp.Req,
		Galleries:          pausedOp.Galleries,
		BackendGalleries:   pausedOp.BackendGalleries,
		// Context and CancelFunc are nil — Start() creates them.
	}

	return nil
}

// applyPause is the broadcast-side counterpart to PauseOperation. The
// wildcard subscriber calls it when a peer publishes a pause event:
// run the local cancel func if we have one, and reflect the pause in the
// local statuses map.
func (g *GalleryService) applyPause(id string) {
	g.Lock()
	cancelCause, hasCancel := g.cancellations[id]
	if hasCancel {
		delete(g.cancellations, id)
	}
	if status, ok := g.statuses[id]; ok {
		if status.Paused {
			g.Unlock()
			return
		}
		status.Paused = true
		status.Message = "paused"
	} else {
		g.statuses[id] = &OpStatus{
			Paused:      true,
			Message:     "paused",
			Cancellable: true,
		}
	}
	g.Unlock()

	if hasCancel {
		cancelCause(downloader.ErrUserPaused)
	}
}

func (g *GalleryService) Start(c context.Context, cl *config.ModelConfigLoader, systemState *system.SystemState) error {
	// Auto-resume downloads that were paused before a restart. Sidecar
	// files persisted by the download layer survive process crashes.
	g.autoResumePausedDownloads(systemState)

	// updates the status with an error
	var updateError func(id string, e error)
	if !g.appConfig.OpaqueErrors {
		updateError = func(id string, e error) {
			g.UpdateStatus(id, &OpStatus{Error: e, Processed: true, Message: "error: " + e.Error()})
		}
	} else {
		updateError = func(id string, _ error) {
			g.UpdateStatus(id, &OpStatus{Error: fmt.Errorf("an error occurred"), Processed: true})
		}
	}

	go func() {
		for {
			select {
			case <-c.Done():
				return
			case op := <-g.BackendGalleryChannel:
				// Create context if not provided
				if op.Context == nil {
					op.Context, cancelCause := newUserCancellableContext(c)
					op.CancelFunc = func() { cancelCause(downloader.ErrUserCancelled) }
					g.storeCancellation(op.ID, cancelCause)
				} else if op.CancelFunc != nil {
					// The caller provided a CancelFunc; wrap it as a CancelCauseFunc
					// that we can also use for pause. We store the wrapped version
					// that always cancels with ErrUserCancelled.
					cc := op.CancelFunc
					g.storeCancellation(op.ID, func(error) { cc() })
				}
				// Create DB record for distributed tracking
				if g.galleryStore != nil {
					g.galleryStore.Create(&distributed.GalleryOperationRecord{
						ID:                 op.ID,
						GalleryElementName: op.GalleryElementName,
						OpType:             "backend_install",
						Status:             "pending",
						Cancellable:        true,
					})
				}
				err := g.backendHandler(&op, systemState)
				if err != nil {
					updateError(op.ID, err)
				} else if g.OnBackendOpCompleted != nil {
					// Let listeners (e.g. UpgradeChecker) refresh their view of
					// installed state. Run off the worker goroutine so a slow
					// callback doesn't stall the next queued operation.
					go g.OnBackendOpCompleted()
				}
				g.removeCancellation(op.ID)

			case op := <-g.ModelGalleryChannel:
				// Create context if not provided
				if op.Context == nil {
					op.Context, cancelCause := newUserCancellableContext(c)
					op.CancelFunc = func() { cancelCause(downloader.ErrUserCancelled) }
					g.storeCancellation(op.ID, cancelCause)
				} else if op.CancelFunc != nil {
					cc := op.CancelFunc
					g.storeCancellation(op.ID, func(error) { cc() })
				}
				// Attach a dynamic rate limiter so the download can be throttled
				// at runtime via SetOperationRateLimit.
				rl := &downloader.DynamicRateLimiter{}
				op.Context = downloader.ContextWithRateLimiter(op.Context, rl)
				g.storeRateLimiter(op.ID, rl)
				// Create DB record for distributed tracking
				if g.galleryStore != nil {
					opType := "model_install"
					if op.Delete {
						opType = "model_delete"
					}
					g.galleryStore.Create(&distributed.GalleryOperationRecord{
						ID:                 op.ID,
						GalleryElementName: op.GalleryElementName,
						OpType:             opType,
						Status:             "pending",
						// A delete is not cancellable; an install is.
						Cancellable: !op.Delete,
					})
				}
				err := g.modelHandler(&op, cl, systemState)
				if err != nil {
					updateError(op.ID, err)
				}
				g.removeCancellation(op.ID)
				g.removeRateLimiter(op.ID)
			}
		}
	}()

	return nil
}

// SubscribeBroadcasts opens the wildcard subscriptions that keep this
// replica's in-memory statuses + cancellation state in sync with peers.
// Returns an error if the progress subscription fails; cancel-sub failures
// are not fatal but are logged.
//
// Hydrate should be called before this so the freshly-started replica has
// the pre-existing operations before live updates start flowing.
func (g *GalleryService) SubscribeBroadcasts() error {
	g.Lock()
	nc := g.natsClient
	g.Unlock()
	if nc == nil {
		return nil
	}

	progressSub, err := messaging.SubscribeJSON(nc, messaging.SubjectGalleryProgressWildcard, func(evt GalleryProgressEvent) {
		if evt.JobID == "" || evt.Status == nil {
			return
		}
		g.mergeStatus(evt.JobID, evt.Status)
	})
	if err != nil {
		return fmt.Errorf("subscribing to gallery progress wildcard: %w", err)
	}

	cancelSub, err := messaging.SubscribeJSON(nc, messaging.SubjectGalleryCancelWildcard, func(evt GalleryCancelEvent) {
		if evt.JobID == "" {
			return
		}
		g.applyCancel(evt.JobID)
	})
	if err != nil {
		if uerr := progressSub.Unsubscribe(); uerr != nil {
			xlog.Warn("failed to unsubscribe partial gallery progress sub", "error", uerr)
		}
		return fmt.Errorf("subscribing to gallery cancel wildcard: %w", err)
	}

	pauseSub, err := messaging.SubscribeJSON(nc, messaging.SubjectGalleryPauseWildcard, func(evt GalleryPauseEvent) {
		if evt.JobID == "" {
			return
		}
		g.applyPause(evt.JobID)
	})
	if err != nil {
		if uerr := progressSub.Unsubscribe(); uerr != nil {
			xlog.Warn("failed to unsubscribe partial gallery progress sub", "error", uerr)
		}
		if uerr := cancelSub.Unsubscribe(); uerr != nil {
			xlog.Warn("failed to unsubscribe partial gallery cancel sub", "error", uerr)
		}
		return fmt.Errorf("subscribing to gallery pause wildcard: %w", err)
	}

	modelsSub, err := messaging.SubscribeJSON(nc, messaging.SubjectCacheInvalidateModels, func(evt messaging.CacheInvalidateEvent) {
		g.Lock()
		cb := g.OnModelsChanged
		g.Unlock()
		if cb != nil {
			cb(evt)
		}
	})
	if err != nil {
		if uerr := progressSub.Unsubscribe(); uerr != nil {
			xlog.Warn("failed to unsubscribe partial gallery progress sub", "error", uerr)
		}
		if uerr := cancelSub.Unsubscribe(); uerr != nil {
			xlog.Warn("failed to unsubscribe partial gallery cancel sub", "error", uerr)
		}
		if uerr := pauseSub.Unsubscribe(); uerr != nil {
			xlog.Warn("failed to unsubscribe partial gallery pause sub", "error", uerr)
		}
		return fmt.Errorf("subscribing to models invalidation: %w", err)
	}

	backendsSub, err := messaging.SubscribeJSON(nc, messaging.SubjectCacheInvalidateBackends, func(_ messaging.CacheInvalidateEvent) {
		g.Lock()
		cb := g.OnBackendOpCompleted
		g.Unlock()
		if cb != nil {
			// Run off-goroutine so a slow UpgradeChecker doesn't stall the
			// NATS receive loop. Matches the local fire-after-install path.
			go cb()
		}
	})
	if err != nil {
		if uerr := progressSub.Unsubscribe(); uerr != nil {
			xlog.Warn("failed to unsubscribe partial gallery progress sub", "error", uerr)
		}
		if uerr := cancelSub.Unsubscribe(); uerr != nil {
			xlog.Warn("failed to unsubscribe partial gallery cancel sub", "error", uerr)
		}
		if uerr := pauseSub.Unsubscribe(); uerr != nil {
			xlog.Warn("failed to unsubscribe partial gallery pause sub", "error", uerr)
		}
		if uerr := modelsSub.Unsubscribe(); uerr != nil {
			xlog.Warn("failed to unsubscribe partial models sub", "error", uerr)
		}
		return fmt.Errorf("subscribing to backends invalidation: %w", err)
	}

	g.Lock()
	g.broadcastSubs = append(g.broadcastSubs, progressSub, cancelSub, pauseSub, modelsSub, backendsSub)
	g.Unlock()
	return nil
}

// CloseBroadcasts drops the wildcard subscriptions. Safe to call multiple times.
func (g *GalleryService) CloseBroadcasts() {
	g.Lock()
	subs := g.broadcastSubs
	g.broadcastSubs = nil
	g.Unlock()
	for _, s := range subs {
		if err := s.Unsubscribe(); err != nil {
			xlog.Warn("GalleryService unsubscribe failed", "error", err)
		}
	}
}

// Hydrate loads still-active operations from the GalleryStore into the
// in-memory statuses map so a freshly-started replica does not return an
// empty /api/operations payload while a peer is mid-install. Idempotent.
// No-op when no store is wired.
//
// The reconstructed OpStatus carries no Error type — the DB stores the
// message as a string and Hydrate surfaces it via errors.New so the UI's
// "operation failed" banner survives a frontend restart.
func (g *GalleryService) Hydrate() error {
	g.Lock()
	store := g.galleryStore
	g.Unlock()
	if store == nil {
		return nil
	}
	ops, err := store.ListActive()
	if err != nil {
		return fmt.Errorf("listing active gallery ops: %w", err)
	}
	g.Lock()
	defer g.Unlock()
	for _, op := range ops {
		// Skip rows that already have an in-memory status — the live
		// broadcast subscriber will fill any gaps with fresher data.
		if _, ok := g.statuses[op.ID]; ok {
			continue
		}
		st := &OpStatus{
			Message:            op.Message,
			Progress:           op.Progress,
			FileName:           op.FileName,
			TotalFileSize:      op.TotalFileSize,
			DownloadedFileSize: op.DownloadedFileSize,
			GalleryElementName: op.GalleryElementName,
			Cancellable:        op.Cancellable,
			Deletion:           op.OpType == "model_delete",
		}
		if op.Error != "" {
			st.Error = errors.New(op.Error)
		}
		g.statuses[op.ID] = st
	}
	xlog.Info("Hydrated gallery service statuses from store", "count", len(ops))
	return nil
}
