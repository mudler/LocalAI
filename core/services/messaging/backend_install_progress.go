package messaging

// BackendInstallProgressEvent is the wire payload published by a worker to
// nodes.<nodeID>.backend.install.<opID>.progress while a long-running install
// is in flight. Transient: dropped events are acceptable, the master relies
// on BackendInstallReply for ground truth on success/failure.
type BackendInstallProgressEvent struct {
	OpID       string  `json:"op_id"`
	NodeID     string  `json:"node_id"`
	Backend    string  `json:"backend"`
	FileName   string  `json:"file_name,omitempty"`
	Current    string  `json:"current,omitempty"` // human-readable size, e.g. "412 MB"
	Total      string  `json:"total,omitempty"`   // human-readable size, e.g. "2.1 GB"
	Percentage float64 `json:"percentage"`
	Phase      string  `json:"phase,omitempty"` // "resolving" | "downloading" | "extracting" | "starting"
}

// SubjectNodeBackendInstallProgress returns the NATS subject for transient
// progress events emitted by a worker during a single backend.install run.
// Per-op so multiple concurrent installs on the same node never alias.
func SubjectNodeBackendInstallProgress(nodeID, opID string) string {
	return subjectNodePrefix + sanitizeSubjectToken(nodeID) + ".backend.install." + sanitizeSubjectToken(opID) + ".progress"
}
