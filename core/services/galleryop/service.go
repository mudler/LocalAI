package galleryop

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

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
	cancellations  map[string]context.CancelFunc

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
		cancellations:         make(map[string]context.CancelFunc),
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

// ModelArtifactMaterializer returns the controller-only acquisition capability
// used by startup paths that install gallery entries outside the operation loop.
func (g *GalleryService) ModelArtifactMaterializer() config.ArtifactMaterializer {
	if g == nil || g.appConfig == nil {
		return nil
	}
	return g.appConfig.ModelArtifactMaterializer
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
			if err := store.UpdateProgress(s, op.Progress, op.Message, op.DownloadedFileSize, op.Cancellable,
				distributed.OperationProgressDetails{
					Phase: op.Phase, CurrentBytes: op.CurrentBytes, TotalBytes: op.TotalBytes,
				}); err != nil {
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
	// Collect the IDs before the update: once CleanStale flips them to
	// "failed" they no longer match the stale predicate.
	staleIDs, err := store.ListStale(age)
	if err != nil {
		xlog.Warn("Failed to list stale gallery operations", "error", err)
	}
	n, err := store.CleanStale(age)
	if err != nil {
		return 0, err
	}
	if n > 0 {
		xlog.Info("Reaped stale gallery operations", "count", n)
	}
	// The database row is only half the picture. GET /models/jobs/<id> and
	// /api/operations read the in-memory statuses map, which is populated
	// locally and via the NATS progress broadcast and never expires. An op
	// orphaned by a replica that died mid-download therefore kept serving its
	// last frozen tick (phase=downloading, processed=false, error=none) on
	// every replica indefinitely, long after the reaper had already given up
	// on the row. Reconcile the in-memory copy with the reap.
	for _, id := range staleIDs {
		g.failStaleStatus(id)
	}
	return n, nil
}

// failStaleStatus flips a locally-cached in-progress status to a terminal
// failure after its store row was reaped. Statuses that already reached a
// terminal state are left alone so a genuine completion or cancellation that
// raced the reaper is not rewritten as a failure.
func (g *GalleryService) failStaleStatus(id string) {
	g.Lock()
	st, ok := g.statuses[id]
	if !ok || st == nil || st.Processed {
		g.Unlock()
		return
	}
	elementName := st.GalleryElementName
	g.Unlock()

	xlog.Warn("Marking orphaned gallery operation as failed", "op_id", id, "element", elementName)
	g.UpdateStatus(id, &OpStatus{
		Processed:          true,
		Error:              errors.New("stale operation reaped (abandoned by a crashed or restarted instance)"),
		Message:            "error: stale operation reaped (abandoned by a crashed or restarted instance)",
		GalleryElementName: elementName,
		Cancellable:        false,
	})
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

	cancelFunc, localExists := g.cancellations[id]
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
	if cancelFunc != nil {
		cancelFunc()
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
	cancelFunc, hasCancel := g.cancellations[id]
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
		cancelFunc()
	}
}

// newUserCancellableContext returns a child context whose CancelFunc cancels
// with the downloader.ErrUserCancelled cause. This lets the download layer
// distinguish a deliberate user cancel (discard the half-downloaded .partial)
// from an incidental cancellation such as process shutdown (keep the .partial
// so the next run resumes via Range instead of restarting from zero).
func newUserCancellableContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancelCause := context.WithCancelCause(parent)
	return ctx, func() { cancelCause(downloader.ErrUserCancelled) }
}

// storeCancellation stores a cancellation function for an operation
func (g *GalleryService) storeCancellation(id string, cancelFunc context.CancelFunc) {
	g.Lock()
	defer g.Unlock()
	g.cancellations[id] = cancelFunc
}

// StoreCancellation is a public method to store a cancellation function for an operation
// This allows cancellation functions to be stored immediately when operations are created,
// enabling cancellation of queued operations that haven't started processing yet.
func (g *GalleryService) StoreCancellation(id string, cancelFunc context.CancelFunc) {
	g.storeCancellation(id, cancelFunc)
}

// removeCancellation removes a cancellation function when operation completes
func (g *GalleryService) removeCancellation(id string) {
	g.Lock()
	defer g.Unlock()
	delete(g.cancellations, id)
}

// runOpHandler runs one operation handler and converts a panic into an error.
//
// The gallery worker is a single goroutine consuming both channels serially. A
// panic anywhere in an install handler (a malformed gallery entry, a nil
// dereference in a backend-specific path) took down the entire process with it,
// and every queued operation went with it. Containing the panic to the
// operation that caused it keeps the consumer alive so subsequent operations
// are still picked up, and surfaces the failure on the op itself instead of as
// an unexplained restart.
func runOpHandler(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			xlog.Error("Gallery operation handler panicked", "panic", r, "stack", string(debug.Stack()))
			err = fmt.Errorf("gallery operation handler panicked: %v", r)
		}
	}()
	return fn()
}

func (g *GalleryService) Start(c context.Context, cl *config.ModelConfigLoader, systemState *system.SystemState) error {
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
					op.Context, op.CancelFunc = newUserCancellableContext(c)
					g.storeCancellation(op.ID, op.CancelFunc)
				} else if op.CancelFunc != nil {
					g.storeCancellation(op.ID, op.CancelFunc)
				}
				// Create DB record for distributed tracking
				if g.galleryStore != nil {
					if err := g.galleryStore.Create(&distributed.GalleryOperationRecord{
						ID:                 op.ID,
						GalleryElementName: op.GalleryElementName,
						OpType:             "backend_install",
						Status:             "pending",
						Cancellable:        true,
					}); err != nil {
						// Not fatal: the install still runs and the in-memory
						// status still updates. Logged because without the row
						// the cross-replica dedup guard and hydration cannot
						// see this operation at all.
						xlog.Warn("Failed to create gallery operation record", "op_id", op.ID, "error", err)
					}
				}
				err := runOpHandler(func() error { return g.backendHandler(&op, systemState) })
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
					op.Context, op.CancelFunc = newUserCancellableContext(c)
					g.storeCancellation(op.ID, op.CancelFunc)
				} else if op.CancelFunc != nil {
					g.storeCancellation(op.ID, op.CancelFunc)
				}
				// Create DB record for distributed tracking
				if g.galleryStore != nil {
					opType := "model_install"
					if op.Delete {
						opType = "model_delete"
					}
					if err := g.galleryStore.Create(&distributed.GalleryOperationRecord{
						ID:                 op.ID,
						GalleryElementName: op.GalleryElementName,
						OpType:             opType,
						Status:             "pending",
						// A delete is not cancellable; an install is.
						Cancellable: !op.Delete,
					}); err != nil {
						xlog.Warn("Failed to create gallery operation record", "op_id", op.ID, "error", err)
					}
				}
				err := runOpHandler(func() error { return g.modelHandler(&op, cl, systemState) })
				if err != nil {
					updateError(op.ID, err)
				}
				g.removeCancellation(op.ID)
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
		if uerr := modelsSub.Unsubscribe(); uerr != nil {
			xlog.Warn("failed to unsubscribe partial models sub", "error", uerr)
		}
		return fmt.Errorf("subscribing to backends invalidation: %w", err)
	}

	g.Lock()
	g.broadcastSubs = append(g.broadcastSubs, progressSub, cancelSub, modelsSub, backendsSub)
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
			Phase:              op.Phase,
			CurrentBytes:       op.CurrentBytes,
			TotalBytes:         op.TotalBytes,
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
