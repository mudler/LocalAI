package galleryop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/distributed"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/pkg/xsync"
	"github.com/mudler/xlog"
)

type ManagementOp[T any, E any] struct {
	ID                 string
	GalleryElementName string
	Delete             bool

	Req T

	// If specified, we install directly the gallery element
	GalleryElement *E

	Galleries        []config.Gallery
	BackendGalleries []config.Gallery

	// Context for cancellation support
	Context    context.Context
	CancelFunc context.CancelFunc

	// External backend installation parameters (for OCI/URL/path)
	// These are used when installing backends from external sources rather than galleries
	ExternalURI   string // The OCI image, URL, or path
	ExternalName  string // Custom name for the backend
	ExternalAlias string // Custom alias for the backend

	// TargetNodeID scopes a backend install/upgrade to a single worker node.
	// Empty means fan out to every healthy backend node (the previous behavior).
	// Set by InstallBackendOnNodeEndpoint so an admin can install a hardware-specific
	// build on one node without touching the rest of the cluster.
	TargetNodeID string

	// Upgrade is true if this is an upgrade operation (not a fresh install)
	Upgrade bool

	// Force reinstalls a backend even when it is already installed and
	// runnable. Without it a backend install op is idempotent — API clients
	// that ensure a backend exists on every boot must not trigger a full
	// artifact re-download each time. The UI's explicit "Reinstall backend"
	// action sets it.
	Force bool
}

type OpStatus struct {
	Deletion           bool    `json:"deletion"` // Deletion is true if the operation is a deletion
	FileName           string  `json:"file_name"`
	Error              error   `json:"-"` // see MarshalJSON: serialized to "error" as a string
	Processed          bool    `json:"processed"`
	Message            string  `json:"message"`
	Progress           float64 `json:"progress"`
	TotalFileSize      string  `json:"file_size"`
	DownloadedFileSize string  `json:"downloaded_size"`
	GalleryElementName string  `json:"gallery_element_name"`
	Cancelled          bool    `json:"cancelled"`   // Cancelled is true if the operation was cancelled
	Cancellable        bool    `json:"cancellable"` // Cancellable is true if the operation can be cancelled
	Paused             bool    `json:"paused"`      // Paused is true if the operation was paused (resumable)

	// Nodes is the per-node breakdown for a fanned-out backend install.
	// Populated by DistributedBackendManager (per-node terminal status)
	// and by the Phase 2 progress bridge (per-byte ticks). The
	// /api/operations handler surfaces this so the UI can render an
	// expandable per-node view of an in-flight install.
	Nodes []NodeProgress `json:"nodes,omitempty"`
}

// opStatusWire is the JSON shape used when an OpStatus crosses a process
// boundary (NATS broadcast). The Error field on OpStatus is an `error`
// interface, which json.Marshal flattens to `{}` because the concrete error
// type usually has no exported fields — so a failed install replicated to a
// peer frontend would arrive with a nil error and the UI would never surface
// the failure. opStatusWire serializes the error as its Error() string and
// reconstructs it on read.
type opStatusWire struct {
	Deletion           bool           `json:"deletion"`
	FileName           string         `json:"file_name"`
	ErrorMessage       string         `json:"error,omitempty"`
	Processed          bool           `json:"processed"`
	Message            string         `json:"message"`
	Progress           float64        `json:"progress"`
	TotalFileSize      string         `json:"file_size"`
	DownloadedFileSize string         `json:"downloaded_size"`
	GalleryElementName string         `json:"gallery_element_name"`
	Cancelled          bool           `json:"cancelled"`
	Cancellable        bool           `json:"cancellable"`
	Paused             bool           `json:"paused"`
	Nodes              []NodeProgress `json:"nodes,omitempty"`
}

func (o OpStatus) MarshalJSON() ([]byte, error) {
	w := opStatusWire{
		Deletion:           o.Deletion,
		FileName:           o.FileName,
		Processed:          o.Processed,
		Message:            o.Message,
		Progress:           o.Progress,
		TotalFileSize:      o.TotalFileSize,
		DownloadedFileSize: o.DownloadedFileSize,
		GalleryElementName: o.GalleryElementName,
		Cancelled:          o.Cancelled,
		Cancellable:        o.Cancellable,
		Paused:             o.Paused,
		Nodes:              o.Nodes,
	}
	if o.Error != nil {
		w.ErrorMessage = o.Error.Error()
	}
	return json.Marshal(w)
}

func (o *OpStatus) UnmarshalJSON(data []byte) error {
	var w opStatusWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	o.Deletion = w.Deletion
	o.FileName = w.FileName
	o.Processed = w.Processed
	o.Message = w.Message
	o.Progress = w.Progress
	o.TotalFileSize = w.TotalFileSize
	o.DownloadedFileSize = w.DownloadedFileSize
	o.GalleryElementName = w.GalleryElementName
	o.Cancelled = w.Cancelled
	o.Cancellable = w.Cancellable
	o.Paused = w.Paused
	o.Nodes = w.Nodes
	if w.ErrorMessage != "" {
		o.Error = errors.New(w.ErrorMessage)
	} else {
		o.Error = nil
	}
	return nil
}

// OpCacheEvent is the NATS payload broadcast by frontend replicas when an
// admin operation is admitted (SubjectGalleryOpStart) or dismissed
// (SubjectGalleryOpEnd). Peers merge these into their local OpCache so a
// load-balanced /api/operations poll never returns an empty list while a
// peer is mid-install.
type OpCacheEvent struct {
	JobID     string `json:"job_id"`
	CacheKey  string `json:"cache_key"`
	IsBackend bool   `json:"is_backend"`
}

// GalleryProgressEvent is the NATS payload for an OpStatus broadcast. It
// wraps OpStatus with the opID/JobID so subscribers reading the wildcard
// subject don't need to parse it back out of the NATS subject string.
type GalleryProgressEvent struct {
	JobID  string    `json:"job_id"`
	Status *OpStatus `json:"status"`
}

// GalleryCancelEvent is the NATS payload for a gallery cancellation. The
// local cancellation func may live on a different frontend replica than the
// one that received the UI cancel button click; the broadcast subscriber
// runs the cancel func on whichever replica registered it.
type GalleryCancelEvent struct {
	JobID string `json:"id"`
}

// GalleryPauseEvent is the NATS payload for a gallery pause. Mirroring the
// cancel pattern, the pause func may live on a different frontend replica;
// the broadcast subscriber applies the pause locally. A paused operation can
// be resumed later — the .partial file is preserved.
type GalleryPauseEvent struct {
	JobID string `json:"id"`
}

// NodeStatus values shared between NodeProgress (per-node tick) and the
// NodeOpStatus surfaced by DistributedBackendManager's fan-out. Defined
// as exported constants so producers (the manager, the progress bridge)
// and consumers (the /api/operations handler, the React OperationsBar
// through its JSON contract) stay in sync via a single source of truth.
const (
	NodeStatusQueued          = "queued"            // node accepted the intent but install has not started
	NodeStatusDownloading     = "downloading"       // worker is actively pulling the OCI image
	NodeStatusRunningOnWorker = "running_on_worker" // NATS round-trip timed out but worker is still installing
	NodeStatusSuccess         = "success"           // install completed on this node
	NodeStatusError           = "error"             // install failed on this node
)

// NodeProgress is a single node's contribution to a backend install
// operation. Populated by DistributedBackendManager (per-node terminal
// status) and by the Phase 2 progress bridge (per-byte ticks). Read by
// the /api/operations handler so the UI can render an expandable
// per-node breakdown.
//
// Status holds one of the NodeStatus* constants above.
type NodeProgress struct {
	NodeID     string  `json:"node_id"`
	NodeName   string  `json:"node_name"`
	Status     string  `json:"status"`
	FileName   string  `json:"file_name,omitempty"`
	Current    string  `json:"current,omitempty"`
	Total      string  `json:"total,omitempty"`
	Percentage float64 `json:"percentage"`
	Phase      string  `json:"phase,omitempty"`
	Error      string  `json:"error,omitempty"`
}

type OpCache struct {
	status         *xsync.SyncedMap[string, string]
	backendOps     *xsync.SyncedMap[string, bool] // Tracks which operations are backend operations
	galleryService *GalleryService

	// Distributed sync (nil when standalone).
	mu    sync.RWMutex
	nats  messaging.MessagingClient
	store *distributed.GalleryStore
	subs  []messaging.Subscription
}

func NewOpCache(galleryService *GalleryService) *OpCache {
	return &OpCache{
		status:         xsync.NewSyncedMap[string, string](),
		backendOps:     xsync.NewSyncedMap[string, bool](),
		galleryService: galleryService,
	}
}

// SetMessagingClient enables cross-replica OpCache sync. Once set, Set/
// SetBackend/DeleteUUID publish OpCacheEvent messages that peer OpCaches
// merge into their local maps. Call Start after this to subscribe.
func (m *OpCache) SetMessagingClient(nc messaging.MessagingClient) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nats = nc
}

// SetGalleryStore enables PostgreSQL-backed OpCache persistence.
// Set/SetBackend upsert the cache_key + is_backend_op columns; Start
// hydrates the in-memory maps from active rows so a freshly-started
// replica does not return an empty /api/operations payload while a peer
// is mid-install.
func (m *OpCache) SetGalleryStore(s *distributed.GalleryStore) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store = s
}

// Start hydrates the in-memory maps from PostgreSQL (if a store was wired)
// and subscribes to the broadcast subjects (if NATS was wired). It returns
// the first subscribe error; hydration errors are logged but non-fatal so
// the frontend still comes up.
//
// Safe to call exactly once after SetMessagingClient / SetGalleryStore. The
// ctx parameter is reserved for future cancellation — current subscriptions
// live for the lifetime of the OpCache and are released by Close.
func (m *OpCache) Start(_ context.Context) error {
	m.mu.RLock()
	store := m.store
	nc := m.nats
	m.mu.RUnlock()

	if store != nil {
		if err := m.hydrateFromStore(store); err != nil {
			xlog.Warn("OpCache hydrate failed; starting empty", "error", err)
		}
	}

	if nc == nil {
		return nil
	}

	startSub, err := messaging.SubscribeJSON(nc, messaging.SubjectGalleryOpStart, func(evt OpCacheEvent) {
		m.applyStart(evt)
	})
	if err != nil {
		return err
	}
	endSub, err := messaging.SubscribeJSON(nc, messaging.SubjectGalleryOpEnd, func(evt OpCacheEvent) {
		m.applyEnd(evt)
	})
	if err != nil {
		if uerr := startSub.Unsubscribe(); uerr != nil {
			xlog.Warn("failed to unsubscribe partial OpCache subscription", "error", uerr)
		}
		return err
	}

	m.mu.Lock()
	m.subs = append(m.subs, startSub, endSub)
	m.mu.Unlock()
	return nil
}

// Close drops all NATS subscriptions. Safe to call multiple times.
func (m *OpCache) Close() {
	m.mu.Lock()
	subs := m.subs
	m.subs = nil
	m.mu.Unlock()
	for _, s := range subs {
		if err := s.Unsubscribe(); err != nil {
			xlog.Warn("OpCache unsubscribe failed", "error", err)
		}
	}
}

func (m *OpCache) hydrateFromStore(store *distributed.GalleryStore) error {
	ops, err := store.ListActive()
	if err != nil {
		return err
	}
	for _, op := range ops {
		if op.CacheKey == "" {
			continue
		}
		m.status.Set(op.CacheKey, op.ID)
		if op.IsBackendOp {
			m.backendOps.Set(op.CacheKey, true)
		}
	}
	return nil
}

// applyStart merges an inbound OpStart event into the local maps. Idempotent:
// receiving our own broadcast is a harmless re-assignment of the same value.
func (m *OpCache) applyStart(evt OpCacheEvent) {
	if evt.CacheKey == "" || evt.JobID == "" {
		return
	}
	m.status.Set(evt.CacheKey, evt.JobID)
	if evt.IsBackend {
		m.backendOps.Set(evt.CacheKey, true)
	}
}

// applyEnd removes any entries whose jobID matches the event. Idempotent.
func (m *OpCache) applyEnd(evt OpCacheEvent) {
	if evt.JobID == "" {
		return
	}
	for _, k := range m.status.Keys() {
		if m.status.Get(k) == evt.JobID {
			m.status.Delete(k)
			m.backendOps.Delete(k)
		}
	}
}

func (m *OpCache) Set(key string, value string) {
	m.status.Set(key, value)
	m.persistAndBroadcastStart(key, value, false)
}

// SetBackend sets a key-value pair and marks it as a backend operation
func (m *OpCache) SetBackend(key string, value string) {
	m.status.Set(key, value)
	m.backendOps.Set(key, true)
	m.persistAndBroadcastStart(key, value, true)
}

func (m *OpCache) persistAndBroadcastStart(key, value string, isBackend bool) {
	m.mu.RLock()
	store := m.store
	nc := m.nats
	m.mu.RUnlock()

	if store != nil {
		if err := store.UpsertCacheKey(value, key, isBackend); err != nil {
			xlog.Warn("OpCache failed to persist cache key", "job_id", value, "error", err)
		}
	}
	if nc != nil {
		if err := nc.Publish(messaging.SubjectGalleryOpStart, OpCacheEvent{
			JobID:     value,
			CacheKey:  key,
			IsBackend: isBackend,
		}); err != nil {
			xlog.Warn("OpCache failed to broadcast start", "job_id", value, "error", err)
		}
	}
}

// IsBackendOp returns true if the given key is a backend operation
func (m *OpCache) IsBackendOp(key string) bool {
	return m.backendOps.Get(key)
}

func (m *OpCache) Get(key string) string {
	return m.status.Get(key)
}

func (m *OpCache) DeleteUUID(uuid string) {
	deleted := false
	for _, k := range m.status.Keys() {
		if m.status.Get(k) == uuid {
			m.status.Delete(k)
			m.backendOps.Delete(k) // Also clean up the backend flag
			deleted = true
		}
	}
	if !deleted {
		return
	}
	m.mu.RLock()
	nc := m.nats
	m.mu.RUnlock()
	if nc != nil {
		if err := nc.Publish(messaging.SubjectGalleryOpEnd, OpCacheEvent{JobID: uuid}); err != nil {
			xlog.Warn("OpCache failed to broadcast end", "job_id", uuid, "error", err)
		}
	}
}

func (m *OpCache) Map() map[string]string {
	return m.status.Map()
}

func (m *OpCache) Exists(key string) bool {
	return m.status.Exists(key)
}

func (m *OpCache) GetStatus() (map[string]string, map[string]string) {
	taskTypes := map[string]string{}
	processingModelsData := map[string]string{}

	// Iterate a snapshot (Keys() copies) and build a fresh result map. We must
	// NOT delete from m.Map() during the range: Map() returns the live internal
	// map by reference, so a bare delete here would be an unsynchronized write
	// to a map four HTTP handlers read every ~1s — a concurrent-map-write crash.
	// Collect evictions and apply them via the locked DeleteUUID after the loop.
	var evict []string
	for _, k := range m.status.Keys() {
		v := m.status.Get(k)
		if v == "" {
			continue // raced with a concurrent Delete
		}
		status := m.galleryService.GetStatus(v)
		// Terminal ops must not keep showing as "processing". Cleanup was
		// previously only triggered by a client polling /api/backends/job/:uid,
		// but the Manage-page Reinstall/Upgrade buttons never poll, so completed
		// ops leaked into processingBackends forever and the card spun
		// "reinstalling" indefinitely. Evict here on the list read (the UI always
		// calls this). DeleteUUID broadcasts the eviction so peer replicas converge.
		//
		// We evict ONLY a clean success (progress 100 + "completed", matching the
		// job-poll's historical delete condition) or a cancellation. Deliberately
		// NOT evicted:
		//   - failed ops (Error != nil): kept so /api/operations can surface the
		//     error and offer Dismiss.
		//   - the ErrWorkerStillInstalling soft-path (Processed=true, Error=nil,
		//     progress != 100): the worker is still installing in the background
		//     and the reconciler confirms the real outcome later — evicting it
		//     would hide an install that may still fail.
		if status != nil && status.Processed &&
			((status.Progress == 100 && status.Message == "completed") || status.Cancelled) {
			evict = append(evict, v)
			continue
		}
		processingModelsData[k] = v
		taskTypes[k] = "Installation"
		if status != nil && status.Deletion {
			taskTypes[k] = "Deletion"
		} else if status == nil {
			taskTypes[k] = "Waiting"
		}
	}

	for _, v := range evict {
		m.DeleteUUID(v)
	}

	return processingModelsData, taskTypes
}

// NodeScopedKeyPrefix is the opcache key prefix used by InstallBackendOnNodeEndpoint
// so per-node installs do not collide on the bare backend name. Format:
// "node:<nodeID>:<backend>". Read by /api/operations to extract nodeID for the UI.
const NodeScopedKeyPrefix = "node:"

// NodeScopedKey returns the opcache key for a node-scoped backend operation.
// The prefix lets ParseNodeScopedKey detach the nodeID back out so the
// operations endpoint can surface it without storing nodeID separately.
func NodeScopedKey(nodeID, backend string) string {
	return NodeScopedKeyPrefix + nodeID + ":" + backend
}

// ParseNodeScopedKey extracts (nodeID, backend) from a key built by NodeScopedKey.
// Returns ok=false for keys that lack the prefix or are missing the nodeID or
// backend segment. Backend names containing colons are preserved because we
// split on the first colon after the prefix only.
func ParseNodeScopedKey(key string) (nodeID, backend string, ok bool) {
	rest, hasPrefix := strings.CutPrefix(key, NodeScopedKeyPrefix)
	if !hasPrefix {
		return "", "", false
	}
	nodeID, backend, ok = strings.Cut(rest, ":")
	if !ok || nodeID == "" || backend == "" {
		return "", "", false
	}
	return nodeID, backend, true
}
