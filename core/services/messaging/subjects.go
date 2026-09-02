package messaging

import "strings"

// sanitizeSubjectToken replaces NATS-reserved characters in a subject token.
// NATS uses '.' as hierarchy delimiter and '*'/'>' as wildcards.
func sanitizeSubjectToken(s string) string {
	r := strings.NewReplacer(".", "-", "*", "-", ">", "-", " ", "-", "\t", "-", "\n", "-")
	return r.Replace(s)
}

// NATS subject constants for the distributed architecture.
// Following the notetaker pattern: <entity>.<action>

// Job Distribution (Queue Groups — load-balanced, one consumer gets each message)
const (
	SubjectJobsNew      = "jobs.new"
	SubjectMCPCIJobsNew = "jobs.mcp-ci.new"
	SubjectAgentExecute = "agent.execute"
	QueueWorkers        = "workers"
)

// Status Updates (Pub/Sub — all subscribers get every message, for SSE bridging)
// These use parameterized subjects: e.g. SubjectAgentEvents("myagent", "user1")
const (
	subjectAgentEventsPrefix = "agent."
	subjectJobProgressPrefix = "jobs."
	subjectFineTunePrefix    = "finetune."
	subjectGalleryPrefix     = "gallery."
)

// SubjectAgentEvents returns the NATS subject for agent SSE events.
func SubjectAgentEvents(agentName, userID string) string {
	if userID == "" {
		userID = "anonymous"
	}
	return subjectAgentEventsPrefix + sanitizeSubjectToken(agentName) + ".events." + sanitizeSubjectToken(userID)
}

// SubjectJobProgress returns the NATS subject for job progress updates.
func SubjectJobProgress(jobID string) string {
	return subjectJobProgressPrefix + sanitizeSubjectToken(jobID) + ".progress"
}

// SubjectJobResult returns the NATS subject for the final job result (terminal state).
func SubjectJobResult(jobID string) string {
	return subjectJobProgressPrefix + sanitizeSubjectToken(jobID) + ".result"
}

// MCP Tool Execution (Request-Reply via NATS — load-balanced across agent workers)
const (
	SubjectMCPToolExecute = "mcp.tools.execute"
	SubjectMCPDiscovery   = "mcp.discovery"
	QueueAgentWorkers     = "agent-workers"
)

// SubjectFineTuneProgress returns the NATS subject for fine-tune progress.
func SubjectFineTuneProgress(jobID string) string {
	return subjectFineTunePrefix + sanitizeSubjectToken(jobID) + ".progress"
}

// SubjectGalleryProgress returns the NATS subject for gallery download progress.
func SubjectGalleryProgress(opID string) string {
	return subjectGalleryPrefix + sanitizeSubjectToken(opID) + ".progress"
}

// SubjectStagingProgress returns the NATS subject a frontend replica publishes
// file-staging progress on. Staging progress is otherwise per-process state
// (the SmartRouter's in-memory StagingTracker), so without this broadcast a
// /api/operations poll that round-robins onto a replica that did not originate
// the staging op sees nothing - the progress row flickers in multi-replica
// deployments. Peers subscribe to the wildcard and merge.
func SubjectStagingProgress(modelID string) string {
	return subjectStagingPrefix + sanitizeSubjectToken(modelID) + ".progress"
}

const subjectStagingPrefix = "staging."

// SubjectStagingProgressWildcard matches every replica's staging-progress
// broadcasts so a peer can mirror staging ops it did not originate.
const SubjectStagingProgressWildcard = "staging.*.progress"

// SubjectGalleryOpStart and SubjectGalleryOpEnd are broadcast subjects for the
// in-memory OpCache lifecycle. Frontend replicas publish to these when an
// admin admits a new install/delete (Start) and when an operation is
// dismissed (End), so peer replicas can keep their OpCache in sync without
// hitting PostgreSQL on every UI poll.
const (
	SubjectGalleryOpStart = "gallery.opcache.start"
	SubjectGalleryOpEnd   = "gallery.opcache.end"
)

// Control Signals (Pub/Sub — targeted cancellation)
const (
	subjectJobCancelPrefix      = "jobs."
	subjectAgentCancelPrefix    = "agent."
	subjectFineTuneCancelPrefix = "finetune."
	subjectGalleryCancelPrefix  = "gallery."
	subjectResponseCancelPrefix = "responses."
)

// Wildcard subjects for NATS subscriptions that match all IDs.
const (
	SubjectJobCancelWildcard       = "jobs.*.cancel"
	SubjectJobResultWildcard       = "jobs.*.result"
	SubjectJobProgressWildcard     = "jobs.*.progress"
	SubjectAgentCancelWildcard     = "agent.*.cancel"
	SubjectGalleryCancelWildcard   = "gallery.*.cancel"
	SubjectGalleryProgressWildcard = "gallery.*.progress"
	SubjectResponseCancelWildcard  = "responses.*.cancel"
)

// SubjectJobCancel returns the NATS subject to cancel a running job.
func SubjectJobCancel(jobID string) string {
	return subjectJobCancelPrefix + sanitizeSubjectToken(jobID) + ".cancel"
}

// SubjectAgentCancel returns the NATS subject to cancel agent execution.
func SubjectAgentCancel(agentID string) string {
	return subjectAgentCancelPrefix + sanitizeSubjectToken(agentID) + ".cancel"
}

// SubjectFineTuneCancel returns the NATS subject to stop fine-tuning.
func SubjectFineTuneCancel(jobID string) string {
	return subjectFineTuneCancelPrefix + sanitizeSubjectToken(jobID) + ".cancel"
}

// SubjectGalleryCancel returns the NATS subject to cancel a gallery download.
func SubjectGalleryCancel(opID string) string {
	return subjectGalleryCancelPrefix + sanitizeSubjectToken(opID) + ".cancel"
}

// SubjectResponseCancel returns the NATS subject used to cancel an in-flight
// Open Responses generation. Only the replica that created the response holds
// its context.CancelFunc, so a cancel that lands on any other replica is
// broadcast here and applied by whichever replica actually owns the function.
// Broadcast rather than request/reply on purpose: if the owner crashed or was
// scaled down, nobody answers and the caller must not block waiting for a
// reply that will never come.
func SubjectResponseCancel(responseID string) string {
	return subjectResponseCancelPrefix + sanitizeSubjectToken(responseID) + ".cancel"
}

// Node Backend Lifecycle
//
// The frontend's control plane no longer travels on NATS. The ten verbs that
// drove a worker's backend and model lifecycle are HTTP routes under
// workerctl.Prefix, served on the worker's own loopback server and reached
// through its tunnel, so a subject builder for any of them would be a subject
// nothing publishes and nothing subscribes to.
//
// ONE survives: backend.stop, and only for AGENT workers. They hold no tunnel,
// so they have no control plane to serve, and they subscribe to it to drop the
// MCP sessions cached for a backend that is going away. See
// nodes.RemoteUnloaderAdapter.stopBackend for the split, and
// core/cli/agent_worker.go for the subscriber.
//
// The request and reply types below are UNCHANGED and still live here: they are
// the wire format of the control routes, byte for byte what the subjects
// carried, so a worker and a frontend from different releases still understand
// each other.
const (
	subjectNodePrefix = "nodes."
)

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
	// (single-replica behavior — no collision because the controller never
	// asks for replica > 0 on a node whose MaxReplicasPerModel is 1).
	ReplicaIndex int32 `json:"replica_index,omitempty"`
	// Force is retained on the wire only for backward compatibility with
	// pre-2026-05-08 masters that did not know about backend.upgrade. New
	// callers MUST use workerctl.PathBackendUpgrade instead. Workers continue
	// to honor Force=true here so a rolling update with new master + old
	// worker still works (the master's install fallback path also uses this
	// when the worker answers that it does not serve the upgrade verb).
	Force bool `json:"force,omitempty"`
	// OpID identifies the admin-side operation. It travels so the worker can
	// name the operation on the BackendInstallProgressEvent values it writes
	// into the install response ahead of the reply, debounced to roughly 250ms.
	// Empty means the caller is a reconciler-driven retry that does not need
	// progress streamed.
	OpID string `json:"op_id,omitempty"`
}

// BackendInstallReply is the response from a backend.install control request.
type BackendInstallReply struct {
	Success bool `json:"success"`
	// WorkerLocalAddress is where the backend process listens ON THE WORKER,
	// which is a loopback address. It is not dialable from the frontend and
	// never was meant to be read that way: the frontend takes its PORT and
	// names it as the target of a stream on that worker's tunnel, and the
	// worker dials its own loopback there.
	//
	// The json tag stays "address" so a worker and a frontend from different
	// releases still understand each other. An older worker sends its
	// advertised host here; only the port is read, and the port is the same.
	WorkerLocalAddress string `json:"address,omitempty"`
	Error              string `json:"error,omitempty"`
}

// BackendUpgradeRequest is the payload for a backend.upgrade control request.
// It is intentionally a strict subset of BackendInstallRequest — there is no
// Force field because the upgrade subject IS the force semantics; no ModelID
// because upgrade is backend-scoped (it stops every replica using the binary
// before re-installing). Per-replica restart happens on the next routine load.
type BackendUpgradeRequest struct {
	Backend          string `json:"backend"`
	BackendGalleries string `json:"backend_galleries,omitempty"`
	URI              string `json:"uri,omitempty"`
	Name             string `json:"name,omitempty"`
	Alias            string `json:"alias,omitempty"`
	// ReplicaIndex is informational — upgrade stops all replicas regardless,
	// but the field lets future per-replica metadata (e.g. progress reporting
	// scoped to a slot) ride the same wire without a v3 type.
	ReplicaIndex int32 `json:"replica_index,omitempty"`
	// OpID identifies the admin-side operation, so an upgrade streams per-node
	// progress in its own response exactly as an install does. Empty on legacy
	// callers.
	OpID string `json:"op_id,omitempty"`
}

// BackendUpgradeReply mirrors BackendInstallReply minus Address — upgrade does
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
	// Version, URI and Digest enable cluster-wide upgrade detection —
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

// SubjectNodeBackendStop tells an AGENT worker that a backend is going away, so
// it can close the MCP sessions it cached for that backend.
//
// It is the one node subject left, and it is addressed only to agent nodes. A
// BACKEND worker takes its stop on workerctl.PathBackendStop over its tunnel,
// where it also kills the process and recycles the port; an agent worker runs
// no backend processes and only needs to hear that one went.
func SubjectNodeBackendStop(nodeID string) string {
	return subjectNodePrefix + sanitizeSubjectToken(nodeID) + ".backend.stop"
}

type ModelStopRequest struct {
	ModelName       string `json:"model_name"`
	ProcessKey      string `json:"process_key"`
	ExpectedAddress string `json:"expected_address"`
	Force           bool   `json:"force,omitempty"`
	ConfigRevision  string `json:"config_revision,omitempty"`
}

type ModelStopReply struct {
	Matched    bool   `json:"matched"`
	Freed      bool   `json:"freed"`
	Terminated bool   `json:"terminated"`
	ProcessKey string `json:"process_key"`
	Address    string `json:"address,omitempty"`
	Error      string `json:"error,omitempty"`
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

// ModelUnloadRequest is the payload for a model.unload control request.
type ModelUnloadRequest struct {
	ModelName string `json:"model_name"`
	Address   string `json:"address,omitempty"` // gRPC address of the backend process to unload from
}

// ModelUnloadReply is the response from a model.unload control request.
type ModelUnloadReply struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// ModelDeleteRequest is the payload for a model.delete control request.
type ModelDeleteRequest struct {
	ModelName string `json:"model_name"`
}

// ModelDeleteReply is the response from a model.delete control request.
type ModelDeleteReply struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// ModelsRunningRequest is the payload for a models.running control request.
type ModelsRunningRequest struct{}

// ModelsRunningReply is the response from a models.running control request.
type ModelsRunningReply struct {
	Models []RunningModelInfo `json:"models"`
	Error  string             `json:"error,omitempty"`
}

// RunningModelInfo identifies one live backend process on a worker. The triple
// is isomorphic to a controller NodeModel row's (model_name, replica_index,
// address), which is what lets the reconciler diff the two directly.
type RunningModelInfo struct {
	ModelID      string `json:"model_id"`
	ReplicaIndex int    `json:"replica_index"`
	Address      string `json:"address,omitempty"`
}

// File staging is no longer carried here. The four nodes.<id>.files.* subjects
// are HTTP routes under workerctl.Prefix, served on the worker's own server and
// reached through its tunnel, so no subject is minted for them.

// Cache Invalidation (Pub/Sub — broadcast to all instances)
const (
	SubjectCacheInvalidateSkills = "cache.invalidate.skills"
	// SubjectCacheInvalidateModels is broadcast by the replica that completed
	// a model install/delete. Peers subscribe and re-run
	// ModelConfigLoader.LoadModelConfigsFromPath so a chat completion routed
	// to a different replica can find the newly installed model.
	SubjectCacheInvalidateModels = "cache.invalidate.models"
	// SubjectCacheInvalidateBackends is broadcast after a backend
	// install/upgrade/delete. Peers retrigger their UpgradeChecker so the
	// 6-hour upgrade-available cache flips to fresh on every replica, not
	// just the one that handled the request.
	SubjectCacheInvalidateBackends = "cache.invalidate.backends"
)

// CacheInvalidateEvent is the payload for cache invalidation broadcasts.
// Element names a specific model/backend when known; empty means "the whole
// set was touched, do a full reload."
type CacheInvalidateEvent struct {
	Element        string `json:"element,omitempty"`
	Op             string `json:"op,omitempty"` // "install" | "delete" | "upgrade"
	ConfigRevision string `json:"config_revision,omitempty"`
}

// SubjectCacheInvalidateCollection returns the NATS subject for collection cache invalidation.
func SubjectCacheInvalidateCollection(name string) string {
	return "cache.invalidate.collections." + sanitizeSubjectToken(name)
}

// SyncedMap State Sync (Pub/Sub — broadcast to all frontends)
//
// The reusable syncstate.SyncedMap component publishes a {op,key,value} delta on
// this subject whenever a replica mutates a piece of cross-replica in-memory
// state. Peers subscribe and apply the delta to their own map, so a round-robin
// API request that lands on a replica which did not originate the change still
// sees it. Convergence on (re)connect is done by re-hydrating from the durable
// source, so no request/reply snapshot subject is needed here.
func SubjectSyncStateDelta(name string) string {
	return subjectSyncStatePrefix + sanitizeSubjectToken(name) + ".delta"
}

const subjectSyncStatePrefix = "state."

// Prefix-Cache Routing Sync (Pub/Sub - broadcast to all frontends)
//
// Frontends share prefix-cache observations so a request routed to any replica
// benefits from the prefix-affinity another replica already learned. This
// mirrors the OpCache live-sync pattern: plain NATS Core pub/sub, no JetStream.
const (
	SubjectPrefixCacheObserve    = "prefixcache.observe"
	SubjectPrefixCacheInvalidate = "prefixcache.invalidate"
)

// PrefixCacheObserveEvent announces that the replica (NodeID, Replica) served a
// request whose prefix chain ends at the given hashes for model. Chain is the
// full shallow-to-deep hash chain so peers can insert the same path. Affinity is
// per replica (a backend process with its own KV cache), not per node, so the
// replica index is carried so peers attribute the observation to the same one.
type PrefixCacheObserveEvent struct {
	Model   string   `json:"model"`
	Chain   []uint64 `json:"chain"`
	NodeID  string   `json:"node_id"`
	Replica int      `json:"replica"`
}

// PrefixCacheInvalidateEvent tells peers to drop entries for a replica. When
// Replica >= 0 it targets the single replica (Model, NodeID, Replica). When
// Replica < 0 it targets ALL replicas of (Model, NodeID), for example when a
// whole node goes offline.
type PrefixCacheInvalidateEvent struct {
	Model   string `json:"model"`
	NodeID  string `json:"node_id"`
	Replica int    `json:"replica"`
}
