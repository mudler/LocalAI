package workerctl

type ModelStopRequest struct {
	ModelName       string `json:"model_name"`
	ProcessKey      string `json:"process_key"`
	ExpectedAddress string `json:"expected_address"`
	Force           bool   `json:"force,omitempty"`
	ConfigRevision  string `json:"config_revision,omitempty"`
	// OperationID and ProcessInstance address one load operation. When set, the
	// worker checks them, together with ProcessKey and ExpectedAddress, against
	// its own records and refuses on any mismatch. The worker never falls back
	// to a name-only match.
	OperationID     string `json:"operation_id,omitempty"`
	ProcessInstance string `json:"process_instance,omitempty"`
}

type ModelStopReply struct {
	Matched    bool   `json:"matched"`
	Freed      bool   `json:"freed"`
	Terminated bool   `json:"terminated"`
	ProcessKey string `json:"process_key"`
	Address    string `json:"address,omitempty"`
	Error      string `json:"error,omitempty"`
}

// ModelUnloadRequest is the payload for a model.unload control request.
type ModelUnloadRequest struct {
	ModelName string `json:"model_name"`
	Address   string `json:"address,omitempty"` // gRPC address of the backend process to unload from
	// ProcessInstance, when set, must match the process at Address. The worker
	// never picks a process on its own: a request with no Address unloads nothing.
	ProcessInstance string `json:"process_instance,omitempty"`
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
	// ReportsOperations is true on workers that track load operations. Without
	// it a controller must treat the node as legacy: it cannot confirm a stop.
	ReportsOperations bool `json:"reports_operations,omitempty"`
}

// RunningModelInfo identifies one live backend process on a worker. The triple
// is isomorphic to a controller NodeModel row's (model_name, replica_index,
// address), which is what lets the reconciler diff the two directly.
type RunningModelInfo struct {
	ModelID      string `json:"model_id"`
	ReplicaIndex int    `json:"replica_index"`
	Address      string `json:"address,omitempty"`
	// OperationID is the load operation that started this process, empty for a
	// process with none (a legacy start). ProcessInstance identifies this
	// incarnation of the process.
	OperationID     string `json:"operation_id,omitempty"`
	ProcessInstance string `json:"process_instance,omitempty"`
}

// OperationRequest renews and completes load operations on a worker. The
// controller sends it on the same tick as its own lease heartbeat, batched per
// node. A missed renewal costs nothing until the worker's kill TTL.
type OperationRequest struct {
	// Renew extends each operation's lease on the worker.
	Renew []string `json:"renew,omitempty"`
	// Complete ends each operation: the load finished and the backend now
	// serves, so the watchdog must stop watching it.
	Complete []string `json:"complete,omitempty"`
}

// OperationReply names the operations the worker does not know, so the
// controller can tell a lost operation from a renewed one.
type OperationReply struct {
	Renewed   []string `json:"renewed,omitempty"`
	Completed []string `json:"completed,omitempty"`
	Unknown   []string `json:"unknown,omitempty"`
}
