package worker

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/mudler/xlog"
)

// Config is the configuration for the distributed agent worker.
//
// Field tags are kong/kong-env metadata read by core/cli/worker.go's WorkerCMD,
// which embeds Config; this package does NOT import kong and the tags are inert
// here.
//
// Workers are backend-agnostic — they wait for backend.install NATS events
// from the SmartRouter to install and start the required backend.
//
// The routes of the worker are the choice of its operator: NATS when it has a
// NATS URL, the tunnel to the frontend it registers with, or both (see
// routes). The worker acts as a process supervisor:
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

	// GRPCMaxPort bounds the dynamic gRPC port allocator at [basePort, this].
	// The width of that range is how many backend processes this worker can run
	// concurrently; released ports also sit in a short quarantine before reuse,
	// so a worker with heavy start/stop churn needs headroom above its true
	// concurrency. 0 = up to 65535.
	GRPCMaxPort int `env:"LOCALAI_GRPC_MAX_PORT" default:"0" help:"Highest port the worker may assign to a backend gRPC process. The range is [base port, this]; its width caps concurrent backends on this worker. 0 uses up to 65535." group:"server"`

	PreferDevelopmentBackends bool   `env:"LOCALAI_PREFER_DEV_BACKENDS,PREFER_DEV_BACKENDS" default:"false" help:"Prefer compatible installed development aliases and development downloads (worker-local; restart to apply)" group:"server"`
	BackendsPath              string `env:"LOCALAI_BACKENDS_PATH,BACKENDS_PATH" type:"path" default:"${basepath}/backends" help:"Path containing backends" group:"server"`
	BackendsSystemPath        string `env:"LOCALAI_BACKENDS_SYSTEM_PATH" type:"path" default:"${backendssystempath}" help:"Path containing system backends" group:"server"`
	BackendGalleries          string `env:"LOCALAI_BACKEND_GALLERIES,BACKEND_GALLERIES" help:"JSON list of backend galleries" group:"server" default:"${backends}"`
	Galleries                 string `env:"LOCALAI_GALLERIES,GALLERIES" help:"JSON list of model galleries (used to resolve --prefetch-models on boot)" group:"server" default:"${galleries}"`
	ModelsPath                string `env:"LOCALAI_MODELS_PATH,MODELS_PATH" type:"path" default:"${basepath}/models" help:"Path containing models" group:"server"`
	RequireBackendIntegrity   bool   `env:"LOCALAI_REQUIRE_BACKEND_INTEGRITY,REQUIRE_BACKEND_INTEGRITY" help:"If true, reject backend installs without a configured signature verification policy (OCI URIs) or SHA256 (tarball/HTTP URIs)." group:"hardening" default:"false"`
	DisableBackendInstall     bool   `env:"LOCALAI_DISABLE_BACKEND_INSTALL,DISABLE_BACKEND_INSTALL" help:"If true, this worker refuses to install or upgrade backends, including installs requested by the frontend when a model is loaded; only backends already present or provided via LOCALAI_BACKENDS_SYSTEM_PATH are used." group:"hardening" default:"false"`

	// PrefetchModels lets a worker download gallery model artifacts (GGUFs, etc.)
	// from its own outbound internet at boot, instead of waiting for the master to
	// stream them over the cluster network at first-inference time. Useful when the
	// cluster-internal path is slow (slirp/circuit-relay, CGNAT) but outbound NAT
	// works fine. Resolution reuses the same gallery installer the master uses, so
	// the on-disk /models layout is identical. Errors are non-fatal — if the gallery
	// is unreachable on boot, the worker logs a warning and starts the NATS loop
	// anyway; the master can still push the file on demand (existing behaviour).
	PrefetchModels []string `env:"LOCALAI_PREFETCH_MODELS,PREFETCH_MODELS" help:"Comma-separated gallery model IDs to download from LOCALAI_GALLERIES at worker boot (e.g. 'llama-3.2-1b-instruct,phi-3-mini-4k'). Skipped if already on disk and SHA matches." group:"server"`

	// HTTP file transfer
	HTTPAddr                     string `env:"LOCALAI_HTTP_ADDR" default:"" help:"HTTP file transfer server address (default: gRPC port + 1)" group:"server" hidden:""`
	AdvertiseHTTPAddr            string `env:"LOCALAI_ADVERTISE_HTTP_ADDR" help:"HTTP address the frontend uses to reach this node for file transfer" group:"server" hidden:""`
	EphemeralStagingByteLimit    int64  `env:"LOCALAI_EPHEMERAL_STAGING_BYTE_LIMIT" default:"0" help:"Maximum bytes used by worker request-input staging across HTTP and S3 caches. Zero or negative uses min(10 GiB, 10% of filesystem capacity)." group:"server"`
	EphemeralStagingMinFreeBytes int64  `env:"LOCALAI_EPHEMERAL_STAGING_MIN_FREE_BYTES" default:"0" help:"Filesystem space kept free while staging request inputs. Zero or negative uses max(1 GiB, 5% of filesystem capacity)." group:"server"`

	// Registration (required)
	AdvertiseAddr           string   `env:"LOCALAI_ADVERTISE_ADDR" help:"Address the frontend uses to reach this node (defaults to hostname:port from Addr)" group:"registration" hidden:""`
	RegisterTo              string   `env:"LOCALAI_REGISTER_TO" help:"Frontend URL for registration. Required unless --join is given" group:"registration"`
	Join                    string   `env:"LOCALAI_JOIN" help:"Join the cluster over P2P with a worker invite link (localai://pair?...), instead of --register-to and a registration token. The worker needs no inbound port; an admin approves it" group:"registration"`
	P2PIdentity             string   `name:"p2p-identity" env:"LOCALAI_P2P_IDENTITY" help:"Key file of the P2P identity of this worker, used with --join and by the host that model-split workers attached to this node connect to (default: <models path>/../data/p2p/identity.key)" group:"registration"`
	P2PBootstrap            []string `name:"p2p-bootstrap" env:"LOCALAI_P2P_BOOTSTRAP_PEERS_MADDRS" help:"Peers that the P2P host connects to at start, multiaddrs that end in /p2p/<peer ID>. With --join, give the address of the frontend on a network where the invite carries none (a LAN with no public address)" group:"registration"`
	P2PListen               []string `name:"p2p-listen" env:"LOCALAI_P2P_LISTEN_MADDRS" help:"Listen multiaddrs of the P2P host of this worker (default: TCP and QUIC on every interface, random ports). Model-split workers attached to this node connect to it; a fixed, open port gives them a direct path" group:"registration"`
	SplitServer             bool     `name:"split-server" env:"LOCALAI_SPLIT_SERVER" help:"Declare this worker a split server: it may serve model-split workers (llama.cpp RPC and MLX helpers). It then runs a P2P host with open ports, the frontend admits it on the relays it administers, and the helpers attached to it join the next model it loads. The admin can also turn it on or off on the Nodes page" group:"registration"`
	AllowOpenRelay          bool     `env:"LOCALAI_ALLOW_OPEN_RELAY" help:"With --join: redeem an invite that names an open relay, which anyone can use" group:"registration"`
	NodeName                string   `env:"LOCALAI_NODE_NAME" help:"Node name for registration (defaults to hostname)" group:"registration"`
	RegistrationToken       string   `env:"LOCALAI_REGISTRATION_TOKEN" help:"Shared registration token of the frontend, if it has one. It is used to register only; after that the worker holds its own credential" group:"registration"`
	JoinToken               string   `name:"join-token" env:"LOCALAI_JOIN_TOKEN" help:"Single-use join token that an admin minted on the frontend. It replaces the registration token for registering. The worker keeps the own credential that it gets in its state directory, so a restart needs no token" group:"registration"`
	RegistrationRequireAuth bool     `env:"LOCALAI_REGISTRATION_REQUIRE_AUTH" default:"false" help:"Refuse to start the HTTP file-transfer server when no registration token is set (otherwise it fails open and serves read/write to models/staging/data unauthenticated)" group:"registration"`
	DistributedRequireAuth  bool     `env:"LOCALAI_DISTRIBUTED_REQUIRE_AUTH" default:"false" help:"Umbrella switch implying both --nats-require-auth and --registration-require-auth" group:"distributed"`
	HeartbeatInterval       string   `env:"LOCALAI_HEARTBEAT_INTERVAL" default:"10s" help:"Interval between heartbeats" group:"registration"`
	ShutdownAbortWait       string   `env:"LOCALAI_SHUTDOWN_ABORT_WAIT" default:"30s" help:"On SIGTERM, how long the worker waits for the loads it aborts to end (their processes to exit and their installs to return) before it drains. The frontend releases a load that did not end after its stop window" group:"registration"`
	ShutdownRequestWait     string   `env:"LOCALAI_SHUTDOWN_REQUEST_WAIT" default:"30s" help:"On SIGTERM, how long the worker waits for the requests in flight on its models before it deregisters. Loads in progress are aborted at once" group:"registration"`
	ShutdownTimeout         string   `env:"LOCALAI_SHUTDOWN_TIMEOUT" default:"60s" help:"On SIGTERM, the bound of drain, wait for requests and deregistration together. Stopping the backends comes after it" group:"registration"`
	NodeLabels              string   `env:"LOCALAI_NODE_LABELS" help:"Comma-separated key=value labels for this node (e.g. tier=fast,gpu=a100)" group:"registration"`
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

	// VRAMTotal sets the total VRAM this worker reports ("40GB"), overriding
	// GPU detection. VRAMBudget, when set, is resolved against it.
	VRAMTotal string `env:"LOCALAI_VRAM_TOTAL" help:"Total VRAM of this worker as an absolute amount (e.g. 40GB or 48GiB). Overrides GPU detection; use it when the detected VRAM is wrong. LOCALAI_VRAM_BUDGET then caps the share used for placement." group:"registration"`

	// The memory settings of this node. Each one, when set, wins over the
	// cluster value of Settings > Models in memory for this node; an admin
	// override on the Nodes page wins over it. Empty: the cluster value.
	MaxActiveBackends        string `env:"LOCALAI_MAX_ACTIVE_BACKENDS" help:"Models kept loaded on this node (0 = no limit). Empty uses the cluster setting." group:"registration"`
	ModelSwap                string `env:"LOCALAI_MODEL_SWAP" help:"true or false: unload the least recently used idle model on this node to make room for another. Empty uses the cluster setting." group:"registration"`
	MemoryReclaimerThreshold string `env:"LOCALAI_MEMORY_RECLAIMER_THRESHOLD" help:"Memory use (0 to 1) above which idle models on this node are unloaded. Empty uses the cluster setting." group:"registration"`

	// Routes are the ways this worker reaches the cluster. Empty derives them
	// from the other flags (see routes).
	Routes []string `env:"LOCALAI_WORKER_ROUTES" help:"Routes this worker holds to the cluster, comma separated: nats, tunnel or both. Default: nats when a NATS URL is set (never with --join), and the tunnel to the frontend (a websocket, or P2P with --join)" group:"registration"`

	// NATS. A URL gives the worker the NATS route.
	NatsURL         string `env:"LOCALAI_NATS_URL" help:"NATS server URL. With it the worker holds the NATS route. With --routes nats and no URL, the worker uses the address that the frontend hands over" group:"distributed"`
	NatsJWT         string `env:"LOCALAI_NATS_JWT" help:"NATS user JWT override (normally from registration nats_jwt)" group:"distributed"`
	NatsUserSeed    string `env:"LOCALAI_NATS_USER_SEED" help:"NATS user signing seed override (normally from registration nats_user_seed)" group:"distributed"`
	NatsRequireAuth bool   `env:"LOCALAI_NATS_REQUIRE_AUTH" default:"false" help:"Require NATS JWT+seed from registration or env" group:"distributed"`
	NatsTLSCA       string `env:"LOCALAI_NATS_TLS_CA" type:"existingfile" help:"PEM file for NATS server CA (private PKI)" group:"distributed"`
	NatsTLSCert     string `env:"LOCALAI_NATS_TLS_CERT" type:"existingfile" help:"Client certificate for NATS mTLS" group:"distributed"`
	NatsTLSKey      string `env:"LOCALAI_NATS_TLS_KEY" type:"existingfile" help:"Client private key for NATS mTLS" group:"distributed"`

	// S3 storage for distributed file transfer
	StorageURL       string `env:"LOCALAI_STORAGE_URL" help:"S3 endpoint URL" group:"distributed"`
	StorageBucket    string `env:"LOCALAI_STORAGE_BUCKET" help:"S3 bucket name" group:"distributed"`
	StorageRegion    string `env:"LOCALAI_STORAGE_REGION" help:"S3 region" group:"distributed"`
	StorageAccessKey string `env:"LOCALAI_STORAGE_ACCESS_KEY" help:"S3 access key" group:"distributed"`
	StorageSecretKey string `env:"LOCALAI_STORAGE_SECRET_KEY" help:"S3 secret key" group:"distributed"`

	// join is the P2P side of a worker that started with --join. Run sets it.
	join *p2pJoin

	// dataToken is the bearer token between the frontends and this worker for the
	// data plane (the gRPC calls to its backends and its file transfer server). Run
	// sets it after registration: the frontend hands it over, or it is derived
	// from the registration token.
	dataToken string
}

// dataDir is the state directory of the worker. It holds its own credential, the
// process ledger and the P2P identity.
func (c Config) dataDir() string { return filepath.Join(c.ModelsPath, "..", "data") }

// nodeName is the name that the worker registers under.
func (c Config) nodeName() string {
	if c.NodeName != "" {
		return c.NodeName
	}
	if hostname, err := os.Hostname(); err == nil {
		return hostname
	}
	return fmt.Sprintf("node-%d", os.Getpid())
}

// validateFrontend checks that the worker knows how to reach the frontend:
// --register-to or --join, and not both.
func (c Config) validateFrontend() error {
	switch {
	case c.Join != "" && c.RegisterTo != "":
		return errors.New("--join and --register-to are two ways to reach the frontend: give one of them")
	case c.Join != "" && c.JoinToken != "":
		return errors.New("--join (a P2P invite) and --join-token (a join token) are two ways to join: give one of them")
	case c.Join == "" && c.RegisterTo == "":
		return errors.New("missing --register-to (LOCALAI_REGISTER_TO): the URL of the frontend, or --join with a worker invite link")
	}
	return nil
}

// NatsAuthRequired reports whether NATS JWT credentials must be present — the
// granular flag or the umbrella (LOCALAI_DISTRIBUTED_REQUIRE_AUTH).
func (c Config) NatsAuthRequired() bool {
	return c.NatsRequireAuth || c.DistributedRequireAuth
}

// RegistrationAuthRequired reports whether a registration token must be set
// before the file-transfer server may start — the granular flag or the umbrella.
func (c Config) RegistrationAuthRequired() bool {
	return c.RegistrationRequireAuth || c.DistributedRequireAuth
}

// routes returns the routes this worker holds, NATS first.
//
// With --routes the operator names them. Without it a NATS URL gives the NATS
// route, and the frontend of the worker gives the tunnel: a websocket to
// --register-to, or P2P to the frontend of the invite with --join. A worker that
// joined over P2P reaches the cluster through its tunnel only and never talks to
// NATS, so its NATS URL is ignored and --routes nats is refused.
func (c Config) routes() ([]Route, error) {
	p2p := c.Join != ""
	if len(c.Routes) == 0 {
		var out []Route
		switch {
		case c.NatsURL != "" && p2p:
			xlog.Warn("This worker joins over P2P and reaches the cluster through its tunnel only; its NATS URL is ignored")
		case c.NatsURL != "":
			out = append(out, RouteNATS)
		}
		return append(out, RouteTunnel), nil
	}
	out, err := ParseRoutes(c.Routes)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("--routes names no route: give nats, tunnel or both")
	}
	if p2p && slices.Contains(out, RouteNATS) {
		return nil, fmt.Errorf("a worker that joins over P2P (--join) reaches the cluster through its tunnel only: drop %q from --routes", RouteNATS)
	}
	return out, nil
}
