package messaging

// NATS subject constants for the distributed architecture.
// Following the notetaker pattern: <entity>.<action>

// Job Distribution (Queue Groups — load-balanced, one consumer gets each message)
const (
	SubjectJobsNew             = "jobs.new"
	SubjectAgentExecute        = "agent.execute"
	QueueWorkers               = "workers"
)

// Status Updates (Pub/Sub — all subscribers get every message, for SSE bridging)
// These use parameterized subjects: e.g. SubjectAgentEvents("myagent", "user1")
const (
	subjectAgentEventsPrefix    = "agent."
	subjectJobProgressPrefix    = "jobs."
	subjectFineTunePrefix       = "finetune."
	subjectGalleryPrefix        = "gallery."
)

// SubjectAgentEvents returns the NATS subject for agent SSE events.
func SubjectAgentEvents(agentName, userID string) string {
	if userID == "" {
		userID = "anonymous"
	}
	return subjectAgentEventsPrefix + agentName + ".events." + userID
}

// SubjectJobProgress returns the NATS subject for job progress updates.
func SubjectJobProgress(jobID string) string {
	return subjectJobProgressPrefix + jobID + ".progress"
}

// SubjectJobResult returns the NATS subject for the final job result (terminal state).
func SubjectJobResult(jobID string) string {
	return subjectJobProgressPrefix + jobID + ".result"
}

// MCP Tool Execution (Request-Reply via NATS — load-balanced across agent workers)
const (
	SubjectMCPToolExecute = "mcp.tools.execute"
	SubjectMCPDiscovery   = "mcp.discovery"
	QueueAgentWorkers     = "agent-workers"
)

// SubjectFineTuneProgress returns the NATS subject for fine-tune progress.
func SubjectFineTuneProgress(jobID string) string {
	return subjectFineTunePrefix + jobID + ".progress"
}

// SubjectGalleryProgress returns the NATS subject for gallery download progress.
func SubjectGalleryProgress(opID string) string {
	return subjectGalleryPrefix + opID + ".progress"
}

// Control Signals (Pub/Sub — targeted cancellation)
const (
	subjectJobCancelPrefix     = "jobs."
	subjectAgentCancelPrefix   = "agent."
	subjectFineTuneCancelPrefix = "finetune."
	subjectGalleryCancelPrefix  = "gallery."
)

// Wildcard subjects for NATS subscriptions that match all IDs.
const (
	SubjectJobCancelWildcard   = "jobs.*.cancel"
	SubjectJobResultWildcard   = "jobs.*.result"
	SubjectJobProgressWildcard = "jobs.*.progress"
	SubjectAgentCancelWildcard = "agent.*.cancel"
	SubjectGalleryCancelWildcard  = "gallery.*.cancel"
	SubjectGalleryProgressWildcard = "gallery.*.progress"
)

// SubjectJobCancel returns the NATS subject to cancel a running job.
func SubjectJobCancel(jobID string) string {
	return subjectJobCancelPrefix + jobID + ".cancel"
}

// SubjectAgentCancel returns the NATS subject to cancel agent execution.
func SubjectAgentCancel(agentID string) string {
	return subjectAgentCancelPrefix + agentID + ".cancel"
}

// SubjectFineTuneCancel returns the NATS subject to stop fine-tuning.
func SubjectFineTuneCancel(jobID string) string {
	return subjectFineTuneCancelPrefix + jobID + ".cancel"
}

// SubjectGalleryCancel returns the NATS subject to cancel a gallery download.
func SubjectGalleryCancel(opID string) string {
	return subjectGalleryCancelPrefix + opID + ".cancel"
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
	return subjectNodePrefix + nodeID + ".backend.install"
}

// BackendInstallRequest is the payload for a backend.install NATS request.
type BackendInstallRequest struct {
	Backend          string `json:"backend"`
	ModelID          string `json:"model_id,omitempty"`          // unique model identifier — each model gets its own gRPC process
	BackendGalleries string `json:"backend_galleries,omitempty"`
}

// BackendInstallReply is the response from a backend.install NATS request.
type BackendInstallReply struct {
	Success bool   `json:"success"`
	Address string `json:"address,omitempty"` // gRPC address of the backend process (host:port)
	Error   string `json:"error,omitempty"`
}

// SubjectNodeBackendList queries a worker node for its installed backends.
// Uses NATS request-reply.
func SubjectNodeBackendList(nodeID string) string {
	return subjectNodePrefix + nodeID + ".backend.list"
}

// BackendListRequest is the payload for a backend.list NATS request.
type BackendListRequest struct{}

// BackendListReply is the response from a backend.list NATS request.
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
}

// SubjectNodeBackendStop tells a worker node to stop its gRPC backend process.
// Equivalent to the local deleteProcess(). The node will:
// 1. Best-effort Free() via gRPC
// 2. Kill the backend process
// 3. Can be restarted via another backend.start event.
func SubjectNodeBackendStop(nodeID string) string {
	return subjectNodePrefix + nodeID + ".backend.stop"
}

// SubjectNodeBackendDelete tells a worker node to delete a backend (stop + remove files).
// Uses NATS request-reply.
func SubjectNodeBackendDelete(nodeID string) string {
	return subjectNodePrefix + nodeID + ".backend.delete"
}

// BackendDeleteRequest is the payload for a backend.delete NATS request.
type BackendDeleteRequest struct {
	Backend string `json:"backend"`
}

// BackendDeleteReply is the response from a backend.delete NATS request.
type BackendDeleteReply struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// SubjectNodeModelUnload tells a worker node to unload a model (gRPC Free) without killing the backend.
// Uses NATS request-reply.
func SubjectNodeModelUnload(nodeID string) string {
	return subjectNodePrefix + nodeID + ".model.unload"
}

// ModelUnloadRequest is the payload for a model.unload NATS request.
type ModelUnloadRequest struct {
	ModelName string `json:"model_name"`
	Address   string `json:"address,omitempty"` // gRPC address of the backend process to unload from
}

// ModelUnloadReply is the response from a model.unload NATS request.
type ModelUnloadReply struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// SubjectNodeModelDelete tells a worker node to delete model files from disk.
// Uses NATS request-reply.
func SubjectNodeModelDelete(nodeID string) string {
	return subjectNodePrefix + nodeID + ".model.delete"
}

// ModelDeleteRequest is the payload for a model.delete NATS request.
type ModelDeleteRequest struct {
	ModelName string `json:"model_name"`
}

// ModelDeleteReply is the response from a model.delete NATS request.
type ModelDeleteReply struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// SubjectNodeStop tells a serve-backend node to shut down entirely
// (deregister + exit). The node will not restart the backend process.
func SubjectNodeStop(nodeID string) string {
	return subjectNodePrefix + nodeID + ".stop"
}

// File Staging (Request-Reply — targeted to specific nodes)
// These subjects use request-reply for synchronous file operations.

// SubjectNodeFilesEnsure tells a serve-backend node to download an S3 key to its local cache.
// Reply: {local_path, error}
func SubjectNodeFilesEnsure(nodeID string) string {
	return subjectNodePrefix + nodeID + ".files.ensure"
}

// SubjectNodeFilesStage tells a serve-backend node to upload a local file to S3.
// Reply: {key, error}
func SubjectNodeFilesStage(nodeID string) string {
	return subjectNodePrefix + nodeID + ".files.stage"
}

// SubjectNodeFilesTemp tells a serve-backend node to allocate a temp file.
// Reply: {local_path, error}
func SubjectNodeFilesTemp(nodeID string) string {
	return subjectNodePrefix + nodeID + ".files.temp"
}

// SubjectNodeFilesListDir tells a serve-backend node to list files in a directory.
// Reply: {files: [...], error}
func SubjectNodeFilesListDir(nodeID string) string {
	return subjectNodePrefix + nodeID + ".files.listdir"
}

// Cache Invalidation (Pub/Sub — broadcast to all instances)
const (
	SubjectCacheInvalidateSkills = "cache.invalidate.skills"
)

// SubjectCacheInvalidateCollection returns the NATS subject for collection cache invalidation.
func SubjectCacheInvalidateCollection(name string) string {
	return "cache.invalidate.collections." + name
}

// PostgreSQL Advisory Lock Keys (used with advisorylock package, NOT NATS)
const (
	AdvisoryLockCronScheduler    int64 = 100
	AdvisoryLockStaleNodeCleanup int64 = 101
	AdvisoryLockGalleryDedup     int64 = 102
	AdvisoryLockAgentScheduler   int64 = 103
	AdvisoryLockHealthCheck      int64 = 104
	AdvisoryLockSchemaMigrate    int64 = 105
)
