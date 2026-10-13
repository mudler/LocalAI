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

// SubjectCarrierChanged is the hint that the cluster carrier row moved. The
// leader and the admin publish it after each move, on the carrier in use, and
// every replica listens on it. It carries the new row and is only a courtesy: a
// replica that never hears it reads the row at its next poll, and what the row
// says is what counts.
const SubjectCarrierChanged = "state.carrier"

// SubjectCarrierProbe asks every replica to look again at which carriers it could
// build, and to write what it finds to the instances table. A dry run of a change
// of carrier sends it before it reads the answers. Like the other hint it is at
// most once, and a replica that does not hear it is reported as stale.
const SubjectCarrierProbe = "state.carrier.probe"

// SubjectClaimWake is the hint that a unit of work was put in the claim queue.
// A consumer that hears it looks for work at once and does not wait for its next
// poll. The hint is a broadcast and broadcasts are at-most-once, so it can be
// lost, and the poll finds the work then.
const SubjectClaimWake = "jobs.claim.wake"

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
	SubjectJobResultWildcard       = "jobs.*.result"
	SubjectJobProgressWildcard     = "jobs.*.progress"
	SubjectJobCancelWildcard       = "jobs.*.cancel"
	SubjectAgentEventsWildcard     = "agent.*.events.*"
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

// Node Backend Lifecycle (Pub/Sub — targeted to specific nodes)
//
// These subjects control the backend *process* lifecycle on a serve-backend node,
// mirroring how the local ModelLoader uses startProcess() / deleteProcess().
//
// Model loading (LoadModel gRPC) is done via direct gRPC calls to the node's
// address — no NATS needed for that, same as local mode.
const (
	subjectNodePrefix = "nodes."
)

// SubjectNodeBackendInstall tells a worker node to install a backend and start its gRPC process.
// Uses NATS request-reply: the SmartRouter sends the request, the worker installs
// the backend from gallery (if not already installed), starts the gRPC process,
// and replies when ready.
func SubjectNodeBackendInstall(nodeID string) string {
	return subjectNodePrefix + sanitizeSubjectToken(nodeID) + ".backend.install"
}

// SubjectNodeBackendUpgrade tells a worker node to force-reinstall a backend
// from the gallery, stop every running process for that backend, and restart.
// Uses NATS request-reply with a long deadline (gallery image pulls can take
// many minutes on slow links). Routine model loads use SubjectNodeBackendInstall
// instead — this subject exists so the slow path doesn't head-of-line-block
// the fast one through a shared subscription goroutine.
func SubjectNodeBackendUpgrade(nodeID string) string {
	return subjectNodePrefix + sanitizeSubjectToken(nodeID) + ".backend.upgrade"
}

// SubjectNodeBackendList queries a worker node for its installed backends.
// Uses NATS request-reply.
func SubjectNodeBackendList(nodeID string) string {
	return subjectNodePrefix + sanitizeSubjectToken(nodeID) + ".backend.list"
}

// SubjectNodeBackendStop tells a worker node to stop its gRPC backend process.
// Equivalent to the local deleteProcess(). The node will:
// 1. Best-effort bounded Free() via gRPC (unless Force is true)
// 2. Kill the backend process
// 3. Can be restarted via another backend.start event.
//
// Request-reply, answered with a workerctl.BackendStopReply. A worker that predates that
// reply never answers, so the controller must treat a timeout as "unconfirmed"
// rather than "failed" — see RemoteUnloaderAdapter.stopBackend.
func SubjectNodeBackendStop(nodeID string) string {
	return subjectNodePrefix + sanitizeSubjectToken(nodeID) + ".backend.stop"
}

// SubjectNodeModelStop targets one supervisor process and acknowledges only
// after that process has exited and its worker-side resources are released.
func SubjectNodeModelStop(nodeID string) string {
	return subjectNodePrefix + sanitizeSubjectToken(nodeID) + ".model.stop"
}

// SubjectNodeModelOp renews and completes the load operations a worker is
// watching. Request-reply, answered with a workerctl.OperationReply. A worker
// that predates it never answers, and the controller then treats the node as
// legacy.
func SubjectNodeModelOp(nodeID string) string {
	return subjectNodePrefix + sanitizeSubjectToken(nodeID) + ".model.op"
}

// SubjectNodeBackendDelete tells a worker node to delete a backend (stop + remove files).
// Uses NATS request-reply.
func SubjectNodeBackendDelete(nodeID string) string {
	return subjectNodePrefix + sanitizeSubjectToken(nodeID) + ".backend.delete"
}

// SubjectNodeModelUnload tells a worker node to unload a model (gRPC Free) without killing the backend.
// Uses NATS request-reply.
func SubjectNodeModelUnload(nodeID string) string {
	return subjectNodePrefix + sanitizeSubjectToken(nodeID) + ".model.unload"
}

// SubjectNodeModelDelete tells a worker node to delete model files from disk.
// Uses NATS request-reply.
func SubjectNodeModelDelete(nodeID string) string {
	return subjectNodePrefix + sanitizeSubjectToken(nodeID) + ".model.delete"
}

// SubjectNodeModelsRunning asks a worker node which model backend processes it
// currently has running. Uses NATS request-reply.
//
// This is the authoritative answer to "is this replica still alive". The worker
// owns the process table, so unlike a health probe against the backend's own
// serving port, its reply does not depend on whether that backend happens to be
// busy: a model mid-generation cannot answer a gRPC health check for minutes at
// a time, but the worker answers immediately either way.
func SubjectNodeModelsRunning(nodeID string) string {
	return subjectNodePrefix + sanitizeSubjectToken(nodeID) + ".models.running"
}

// SubjectNodeStop tells a serve-backend node to shut down entirely
// (deregister + exit). The node will not restart the backend process.
func SubjectNodeStop(nodeID string) string {
	return subjectNodePrefix + sanitizeSubjectToken(nodeID) + ".stop"
}

// File Staging (Request-Reply — targeted to specific nodes)
// These subjects use request-reply for synchronous file operations.

// SubjectNodeFilesEnsure tells a serve-backend node to download an S3 key to its local cache.
// Reply: workerctl.FileEnsureReply
func SubjectNodeFilesEnsure(nodeID string) string {
	return subjectNodePrefix + sanitizeSubjectToken(nodeID) + ".files.ensure"
}

// SubjectNodeFilesStage tells a serve-backend node to upload a local file to S3.
// Reply: workerctl.FileStageReply
func SubjectNodeFilesStage(nodeID string) string {
	return subjectNodePrefix + sanitizeSubjectToken(nodeID) + ".files.stage"
}

// SubjectNodeFilesRelease tells a serve-backend node to evict one request's ephemeral cache keys.
// Reply: workerctl.FileReleaseReply
func SubjectNodeFilesRelease(nodeID string) string {
	return subjectNodePrefix + sanitizeSubjectToken(nodeID) + ".files.release"
}

// SubjectNodeFilesTemp tells a serve-backend node to allocate a temp file.
// Reply: workerctl.FileTempReply
func SubjectNodeFilesTemp(nodeID string) string {
	return subjectNodePrefix + sanitizeSubjectToken(nodeID) + ".files.temp"
}

// SubjectNodeFilesListDir tells a serve-backend node to list files in a directory.
// Reply: workerctl.FileListDirReply
func SubjectNodeFilesListDir(nodeID string) string {
	return subjectNodePrefix + sanitizeSubjectToken(nodeID) + ".files.listdir"
}

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

// SubjectCacheInvalidateCollectionAll matches the collection cache
// invalidation subject of every collection.
const SubjectCacheInvalidateCollectionAll = "cache.invalidate.collections.*"

// SubjectCacheInvalidateCollection returns the NATS subject for collection cache invalidation.
func SubjectCacheInvalidateCollection(name string) string {
	return "cache.invalidate.collections." + sanitizeSubjectToken(name)
}

// SubjectSyncStateDelta returns the SyncedMap state sync subject (Pub/Sub — broadcast to all frontends).
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
	SubjectPrefixCachePressure   = "prefixcache.pressure"
	SubjectPrefixCacheResidency  = "prefixcache.residency"
)

// PrefixCacheOperation describes a backend-reported KV-cache residency change.
type PrefixCacheOperation string

const (
	PrefixCacheStore  PrefixCacheOperation = "store"
	PrefixCacheRemove PrefixCacheOperation = "remove"
	PrefixCacheClear  PrefixCacheOperation = "clear"
)

// PrefixCacheResidencyEvent reports exact backend KV-cache residency. Chain is
// the compatible shallow-to-deep prefix hash chain used by the router.
type PrefixCacheResidencyEvent struct {
	Operation PrefixCacheOperation `json:"operation"`
	Model     string               `json:"model"`
	NodeID    string               `json:"node_id"`
	Replica   int                  `json:"replica"`
	Chain     []uint64             `json:"chain,omitempty"`
}

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

// PrefixCachePressureEvent announces one forced-disturb observed by a frontend.
// ID lets the publisher ignore its own NATS echo and all frontends ignore
// redelivery without inflating the autoscale signal.
type PrefixCachePressureEvent struct {
	ID    string `json:"id"`
	Model string `json:"model"`
	Reset bool   `json:"reset,omitempty"`
}
