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

// BackendInstallProgressEvent is the wire payload a worker writes as a progress
// line of its backend.install and backend.upgrade responses while a
// long-running install is in flight. Transient: a line the frontend cannot read
// is acceptable, and BackendInstallReply is the ground truth on
// success/failure.
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
