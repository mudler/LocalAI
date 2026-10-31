package config

import (
	"context"
	"encoding/json"
	"regexp"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/diagnostics"
	"github.com/mudler/LocalAI/pkg/modelartifacts"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/LocalAI/pkg/vrambudget"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/mudler/xlog"
)

// AddonConfig is startup-only deployment configuration. Authority is injected
// by the embedding host and cannot be loaded from runtime settings.
type AddonConfig struct {
	Transport     *gateway.CarrierConfig `json:"-" yaml:"-"`
	ListenAddress string

	// Frontend is set for the frontend role, also when add-ons are off. A
	// frontend keeps no owner state, so it never opens the library store.
	Frontend bool

	Enabled                                            bool
	InstanceID, OwnerID, OwnerAddress, StorageID, Root string
	SharedStorage                                      bool
	// OwnershipMode is "named-user" (the default when empty) or "local-owner".
	// Local-owner mode is never inferred from a request. The CLI sets it as the
	// documented default when authentication is off and the operator chose no
	// mode. Every caller that reaches the addon routes shares one authority and
	// sharing is disabled.
	// Every frontend of a deployment must use the same value.
	OwnershipMode string
	// OfficialLibrary registers the official add-on library on the first start,
	// when the owner has no sources.json yet. A sources.json that exists is
	// never changed by it, so a library the admin removed stays removed.
	OfficialLibrary bool
	// OfficialBackendLibrary also registers the backend library on the first
	// start, under the same rule: a sources.json that exists is never changed,
	// so a library the admin removed stays removed. It is on by default, but
	// whether it is enabled depends on the build (core/library.BackendSource):
	// a build that reads the backend gallery lists the library and leaves it
	// off.
	OfficialBackendLibrary bool
	// BackendMaxBytes bounds the compressed size of one backend image
	// installed from a library; zero is 8 GiB. Add-ons keep their own limit.
	BackendMaxBytes int64
	Host            contract.HostCompatibility
	AdminCheck      func(context.Context, contract.Actor) error `json:"-" yaml:"-"`
	// BackupKeep is how many backups of one add-on the owner keeps; zero is
	// three. BackupMaxAge removes older ones; zero keeps them by count only.
	// The newest verified backup and the recovery copy of an upgrade are
	// never removed by either.
	BackupKeep   int
	BackupMaxAge time.Duration
	// UpdateCacheTTL is how long an answer of the update check, and the
	// library data under it, counts as fresh; zero is 15 minutes.
	// UpdateRefreshInterval is the least time between two explicit refreshes
	// of one add-on and of one library; zero is 15 seconds.
	UpdateCacheTTL        time.Duration
	UpdateRefreshInterval time.Duration
	// ArtifactExpiry is how long a private file result stays downloadable;
	// zero is the contract default of one hour. It is a programmatic option
	// that no flag sets: the acceptance fixture shortens it to test expiry.
	ArtifactExpiry time.Duration
}

// IsFrontend says whether this process is an add-on frontend: the role was set,
// or the instance is not the owner it forwards to.
func (c AddonConfig) IsFrontend() bool {
	return c.Frontend || (c.Enabled && c.OwnerID != "" && c.InstanceID != c.OwnerID)
}

func WithAddons(c AddonConfig) AppOption {
	return func(o *ApplicationConfig) { o.Addons = c }
}

type ApplicationConfig struct {
	// OfficialLibraries is the switch for the shipped default library sources
	// of every kind. On by default; off keeps them listed but unread.
	OfficialLibraries bool

	Addons AddonConfig `json:"-" yaml:"-"`

	// AddonHostOps maps an add-on ID to the host operation prefixes it may
	// call. It is startup-only. Nil or empty means no add-on gets a host
	// operation endpoint.
	AddonHostOps map[string][]string `json:"-" yaml:"-"`

	// DiagnosticsRecorder is startup-only and local to this frontend process.
	DiagnosticsRecorder *diagnostics.Recorder `json:"-" yaml:"-"`

	// ProxyAPIKeyEnvLookup resolves upstream credentials at the CLI boundary.
	ProxyAPIKeyEnvLookup func(string) string `json:"-" yaml:"-"`

	Context          context.Context
	ConfigFile       string
	SystemState      *system.SystemState
	ExternalBackends []string

	// ModelArtifactMaterializer is a controller-only acquisition capability.
	// It is excluded from serialization so credentials captured by its concrete
	// implementation cannot enter persisted settings or distributed payloads.
	ModelArtifactMaterializer ArtifactMaterializer `json:"-" yaml:"-"`
	ModelPreloadRenderMode    string               `json:"-" yaml:"-"`
	DisableModelPreloadColor  bool                 `json:"-" yaml:"-"`

	// WebRTCNAT1To1IPs, when set, are advertised as the host ICE candidates for
	// /v1/realtime WebRTC instead of every local interface address. Needed when
	// the routable address differs from what pion gathers — e.g. Docker host
	// networking (where pion also offers unreachable bridge IPs) or NAT.
	WebRTCNAT1To1IPs []string
	// WebRTCICEInterfaces, when set, restricts ICE candidate gathering to these
	// network interfaces (e.g. eth0), filtering out docker0/veth noise.
	WebRTCICEInterfaces []string
	// WebRTCUDPPort, when positive, is the shared UDP port used by all WebRTC
	// peer connections. Zero keeps pion's default ephemeral-port behavior.
	WebRTCUDPPort                       int
	UploadLimitMB, Threads, ContextSize int
	ArtifactDownloadConcurrency         int

	// MaxConcurrentBackendOperations bounds how many backend install, upgrade
	// and delete operations run at once. Always >= 1.
	MaxConcurrentBackendOperations int
	// liveMaxBackendOps mirrors MaxConcurrentBackendOperations for readers on
	// other goroutines, since a runtime settings change can land at any time.
	liveMaxBackendOps atomic.Int64

	F16                          bool
	Debug                        bool
	EnableTracing                bool
	MaxConcurrentBackendRequests int
	TracingMaxItems              int
	TracingMaxBodyBytes          int // Per-body cap for captured request/response bodies; 0 disables the cap
	EnableBackendLogging         bool
	GeneratedContentDir          string

	UploadDir string
	DataPath  string // Persistent data directory for collectiondb, agents, etc.

	DynamicConfigsDir             string
	DynamicConfigsDirPollInterval time.Duration
	CORS                          bool
	DisableCSRF                   bool
	// DisableHTTPCompression turns off the gzip response middleware. Gzip is
	// on by default because the React UI bundle and the admin JSON APIs are
	// text-heavy; turn it off only when a fronting proxy already compresses.
	DisableHTTPCompression bool
	// HTTPCompressionMinLength is the response-size floor (bytes) below which
	// gzip is skipped. 0 keeps middleware.DefaultCompressionMinLength.
	HTTPCompressionMinLength int
	PreloadJSONModels        string
	PreloadModelsFromPath    string
	CORSAllowOrigins         string
	ApiKeys                  []string
	P2PToken                 string
	P2PNetworkID             string
	Federated                bool

	// ExternalBaseURL is the externally visible base URL of this instance
	// (scheme+host[:port]), set via LOCALAI_BASE_URL. When non-empty it is
	// authoritative for every self-referential URL LocalAI emits (OAuth
	// callbacks, generated image/video links, async job StatusURLs),
	// overriding proxy-header detection. Empty = derive from request headers.
	ExternalBaseURL string

	// DisableStats turns off per-request token tracking. By default the
	// routing module's billing recorder runs in every mode (including
	// no-auth single-user) so dashboards and `/api/usage` are immediately
	// useful; set this to opt out of that, e.g., for ephemeral CI runs
	// or privacy-strict deployments where no token-count history should
	// touch disk or memory.
	DisableStats bool

	// RouterDecisionLog retains the bounded in-memory routing log even when
	// billing stats are disabled. It does not enable token usage recording.
	RouterDecisionLog bool

	// MITMListen is the address (host:port) the cloudproxy MITM
	// listener binds on. Empty disables the MITM proxy entirely.
	// Use case: redacting PII from Claude Code / Codex CLI traffic
	// without LocalAI holding the upstream API key. Clients set
	// HTTPS_PROXY=http://localai:port and trust the CA cert
	// LocalAI exposes at /api/middleware/proxy-ca.crt.
	MITMListen string

	// PIIDefaultDetectors lists token-classification (NER) detector model
	// names applied to any PII-enabled model that does not name its own
	// pii.detectors. This makes cloud-proxy / MITM redaction work out of the
	// box (those default to PII-enabled but carry no detector list) and lets
	// an operator set one detector for the whole instance. Set at runtime via
	// POST /api/settings; read live by Application.ResolvePIIPolicy.
	PIIDefaultDetectors []string

	// MITMCADir holds the persisted MITM proxy CA cert and private
	// key. The CA is generated on first start; subsequent starts
	// reload it so clients keep trusting the same root. The key
	// file is mode 0600.
	MITMCADir string

	DisableWebUI                       bool
	OllamaAPIRootEndpoint              bool
	EnforcePredownloadScans            bool
	OpaqueErrors                       bool
	UseSubtleKeyComparison             bool
	DisableApiKeyRequirementForHttpGet bool
	DisableMetrics                     bool
	HttpGetExemptedEndpoints           []*regexp.Regexp
	DisableGalleryEndpoint             bool
	DisableMCP                         bool
	LoadToMemory                       []string

	Galleries        []Gallery
	BackendGalleries []Gallery

	ExternalGRPCBackends map[string]string

	AutoloadGalleries, AutoloadBackendGalleries bool
	VRAMPersistentCache                         bool
	AutoUpgradeBackends                         bool
	AutoUpgradeAddons                           bool
	PreferDevelopmentBackends                   bool

	// RequireBackendIntegrity promotes a missing SHA256 (tarball/HTTP URIs)
	// or missing verification policy (OCI URIs) from a warning to a hard
	// failure during backend install/upgrade. Off by default to keep
	// upgrades non-breaking; operators opt in explicitly via
	// --require-backend-integrity / LOCALAI_REQUIRE_BACKEND_INTEGRITY.
	RequireBackendIntegrity bool

	// DisableBackendInstall mirrors SystemState.DisableBackendInstall for the
	// HTTP layer: admission guards and the capability surfaces read it here.
	DisableBackendInstall bool

	SingleBackend     bool // Deprecated: use MaxActiveBackends = 1 instead
	MaxActiveBackends int  // Maximum number of active backends (0 = unlimited, 1 = single backend mode)
	WatchDogIdle      bool
	WatchDogBusy      bool
	WatchDog          bool

	// Memory Reclaimer settings (works with GPU if available, otherwise RAM)
	MemoryReclaimerEnabled   bool    // Enable memory threshold monitoring
	MemoryReclaimerThreshold float64 // Threshold 0.0-1.0 (e.g., 0.95 = 95%)

	// VRAMBudget optionally caps how much VRAM this instance uses for model
	// allocation, as "80%" or "12GB". Empty = use full detected VRAM.
	VRAMBudget string

	// Eviction settings
	ForceEvictionWhenBusy    bool          // Force eviction even when models have active API calls (default: false for safety)
	SizeAwareEviction        bool          // Evict largest models first rather than least-recently-used (default: false)
	LRUEvictionMaxRetries    int           // Maximum number of retries when waiting for busy models to become idle (default: 30)
	LRUEvictionRetryInterval time.Duration // Interval between retries when waiting for busy models (default: 1s)

	// ModelLoadFailureCooldown is the base cooldown applied after a model load
	// fails: new load attempts for that model are refused (HTTP 503 + Retry-After)
	// until it elapses, doubling per consecutive failure up to a 5m cap and reset
	// on success. Prevents a client polling a broken model from respawning a
	// crashing backend on every request. 0 disables it. Default: 10s.
	ModelLoadFailureCooldown time.Duration

	ModelsURL []string

	WatchDogBusyTimeout, WatchDogIdleTimeout time.Duration
	WatchDogInterval                         time.Duration // Interval between watchdog checks

	MachineTag string

	APIAddress string

	LlamaCPPTunnelCallback func(tunnels []string)
	MLXTunnelCallback      func(tunnels []string)

	DisableRuntimeSettings bool

	OpenResponsesStoreTTL time.Duration // TTL for Open Responses store (0 = no expiration)

	PathWithoutAuth []string

	// Collections (the knowledge base)
	Collections CollectionsConfig

	// ChatsVisionModel and ChatsTranscriptionModel are the models the chats'
	// media tools use to describe images and transcribe audio. Empty: the first
	// installed model with that capability.
	ChatsVisionModel        string
	ChatsTranscriptionModel string

	// Authentication & Authorization
	Auth AuthConfig

	// Distributed / Horizontal Scaling
	Distributed DistributedConfig

	// ExposeNodeHeader, when true, activates middleware.ExposeNodeHeader on
	// the inference routes (OpenAI chat/completions/embeddings, Anthropic
	// /v1/messages, Ollama /api/chat,/api/generate,/api/embed). The
	// middleware wraps the response writer and attaches an "X-LocalAI-Node"
	// response header carrying the ID of the distributed-mode worker node
	// that served the request. Off by default because the node ID is
	// internal topology that can aid attacker reconnaissance if surfaced on
	// a public endpoint; operators opt in explicitly via
	// --expose-node-header / LOCALAI_EXPOSE_NODE_HEADER for debugging,
	// observability and load-balancer attribution.
	ExposeNodeHeader bool

	// LocalAI Assistant chat modality. Hard-disable the in-process admin MCP
	// server with this flag; runtime-toggleable via /api/settings.
	DisableLocalAIAssistant bool

	// DisableSharedPages turns off the pages of shared folders: they are not
	// listed as runnable, do not open and their bridge refuses every call.
	// Runtime-toggleable via /api/settings (shared_pages_enabled).
	DisableSharedPages bool

	// ChatsHostExec is who gets the chats' built-in bash, which runs commands
	// on this machine with no isolation: off, ask (administrators, with a card
	// for each command) or on. Empty: off with accounts, ask without.
	// Runtime-toggleable via /api/settings (chats_host_exec).
	ChatsHostExec string
	// ChatsToolProviders pins, for a built-in chat tool or skill that several
	// add-ons replace, the add-on that serves it (tool or skill name -> add-on
	// ID). Tool and skill names never overlap.
	ChatsToolProviders map[string]string

	// DisableChats turns off the whole chats surface: the /api/chats API, saved
	// agents, triggers, connectors and the agent tasks and jobs. Nothing starts
	// (no loop, bot or trigger runner).
	DisableChats bool
	// DisableTriggers and DisableConnectors turn off one part of the chats:
	// the trigger runner, or the connector bots. The API for the part answers
	// 404.
	DisableTriggers   bool
	DisableConnectors bool
	// AgentJobRetentionDays is how long finished agent jobs are kept. Zero
	// means DefaultAgentJobRetentionDays.
	AgentJobRetentionDays int
	// MCPCIJobTimeout is the longest an agent job may run. Zero means
	// DefaultMCPCIJobTimeout.
	MCPCIJobTimeout time.Duration
	// TriggerRunKeep is the number of finished runs kept for each trigger
	// (beyond the last two days). Zero means triggers.DefaultKeepRuns, a negative
	// value keeps every run.
	TriggerRunKeep int
	// TriggerRunRetentionDays is how long finished trigger runs are kept. Zero
	// means 30 days (triggers.DefaultRunMaxAge), a negative value keeps them forever.
	TriggerRunRetentionDays int

	// MCPConnectorsAllowPrivate lets the MCP connectors that users define reach
	// addresses on private networks (loopback, link-local, RFC 1918, metadata).
	// Off by default: such an address lets a user probe the internal network
	// through the server. Runtime-toggleable via /api/settings.
	MCPConnectorsAllowPrivate bool

	// The built-in web tools of the chats. Each is runtime-toggleable via
	// /api/settings and read at each turn. DisableChatsWebSearch and
	// DisableChatsWebFetch are stored as the negation (chats_web_search,
	// chats_web_fetch), so both tools are on by default. ChatsWebSearXNGURL
	// sends the searches to that SearXNG instance instead of DuckDuckGo.
	// ChatsWebAllowPrivate lets web_fetch reach addresses on private networks
	// (never the cloud metadata addresses). ChatsWebPorts is a comma list of
	// the ports web_fetch may use; empty means 80 and 443.
	DisableChatsWebSearch bool
	DisableChatsWebFetch  bool
	ChatsWebSearXNGURL    string
	ChatsWebAllowPrivate  bool
	ChatsWebPorts         string

	// The network of the commands that chats run in the Sandbox, through the
	// host proxy. Each is runtime-toggleable via /api/settings and read at
	// each check, so no restart. ChatsEgressCeiling is the widest preset
	// (off, registries, all) any chat may use; ChatsEgressDefault the preset
	// of a new chat, capped by the ceiling. ChatsEgressAllowPrivate lets an
	// administrator's rule or card answer reach private addresses (never the
	// metadata addresses). ChatsEgressPorts is a comma list of the ports a
	// command may reach (empty: 80 and 443), ChatsEgressExtraRegistries a
	// comma list of host names added to the package registries, and
	// ChatsEgressAllowedHosts exact hosts (host or host:port, comma separated)
	// that any chat with the network on may always reach;
	// ChatsEgressAuditDays how long the audit keeps a line (0: 30 days).
	ChatsEgressCeiling         string
	ChatsEgressDefault         string
	ChatsEgressAllowPrivate    bool
	ChatsEgressPorts           string
	ChatsEgressExtraRegistries string
	ChatsEgressAllowedHosts    string
	ChatsEgressAuditDays       int

	// DisableChatsSavedAgents switches off, for every chat, the saved agents
	// as sub-agents (list_agents and the agent argument of spawn_agent).
	// Stored as the negation (chats_saved_agents), so it is on by default;
	// each chat still has to switch it on. Read at each turn.
	DisableChatsSavedAgents bool

	// DisableChatSuggestions stops the assistant from offering things (to
	// remember a name, to make a shortcut, to update a widget on a schedule)
	// to users who did not choose for themselves. Runtime-toggleable via
	// /api/settings as chats_suggestions (stored as the negation).
	DisableChatSuggestions bool

	// LegacyNavigation shows the previous sidebar. See WithLegacyNavigation.
	LegacyNavigation bool

	// Branding / whitelabeling — runtime-mutable via /api/settings (text) and
	// /api/branding/asset/:kind (binary uploads). All values optional; empty
	// strings fall back to bundled LocalAI defaults.
	Branding BrandingConfig
}

const DefaultMaxConcurrentBackendRequests = 1024

// BrandingConfig holds the whitelabel/branding configuration of the instance.
// Text fields are exposed via the public GET /api/branding endpoint so the
// login page can read them before authentication. Binary asset filenames
// (logo, horizontal logo, favicon) are stored as basenames; the actual files
// live under {DynamicConfigsDir}/branding/.
type BrandingConfig struct {
	InstanceName       string
	InstanceTagline    string
	LogoFile           string
	LogoHorizontalFile string
	FaviconFile        string
}

// AuthConfig holds configuration for user authentication and authorization.
type AuthConfig struct {
	Enabled             bool
	DatabaseURL         string // "postgres://..." or file path for SQLite
	GitHubClientID      string
	GitHubClientSecret  string
	OIDCIssuer          string // OIDC issuer URL for auto-discovery (e.g. https://accounts.google.com)
	OIDCClientID        string
	OIDCClientSecret    string
	AdminEmail          string // auto-promote to admin on login
	RegistrationMode    string // "open", "approval" (default when empty), "invite"
	DisableLocalAuth    bool   // disable local email/password registration and login
	APIKeyHMACSecret    string // HMAC secret for API key hashing; auto-generated if empty
	DefaultAPIKeyExpiry string // default expiry duration for API keys (e.g. "90d"); empty = no expiry
}

// CollectionsConfig holds the configuration of the collections service.
type CollectionsConfig struct {
	Enabled          bool   // default: true (disabled by LOCALAI_DISABLE_COLLECTIONS=true)
	StateDir         string // default: DynamicConfigsDir (LocalAI configuration folder)
	APIURL           string // default: self-referencing LocalAI (http://127.0.0.1:<port>)
	APIKey           string // default: first API key from LocalAI config
	DefaultModel     string
	CollectionDBPath string
	VectorEngine     string // default: "chromem"
	EmbeddingModel   string // default: "granite-embedding-107m-multilingual"
	MaxChunkingSize  int    // default: 400
	ChunkOverlap     int    // default: 0
	DatabaseURL      string
}

type AppOption func(*ApplicationConfig)

func WithProxyAPIKeyEnvLookup(lookup func(string) string) AppOption {
	return func(o *ApplicationConfig) { o.ProxyAPIKeyEnvLookup = lookup }
}

func NewApplicationConfig(o ...AppOption) *ApplicationConfig {
	opt := &ApplicationConfig{
		Context:                   context.Background(),
		UploadLimitMB:             15,
		Debug:                     true,
		ModelArtifactMaterializer: modelartifacts.NewDefaultManager(),
		OfficialLibraries:         true,
		ModelPreloadRenderMode:    "dark",
		// Capture backend process stdout/stderr into the per-model
		// BackendLogStore by default so the UI "Backend Logs" page works out
		// of the box in single mode, matching worker/distributed mode (which
		// force-enables it). It's a small in-memory ring buffer; the Settings
		// toggle can still turn it off (a persisted false wins - see
		// loadRuntimeSettingsFromFile).
		EnableBackendLogging:        true,
		VRAMPersistentCache:         true,
		ArtifactDownloadConcurrency: modelartifacts.DefaultDownloadConcurrency,

		MaxConcurrentBackendOperations: 1,

		LRUEvictionMaxRetries:    30,               // Default: 30 retries
		LRUEvictionRetryInterval: 1 * time.Second,  // Default: 1 second
		ModelLoadFailureCooldown: 10 * time.Second, // Default: 10s base cooldown after a failed load
		// WatchDogInterval is intentionally left at the zero value here.
		// The startup loader applies a persisted runtime_settings.json value
		// only when the interval is still 0 (its "not set by env var"
		// heuristic, matching the idle/busy timeouts); a non-zero baseline
		// default would defeat that and silently revert a UI-saved Check
		// Interval to the default on every restart (#10601). The effective
		// 500ms default is supplied at the watchdog layer (DefaultWatchdogInterval)
		// when the value is still 0.
		MaxConcurrentBackendRequests: DefaultMaxConcurrentBackendRequests,
		TracingMaxItems:              1024,
		TracingMaxBodyBytes:          64 * 1024, // 64 KiB - caps each request/response body in the trace buffer
		Collections: CollectionsConfig{
			Enabled:         true,
			VectorEngine:    "chromem",
			EmbeddingModel:  "granite-embedding-107m-multilingual",
			MaxChunkingSize: 400,
		},
		PathWithoutAuth: []string{},
	}
	for _, oo := range o {
		oo(opt)
	}
	opt.SetMaxConcurrentBackendOperations(opt.MaxConcurrentBackendOperations)
	return opt
}

func WithModelsURL(urls ...string) AppOption {
	return func(o *ApplicationConfig) {
		o.ModelsURL = urls
	}
}

func WithSystemState(state *system.SystemState) AppOption {
	return func(o *ApplicationConfig) {
		o.SystemState = state
	}
}

func WithExternalBackends(backends ...string) AppOption {
	return func(o *ApplicationConfig) {
		o.ExternalBackends = backends
	}
}

func WithWebRTCNAT1To1IPs(ips ...string) AppOption {
	return func(o *ApplicationConfig) {
		o.WebRTCNAT1To1IPs = ips
	}
}

func WithWebRTCICEInterfaces(interfaces ...string) AppOption {
	return func(o *ApplicationConfig) {
		o.WebRTCICEInterfaces = interfaces
	}
}

func WithWebRTCUDPPort(port int) AppOption {
	return func(o *ApplicationConfig) {
		o.WebRTCUDPPort = port
	}
}

func WithMachineTag(tag string) AppOption {
	return func(o *ApplicationConfig) {
		o.MachineTag = tag
	}
}

func WithCors(b bool) AppOption {
	return func(o *ApplicationConfig) {
		o.CORS = b
	}
}

func WithP2PNetworkID(s string) AppOption {
	return func(o *ApplicationConfig) {
		o.P2PNetworkID = s
	}
}

func WithDisableCSRF(b bool) AppOption {
	return func(o *ApplicationConfig) {
		o.DisableCSRF = b
	}
}

func WithDisableHTTPCompression(b bool) AppOption {
	return func(o *ApplicationConfig) {
		o.DisableHTTPCompression = b
	}
}

func WithHTTPCompressionMinLength(n int) AppOption {
	return func(o *ApplicationConfig) {
		o.HTTPCompressionMinLength = n
	}
}

func WithP2PToken(s string) AppOption {
	return func(o *ApplicationConfig) {
		o.P2PToken = s
	}
}

var EnableWatchDog = func(o *ApplicationConfig) {
	o.WatchDog = true
}

var EnableTracing = func(o *ApplicationConfig) {
	o.EnableTracing = true
}

var EnableBackendLogging = func(o *ApplicationConfig) {
	o.EnableBackendLogging = true
}

var EnableWatchDogIdleCheck = func(o *ApplicationConfig) {
	o.WatchDog = true
	o.WatchDogIdle = true
}

var DisableGalleryEndpoint = func(o *ApplicationConfig) {
	o.DisableGalleryEndpoint = true
}

var DisableMCP = func(o *ApplicationConfig) {
	o.DisableMCP = true
}

var EnableWatchDogBusyCheck = func(o *ApplicationConfig) {
	o.WatchDog = true
	o.WatchDogBusy = true
}

var DisableWebUI = func(o *ApplicationConfig) {
	o.DisableWebUI = true
}

var EnableOllamaAPIRootEndpoint = func(o *ApplicationConfig) {
	o.OllamaAPIRootEndpoint = true
}

var DisableRuntimeSettings = func(o *ApplicationConfig) {
	o.DisableRuntimeSettings = true
}

func SetWatchDogBusyTimeout(t time.Duration) AppOption {
	return func(o *ApplicationConfig) {
		o.WatchDogBusyTimeout = t
	}
}

func SetWatchDogIdleTimeout(t time.Duration) AppOption {
	return func(o *ApplicationConfig) {
		o.WatchDogIdleTimeout = t
	}
}

func SetWatchDogInterval(t time.Duration) AppOption {
	return func(o *ApplicationConfig) {
		o.WatchDogInterval = t
	}
}

// EnableMemoryReclaimer enables memory threshold monitoring.
// When enabled, the watchdog will evict backends if memory usage exceeds the threshold.
// Works with GPU VRAM if available, otherwise uses system RAM.
var EnableMemoryReclaimer = func(o *ApplicationConfig) {
	o.MemoryReclaimerEnabled = true
	o.WatchDog = true // Memory reclaimer requires watchdog infrastructure
}

// SetMemoryReclaimerThreshold sets the memory usage threshold (0.0-1.0).
// When memory usage exceeds this threshold, backends will be evicted using LRU strategy.
func SetMemoryReclaimerThreshold(threshold float64) AppOption {
	return func(o *ApplicationConfig) {
		if threshold > 0 && threshold <= 1.0 {
			o.MemoryReclaimerThreshold = threshold
			o.MemoryReclaimerEnabled = true
			o.WatchDog = true // Memory reclaimer requires watchdog infrastructure
		}
	}
}

// SetVRAMBudget sets the VRAM allocation cap ("80%" or "12GB", "" = no cap).
func SetVRAMBudget(v string) AppOption {
	return func(o *ApplicationConfig) {
		o.VRAMBudget = v
	}
}

// WithMemoryReclaimer configures the memory reclaimer with the given settings
func WithMemoryReclaimer(enabled bool, threshold float64) AppOption {
	return func(o *ApplicationConfig) {
		o.MemoryReclaimerEnabled = enabled
		if threshold > 0 && threshold <= 1.0 {
			o.MemoryReclaimerThreshold = threshold
		}
		if enabled {
			o.WatchDog = true // Memory reclaimer requires watchdog infrastructure
		}
	}
}

// EnableSingleBackend is deprecated: use SetMaxActiveBackends(1) instead.
// This is kept for backward compatibility.
var EnableSingleBackend = func(o *ApplicationConfig) {
	o.SingleBackend = true
	o.MaxActiveBackends = 1
}

// SetMaxActiveBackends sets the maximum number of active backends.
// 0 = unlimited, 1 = single backend mode (replaces EnableSingleBackend)
func SetMaxActiveBackends(n int) AppOption {
	return func(o *ApplicationConfig) {
		o.MaxActiveBackends = n
		// For backward compatibility, also set SingleBackend if n == 1
		if n == 1 {
			o.SingleBackend = true
		}
	}
}

// GetEffectiveMaxActiveBackends returns the effective max active backends limit.
// It considers both MaxActiveBackends and the deprecated SingleBackend setting.
// If MaxActiveBackends is set (> 0), it takes precedence.
// If SingleBackend is true and MaxActiveBackends is 0, returns 1.
// Otherwise returns 0 (unlimited).
func (o *ApplicationConfig) GetEffectiveMaxActiveBackends() int {
	if o.MaxActiveBackends > 0 {
		return o.MaxActiveBackends
	}
	if o.SingleBackend {
		return 1
	}
	return 0
}

// WatchdogShouldRun reports whether the live watchdog process should be
// running for the current config. It mirrors the gating in
// (*Application).startWatchdog so the /api/settings start/stop decision and
// the startup path agree on a single source of truth: the watchdog runs when
// idle/busy checks are enabled (WatchDog), when LRU eviction is active
// (effective max active backends > 0), or when the memory reclaimer is on.
func (o *ApplicationConfig) WatchdogShouldRun() bool {
	return o.WatchDog || o.GetEffectiveMaxActiveBackends() > 0 || o.MemoryReclaimerEnabled
}

// WithForceEvictionWhenBusy sets whether to force eviction even when models have active API calls
func WithForceEvictionWhenBusy(enabled bool) AppOption {
	return func(o *ApplicationConfig) {
		o.ForceEvictionWhenBusy = enabled
	}
}

// WithSizeAwareEviction enables size-aware eviction ordering.
// When true, the watchdog evicts the largest loaded model first rather than the
// least-recently-used one, keeping small utility models resident and maximizing
// memory freed per eviction.
func WithSizeAwareEviction(enabled bool) AppOption {
	return func(o *ApplicationConfig) {
		o.SizeAwareEviction = enabled
	}
}

// WithLRUEvictionMaxRetries sets the maximum number of retries when waiting for busy models to become idle
func WithLRUEvictionMaxRetries(maxRetries int) AppOption {
	return func(o *ApplicationConfig) {
		if maxRetries > 0 {
			o.LRUEvictionMaxRetries = maxRetries
		}
	}
}

// WithLRUEvictionRetryInterval sets the interval between retries when waiting for busy models
func WithLRUEvictionRetryInterval(interval time.Duration) AppOption {
	return func(o *ApplicationConfig) {
		if interval > 0 {
			o.LRUEvictionRetryInterval = interval
		}
	}
}

// WithModelLoadFailureCooldown sets the base cooldown applied after a failed
// model load. 0 disables the cooldown, so unlike most options it accepts any
// non-negative value.
func WithModelLoadFailureCooldown(cooldown time.Duration) AppOption {
	return func(o *ApplicationConfig) {
		if cooldown >= 0 {
			o.ModelLoadFailureCooldown = cooldown
		}
	}
}

var EnableGalleriesAutoload = func(o *ApplicationConfig) {
	o.AutoloadGalleries = true
}

var EnableBackendGalleriesAutoload = func(o *ApplicationConfig) {
	o.AutoloadBackendGalleries = true
}

func WithOfficialLibraries(v bool) AppOption {
	return func(o *ApplicationConfig) { o.OfficialLibraries = v }
}

func WithAutoUpgradeBackends(v bool) AppOption {
	return func(o *ApplicationConfig) { o.AutoUpgradeBackends = v }
}

func WithAutoUpgradeAddons(v bool) AppOption {
	return func(o *ApplicationConfig) { o.AutoUpgradeAddons = v }
}

func WithVRAMPersistentCache(v bool) AppOption {
	return func(o *ApplicationConfig) { o.VRAMPersistentCache = v }
}

func WithRequireBackendIntegrity(v bool) AppOption {
	return func(o *ApplicationConfig) { o.RequireBackendIntegrity = v }
}

func WithDisableBackendInstall(v bool) AppOption {
	return func(o *ApplicationConfig) { o.DisableBackendInstall = v }
}

func WithPreferDevelopmentBackends(v bool) AppOption {
	return func(o *ApplicationConfig) { o.PreferDevelopmentBackends = v }
}

var EnableFederated = func(o *ApplicationConfig) {
	o.Federated = true
}

func WithExternalBackend(name string, uri string) AppOption {
	return func(o *ApplicationConfig) {
		if o.ExternalGRPCBackends == nil {
			o.ExternalGRPCBackends = make(map[string]string)
		}
		o.ExternalGRPCBackends[name] = uri
	}
}

func WithCorsAllowOrigins(b string) AppOption {
	return func(o *ApplicationConfig) {
		o.CORSAllowOrigins = b
	}
}

func WithStringGalleries(galls string) AppOption {
	return func(o *ApplicationConfig) {
		if galls == "" {
			o.Galleries = []Gallery{}
			return
		}
		var galleries []Gallery
		if err := json.Unmarshal([]byte(galls), &galleries); err != nil {
			xlog.Error("failed loading galleries", "error", err)
		}
		o.Galleries = append(o.Galleries, galleries...)
	}
}

func WithBackendGalleries(galls string) AppOption {
	return func(o *ApplicationConfig) {
		if galls == "" {
			o.BackendGalleries = []Gallery{}
			return
		}
		var galleries []Gallery
		if err := json.Unmarshal([]byte(galls), &galleries); err != nil {
			xlog.Error("failed loading galleries", "error", err)
		}
		o.BackendGalleries = append(o.BackendGalleries, galleries...)
	}
}

func WithGalleries(galleries []Gallery) AppOption {
	return func(o *ApplicationConfig) {
		o.Galleries = append(o.Galleries, galleries...)
	}
}

func WithContext(ctx context.Context) AppOption {
	return func(o *ApplicationConfig) {
		o.Context = ctx
	}
}

// WithModelArtifactMaterializer injects the controller-only model acquisition capability.
func WithModelArtifactMaterializer(materializer ArtifactMaterializer) AppOption {
	return func(o *ApplicationConfig) {
		if materializer != nil {
			o.ModelArtifactMaterializer = materializer
		}
	}
}

// WithMaxConcurrentBackendOperations sets how many backend operations run at
// once. Values below 1 fall back to 1.
func WithMaxConcurrentBackendOperations(n int) AppOption {
	return func(o *ApplicationConfig) {
		if n < 1 {
			n = 1
		}
		o.SetMaxConcurrentBackendOperations(n)
	}
}

// SetMaxConcurrentBackendOperations stores the limit, clamped to at least 1.
func (o *ApplicationConfig) SetMaxConcurrentBackendOperations(n int) {
	if n < 1 {
		n = 1
	}
	o.MaxConcurrentBackendOperations = n
	o.liveMaxBackendOps.Store(int64(n))
}

// BackendOperationLimit returns how many backend operations may run at once.
// It is safe to call while a runtime settings change is applied.
func (o *ApplicationConfig) BackendOperationLimit() int {
	if n := o.liveMaxBackendOps.Load(); n >= 1 {
		return int(n)
	}
	if o.MaxConcurrentBackendOperations >= 1 {
		return o.MaxConcurrentBackendOperations
	}
	return 1
}

func WithArtifactDownloadConcurrency(concurrency int) AppOption {
	return func(o *ApplicationConfig) {
		if concurrency < 1 {
			concurrency = modelartifacts.DefaultDownloadConcurrency
		}
		o.ArtifactDownloadConcurrency = concurrency
	}
}

// WithModelPreloadDisplay configures terminal rendering for model preload output.
func WithModelPreloadDisplay(renderMode string, disableColor bool) AppOption {
	return func(o *ApplicationConfig) {
		if renderMode != "" {
			o.ModelPreloadRenderMode = renderMode
		}
		o.DisableModelPreloadColor = disableColor
	}
}

func WithYAMLConfigPreload(configFile string) AppOption {
	return func(o *ApplicationConfig) {
		o.PreloadModelsFromPath = configFile
	}
}

func WithJSONStringPreload(configFile string) AppOption {
	return func(o *ApplicationConfig) {
		o.PreloadJSONModels = configFile
	}
}

func WithConfigFile(configFile string) AppOption {
	return func(o *ApplicationConfig) {
		o.ConfigFile = configFile
	}
}

func WithUploadLimitMB(limit int) AppOption {
	return func(o *ApplicationConfig) {
		o.UploadLimitMB = limit
	}
}

// WithThreads sets the thread-count hint. 0 means "unset": the effective
// physical-core default is resolved in application.New() only after
// persisted runtime settings merge, so a file-saved value is not masked by
// an eager fallback (#10845).
func WithThreads(threads int) AppOption {
	return func(o *ApplicationConfig) {
		o.Threads = threads
	}
}

func WithContextSize(ctxSize int) AppOption {
	return func(o *ApplicationConfig) {
		o.ContextSize = ctxSize
	}
}

func WithLlamaCPPTunnelCallback(callback func(tunnels []string)) AppOption {
	return func(o *ApplicationConfig) {
		o.LlamaCPPTunnelCallback = callback
	}
}

func WithMLXTunnelCallback(callback func(tunnels []string)) AppOption {
	return func(o *ApplicationConfig) {
		o.MLXTunnelCallback = callback
	}
}

func WithF16(f16 bool) AppOption {
	return func(o *ApplicationConfig) {
		o.F16 = f16
	}
}

func WithDebug(debug bool) AppOption {
	return func(o *ApplicationConfig) {
		o.Debug = debug
	}
}

func WithTracingMaxItems(items int) AppOption {
	return func(o *ApplicationConfig) {
		o.TracingMaxItems = items
	}
}

func WithMaxConcurrentBackendRequests(requests int) AppOption {
	return func(o *ApplicationConfig) {
		if requests <= 0 {
			requests = DefaultMaxConcurrentBackendRequests
		}
		o.MaxConcurrentBackendRequests = requests
	}
}

func WithTracingMaxBodyBytes(bytes int) AppOption {
	return func(o *ApplicationConfig) {
		o.TracingMaxBodyBytes = bytes
	}
}

func WithGeneratedContentDir(generatedContentDir string) AppOption {
	return func(o *ApplicationConfig) {
		o.GeneratedContentDir = generatedContentDir
	}
}

func WithUploadDir(uploadDir string) AppOption {
	return func(o *ApplicationConfig) {
		o.UploadDir = uploadDir
	}
}

func WithDataPath(dataPath string) AppOption {
	return func(o *ApplicationConfig) {
		o.DataPath = dataPath
	}
}

// WithDisableStats turns off the billing recorder. CLI: --disable-stats.
func WithDisableStats(disable bool) AppOption {
	return func(o *ApplicationConfig) {
		o.DisableStats = disable
	}
}

// WithMITMListen sets the address the cloudproxy MITM listener
// binds on. Empty = disabled. CLI: --mitm-listen.
func WithMITMListen(addr string) AppOption {
	return func(o *ApplicationConfig) {
		o.MITMListen = addr
	}
}

// WithMITMCADir sets the directory used to persist the MITM proxy
// CA cert + key. CLI: --mitm-ca-dir.
func WithMITMCADir(dir string) AppOption {
	return func(o *ApplicationConfig) {
		o.MITMCADir = dir
	}
}

// WithPIIDefaultDetectors sets the instance-wide default PII/secret detector
// model names applied to any PII-enabled model (chiefly cloud-proxy / MITM
// models) that names no pii.detectors of its own. CLI/env:
// LOCALAI_PII_DEFAULT_DETECTORS. Empty leaves the value to
// runtime_settings.json / the Middleware UI; a non-empty value takes
// precedence over the file (env > file).
func WithPIIDefaultDetectors(detectors []string) AppOption {
	return func(o *ApplicationConfig) {
		o.PIIDefaultDetectors = detectors
	}
}

func WithDynamicConfigDir(dynamicConfigsDir string) AppOption {
	return func(o *ApplicationConfig) {
		o.DynamicConfigsDir = dynamicConfigsDir
	}
}

func WithDynamicConfigDirPollInterval(interval time.Duration) AppOption {
	return func(o *ApplicationConfig) {
		o.DynamicConfigsDirPollInterval = interval
	}
}

func WithApiKeys(apiKeys []string) AppOption {
	return func(o *ApplicationConfig) {
		o.ApiKeys = apiKeys
	}
}

func WithOpenResponsesStoreTTL(ttl time.Duration) AppOption {
	return func(o *ApplicationConfig) {
		o.OpenResponsesStoreTTL = ttl
	}
}

func WithEnforcedPredownloadScans(enforced bool) AppOption {
	return func(o *ApplicationConfig) {
		o.EnforcePredownloadScans = enforced
	}
}

func WithOpaqueErrors(opaque bool) AppOption {
	return func(o *ApplicationConfig) {
		o.OpaqueErrors = opaque
	}
}

func WithLoadToMemory(models []string) AppOption {
	return func(o *ApplicationConfig) {
		o.LoadToMemory = models
	}
}

func WithSubtleKeyComparison(subtle bool) AppOption {
	return func(o *ApplicationConfig) {
		o.UseSubtleKeyComparison = subtle
	}
}

func WithDisableApiKeyRequirementForHttpGet(required bool) AppOption {
	return func(o *ApplicationConfig) {
		o.DisableApiKeyRequirementForHttpGet = required
	}
}

func WithAPIAddress(address string) AppOption {
	return func(o *ApplicationConfig) {
		o.APIAddress = address
	}
}

var DisableMetricsEndpoint AppOption = func(o *ApplicationConfig) {
	o.DisableMetrics = true
}

func WithHttpGetExemptedEndpoints(endpoints []string) AppOption {
	return func(o *ApplicationConfig) {
		o.HttpGetExemptedEndpoints = []*regexp.Regexp{}
		for _, epr := range endpoints {
			r, err := regexp.Compile(epr)
			if err == nil && r != nil {
				o.HttpGetExemptedEndpoints = append(o.HttpGetExemptedEndpoints, r)
			} else {
				xlog.Warn("Error while compiling HTTP Get Exemption regex, skipping this entry.", "error", err, "regex", epr)
			}
		}
	}
}

// Agent Pool options

var DisableCollections = func(o *ApplicationConfig) {
	o.Collections.Enabled = false
}

func WithCollectionsAPIURL(url string) AppOption {
	return func(o *ApplicationConfig) {
		o.Collections.APIURL = url
	}
}

func WithCollectionsAPIKey(key string) AppOption {
	return func(o *ApplicationConfig) {
		o.Collections.APIKey = key
	}
}

func WithCollectionsDefaultModel(model string) AppOption {
	return func(o *ApplicationConfig) {
		o.Collections.DefaultModel = model
	}
}

func WithCollectionsStateDir(dir string) AppOption {
	return func(o *ApplicationConfig) {
		o.Collections.StateDir = dir
	}
}

func WithCollectionsVectorEngine(engine string) AppOption {
	return func(o *ApplicationConfig) {
		o.Collections.VectorEngine = engine
	}
}

func WithCollectionsEmbeddingModel(model string) AppOption {
	return func(o *ApplicationConfig) {
		o.Collections.EmbeddingModel = model
	}
}

func WithCollectionsDatabaseURL(url string) AppOption {
	return func(o *ApplicationConfig) {
		o.Collections.DatabaseURL = url
	}
}

func WithCollectionsMaxChunkingSize(size int) AppOption {
	return func(o *ApplicationConfig) {
		o.Collections.MaxChunkingSize = size
	}
}

func WithCollectionsChunkOverlap(overlap int) AppOption {
	return func(o *ApplicationConfig) {
		o.Collections.ChunkOverlap = overlap
	}
}

func WithCollectionsCollectionDBPath(path string) AppOption {
	return func(o *ApplicationConfig) {
		o.Collections.CollectionDBPath = path
	}
}

// Auth options

func WithAuthEnabled(enabled bool) AppOption {
	return func(o *ApplicationConfig) {
		o.Auth.Enabled = enabled
	}
}

func WithAuthDatabaseURL(url string) AppOption {
	return func(o *ApplicationConfig) {
		o.Auth.DatabaseURL = url
	}
}

func WithAuthGitHubClientID(clientID string) AppOption {
	return func(o *ApplicationConfig) {
		o.Auth.GitHubClientID = clientID
	}
}

func WithAuthGitHubClientSecret(clientSecret string) AppOption {
	return func(o *ApplicationConfig) {
		o.Auth.GitHubClientSecret = clientSecret
	}
}

func WithExternalBaseURL(url string) AppOption {
	return func(o *ApplicationConfig) {
		o.ExternalBaseURL = url
	}
}

func WithAuthAdminEmail(email string) AppOption {
	return func(o *ApplicationConfig) {
		o.Auth.AdminEmail = email
	}
}

func WithAuthRegistrationMode(mode string) AppOption {
	return func(o *ApplicationConfig) {
		o.Auth.RegistrationMode = mode
	}
}

func WithAuthDisableLocalAuth(disable bool) AppOption {
	return func(o *ApplicationConfig) {
		o.Auth.DisableLocalAuth = disable
	}
}

func WithAuthOIDCIssuer(issuer string) AppOption {
	return func(o *ApplicationConfig) {
		o.Auth.OIDCIssuer = issuer
	}
}

func WithAuthOIDCClientID(clientID string) AppOption {
	return func(o *ApplicationConfig) {
		o.Auth.OIDCClientID = clientID
	}
}

func WithAuthOIDCClientSecret(clientSecret string) AppOption {
	return func(o *ApplicationConfig) {
		o.Auth.OIDCClientSecret = clientSecret
	}
}

func WithAuthAPIKeyHMACSecret(secret string) AppOption {
	return func(o *ApplicationConfig) {
		o.Auth.APIKeyHMACSecret = secret
	}
}

func WithAuthDefaultAPIKeyExpiry(expiry string) AppOption {
	return func(o *ApplicationConfig) {
		o.Auth.DefaultAPIKeyExpiry = expiry
	}
}

// WithLegacyNavigation brings the old sidebar back without a rebuild. The UI
// reads it from /api/features. It is a rollback switch and is removed one
// release after the add-on navigation ships.
func WithLegacyNavigation(on bool) AppOption {
	return func(o *ApplicationConfig) {
		o.LegacyNavigation = on
	}
}

// DisableChats, DisableTriggers and DisableConnectors are the options of the
// operator switches LOCALAI_DISABLE_CHATS, LOCALAI_DISABLE_TRIGGERS and
// LOCALAI_DISABLE_CONNECTORS.
var (
	DisableChats      = func(o *ApplicationConfig) { o.DisableChats = true }
	DisableTriggers   = func(o *ApplicationConfig) { o.DisableTriggers = true }
	DisableConnectors = func(o *ApplicationConfig) { o.DisableConnectors = true }
)

// WithAgentJobRetentionDays sets how long finished agent jobs are kept.
func WithAgentJobRetentionDays(days int) AppOption {
	return func(o *ApplicationConfig) { o.AgentJobRetentionDays = days }
}

// WithMCPCIJobTimeout sets the longest an agent job may run.
func WithMCPCIJobTimeout(d time.Duration) AppOption {
	return func(o *ApplicationConfig) { o.MCPCIJobTimeout = d }
}

// WithTriggerRunRetention sets how many finished runs are kept for each
// trigger, and for how many days.
func WithTriggerRunRetention(keep, days int) AppOption {
	return func(o *ApplicationConfig) { o.TriggerRunKeep, o.TriggerRunRetentionDays = keep, days }
}

// Defaults of the agent jobs.
const (
	DefaultAgentJobRetentionDays = 30
	DefaultMCPCIJobTimeout       = 10 * time.Minute
)

// WithChatsHostExec sets who gets the chats' built-in bash: off, ask or on.
func WithChatsHostExec(mode string) AppOption {
	return func(o *ApplicationConfig) {
		o.ChatsHostExec = mode
	}
}

// WithDisableSharedPages turns off the pages of shared folders.
func WithDisableSharedPages(disabled bool) AppOption {
	return func(o *ApplicationConfig) {
		o.DisableSharedPages = disabled
	}
}

// WithDisableLocalAIAssistant hard-disables the in-process admin MCP server.
// When set, the chat-handler branch for metadata.localai_assistant=true
// returns a "feature unavailable" error.
func WithDisableLocalAIAssistant(disabled bool) AppOption {
	return func(o *ApplicationConfig) {
		o.DisableLocalAIAssistant = disabled
	}
}

// WithExposeNodeHeader enables the X-LocalAI-Node response header on
// inference endpoints. Default off; the node ID reveals internal cluster
// topology and is opt-in for that reason.
func WithExposeNodeHeader(enabled bool) AppOption {
	return func(o *ApplicationConfig) {
		o.ExposeNodeHeader = enabled
	}
}

// ToConfigLoaderOptions returns a slice of ConfigLoader Option.
// Some options defined at the application level are going to be passed as defaults for
// all the configuration for the models.
// This includes for instance the context size or the number of threads.
// If a model doesn't set configs directly to the config model file
// it will use the defaults defined here.
func (o *ApplicationConfig) ToConfigLoaderOptions() []ConfigLoaderOption {
	return []ConfigLoaderOption{
		LoadOptionContextSize(o.ContextSize),
		LoadOptionDebug(o.Debug),
		LoadOptionF16(o.F16),
		LoadOptionThreads(o.Threads),
		ModelPath(o.SystemState.Model.ModelsPath),
		LoadOptionGalleryFiles(o.Galleries...),
		// Select later YAML/default phases; early phases use WithReloadDiagnostics
		// at loader construction so callbacks retain their original execution point.
		LoadOptionDiagnostics(o.DiagnosticsRecorder),
	}
}

// ToRuntimeSettings converts ApplicationConfig to RuntimeSettings for API
// responses and JSON serialization. Field coverage comes entirely from the
// runtimeSettingsFields registry (runtime_settings_registry.go).
func (o *ApplicationConfig) ToRuntimeSettings() RuntimeSettings {
	var s RuntimeSettings
	for _, f := range runtimeSettingsFields {
		f.snapshot(o, &s)
	}
	return s
}

// ApplyRuntimeSettings applies RuntimeSettings to ApplicationConfig (the
// live POST /api/settings path). Only non-nil fields are applied. Returns
// true when legacy service-reconfiguration fields were accepted (see each
// row's restartRequired flag in runtimeSettingsFields). The settings endpoint
// consumes this as a watchdog reconfiguration signal, not a server restart.
func (o *ApplicationConfig) ApplyRuntimeSettings(settings *RuntimeSettings) (requireRestart bool) {
	if settings == nil {
		return false
	}
	for _, f := range runtimeSettingsFields {
		if f.snapshotOnly || !f.isSet(settings) {
			continue
		}
		if f.apply(o, settings) && f.requiresRestart {
			requireRestart = true
		}
	}
	// The React Settings "Enable Watchdog" master toggle manages only the
	// idle/busy checks - watchdog_enabled is vestigial in that UI. Whenever
	// either idle/busy field is present in the body, derive the run-state
	// from idle||busy so a cold enable starts the watchdog and a full
	// disable stops it, instead of trusting the stale watchdog_enabled the
	// UI never updates. An API client posting only watchdog_enabled
	// (idle/busy absent) keeps its explicit value. (#9125)
	if settings.WatchdogIdleEnabled != nil || settings.WatchdogBusyEnabled != nil {
		o.WatchDog = o.WatchDogIdle || o.WatchDogBusy
	}
	// The memory reclaimer needs the watchdog run loop.
	if settings.MemoryReclaimerEnabled != nil && *settings.MemoryReclaimerEnabled {
		o.WatchDog = true
	}
	// VRAM budget live-apply: a cross-cutting process-global side effect
	// (xsysinfo), kept out of the registry row like the watchdog/reclaimer
	// invariants above - rows only own config members. An empty string
	// clears the cap; a malformed value is rejected by the settings
	// endpoint, but stay fail-open here too so a bad persisted value
	// cannot wedge apply.
	if settings.VRAMBudget != nil {
		if b, err := vrambudget.Parse(o.VRAMBudget); err == nil {
			xsysinfo.SetDefaultVRAMBudget(b)
		}
	}
	if settings.ArtifactDownloadConcurrency != nil {
		if configurable, ok := o.ModelArtifactMaterializer.(interface{ SetDownloadConcurrency(int) }); ok {
			configurable.SetDownloadConcurrency(o.ArtifactDownloadConcurrency)
		}
	}
	// Note: ApiKeys need env-merge handling (MergeAPIKeys) - done by the
	// caller, because the env-provided keys live on the startup config.
	return requireRestart
}

// func WithMetrics(meter *metrics.Metrics) AppOption {
// 	return func(o *StartupOptions) {
// 		o.Metrics = meter
// 	}
// }

// WithRouterDecisionLog retains routing decisions independently of billing stats.
func WithRouterDecisionLog(enabled bool) AppOption {
	return func(o *ApplicationConfig) { o.RouterDecisionLog = enabled }
}

// WithDiagnosticsRecorder installs the optional startup-only timing recorder.
func WithDiagnosticsRecorder(r *diagnostics.Recorder) AppOption {
	return func(o *ApplicationConfig) { o.DiagnosticsRecorder = r }
}
