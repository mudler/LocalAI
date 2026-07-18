package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/application"
	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/core/p2p"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
	"github.com/mudler/LocalAI/pkg/signals"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/LocalAI/pkg/vrambudget"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/mudler/xlog"
)

// CLI Flag Naming Convention:
// All CLI flags use kebab-case (e.g., --backends-path, --p2p-token).
// When renaming flags, add the old name as an alias for backward compatibility
// and document the deprecation in the help text.

type RunCMD struct {
	ModelArgs []string `arg:"" optional:"" name:"models" help:"Model configuration URLs to load"`
	Color     string   `env:"COLOR" hidden:""`
	NoColor   string   `env:"NO_COLOR" hidden:""`
	HFToken   string   `env:"HF_TOKEN" hidden:""`

	ExternalBackends             []string      `env:"LOCALAI_EXTERNAL_BACKENDS,EXTERNAL_BACKENDS" help:"A list of external backends to load from gallery on boot" group:"backends"`
	WebRTCNAT1To1IPs             []string      `env:"LOCALAI_WEBRTC_NAT_1TO1_IPS,WEBRTC_NAT_1TO1_IPS" help:"IPs advertised as the host ICE candidates for /v1/realtime WebRTC instead of every local interface. Set to the reachable host/LAN IP when running under Docker host networking or NAT, where pion otherwise offers unreachable bridge addresses and the connection drops after ICE consent checks fail." group:"api"`
	WebRTCICEInterfaces          []string      `env:"LOCALAI_WEBRTC_ICE_INTERFACES,WEBRTC_ICE_INTERFACES" help:"Restrict /v1/realtime WebRTC ICE candidate gathering to these network interfaces (e.g. eth0), filtering out docker0/veth noise." group:"api"`
	BackendsPath                 string        `env:"LOCALAI_BACKENDS_PATH,BACKENDS_PATH" type:"path" default:"${basepath}/backends" help:"Path containing backends used for inferencing" group:"backends"`
	BackendsSystemPath           string        `env:"LOCALAI_BACKENDS_SYSTEM_PATH,BACKEND_SYSTEM_PATH" type:"path" default:"/var/lib/local-ai/backends" help:"Path containing system backends used for inferencing" group:"backends"`
	ModelsPath                   string        `env:"LOCALAI_MODELS_PATH,MODELS_PATH" type:"path" default:"${basepath}/models" help:"Path containing models used for inferencing" group:"storage"`
	GeneratedContentPath         string        `env:"LOCALAI_GENERATED_CONTENT_PATH,GENERATED_CONTENT_PATH" type:"path" default:"${generatedcontentpath}" help:"Location for generated content (e.g. images, audio, videos)" group:"storage"`
	UploadPath                   string        `env:"LOCALAI_UPLOAD_PATH,UPLOAD_PATH" type:"path" default:"${uploadpath}" help:"Path to store uploads from files api" group:"storage"`
	DataPath                     string        `env:"LOCALAI_DATA_PATH" type:"path" default:"${basepath}/data" help:"Path for persistent data (collectiondb, agent state, tasks, jobs). Separates mutable data from configuration" group:"storage"`
	LocalaiConfigDir             string        `env:"LOCALAI_CONFIG_DIR" type:"path" default:"${basepath}/configuration" help:"Directory for dynamic loading of certain configuration files (currently api_keys.json and external_backends.json)" group:"storage"`
	LocalaiConfigDirPollInterval time.Duration `env:"LOCALAI_CONFIG_DIR_POLL_INTERVAL" help:"Typically the config path picks up changes automatically, but if your system has broken fsnotify events, set this to an interval to poll the LocalAI Config Dir (example: 1m)" group:"storage"`
	// The alias on this option is there to preserve functionality with the old `--config-file` parameter
	ModelsConfigFile          string   `env:"LOCALAI_MODELS_CONFIG_FILE,CONFIG_FILE" aliases:"config-file" help:"YAML file containing a list of model backend configs" group:"storage"`
	BackendGalleries          string   `env:"LOCALAI_BACKEND_GALLERIES,BACKEND_GALLERIES" help:"JSON list of backend galleries" group:"backends" default:"${backends}"`
	Galleries                 string   `env:"LOCALAI_GALLERIES,GALLERIES" help:"JSON list of galleries" group:"models" default:"${galleries}"`
	AutoloadGalleries         bool     `env:"LOCALAI_AUTOLOAD_GALLERIES,AUTOLOAD_GALLERIES" group:"models" default:"true"`
	AutoloadBackendGalleries  bool     `env:"LOCALAI_AUTOLOAD_BACKEND_GALLERIES,AUTOLOAD_BACKEND_GALLERIES" group:"backends" default:"true"`
	BackendImagesReleaseTag   string   `env:"LOCALAI_BACKEND_IMAGES_RELEASE_TAG,BACKEND_IMAGES_RELEASE_TAG" help:"Fallback release tag for backend images" group:"backends" default:"latest"`
	BackendImagesBranchTag    string   `env:"LOCALAI_BACKEND_IMAGES_BRANCH_TAG,BACKEND_IMAGES_BRANCH_TAG" help:"Fallback branch tag for backend images" group:"backends" default:"master"`
	BackendDevSuffix          string   `env:"LOCALAI_BACKEND_DEV_SUFFIX,BACKEND_DEV_SUFFIX" help:"Development suffix for backend images" group:"backends" default:"development"`
	AutoUpgradeBackends       bool     `env:"LOCALAI_AUTO_UPGRADE_BACKENDS,AUTO_UPGRADE_BACKENDS" help:"Automatically upgrade backends when new versions are detected" group:"backends" default:"false"`
	PreferDevelopmentBackends bool     `env:"LOCALAI_PREFER_DEV_BACKENDS,PREFER_DEV_BACKENDS" help:"Prefer development backend versions (shows development backends by default in UI)" group:"backends" default:"false"`
	PreloadModels             string   `env:"LOCALAI_PRELOAD_MODELS,PRELOAD_MODELS" help:"A List of models to apply in JSON at start" group:"models"`
	Models                    []string `env:"LOCALAI_MODELS,MODELS" help:"A List of model configuration URLs to load" group:"models"`
	PreloadModelsConfig       string   `env:"LOCALAI_PRELOAD_MODELS_CONFIG,PRELOAD_MODELS_CONFIG" help:"A List of models to apply at startup. Path to a YAML config file" group:"models"`

	F16         bool `name:"f16" env:"LOCALAI_F16,F16" help:"Enable GPU acceleration" group:"performance"`
	Threads     int  `env:"LOCALAI_THREADS,THREADS" short:"t" help:"Number of threads used for parallel computation. Usage of the number of physical cores in the system is suggested" group:"performance"`
	ContextSize int  `env:"LOCALAI_CONTEXT_SIZE,CONTEXT_SIZE" help:"Default context size for models" group:"performance"`

	Address                            string   `env:"LOCALAI_ADDRESS,ADDRESS" default:":8080" help:"Bind address for the API server" group:"api"`
	CORS                               bool     `env:"LOCALAI_CORS,CORS" help:"" group:"api"`
	CORSAllowOrigins                   string   `env:"LOCALAI_CORS_ALLOW_ORIGINS,CORS_ALLOW_ORIGINS" group:"api"`
	DisableCSRF                        bool     `env:"LOCALAI_DISABLE_CSRF" help:"Disable CSRF middleware (enabled by default)" group:"api"`
	UploadLimit                        int      `env:"LOCALAI_UPLOAD_LIMIT,UPLOAD_LIMIT" default:"15" help:"Default upload-limit in MB" group:"api"`
	APIKeys                            []string `env:"LOCALAI_API_KEY,API_KEY" help:"List of API Keys to enable API authentication. When this is set, all the requests must be authenticated with one of these API keys" group:"api"`
	DisableWebUI                       bool     `env:"LOCALAI_DISABLE_WEBUI,DISABLE_WEBUI" default:"false" help:"Disables the web user interface. When set to true, the server will only expose API endpoints without serving the web interface" group:"api"`
	OllamaAPIRootEndpoint              bool     `env:"LOCALAI_OLLAMA_API_ROOT_ENDPOINT" default:"false" help:"Register Ollama-compatible health check on / (replaces web UI on root path). The /api/* Ollama endpoints are always available regardless of this flag" group:"api"`
	DisableRuntimeSettings             bool     `env:"LOCALAI_DISABLE_RUNTIME_SETTINGS,DISABLE_RUNTIME_SETTINGS" default:"false" help:"Disables the runtime settings. When set to true, the server will not load the runtime settings from the runtime_settings.json file" group:"api"`
	DisablePredownloadScan             bool     `env:"LOCALAI_DISABLE_PREDOWNLOAD_SCAN" help:"If true, disables the best-effort security scanner before downloading any files." group:"hardening" default:"false"`
	RequireBackendIntegrity            bool     `env:"LOCALAI_REQUIRE_BACKEND_INTEGRITY,REQUIRE_BACKEND_INTEGRITY" help:"If true, backend installs without a configured signature verification policy (for OCI URIs) or SHA256 (for tarball/HTTP URIs) are rejected. Default is to warn and install. Set this in production once your gallery's verification: block is populated." group:"hardening" default:"false"`
	OpaqueErrors                       bool     `env:"LOCALAI_OPAQUE_ERRORS" default:"false" help:"If true, all error responses are replaced with blank 500 errors. This is intended only for hardening against information leaks and is normally not recommended." group:"hardening"`
	UseSubtleKeyComparison             bool     `env:"LOCALAI_SUBTLE_KEY_COMPARISON" default:"false" help:"If true, API Key validation comparisons will be performed using constant-time comparisons rather than simple equality. This trades off performance on each request for resiliancy against timing attacks." group:"hardening"`
	DisableApiKeyRequirementForHttpGet bool     `env:"LOCALAI_DISABLE_API_KEY_REQUIREMENT_FOR_HTTP_GET" default:"false" help:"If true, a valid API key is not required to issue GET requests to portions of the web ui. This should only be enabled in secure testing environments" group:"hardening"`
	AllowInsecurePublicBind            bool     `env:"LOCALAI_ALLOW_INSECURE_PUBLIC_BIND" default:"false" help:"Allow binding the API to a public-internet address without any authentication configured. Without this flag the server refuses to start when the bind address is public (or a wildcard on a host with a public interface) and no auth backend or static API key is set. Loopback, RFC 1918 LAN, ULA, link-local, and CGNAT (Tailscale) ranges are accepted regardless." group:"hardening"`
	DisableMetricsEndpoint             bool     `env:"LOCALAI_DISABLE_METRICS_ENDPOINT,DISABLE_METRICS_ENDPOINT" default:"false" help:"Disable the /metrics endpoint" group:"api"`
	HttpGetExemptedEndpoints           []string `env:"LOCALAI_HTTP_GET_EXEMPTED_ENDPOINTS" default:"^/$,^/app(/.*)?$,^/browse(/.*)?$,^/login/?$,^/explorer/?$,^/assets/.*$,^/static/.*$,^/swagger.*$" help:"If LOCALAI_DISABLE_API_KEY_REQUIREMENT_FOR_HTTP_GET is overriden to true, this is the list of endpoints to exempt. Only adjust this in case of a security incident or as a result of a personal security posture review" group:"hardening"`
	Peer2Peer                          bool     `env:"LOCALAI_P2P,P2P" name:"p2p" default:"false" help:"Enable P2P mode" group:"p2p"`
	Peer2PeerDHTInterval               int      `env:"LOCALAI_P2P_DHT_INTERVAL,P2P_DHT_INTERVAL" default:"360" name:"p2p-dht-interval" help:"Interval for DHT refresh (used during token generation)" group:"p2p"`
	Peer2PeerOTPInterval               int      `env:"LOCALAI_P2P_OTP_INTERVAL,P2P_OTP_INTERVAL" default:"9000" name:"p2p-otp-interval" help:"Interval for OTP refresh (used during token generation)" group:"p2p"`
	Peer2PeerToken                     string   `env:"LOCALAI_P2P_TOKEN,P2P_TOKEN,TOKEN" name:"p2p-token" aliases:"p2ptoken" help:"Token for P2P mode (optional; --p2ptoken is deprecated, use --p2p-token)" group:"p2p"`
	Peer2PeerNetworkID                 string   `env:"LOCALAI_P2P_NETWORK_ID,P2P_NETWORK_ID" help:"Network ID for P2P mode, can be set arbitrarly by the user for grouping a set of instances" group:"p2p"`
	SingleActiveBackend                bool     `env:"LOCALAI_SINGLE_ACTIVE_BACKEND,SINGLE_ACTIVE_BACKEND" help:"Allow only one backend to be run at a time (deprecated: use --max-active-backends=1 instead)" group:"backends"`
	MaxActiveBackends                  int      `env:"LOCALAI_MAX_ACTIVE_BACKENDS,MAX_ACTIVE_BACKENDS" default:"0" help:"Maximum number of backends to keep loaded at once (0 = unlimited, 1 = single backend mode). Least recently used backends are evicted when limit is reached" group:"backends"`
	PreloadBackendOnly                 bool     `env:"LOCALAI_PRELOAD_BACKEND_ONLY,PRELOAD_BACKEND_ONLY" default:"false" help:"Do not launch the API services, only the preloaded models / backends are started (useful for multi-node setups)" group:"backends"`
	ExternalGRPCBackends               []string `env:"LOCALAI_EXTERNAL_GRPC_BACKENDS,EXTERNAL_GRPC_BACKENDS" help:"A list of external grpc backends" group:"backends"`
	EnableWatchdogIdle                 bool     `env:"LOCALAI_WATCHDOG_IDLE,WATCHDOG_IDLE" default:"false" help:"Enable watchdog for stopping backends that are idle longer than the watchdog-idle-timeout" group:"backends"`
	WatchdogIdleTimeout                string   `env:"LOCALAI_WATCHDOG_IDLE_TIMEOUT,WATCHDOG_IDLE_TIMEOUT" default:"15m" help:"Threshold beyond which an idle backend should be stopped" group:"backends"`
	EnableWatchdogBusy                 bool     `env:"LOCALAI_WATCHDOG_BUSY,WATCHDOG_BUSY" default:"false" help:"Enable watchdog for stopping backends that are busy longer than the watchdog-busy-timeout" group:"backends"`
	WatchdogBusyTimeout                string   `env:"LOCALAI_WATCHDOG_BUSY_TIMEOUT,WATCHDOG_BUSY_TIMEOUT" default:"5m" help:"Threshold beyond which a busy backend should be stopped" group:"backends"`
	WatchdogInterval                   string   `env:"LOCALAI_WATCHDOG_INTERVAL,WATCHDOG_INTERVAL" default:"500ms" help:"Interval between watchdog checks (e.g., 500ms, 5s, 1m) (default: 500ms)" group:"backends"`
	EnableMemoryReclaimer              bool     `env:"LOCALAI_MEMORY_RECLAIMER,MEMORY_RECLAIMER,LOCALAI_GPU_RECLAIMER,GPU_RECLAIMER" default:"false" help:"Enable memory threshold monitoring to auto-evict backends when memory usage exceeds threshold (uses GPU VRAM if available, otherwise RAM)" group:"backends"`
	MemoryReclaimerThreshold           float64  `env:"LOCALAI_MEMORY_RECLAIMER_THRESHOLD,MEMORY_RECLAIMER_THRESHOLD,LOCALAI_GPU_RECLAIMER_THRESHOLD,GPU_RECLAIMER_THRESHOLD" default:"0.95" help:"Memory usage threshold (0.0-1.0) that triggers backend eviction (default 0.95 = 95%%)" group:"backends"`
	VRAMBudget                         string   `env:"LOCALAI_VRAM_BUDGET" help:"Cap VRAM used for model allocation on this node, as a percentage (e.g. 80%) or absolute amount (e.g. 12GB). Empty uses all detected VRAM." group:"backends"`
	ForceEvictionWhenBusy              bool     `env:"LOCALAI_FORCE_EVICTION_WHEN_BUSY,FORCE_EVICTION_WHEN_BUSY" default:"false" help:"Force eviction even when models have active API calls (default: false for safety)" group:"backends"`
	SizeAwareEviction                  bool     `env:"LOCALAI_SIZE_AWARE_EVICTION,SIZE_AWARE_EVICTION" default:"false" help:"Evict the largest loaded model first rather than the least-recently-used one, keeping small utility models resident and maximizing freed memory per eviction" group:"backends"`
	LRUEvictionMaxRetries              int      `env:"LOCALAI_LRU_EVICTION_MAX_RETRIES,LRU_EVICTION_MAX_RETRIES" default:"30" help:"Maximum number of retries when waiting for busy models to become idle before eviction (default: 30)" group:"backends"`
	LRUEvictionRetryInterval           string   `env:"LOCALAI_LRU_EVICTION_RETRY_INTERVAL,LRU_EVICTION_RETRY_INTERVAL" default:"1s" help:"Interval between retries when waiting for busy models to become idle (e.g., 1s, 2s) (default: 1s)" group:"backends"`
	ModelLoadFailureCooldown           string   `env:"LOCALAI_MODEL_LOAD_FAILURE_COOLDOWN,MODEL_LOAD_FAILURE_COOLDOWN" default:"10s" help:"After a model load fails, refuse new load attempts for that model for this long (returned as HTTP 503 + Retry-After) so a client polling a broken model doesn't respawn a crashing backend every request. Doubles per consecutive failure up to 5m; reset on success. Set to 0 to disable (e.g., 10s, 30s)" group:"backends"`
	Federated                          bool     `env:"LOCALAI_FEDERATED,FEDERATED" help:"Enable federated instance" group:"federated"`
	DisableGalleryEndpoint             bool     `env:"LOCALAI_DISABLE_GALLERY_ENDPOINT,DISABLE_GALLERY_ENDPOINT" help:"Disable the gallery endpoints" group:"api"`
	DisableMCP                         bool     `env:"LOCALAI_DISABLE_MCP,DISABLE_MCP" help:"Disable MCP (Model Context Protocol) support" group:"api" default:"false"`
	MachineTag                         string   `env:"LOCALAI_MACHINE_TAG,MACHINE_TAG" help:"Add Machine-Tag header to each response which is useful to track the machine in the P2P network" group:"api"`
	LoadToMemory                       []string `env:"LOCALAI_LOAD_TO_MEMORY,LOAD_TO_MEMORY" help:"A list of models to load into memory at startup" group:"models"`
	EnableTracing                      bool     `env:"LOCALAI_ENABLE_TRACING,ENABLE_TRACING" help:"Enable API tracing" group:"api"`
	TracingMaxItems                    int      `env:"LOCALAI_TRACING_MAX_ITEMS" default:"1024" help:"Maximum number of traces to keep" group:"api"`
	TracingMaxBodyBytes                int      `env:"LOCALAI_TRACING_MAX_BODY_BYTES" default:"65536" help:"Maximum bytes captured per request/response body in the trace buffer (0 = uncapped). Caps memory growth from chatty endpoints like /embeddings." group:"api"`
	AgentJobRetentionDays              int      `env:"LOCALAI_AGENT_JOB_RETENTION_DAYS,AGENT_JOB_RETENTION_DAYS" default:"30" help:"Number of days to keep agent job history (default: 30)" group:"api"`
	OpenResponsesStoreTTL              string   `env:"LOCALAI_OPEN_RESPONSES_STORE_TTL,OPEN_RESPONSES_STORE_TTL" default:"0" help:"TTL for Open Responses store (e.g., 1h, 30m, 0 = no expiration)" group:"api"`

	// LocalAI Assistant chat modality (in-process admin MCP server)
	DisableLocalAIAssistant bool `env:"LOCALAI_DISABLE_ASSISTANT" default:"false" help:"Disable the LocalAI Assistant chat modality (in-process admin MCP server)" group:"assistant"`

	// Agent Pool (LocalAGI)
	DisableAgents                  bool   `env:"LOCALAI_DISABLE_AGENTS" default:"false" help:"Disable the agent pool feature" group:"agents"`
	AgentPoolAPIURL                string `env:"LOCALAI_AGENT_POOL_API_URL" help:"Default API URL for agents (defaults to self-referencing LocalAI)" group:"agents"`
	AgentPoolAPIKey                string `env:"LOCALAI_AGENT_POOL_API_KEY" help:"Default API key for agents (defaults to first LocalAI API key)" group:"agents"`
	AgentPoolDefaultModel          string `env:"LOCALAI_AGENT_POOL_DEFAULT_MODEL" help:"Default model for agents" group:"agents"`
	AgentPoolMultimodalModel       string `env:"LOCALAI_AGENT_POOL_MULTIMODAL_MODEL" help:"Default multimodal model for agents" group:"agents"`
	AgentPoolTranscriptionModel    string `env:"LOCALAI_AGENT_POOL_TRANSCRIPTION_MODEL" help:"Default transcription model for agents" group:"agents"`
	AgentPoolTranscriptionLanguage string `env:"LOCALAI_AGENT_POOL_TRANSCRIPTION_LANGUAGE" help:"Default transcription language for agents" group:"agents"`
	AgentPoolTTSModel              string `env:"LOCALAI_AGENT_POOL_TTS_MODEL" help:"Default TTS model for agents" group:"agents"`
	AgentPoolStateDir              string `env:"LOCALAI_AGENT_POOL_STATE_DIR" help:"State directory for agent pool" group:"agents"`
	AgentPoolTimeout               string `env:"LOCALAI_AGENT_POOL_TIMEOUT" default:"5m" help:"Default agent timeout" group:"agents"`
	AgentPoolEnableSkills          bool   `env:"LOCALAI_AGENT_POOL_ENABLE_SKILLS" default:"false" help:"Enable skills service for agents" group:"agents"`
	AgentPoolVectorEngine          string `env:"LOCALAI_AGENT_POOL_VECTOR_ENGINE" default:"chromem" help:"Vector engine type for agent knowledge base" group:"agents"`
	AgentPoolEmbeddingModel        string `env:"LOCALAI_AGENT_POOL_EMBEDDING_MODEL" default:"granite-embedding-107m-multilingual" help:"Embedding model for agent knowledge base" group:"agents"`
	AgentPoolCustomActionsDir      string `env:"LOCALAI_AGENT_POOL_CUSTOM_ACTIONS_DIR" help:"Custom actions directory for agents" group:"agents"`
	AgentPoolDatabaseURL           string `env:"LOCALAI_AGENT_POOL_DATABASE_URL" help:"Database URL for agent collections" group:"agents"`
	AgentPoolMaxChunkingSize       int    `env:"LOCALAI_AGENT_POOL_MAX_CHUNKING_SIZE" default:"400" help:"Maximum chunking size for knowledge base documents" group:"agents"`
	AgentPoolChunkOverlap          int    `env:"LOCALAI_AGENT_POOL_CHUNK_OVERLAP" default:"0" help:"Chunk overlap size for knowledge base documents" group:"agents"`
	AgentPoolEnableLogs            bool   `env:"LOCALAI_AGENT_POOL_ENABLE_LOGS" default:"false" help:"Enable agent logging" group:"agents"`
	AgentPoolCollectionDBPath      string `env:"LOCALAI_AGENT_POOL_COLLECTION_DB_PATH" help:"Database path for agent collections" group:"agents"`
	AgentHubURL                    string `env:"LOCALAI_AGENT_HUB_URL" default:"https://agenthub.localai.io" help:"URL for the agent hub where users can browse and download agent configurations" group:"agents"`

	// Authentication
	AuthEnabled          bool   `env:"LOCALAI_AUTH" default:"false" help:"Enable user authentication and authorization" group:"auth"`
	AuthDatabaseURL      string `env:"LOCALAI_AUTH_DATABASE_URL,DATABASE_URL" help:"Database URL for auth (postgres:// or file path for SQLite). Defaults to {DataPath}/database.db" group:"auth"`
	GitHubClientID       string `env:"GITHUB_CLIENT_ID" help:"GitHub OAuth App Client ID (auto-enables auth when set)" group:"auth"`
	GitHubClientSecret   string `env:"GITHUB_CLIENT_SECRET" help:"GitHub OAuth App Client Secret" group:"auth"`
	OIDCIssuer           string `env:"LOCALAI_OIDC_ISSUER" help:"OIDC issuer URL for auto-discovery" group:"auth"`
	OIDCClientID         string `env:"LOCALAI_OIDC_CLIENT_ID" help:"OIDC Client ID (auto-enables auth)" group:"auth"`
	OIDCClientSecret     string `env:"LOCALAI_OIDC_CLIENT_SECRET" help:"OIDC Client Secret" group:"auth"`
	ExternalBaseURL      string `env:"LOCALAI_BASE_URL" help:"External base URL of this instance (e.g. https://localhost:8080). Used for OAuth callbacks and self-referential links (generated images/videos, job status). When unset, derived from X-Forwarded-Proto/Host or Forwarded headers." group:"api"`
	AuthAdminEmail       string `env:"LOCALAI_ADMIN_EMAIL" help:"Email address to auto-promote to admin role" group:"auth"`
	AuthRegistrationMode string `env:"LOCALAI_REGISTRATION_MODE" default:"open" help:"Registration mode: 'open' (default), 'approval', or 'invite' (invite code required)" group:"auth"`
	DisableLocalAuth     bool   `env:"LOCALAI_DISABLE_LOCAL_AUTH" default:"false" help:"Disable local email/password registration and login (use with OAuth/OIDC-only setups)" group:"auth"`
	AuthAPIKeyHMACSecret string `env:"LOCALAI_AUTH_HMAC_SECRET" help:"HMAC secret for API key hashing (auto-generated if empty)" group:"auth"`
	DefaultAPIKeyExpiry  string `env:"LOCALAI_DEFAULT_API_KEY_EXPIRY" help:"Default expiry for API keys (e.g. 90d, 1y; empty = no expiry)" group:"auth"`

	// Distributed / Horizontal Scaling
	Distributed               bool   `env:"LOCALAI_DISTRIBUTED" default:"false" help:"Enable distributed mode (requires PostgreSQL + NATS)" group:"distributed"`
	InstanceID                string `env:"LOCALAI_INSTANCE_ID" help:"Unique instance ID for distributed mode (auto-generated UUID if empty)" group:"distributed"`
	NatsURL                   string `env:"LOCALAI_NATS_URL" help:"NATS server URL (e.g., nats://localhost:4222)" group:"distributed"`
	StorageURL                string `env:"LOCALAI_STORAGE_URL" help:"S3-compatible storage endpoint URL (e.g., http://minio:9000)" group:"distributed"`
	StorageBucket             string `env:"LOCALAI_STORAGE_BUCKET" default:"localai" help:"S3 bucket name for object storage" group:"distributed"`
	StorageRegion             string `env:"LOCALAI_STORAGE_REGION" default:"us-east-1" help:"S3 region" group:"distributed"`
	StorageAccessKey          string `env:"LOCALAI_STORAGE_ACCESS_KEY" help:"S3 access key ID" group:"distributed"`
	StorageSecretKey          string `env:"LOCALAI_STORAGE_SECRET_KEY" help:"S3 secret access key" group:"distributed"`
	RegistrationToken         string `env:"LOCALAI_REGISTRATION_TOKEN" help:"Token that backend nodes must provide to register (empty = no auth required)" group:"distributed"`
	RegistrationRequireAuth   bool   `env:"LOCALAI_REGISTRATION_REQUIRE_AUTH" default:"false" help:"Fail startup when distributed mode is enabled but LOCALAI_REGISTRATION_TOKEN is empty (node endpoints and worker file-transfer server would otherwise be unauthenticated)" group:"distributed"`
	DistributedRequireAuth    bool   `env:"LOCALAI_DISTRIBUTED_REQUIRE_AUTH" default:"false" help:"Umbrella switch: require BOTH NATS JWT credentials and a registration token when distributed mode is enabled (implies --nats-require-auth and --registration-require-auth)" group:"distributed"`
	AutoApproveNodes          bool   `env:"LOCALAI_AUTO_APPROVE_NODES" default:"false" help:"Auto-approve new worker nodes (skip admin approval)" group:"distributed"`
	DistributedSharedModels   bool   `env:"LOCALAI_DISTRIBUTED_SHARED_MODELS" default:"false" help:"Assert that every node mounts the SAME models directory at the SAME path (shared volume). When true, the router skips staging model files to workers and loads them directly from the shared path, avoiding re-downloads." group:"distributed"`
	DistributedPrefixCache    bool   `env:"LOCALAI_DISTRIBUTED_PREFIX_CACHE" default:"true" help:"Enable prefix-cache-aware routing in distributed mode (default true). When false, routing falls back to round-robin." group:"distributed"`
	DistributedPrefixCacheTTL string `env:"LOCALAI_DISTRIBUTED_PREFIX_CACHE_TTL" help:"Idle-timeout for prefix-cache index entries; also drives the background eviction cadence (every TTL/2). Default 5m." group:"distributed"`
	BackendInstallTimeout     string `env:"LOCALAI_NATS_BACKEND_INSTALL_TIMEOUT" help:"NATS round-trip timeout for backend.install requests sent to worker nodes (default 15m). Increase for slow links pulling multi-GB images." group:"distributed"`
	BackendUpgradeTimeout     string `env:"LOCALAI_NATS_BACKEND_UPGRADE_TIMEOUT" help:"NATS round-trip timeout for backend.upgrade requests (default 15m)." group:"distributed"`
	NatsAccountSeed           string `env:"LOCALAI_NATS_ACCOUNT_SEED" help:"NATS account signing seed (SU...) used to mint per-node worker JWTs at registration" group:"distributed"`
	NatsServiceJWT            string `env:"LOCALAI_NATS_SERVICE_JWT" help:"NATS user JWT for the frontend (and agent workers) to publish control-plane messages" group:"distributed"`
	NatsServiceSeed           string `env:"LOCALAI_NATS_SERVICE_SEED" help:"NATS user signing seed (SU...) paired with LOCALAI_NATS_SERVICE_JWT" group:"distributed"`
	NatsWorkerJWTTTL          string `env:"LOCALAI_NATS_WORKER_JWT_TTL" help:"Lifetime of minted per-node NATS JWTs (e.g. 24h, default 24h)" group:"distributed"`
	NatsRequireAuth           bool   `env:"LOCALAI_NATS_REQUIRE_AUTH" default:"false" help:"Require NATS JWT credentials (service JWT + account seed) when distributed mode is enabled" group:"distributed"`
	NatsTLSCA                 string `env:"LOCALAI_NATS_TLS_CA" type:"existingfile" help:"PEM file for NATS server CA (private PKI); use with tls:// in --nats-url" group:"distributed"`
	NatsTLSCert               string `env:"LOCALAI_NATS_TLS_CERT" type:"existingfile" help:"Client certificate for NATS mTLS" group:"distributed"`
	NatsTLSKey                string `env:"LOCALAI_NATS_TLS_KEY" type:"existingfile" help:"Client private key for NATS mTLS" group:"distributed"`
	ExposeNodeHeader          bool   `env:"LOCALAI_EXPOSE_NODE_HEADER" default:"false" help:"Set the X-LocalAI-Node response header on inference responses (OpenAI chat/completions/embeddings, Anthropic /v1/messages, Ollama /api/chat,/api/generate,/api/embed) with the ID of the worker that served the request. Disabled by default: the node ID reveals internal topology and should not be exposed on a public endpoint. Best-effort: under heavy concurrency the header may reflect a recent routing decision rather than this exact request's." group:"distributed"`
	ModelScheduling           string `env:"LOCALAI_MODEL_SCHEDULING" help:"Declarative per-model scheduling config applied at startup (inline JSON list of {model_name,node_selector,min_replicas,max_replicas,replicas:\"all\"}). Authoritative: overwrites matching models on every boot. Distributed mode only." group:"distributed"`
	ModelSchedulingConfig     string `env:"LOCALAI_MODEL_SCHEDULING_CONFIG" help:"Path to a YAML file with the same per-model scheduling list as LOCALAI_MODEL_SCHEDULING. Distributed mode only." group:"distributed"`

	Version bool

	// Cloud-proxy MITM listener (off by default).
	MITMListen string `env:"LOCALAI_MITM_LISTEN" help:"Address (host:port) for the cloudproxy MITM listener. Empty = disabled. Clients set HTTPS_PROXY=http://<this>:<port>. Intercept hosts are declared per-model via the model YAML mitm.hosts: block; create one from the Add Model UI." group:"middleware"`
	MITMCADir  string `env:"LOCALAI_MITM_CA_DIR" type:"path" help:"Directory holding the MITM proxy CA cert + key. Defaults to <data-path>/mitm-ca." group:"middleware"`

	PIIDefaultDetectors []string `env:"LOCALAI_PII_DEFAULT_DETECTORS" help:"Instance-wide default PII/secret detector model names applied to any PII-enabled model (chiefly cloud-proxy / MITM models) that names no pii.detectors of its own. Comma-separated, e.g. privacy-filter-nemotron,secret-filter. Takes precedence over the value persisted via the Middleware UI." group:"middleware"`
}

// userScopedTempDir returns a temp directory namespaced to the current user.
//
// The generated-content and upload directories are ephemeral, so they live
// under the OS temp dir - but a fixed shared name like /tmp/generated is a trap
// on any multi-user host. macOS routes /tmp to the shared /private/tmp for every
// account, so whichever user starts LocalAI first creates the parent with 0750
// perms and every other account then fails startup with
// "mkdir /tmp/generated/content: permission denied" (the same happens on Linux
// once a stale root-owned /tmp/generated is left behind). Scoping to the current
// UID gives each account its own tree so they never collide.
func userScopedTempDir() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("localai-%d", os.Getuid()))
}

// DefaultGeneratedContentPath returns the default location for backend-generated
// content (images, audio, videos).
func DefaultGeneratedContentPath() string {
	return filepath.Join(userScopedTempDir(), "generated", "content")
}

// DefaultUploadPath returns the default location for uploads from the files API.
func DefaultUploadPath() string {
	return filepath.Join(userScopedTempDir(), "upload")
}

func (r *RunCMD) Run(ctx *cliContext.Context) error {
	warnDeprecatedFlags()

	if r.Version {
		fmt.Println(internal.Version)
		return nil
	}

	os.MkdirAll(r.BackendsPath, 0750)
	os.MkdirAll(r.ModelsPath, 0750)

	systemState, err := system.GetSystemState(
		system.WithBackendSystemPath(r.BackendsSystemPath),
		system.WithModelPath(r.ModelsPath),
		system.WithBackendPath(r.BackendsPath),
		system.WithBackendImagesReleaseTag(r.BackendImagesReleaseTag),
		system.WithBackendImagesBranchTag(r.BackendImagesBranchTag),
		system.WithBackendDevSuffix(r.BackendDevSuffix),
		system.WithPreferDevelopmentBackends(r.PreferDevelopmentBackends),
	)
	if err != nil {
		return err
	}

	opts := []config.AppOption{
		config.WithContext(context.Background()),
		config.WithModelArtifactMaterializer(modelartifacts.NewDefaultManager(
			modelartifacts.WithHuggingFaceToken(r.HFToken),
		)),
		config.WithModelPreloadDisplay(r.Color, r.NoColor != ""),
		config.WithConfigFile(r.ModelsConfigFile),
		config.WithJSONStringPreload(r.PreloadModels),
		config.WithYAMLConfigPreload(r.PreloadModelsConfig),
		config.WithSystemState(systemState),
		config.WithContextSize(r.ContextSize),
		config.WithDebug(ctx.Debug || (ctx.LogLevel != nil && *ctx.LogLevel == "debug")),
		config.WithGeneratedContentDir(r.GeneratedContentPath),
		config.WithUploadDir(r.UploadPath),
		config.WithDataPath(r.DataPath),
		config.WithDynamicConfigDir(r.LocalaiConfigDir),
		config.WithDynamicConfigDirPollInterval(r.LocalaiConfigDirPollInterval),
		config.WithF16(r.F16),
		config.WithStringGalleries(r.Galleries),
		config.WithBackendGalleries(r.BackendGalleries),
		config.WithCors(r.CORS),
		config.WithCorsAllowOrigins(r.CORSAllowOrigins),
		config.WithDisableCSRF(r.DisableCSRF),
		config.WithThreads(r.Threads),
		config.WithUploadLimitMB(r.UploadLimit),
		config.WithApiKeys(r.APIKeys),
		config.WithModelsURL(append(r.Models, r.ModelArgs...)...),
		config.WithExternalBackends(r.ExternalBackends...),
		config.WithWebRTCNAT1To1IPs(r.WebRTCNAT1To1IPs...),
		config.WithWebRTCICEInterfaces(r.WebRTCICEInterfaces...),
		config.WithOpaqueErrors(r.OpaqueErrors),
		config.WithEnforcedPredownloadScans(!r.DisablePredownloadScan),
		config.WithSubtleKeyComparison(r.UseSubtleKeyComparison),
		config.WithDisableApiKeyRequirementForHttpGet(r.DisableApiKeyRequirementForHttpGet),
		config.WithHttpGetExemptedEndpoints(r.HttpGetExemptedEndpoints),
		config.WithP2PNetworkID(r.Peer2PeerNetworkID),
		config.WithLoadToMemory(r.LoadToMemory),
		config.WithMachineTag(r.MachineTag),
		config.WithAPIAddress(r.Address),
		config.WithMITMListen(r.MITMListen),
		config.WithMITMCADir(r.MITMCADir),
		config.WithPIIDefaultDetectors(r.PIIDefaultDetectors),
		config.WithAgentJobRetentionDays(r.AgentJobRetentionDays),
		config.WithLlamaCPPTunnelCallback(func(tunnels []string) {
			tunnelEnvVar := strings.Join(tunnels, ",")
			os.Setenv("LLAMACPP_GRPC_SERVERS", tunnelEnvVar)
			xlog.Debug("setting LLAMACPP_GRPC_SERVERS", "value", tunnelEnvVar)
		}),
		config.WithMLXTunnelCallback(func(tunnels []string) {
			hostfile := filepath.Join(os.TempDir(), "localai_mlx_hostfile.json")
			data, _ := json.Marshal(tunnels)
			os.WriteFile(hostfile, data, 0644)
			os.Setenv("MLX_DISTRIBUTED_HOSTFILE", hostfile)
			xlog.Debug("setting MLX_DISTRIBUTED_HOSTFILE", "value", hostfile, "tunnels", tunnels)
		}),
	}

	// Distributed mode
	if r.Distributed {
		opts = append(opts, config.EnableDistributed)
	}
	if r.InstanceID != "" {
		opts = append(opts, config.WithDistributedInstanceID(r.InstanceID))
	}
	if r.NatsURL != "" {
		opts = append(opts, config.WithNatsURL(r.NatsURL))
	}
	if r.StorageURL != "" {
		opts = append(opts, config.WithStorageURL(r.StorageURL))
	}
	if r.StorageBucket != "" {
		opts = append(opts, config.WithStorageBucket(r.StorageBucket))
	}
	if r.StorageRegion != "" {
		opts = append(opts, config.WithStorageRegion(r.StorageRegion))
	}
	if r.StorageAccessKey != "" {
		opts = append(opts, config.WithStorageAccessKey(r.StorageAccessKey))
	}
	if r.StorageSecretKey != "" {
		opts = append(opts, config.WithStorageSecretKey(r.StorageSecretKey))
	}
	if r.BackendInstallTimeout != "" {
		d, err := time.ParseDuration(r.BackendInstallTimeout)
		if err != nil {
			return fmt.Errorf("invalid LOCALAI_NATS_BACKEND_INSTALL_TIMEOUT %q: %w", r.BackendInstallTimeout, err)
		}
		opts = append(opts, config.WithBackendInstallTimeout(d))
	}
	if r.BackendUpgradeTimeout != "" {
		d, err := time.ParseDuration(r.BackendUpgradeTimeout)
		if err != nil {
			return fmt.Errorf("invalid LOCALAI_NATS_BACKEND_UPGRADE_TIMEOUT %q: %w", r.BackendUpgradeTimeout, err)
		}
		opts = append(opts, config.WithBackendUpgradeTimeout(d))
	}
	if r.RegistrationToken != "" {
		opts = append(opts, config.WithRegistrationToken(r.RegistrationToken))
	}
	if r.RegistrationRequireAuth {
		opts = append(opts, config.EnableRegistrationRequireAuth)
	}
	if r.DistributedRequireAuth {
		opts = append(opts, config.EnableDistributedRequireAuth)
	}
	if r.DistributedSharedModels {
		opts = append(opts, config.EnableDistributedSharedModels)
	}
	if r.NatsAccountSeed != "" {
		opts = append(opts, config.WithNatsAccountSeed(r.NatsAccountSeed))
	}
	if r.NatsServiceJWT != "" {
		opts = append(opts, config.WithNatsServiceJWT(r.NatsServiceJWT))
	}
	if r.NatsServiceSeed != "" {
		opts = append(opts, config.WithNatsServiceSeed(r.NatsServiceSeed))
	}
	if r.NatsWorkerJWTTTL != "" {
		d, err := time.ParseDuration(r.NatsWorkerJWTTTL)
		if err != nil {
			return fmt.Errorf("invalid LOCALAI_NATS_WORKER_JWT_TTL %q: %w", r.NatsWorkerJWTTTL, err)
		}
		opts = append(opts, config.WithNatsWorkerJWTTTL(d))
	}
	if r.NatsRequireAuth {
		opts = append(opts, config.EnableNatsRequireAuth)
	}
	if r.NatsTLSCA != "" {
		opts = append(opts, config.WithNatsTLSCA(r.NatsTLSCA))
	}
	if r.NatsTLSCert != "" {
		opts = append(opts, config.WithNatsTLSCert(r.NatsTLSCert))
	}
	if r.NatsTLSKey != "" {
		opts = append(opts, config.WithNatsTLSKey(r.NatsTLSKey))
	}
	if r.AutoApproveNodes {
		opts = append(opts, config.EnableAutoApproveNodes)
	}
	if !r.DistributedPrefixCache {
		opts = append(opts, config.DisablePrefixCache)
	}
	if r.DistributedPrefixCacheTTL != "" {
		d, err := time.ParseDuration(r.DistributedPrefixCacheTTL)
		if err != nil {
			return fmt.Errorf("invalid LOCALAI_DISTRIBUTED_PREFIX_CACHE_TTL %q: %w", r.DistributedPrefixCacheTTL, err)
		}
		opts = append(opts, config.WithPrefixCacheTTL(d))
	}
	if r.ExposeNodeHeader {
		opts = append(opts, config.WithExposeNodeHeader(true))
	}
	if r.ModelScheduling != "" {
		opts = append(opts, config.WithModelSchedulingJSON(r.ModelScheduling))
	}
	if r.ModelSchedulingConfig != "" {
		opts = append(opts, config.WithModelSchedulingConfigPath(r.ModelSchedulingConfig))
	}
	if !r.Distributed && (r.ModelScheduling != "" || r.ModelSchedulingConfig != "") {
		xlog.Warn("LOCALAI_MODEL_SCHEDULING / LOCALAI_MODEL_SCHEDULING_CONFIG is set but distributed mode is disabled (LOCALAI_DISTRIBUTED=false) - ignoring")
	}

	if r.DisableMetricsEndpoint {
		opts = append(opts, config.DisableMetricsEndpoint)
	}

	if r.DisableRuntimeSettings {
		opts = append(opts, config.DisableRuntimeSettings)
	}

	if r.EnableTracing {
		opts = append(opts, config.EnableTracing)
	}
	opts = append(opts, config.WithTracingMaxItems(r.TracingMaxItems))
	opts = append(opts, config.WithTracingMaxBodyBytes(r.TracingMaxBodyBytes))

	token := ""
	if r.Peer2Peer || r.Peer2PeerToken != "" {
		xlog.Info("P2P mode enabled")
		token = r.Peer2PeerToken
		if token == "" {
			// IF no token is provided, and p2p is enabled,
			// we generate one and wait for the user to pick up the token (this is for interactive)
			xlog.Info("No token provided, generating one")
			token = p2p.GenerateToken(r.Peer2PeerDHTInterval, r.Peer2PeerOTPInterval)
			xlog.Info("Generated Token:")
			fmt.Println(token)

			xlog.Info("To use the token, you can run the following command in another node or terminal:")
			fmt.Printf("export TOKEN=\"%s\"\nlocal-ai worker p2p-llama-cpp-rpc\n", token)
		}
		opts = append(opts, config.WithP2PToken(token))
	}

	if r.Federated {
		opts = append(opts, config.EnableFederated)
	}

	idleWatchDog := r.EnableWatchdogIdle
	busyWatchDog := r.EnableWatchdogBusy

	if r.DisableWebUI {
		opts = append(opts, config.DisableWebUI)
	}

	if r.OllamaAPIRootEndpoint {
		opts = append(opts, config.EnableOllamaAPIRootEndpoint)
	}

	if r.DisableGalleryEndpoint {
		opts = append(opts, config.DisableGalleryEndpoint)
	}

	if r.DisableMCP {
		opts = append(opts, config.DisableMCP)
	}

	// Agent Pool
	if r.DisableAgents {
		opts = append(opts, config.DisableAgentPool)
	}
	if r.AgentPoolAPIURL != "" {
		opts = append(opts, config.WithAgentPoolAPIURL(r.AgentPoolAPIURL))
	}
	if r.AgentPoolAPIKey != "" {
		opts = append(opts, config.WithAgentPoolAPIKey(r.AgentPoolAPIKey))
	}
	if r.AgentPoolDefaultModel != "" {
		opts = append(opts, config.WithAgentPoolDefaultModel(r.AgentPoolDefaultModel))
	}
	if r.DisableLocalAIAssistant {
		opts = append(opts, config.WithDisableLocalAIAssistant(true))
	}
	if r.AgentPoolMultimodalModel != "" {
		opts = append(opts, config.WithAgentPoolMultimodalModel(r.AgentPoolMultimodalModel))
	}
	if r.AgentPoolTranscriptionModel != "" {
		opts = append(opts, config.WithAgentPoolTranscriptionModel(r.AgentPoolTranscriptionModel))
	}
	if r.AgentPoolTranscriptionLanguage != "" {
		opts = append(opts, config.WithAgentPoolTranscriptionLanguage(r.AgentPoolTranscriptionLanguage))
	}
	if r.AgentPoolTTSModel != "" {
		opts = append(opts, config.WithAgentPoolTTSModel(r.AgentPoolTTSModel))
	}
	if r.AgentPoolStateDir != "" {
		opts = append(opts, config.WithAgentPoolStateDir(r.AgentPoolStateDir))
	}
	if r.AgentPoolTimeout != "" {
		opts = append(opts, config.WithAgentPoolTimeout(r.AgentPoolTimeout))
	}
	if r.AgentPoolEnableSkills {
		opts = append(opts, config.EnableAgentPoolSkills)
	}
	if r.AgentPoolVectorEngine != "" {
		opts = append(opts, config.WithAgentPoolVectorEngine(r.AgentPoolVectorEngine))
	}
	if r.AgentPoolEmbeddingModel != "" {
		opts = append(opts, config.WithAgentPoolEmbeddingModel(r.AgentPoolEmbeddingModel))
	}
	if r.AgentPoolCustomActionsDir != "" {
		opts = append(opts, config.WithAgentPoolCustomActionsDir(r.AgentPoolCustomActionsDir))
	}
	if r.AgentPoolDatabaseURL != "" {
		opts = append(opts, config.WithAgentPoolDatabaseURL(r.AgentPoolDatabaseURL))
	}
	if r.AgentPoolMaxChunkingSize > 0 {
		opts = append(opts, config.WithAgentPoolMaxChunkingSize(r.AgentPoolMaxChunkingSize))
	}
	if r.AgentPoolChunkOverlap > 0 {
		opts = append(opts, config.WithAgentPoolChunkOverlap(r.AgentPoolChunkOverlap))
	}
	if r.AgentPoolEnableLogs {
		opts = append(opts, config.EnableAgentPoolLogs)
	}
	if r.AgentPoolCollectionDBPath != "" {
		opts = append(opts, config.WithAgentPoolCollectionDBPath(r.AgentPoolCollectionDBPath))
	}
	if r.AgentHubURL != "" {
		opts = append(opts, config.WithAgentHubURL(r.AgentHubURL))
	}

	// Authentication
	authEnabled := r.AuthEnabled || r.GitHubClientID != "" || r.OIDCClientID != ""
	if authEnabled {
		opts = append(opts, config.WithAuthEnabled(true))

		dbURL := r.AuthDatabaseURL
		if dbURL == "" {
			dbURL = filepath.Join(r.DataPath, "database.db")
		}
		opts = append(opts, config.WithAuthDatabaseURL(dbURL))

		if r.GitHubClientID != "" {
			opts = append(opts, config.WithAuthGitHubClientID(r.GitHubClientID))
			opts = append(opts, config.WithAuthGitHubClientSecret(r.GitHubClientSecret))
		}
		if r.OIDCClientID != "" {
			opts = append(opts, config.WithAuthOIDCIssuer(r.OIDCIssuer))
			opts = append(opts, config.WithAuthOIDCClientID(r.OIDCClientID))
			opts = append(opts, config.WithAuthOIDCClientSecret(r.OIDCClientSecret))
		}
		if r.AuthAdminEmail != "" {
			opts = append(opts, config.WithAuthAdminEmail(r.AuthAdminEmail))
		}
		if r.AuthRegistrationMode != "" {
			opts = append(opts, config.WithAuthRegistrationMode(r.AuthRegistrationMode))
		}
		if r.DisableLocalAuth {
			opts = append(opts, config.WithAuthDisableLocalAuth(true))
		}
		if r.AuthAPIKeyHMACSecret != "" {
			opts = append(opts, config.WithAuthAPIKeyHMACSecret(r.AuthAPIKeyHMACSecret))
		}
		if r.DefaultAPIKeyExpiry != "" {
			opts = append(opts, config.WithAuthDefaultAPIKeyExpiry(r.DefaultAPIKeyExpiry))
		}
	}

	// Applied unconditionally: the external base URL governs all self-referential
	// links (not just OAuth callbacks), so it must take effect even when auth is off.
	if r.ExternalBaseURL != "" {
		opts = append(opts, config.WithExternalBaseURL(r.ExternalBaseURL))
	}

	if idleWatchDog || busyWatchDog {
		opts = append(opts, config.EnableWatchDog)
		if idleWatchDog {
			opts = append(opts, config.EnableWatchDogIdleCheck)
			dur, err := time.ParseDuration(r.WatchdogIdleTimeout)
			if err != nil {
				return err
			}
			opts = append(opts, config.SetWatchDogIdleTimeout(dur))
		}
		if busyWatchDog {
			opts = append(opts, config.EnableWatchDogBusyCheck)
			dur, err := time.ParseDuration(r.WatchdogBusyTimeout)
			if err != nil {
				return err
			}
			opts = append(opts, config.SetWatchDogBusyTimeout(dur))
		}
		if r.WatchdogInterval != "" {
			dur, err := time.ParseDuration(r.WatchdogInterval)
			if err != nil {
				return err
			}
			opts = append(opts, config.SetWatchDogInterval(dur))
		}
	}

	// Memory reclaimer (GPU VRAM if available, otherwise RAM). Injected
	// unconditionally so the kong threshold default always reaches the
	// config: DefaultRuntimeBaseline models an option-less boot with
	// threshold 0.95, and the settings loader treats a live value equal
	// to the baseline as "not env-set" (file may apply). Gating this on
	// EnableMemoryReclaimer left the threshold at 0 and made a UI-saved
	// threshold look env-set at boot.
	opts = append(opts, config.WithMemoryReclaimer(r.EnableMemoryReclaimer, r.MemoryReclaimerThreshold))

	// Handle max active backends (LRU eviction)
	// MaxActiveBackends takes precedence over SingleActiveBackend
	if r.MaxActiveBackends > 0 {
		opts = append(opts, config.SetMaxActiveBackends(r.MaxActiveBackends))
	} else if r.SingleActiveBackend {
		// Backward compatibility: --single-active-backend is equivalent to --max-active-backends=1
		opts = append(opts, config.EnableSingleBackend)
	}

	// Handle LRU eviction settings
	if r.ForceEvictionWhenBusy {
		opts = append(opts, config.WithForceEvictionWhenBusy(true))
	}
	if r.SizeAwareEviction {
		opts = append(opts, config.WithSizeAwareEviction(true))
	}
	if r.LRUEvictionMaxRetries > 0 {
		opts = append(opts, config.WithLRUEvictionMaxRetries(r.LRUEvictionMaxRetries))
	}
	if r.LRUEvictionRetryInterval != "" {
		dur, err := time.ParseDuration(r.LRUEvictionRetryInterval)
		if err != nil {
			return fmt.Errorf("invalid LRU eviction retry interval: %w", err)
		}
		opts = append(opts, config.WithLRUEvictionRetryInterval(dur))
	}
	if r.ModelLoadFailureCooldown != "" {
		dur, err := time.ParseDuration(r.ModelLoadFailureCooldown)
		if err != nil {
			return fmt.Errorf("invalid model load failure cooldown: %w", err)
		}
		opts = append(opts, config.WithModelLoadFailureCooldown(dur))
	}

	// Handle Open Responses store TTL
	if r.OpenResponsesStoreTTL != "" && r.OpenResponsesStoreTTL != "0" {
		dur, err := time.ParseDuration(r.OpenResponsesStoreTTL)
		if err != nil {
			return fmt.Errorf("invalid Open Responses store TTL: %w", err)
		}
		opts = append(opts, config.WithOpenResponsesStoreTTL(dur))
	}

	// split ":" to get backend name and the uri
	for _, v := range r.ExternalGRPCBackends {
		backend := v[:strings.IndexByte(v, ':')]
		uri := v[strings.IndexByte(v, ':')+1:]
		opts = append(opts, config.WithExternalBackend(backend, uri))
	}

	if r.AutoloadGalleries {
		opts = append(opts, config.EnableGalleriesAutoload)
	}

	if r.AutoloadBackendGalleries {
		opts = append(opts, config.EnableBackendGalleriesAutoload)
	}

	if r.AutoUpgradeBackends {
		opts = append(opts, config.WithAutoUpgradeBackends(r.AutoUpgradeBackends))
	}

	if r.RequireBackendIntegrity {
		opts = append(opts, config.WithRequireBackendIntegrity(r.RequireBackendIntegrity))
	}

	if r.PreferDevelopmentBackends {
		opts = append(opts, config.WithPreferDevelopmentBackends(r.PreferDevelopmentBackends))
	}

	// Per-node VRAM allocation budget. Record it on the ApplicationConfig and,
	// fail-open, install it as the process-global xsysinfo default so allocation
	// heuristics cap at it. A malformed value must never block startup: it is
	// logged and treated as unset (full detected VRAM).
	if r.VRAMBudget != "" {
		opts = append(opts, config.SetVRAMBudget(r.VRAMBudget))
		if b, err := vrambudget.Parse(r.VRAMBudget); err != nil {
			xlog.Warn("Ignoring invalid LOCALAI_VRAM_BUDGET", "value", r.VRAMBudget, "error", err)
		} else {
			xsysinfo.SetDefaultVRAMBudget(b)
			xlog.Info("VRAM allocation budget set", "budget", b.String())
		}
	}

	if r.PreloadBackendOnly {
		_, err := application.New(opts...)
		return err
	}

	app, err := application.New(opts...)
	if err != nil {
		return fmt.Errorf("LocalAI failed to start: %w.\nTroubleshooting steps:\n  1. Check that your models directory exists and is accessible: %s\n  2. Verify model config files are valid YAML: 'local-ai util usecase-heuristic <config>'\n  3. Check available disk space and file permissions\n  4. Run with --log-level=debug for more details\nSee https://localai.io/basics/troubleshooting/ for more help", err, r.ModelsPath)
	}

	// Refuse to bind a public-internet address without authentication unless
	// the operator has explicitly opted in. The auth middleware degrades to
	// pass-through when there is no auth DB and no legacy keys; on a loopback,
	// LAN, or VPN that's the historical "trusted network" deployment, but on
	// a public IP it makes every model, gallery install, settings change, and
	// admin endpoint reachable by anyone who can connect to the port.
	authConfigured := app.AuthDB() != nil || len(r.APIKeys) > 0
	if err := requireAuthOrTrustedBind(r.Address, authConfigured, r.AllowInsecurePublicBind); err != nil {
		return err
	}

	appHTTP, err := http.API(app)
	if err != nil {
		xlog.Error("error during HTTP App construction", "error", err)
		return err
	}

	xlog.Info("LocalAI is started and running", "address", r.Address)

	// Start P2P if token was provided via CLI/env or loaded from runtime_settings.json
	if token != "" || app.ApplicationConfig().P2PToken != "" {
		if err := app.StartP2P(); err != nil {
			return err
		}
	}

	signals.RegisterGracefulTerminationHandler(func() {
		if err := app.Shutdown(); err != nil {
			xlog.Error("error while shutting down application", "error", err)
		}
	})

	// Start the agent pool after the HTTP server is listening, because
	// backends like PostgreSQL need to call the embeddings API during
	// collection initialization.
	go func() {
		waitForServerReady(r.Address, app.ApplicationConfig().Context)
		app.StartAgentPool()
	}()

	return appHTTP.Start(r.Address)
}

// waitForServerReady polls the given address until the HTTP server is
// accepting connections or the context is cancelled.
func waitForServerReady(address string, ctx context.Context) {
	host, port, err := net.SplitHostPort(address)
	if err == nil && host == "" {
		address = "127.0.0.1:" + port
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		conn, err := net.DialTimeout("tcp", address, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
