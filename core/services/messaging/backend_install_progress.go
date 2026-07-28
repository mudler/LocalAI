package messaging

// Phase values published on the BackendInstallProgressEvent.Phase field.
// Defined as exported constants so producer (worker install handler) and
// consumer (master bridge into OpStatus) share a single source of truth
// instead of two copies of the literal string.
const (
	PhaseResolving   = "resolving"   // worker is locating the gallery / image manifest
	PhaseDownloading = "downloading" // worker is actively pulling layers
	PhaseExtracting  = "extracting"  // worker is unpacking the downloaded archive
	PhaseStarting    = "starting"    // worker is spawning the gRPC backend process
)

// BackendInstallProgressEvent is the wire payload published by a worker to
// nodes.<nodeID>.backend.install.<opID>.progress while a long-running install
// is in flight. Transient: dropped events are acceptable, the master relies
// on BackendInstallReply for ground truth on success/failure.
//
// Phase holds one of the Phase* constants above.
type BackendInstallProgressEvent struct {
	OpID       string  `json:"op_id"`
	NodeID     string  `json:"node_id"`
	Backend    string  `json:"backend"`
	FileName   string  `json:"file_name,omitempty"`
	Current    string  `json:"current,omitempty"` // human-readable size, e.g. "412 MB"
	Total      string  `json:"total,omitempty"`   // human-readable size, e.g. "2.1 GB"
	Percentage float64 `json:"percentage"`
	Phase      string  `json:"phase,omitempty"`
}

// SubjectNodeBackendInstallProgress returns the NATS subject for transient
// progress events emitted by a worker during a single backend.install run.
// Per-op so multiple concurrent installs on the same node never alias.
func SubjectNodeBackendInstallProgress(nodeID, opID string) string {
	return subjectNodePrefix + sanitizeSubjectToken(nodeID) + ".backend.install." + sanitizeSubjectToken(opID) + ".progress"
}
