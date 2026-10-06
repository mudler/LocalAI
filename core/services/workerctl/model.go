package workerctl

// OperationIdentity fences one durable load attempt on one worker boot.
// Empty identities are legacy and never constitute recovery evidence.
type OperationIdentity struct {
	TrackingKey string `json:"tracking_key"`
	Generation  string `json:"generation"`
	Incarnation string `json:"incarnation"`
}

type LoadOperation struct {
	Identity   OperationIdentity `json:"identity"`
	ProcessKey string            `json:"process_key"`
	Phase      string            `json:"phase"`
	Active     bool              `json:"active"`
}

type ModelStopRequest struct {
	Operation       *OperationIdentity `json:"operation,omitempty"`
	ProcessInstance string             `json:"process_instance,omitempty"`
	ModelName       string             `json:"model_name"`
	ProcessKey      string             `json:"process_key"`
	ExpectedAddress string             `json:"expected_address"`
	Force           bool               `json:"force,omitempty"`
	ConfigRevision  string             `json:"config_revision,omitempty"`
}

type ModelStopReply struct {
	OperationAcknowledged bool   `json:"operation_acknowledged,omitempty"`
	Matched               bool   `json:"matched"`
	Freed                 bool   `json:"freed"`
	Terminated            bool   `json:"terminated"`
	ProcessKey            string `json:"process_key"`
	Address               string `json:"address,omitempty"`
	Error                 string `json:"error,omitempty"`
}

// ModelUnloadRequest is the payload for a model.unload control request.
type ModelUnloadRequest struct {
	Operation *OperationIdentity `json:"operation,omitempty"`
	ModelName string             `json:"model_name"`
	Address   string             `json:"address,omitempty"` // gRPC address of the backend process to unload from
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
	Incarnation       string             `json:"incarnation,omitempty"`
	ReportsOperations bool               `json:"reports_operations,omitempty"`
	Operations        []LoadOperation    `json:"operations,omitempty"`
	Models            []RunningModelInfo `json:"models"`
	Error             string             `json:"error,omitempty"`
}

// RunningModelInfo identifies one live backend process on a worker. The triple
// is isomorphic to a controller NodeModel row's (model_name, replica_index,
// address), which is what lets the reconciler diff the two directly.
type RunningModelInfo struct {
	ProcessInstance string             `json:"process_instance,omitempty"`
	ConfigRevision  string             `json:"config_revision,omitempty"`
	Operation       *OperationIdentity `json:"operation,omitempty"`
	ModelID         string             `json:"model_id"`
	ReplicaIndex    int                `json:"replica_index"`
	Address         string             `json:"address,omitempty"`
}
