package workerctl

// BackendInstallRequest is the payload for a backend.install control request.
type BackendInstallRequest struct {
	Backend          string `json:"backend"`
	ModelID          string `json:"model_id,omitempty"`
	BackendGalleries string `json:"backend_galleries,omitempty"`
	// URI is set for external installs (OCI image, URL, or path). When non-empty
	// the worker routes to InstallExternalBackend instead of the gallery lookup.
	URI   string `json:"uri,omitempty"`
	Name  string `json:"name,omitempty"`
	Alias string `json:"alias,omitempty"`
	// ReplicaIndex selects which slot on the worker this load occupies, so two
	// concurrent backend.install requests for the same model land on distinct
	// gRPC processes and ports. Workers older than this field treat it as 0
	// (single-replica behavior: no collision because the controller never
	// asks for replica > 0 on a node whose MaxReplicasPerModel is 1).
	ReplicaIndex int32 `json:"replica_index,omitempty"`
	// Force is retained on the wire only for backward compatibility with
	// pre-2026-05-08 masters that did not know about backend.upgrade. New
	// callers MUST send to messaging.SubjectNodeBackendUpgrade instead. Workers continue
	// to honor Force=true here so a rolling update with new master + old
	// worker still works (the master's install fallback path also uses this
	// when backend.upgrade finds no route to the worker).
	Force bool `json:"force,omitempty"`
	// OpID identifies the admin-side operation. When non-empty the worker
	// publishes BackendInstallProgressEvent values to
	// messaging.SubjectNodeBackendInstallProgress(nodeID, OpID) while the install is
	// running, debounced to roughly 250ms. Empty means the caller is a
	// reconciler-driven retry that does not need progress streamed.
	OpID string `json:"op_id,omitempty"`
	// OperationID names the load this install belongs to (the load job
	// generation). With it the worker tracks the load as an operation it can
	// bound: it kills the backend when renewals stop or the deadline passes.
	// Workers older than this field ignore it. Empty means a controller older
	// than this field: the worker then tracks an anonymous operation.
	OperationID string `json:"operation_id,omitempty"`
	// DeadlineMs is the longest the load may run, as a duration in
	// milliseconds, not a timestamp, so worker clock skew does not matter. The
	// worker converts it to its own monotonic deadline when the request
	// arrives. Zero means the worker's default.
	DeadlineMs int64 `json:"deadline_ms,omitempty"`
}

// BackendInstallReply is the response from a backend.install control request.
type BackendInstallReply struct {
	Success bool   `json:"success"`
	Address string `json:"address,omitempty"` // gRPC address of the backend process (host:port)
	Error   string `json:"error,omitempty"`
	// ProcessInstance identifies this incarnation of the backend process, so a
	// later stop cannot hit a replacement that took the same port.
	ProcessInstance string `json:"process_instance,omitempty"`
	// ReportsOperations is true on a worker that tracks load operations. A reply
	// without it comes from a worker that predates them: it cannot confirm a
	// stop, so the controller stops its process by exact address and holds the
	// model for the load deadline. The capability belongs to the worker, not to
	// the transport.
	ReportsOperations bool `json:"reports_operations,omitempty"`
}

// BackendUpgradeRequest is the payload for a backend.upgrade control request.
// It is intentionally a strict subset of BackendInstallRequest: there is no
// Force field because the upgrade subject IS the force semantics; no ModelID
// because upgrade is backend-scoped (it stops every replica using the binary
// before re-installing). Per-replica restart happens on the next routine load.
type BackendUpgradeRequest struct {
	Backend          string `json:"backend"`
	BackendGalleries string `json:"backend_galleries,omitempty"`
	URI              string `json:"uri,omitempty"`
	Name             string `json:"name,omitempty"`
	Alias            string `json:"alias,omitempty"`
	// ReplicaIndex is informational: upgrade stops all replicas regardless,
	// but the field lets future per-replica metadata (e.g. progress reporting
	// scoped to a slot) ride the same wire without a v3 type.
	ReplicaIndex int32 `json:"replica_index,omitempty"`
	// OpID identifies the admin-side operation. When non-empty the worker
	// publishes BackendInstallProgressEvent values to
	// messaging.SubjectNodeBackendInstallProgress(nodeID, OpID) while the force-reinstall
	// runs, so the master can stream per-node progress for upgrades exactly as
	// it already does for installs (an upgrade IS a force-reinstall, so the
	// install-progress subject is reused rather than minting a new one; that adds no new
	// NATS permission or rolling-update compat surface). Empty on legacy callers.
	OpID string `json:"op_id,omitempty"`
}

// BackendUpgradeReply mirrors BackendInstallReply minus Address: upgrade does
// not start a process, so there is no port to advertise. The subsequent
// routine load will re-bind via backend.install and learn the new address.
type BackendUpgradeReply struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`

	// StoppedProcessKeys / ReportsStoppedProcesses carry the same
	// stale-row-invalidation contract as on BackendDeleteReply; an upgrade
	// force-stops every process using the binary and starts none back up, so it
	// recycles ports exactly the way a delete does. See that type for why the
	// boolean is not redundant with an empty list.
	StoppedProcessKeys      []string `json:"stopped_process_keys,omitempty"`
	ReportsStoppedProcesses bool     `json:"reports_stopped_processes,omitempty"`
}

// BackendListRequest is the payload for a backend.list control request.
type BackendListRequest struct{}

// BackendListReply is the response from a backend.list control request.
type BackendListReply struct {
	Backends []NodeBackendInfo `json:"backends"`
	Error    string            `json:"error,omitempty"`
}

// NodeBackendInfo describes a backend installed on a worker node.
type NodeBackendInfo struct {
	Name        string `json:"name"`
	IsSystem    bool   `json:"is_system"`
	IsMeta      bool   `json:"is_meta"`
	InstalledAt string `json:"installed_at,omitempty"`
	GalleryURL  string `json:"gallery_url,omitempty"`
	// Version, URI and Digest enable cluster-wide upgrade detection:
	// without them, the frontend cannot tell whether the installed OCI
	// image matches the gallery entry, and upgrades silently never surface.
	Version string `json:"version,omitempty"`
	URI     string `json:"uri,omitempty"`
	Digest  string `json:"digest,omitempty"`
}

// BackendStopRequest controls worker-side process shutdown. Force skips the
// best-effort Free RPC so a backend stuck serving a request can still be
// terminated by the watchdog.
type BackendStopRequest struct {
	Backend string `json:"backend"`
	Force   bool   `json:"force,omitempty"`
}

// BackendStopReply is the worker's answer to a backend.stop request.
//
// backend.stop had no reply until this type existed. The controller published
// and returned success as soon as the local publish succeeded, so a stop that
// killed nothing, and a stop that failed outright, both looked identical to a
// stop that worked. An operator calling the unload endpoint got HTTP 200 while
// the backend kept running and holding its VRAM.
type BackendStopReply struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`

	// StoppedProcessKeys names every `modelID#replica` process the worker
	// terminated while serving this request.
	StoppedProcessKeys []string `json:"stopped_process_keys,omitempty"`

	// ReportsStoppedProcesses distinguishes "this worker enumerates what it
	// stopped and stopped nothing" from "this worker predates the field", the
	// same way BackendDeleteReply does. Both send an empty list and only the
	// first is authoritative, so a controller that cannot tell them apart would
	// read silence as a completed stop, the exact conclusion this reply exists
	// to prevent.
	ReportsStoppedProcesses bool `json:"reports_stopped_processes,omitempty"`
}

// BackendDeleteRequest is the payload for a backend.delete control request.
type BackendDeleteRequest struct {
	Backend string `json:"backend"`
}

// BackendDeleteReply is the response from a backend.delete control request.
type BackendDeleteReply struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`

	// StoppedProcessKeys names every `modelID#replica` process the worker
	// terminated while serving this delete. Stopping a process returns its gRPC
	// port to the worker's allocator, so any NodeModel row still pointing at
	// that address becomes a live misroute the moment an unrelated backend
	// binds the recycled port: probeHealth verifies liveness, not identity, so
	// the request is served by the wrong backend rather than failing. The
	// controller uses these keys to drop the rows eagerly.
	StoppedProcessKeys []string `json:"stopped_process_keys,omitempty"`

	// ReportsStoppedProcesses distinguishes "this worker enumerates what it
	// stopped and stopped nothing" from "this worker predates the field". Both
	// send an empty list, and only the first is authoritative. Without this
	// flag a controller cannot tell them apart and would eventually be tempted
	// to read silence as a completed cleanup, which is precisely the wrong
	// conclusion against an older worker.
	ReportsStoppedProcesses bool `json:"reports_stopped_processes,omitempty"`
}
