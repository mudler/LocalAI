package worker

import "fmt"

// Config is the configuration for the distributed agent worker.
//
// Field tags are kong/kong-env metadata read by core/cli/worker.go's WorkerCMD,
// which embeds Config; this package does NOT import kong and the tags are inert
// here.
//
// Workers are backend-agnostic: they install and start whichever backend the
// frontend asks for. The worker acts as a process supervisor, and every verb
// the frontend gives it is an HTTP route on its own loopback server, served to
// the frontend through this worker's outbound tunnel (see control_routes.go and
// core/services/workerctl). A backend worker connects to no message bus.
type Config struct {
	// Addr and ServeAddr are read for their PORT only. A worker binds nothing
	// on a routable interface: backend processes and the file-transfer server
	// both listen on loopback and are reached through this worker's outbound
	// tunnel. The port still matters because it is the base of the backend
	// port range (and port-1 is the HTTP server), so an operator who needs a
	// different range sets it here. The host half is ignored, and is kept
	// accepted rather than rejected so an upgraded worker starts on the
	// environment it already had.
	Addr      string `env:"LOCALAI_ADDR" help:"Base port for this worker, as host:port; only the port is used. Backends take ports upward from it, the HTTP file-transfer server takes port-1. Nothing binds a routable interface." group:"server"`
	ServeAddr string `env:"LOCALAI_SERVE_ADDR" default:"0.0.0.0:50051" help:"(Advanced) gRPC base port; only the port is used" group:"server" hidden:""`

	// GRPCMaxPort bounds the dynamic gRPC port allocator at [basePort, this].
	// The width of that range is how many backend processes this worker can run
	// concurrently; released ports also sit in a short quarantine before reuse,
	// so a worker with heavy start/stop churn needs headroom above its true
	// concurrency. 0 = up to 65535.
	GRPCMaxPort int `env:"LOCALAI_GRPC_MAX_PORT" default:"0" help:"Highest port the worker may assign to a backend gRPC process. The range is [base port, this]; its width caps concurrent backends on this worker. 0 uses up to 65535." group:"server"`

	BackendsPath            string `env:"LOCALAI_BACKENDS_PATH,BACKENDS_PATH" type:"path" default:"${basepath}/backends" help:"Path containing backends" group:"server"`
	BackendsSystemPath      string `env:"LOCALAI_BACKENDS_SYSTEM_PATH" type:"path" default:"/var/lib/local-ai/backends" help:"Path containing system backends" group:"server"`
	BackendGalleries        string `env:"LOCALAI_BACKEND_GALLERIES,BACKEND_GALLERIES" help:"JSON list of backend galleries" group:"server" default:"${backends}"`
	Galleries               string `env:"LOCALAI_GALLERIES,GALLERIES" help:"JSON list of model galleries (used to resolve --prefetch-models on boot)" group:"server" default:"${galleries}"`
	ModelsPath              string `env:"LOCALAI_MODELS_PATH,MODELS_PATH" type:"path" default:"${basepath}/models" help:"Path containing models" group:"server"`
	RequireBackendIntegrity bool   `env:"LOCALAI_REQUIRE_BACKEND_INTEGRITY,REQUIRE_BACKEND_INTEGRITY" help:"If true, reject backend installs without a configured signature verification policy (OCI URIs) or SHA256 (tarball/HTTP URIs)." group:"hardening" default:"false"`

	// PrefetchModels lets a worker download gallery model artifacts (GGUFs, etc.)
	// from its own outbound internet at boot, instead of waiting for the master to
	// stream them over the cluster network at first-inference time. Useful when the
	// cluster-internal path is slow (slirp/circuit-relay, CGNAT) but outbound NAT
	// works fine. Resolution reuses the same gallery installer the master uses, so
	// the on-disk /models layout is identical. Errors are non-fatal — if the gallery
	// is unreachable on boot, the worker logs a warning and starts anyway; the master can still push the file on demand (existing behaviour).
	PrefetchModels []string `env:"LOCALAI_PREFETCH_MODELS,PREFETCH_MODELS" help:"Comma-separated gallery model IDs to download from LOCALAI_GALLERIES at worker boot (e.g. 'llama-3.2-1b-instruct,phi-3-mini-4k'). Skipped if already on disk and SHA matches." group:"server"`

	// HTTPAddr binds the HTTP file-transfer server. Default is loopback on
	// basePort-1; an explicit value is bound exactly as given.
	HTTPAddr string `env:"LOCALAI_HTTP_ADDR" default:"" help:"HTTP file transfer server bind address (default: loopback on the gRPC base port - 1)" group:"server" hidden:""`

	// Registration (required)
	RegisterTo              string `env:"LOCALAI_REGISTER_TO" required:"" help:"Frontend URL for registration" group:"registration"`
	NodeName                string `env:"LOCALAI_NODE_NAME" help:"Node name for registration (defaults to hostname)" group:"registration"`
	RegistrationToken       string `env:"LOCALAI_REGISTRATION_TOKEN" help:"Token for authenticating with the frontend" group:"registration"`
	RegistrationRequireAuth bool   `env:"LOCALAI_REGISTRATION_REQUIRE_AUTH" default:"false" help:"Refuse to start the HTTP file-transfer server when no registration token is set (otherwise it fails open and serves read/write to models/staging/data unauthenticated)" group:"registration"`
	DistributedRequireAuth  bool   `env:"LOCALAI_DISTRIBUTED_REQUIRE_AUTH" default:"false" help:"Umbrella switch implying --registration-require-auth" group:"distributed"`
	HeartbeatInterval       string `env:"LOCALAI_HEARTBEAT_INTERVAL" default:"10s" help:"Interval between heartbeats" group:"registration"`
	// WorkerTunnel holds one outbound multiplexed connection to the frontend
	// and serves the frontend's requests over it, so the worker needs no
	// inbound port.
	//
	// Turning it off is now a fatal misconfiguration and validateStartup
	// refuses to boot on it, which is a behaviour change from when this flag
	// had a working "off" position. It no longer has one: this worker
	// advertises no address and binds only loopback, and no frontend path
	// dials a worker's address, so a worker without its tunnel is reachable by
	// nothing. Left running it would be the worst available failure shape,
	// because it registers, heartbeats and reports healthy, so the scheduler
	// keeps placing models on it and every one of them fails.
	//
	// The flag is kept rather than deleted so that an operator who set it, on
	// the old promise that it fell back to the advertised address, is told
	// exactly that the promise is gone instead of having their setting quietly
	// ignored.
	WorkerTunnel bool   `env:"LOCALAI_WORKER_TUNNEL" default:"true" help:"Hold one outbound multiplexed tunnel to the frontend and serve its requests over it, so this worker needs no inbound port. Setting it false is refused: a worker has no other way to be reached." group:"distributed"`
	NodeLabels   string `env:"LOCALAI_NODE_LABELS" help:"Comma-separated key=value labels for this node (e.g. tier=fast,gpu=a100)" group:"registration"`
	// MaxReplicasPerModel caps how many replicas of any one model can run on
	// this worker concurrently. Default 1 = historical single-replica
	// behavior. Set higher when a node has enough VRAM to host multiple
	// copies of the same model (e.g. a fat 128 GiB box running 4× of a
	// 24 GiB model for throughput). The auto-label `node.replica-slots=N`
	// is published so model schedulers can target high-capacity nodes via
	// the existing label selector.
	MaxReplicasPerModel int `env:"LOCALAI_MAX_REPLICAS_PER_MODEL" default:"1" help:"Max replicas of any single model on this worker. Default 1 preserves single-replica behavior; set higher to allow stacking replicas on a fat node." group:"registration"`

	// VRAMBudget optionally caps this node's VRAM for model allocation ("80%" or
	// "12GB"). Reported to the server as a string; the server resolves and
	// enforces it against the raw VRAM this worker reports. Empty = no cap.
	VRAMBudget string `env:"LOCALAI_VRAM_BUDGET" help:"Cap VRAM used for model allocation on this worker node, as a percentage (e.g. 80%) or absolute amount (e.g. 12GB)." group:"registration"`

	// NatsURL is accepted and ignored. A backend worker no longer connects to
	// NATS at all, and the credential and TLS flags that went with it are gone
	// from this command. This one is kept so that an operator whose worker
	// command line or unit file still carries --nats-url gets a worker that
	// starts, rather than a kong parse error on an upgrade whose whole point is
	// that the bus is no longer needed here. The frontend and agent workers
	// still take it and still mean it.
	NatsURL string `env:"LOCALAI_NATS_URL" help:"Ignored. A backend worker connects to no message bus; the frontend reaches it over its outbound tunnel. Accepted so an existing worker command line still starts." group:"distributed" hidden:""`

	// S3 storage for distributed file transfer
	StorageURL       string `env:"LOCALAI_STORAGE_URL" help:"S3 endpoint URL" group:"distributed"`
	StorageBucket    string `env:"LOCALAI_STORAGE_BUCKET" help:"S3 bucket name" group:"distributed"`
	StorageRegion    string `env:"LOCALAI_STORAGE_REGION" help:"S3 region" group:"distributed"`
	StorageAccessKey string `env:"LOCALAI_STORAGE_ACCESS_KEY" help:"S3 access key" group:"distributed"`
	StorageSecretKey string `env:"LOCALAI_STORAGE_SECRET_KEY" help:"S3 secret key" group:"distributed"`
}

// RegistrationAuthRequired reports whether a registration token must be set
// before the file-transfer server may start — the granular flag or the umbrella.
func (c Config) RegistrationAuthRequired() bool {
	return c.RegistrationRequireAuth || c.DistributedRequireAuth
}

// validateStartup reports a configuration this worker must refuse to boot on,
// as opposed to one it can degrade under.
//
// It runs before prefetch and registration, so a refusal happens while the
// worker is still invisible to the cluster. That ordering is the point of
// checking here at all: these conditions produce a worker that would register,
// heartbeat and be scheduled onto, so discovering them later means discovering
// them as failed inferences on a node the frontend believes is healthy.
func (c Config) validateStartup() error {
	// kong marks --register-to required, so a worker started from the CLI
	// cannot miss it. Checked again here because this is the fail-fast site the
	// other startup refusals live at, and because RegisterTo is now load
	// bearing twice over: it is where the worker registers AND the endpoint its
	// tunnel dials, which is the only way anything reaches it.
	if c.RegisterTo == "" {
		return fmt.Errorf("no frontend URL: set LOCALAI_REGISTER_TO (or --register-to). It is where this worker registers and the endpoint its tunnel dials, and nothing can reach a worker without it")
	}
	// The file-transfer server fails open on an empty token (see
	// nodes.checkBearerToken), so enforcement plus no token is a request to
	// serve the models directory unauthenticated.
	if c.RegistrationAuthRequired() && c.RegistrationToken == "" {
		return fmt.Errorf("registration auth is required (LOCALAI_REGISTRATION_REQUIRE_AUTH or LOCALAI_DISTRIBUTED_REQUIRE_AUTH) but LOCALAI_REGISTRATION_TOKEN is empty: refusing to start an unauthenticated file-transfer server")
	}
	if !c.WorkerTunnel {
		return fmt.Errorf("LOCALAI_WORKER_TUNNEL is false, but this worker advertises no address and binds only loopback, and no frontend path dials a worker's address: without its tunnel nothing can reach it. Remove the setting, or run the pre-tunnel release on both the worker and the frontend")
	}
	return nil
}
