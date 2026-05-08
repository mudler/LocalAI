package worker

// Config is the configuration for the distributed agent worker.
//
// Field tags are kong/kong-env metadata read by core/cli/worker.go's WorkerCMD,
// which embeds Config; this package does NOT import kong and the tags are inert
// here.
//
// Workers are backend-agnostic — they wait for backend.install NATS events
// from the SmartRouter to install and start the required backend.
//
// NATS is required. The worker acts as a process supervisor:
// - Receives backend.install → installs backend from gallery, starts gRPC process, replies success
// - Receives backend.stop → stops the gRPC process
// - Receives stop → full shutdown (deregister + exit)
//
// Model loading (LoadModel) is always via direct gRPC — no NATS needed for that.
type Config struct {
	// Primary address — the reachable address of this worker.
	// Host is used for advertise, port is the base for gRPC backends.
	// HTTP file transfer runs on port-1.
	Addr      string `env:"LOCALAI_ADDR" help:"Address where this worker is reachable (host:port). Port is base for gRPC backends, port-1 for HTTP." group:"server"`
	ServeAddr string `env:"LOCALAI_SERVE_ADDR" default:"0.0.0.0:50051" help:"(Advanced) gRPC base port bind address" group:"server" hidden:""`

	BackendsPath       string `env:"LOCALAI_BACKENDS_PATH,BACKENDS_PATH" type:"path" default:"${basepath}/backends" help:"Path containing backends" group:"server"`
	BackendsSystemPath string `env:"LOCALAI_BACKENDS_SYSTEM_PATH" type:"path" default:"/var/lib/local-ai/backends" help:"Path containing system backends" group:"server"`
	BackendGalleries   string `env:"LOCALAI_BACKEND_GALLERIES,BACKEND_GALLERIES" help:"JSON list of backend galleries" group:"server" default:"${backends}"`
	ModelsPath         string `env:"LOCALAI_MODELS_PATH,MODELS_PATH" type:"path" default:"${basepath}/models" help:"Path containing models" group:"server"`

	// HTTP file transfer
	HTTPAddr          string `env:"LOCALAI_HTTP_ADDR" default:"" help:"HTTP file transfer server address (default: gRPC port + 1)" group:"server" hidden:""`
	AdvertiseHTTPAddr string `env:"LOCALAI_ADVERTISE_HTTP_ADDR" help:"HTTP address the frontend uses to reach this node for file transfer" group:"server" hidden:""`

	// Registration (required)
	AdvertiseAddr     string `env:"LOCALAI_ADVERTISE_ADDR" help:"Address the frontend uses to reach this node (defaults to hostname:port from Addr)" group:"registration" hidden:""`
	RegisterTo        string `env:"LOCALAI_REGISTER_TO" required:"" help:"Frontend URL for registration" group:"registration"`
	NodeName          string `env:"LOCALAI_NODE_NAME" help:"Node name for registration (defaults to hostname)" group:"registration"`
	RegistrationToken string `env:"LOCALAI_REGISTRATION_TOKEN" help:"Token for authenticating with the frontend" group:"registration"`
	HeartbeatInterval string `env:"LOCALAI_HEARTBEAT_INTERVAL" default:"10s" help:"Interval between heartbeats" group:"registration"`
	NodeLabels        string `env:"LOCALAI_NODE_LABELS" help:"Comma-separated key=value labels for this node (e.g. tier=fast,gpu=a100)" group:"registration"`
	// MaxReplicasPerModel caps how many replicas of any one model can run on
	// this worker concurrently. Default 1 = historical single-replica
	// behavior. Set higher when a node has enough VRAM to host multiple
	// copies of the same model (e.g. a fat 128 GiB box running 4× of a
	// 24 GiB model for throughput). The auto-label `node.replica-slots=N`
	// is published so model schedulers can target high-capacity nodes via
	// the existing label selector.
	MaxReplicasPerModel int `env:"LOCALAI_MAX_REPLICAS_PER_MODEL" default:"1" help:"Max replicas of any single model on this worker. Default 1 preserves single-replica behavior; set higher to allow stacking replicas on a fat node." group:"registration"`

	// NATS (required)
	NatsURL string `env:"LOCALAI_NATS_URL" required:"" help:"NATS server URL" group:"distributed"`

	// S3 storage for distributed file transfer
	StorageURL       string `env:"LOCALAI_STORAGE_URL" help:"S3 endpoint URL" group:"distributed"`
	StorageBucket    string `env:"LOCALAI_STORAGE_BUCKET" help:"S3 bucket name" group:"distributed"`
	StorageRegion    string `env:"LOCALAI_STORAGE_REGION" help:"S3 region" group:"distributed"`
	StorageAccessKey string `env:"LOCALAI_STORAGE_ACCESS_KEY" help:"S3 access key" group:"distributed"`
	StorageSecretKey string `env:"LOCALAI_STORAGE_SECRET_KEY" help:"S3 secret key" group:"distributed"`
}
