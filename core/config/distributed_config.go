package config

import (
	"cmp"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/pkg/natsauth"
	"github.com/mudler/xlog"
)

// DistributedConfig holds configuration for horizontal scaling mode.
// When Enabled is true, PostgreSQL and NATS are required.
type DistributedConfig struct {
	Enabled           bool   // --distributed / LOCALAI_DISTRIBUTED
	InstanceID        string // --instance-id / LOCALAI_INSTANCE_ID (auto-generated UUID if empty)
	NatsURL           string // --nats-url / LOCALAI_NATS_URL
	StorageURL        string // --storage-url / LOCALAI_STORAGE_URL (S3 endpoint)
	RegistrationToken string // --registration-token / LOCALAI_REGISTRATION_TOKEN (required token for node registration)
	// RegistrationRequireAuth fails startup when distributed mode is enabled but
	// RegistrationToken is empty. The default (false) keeps the historical
	// fail-open behavior with a loud warning; production should set it so the
	// node-register endpoints and the worker file-transfer server cannot run
	// unauthenticated. Mirrors NatsRequireAuth for the NATS bus.
	RegistrationRequireAuth bool // LOCALAI_REGISTRATION_REQUIRE_AUTH
	// RequireAuth is the umbrella switch (LOCALAI_DISTRIBUTED_REQUIRE_AUTH) for
	// distributed-mode auth: when true it implies BOTH NatsRequireAuth and
	// RegistrationRequireAuth, so a single knob locks down the bus and the
	// registration/file-transfer layer together. The granular flags remain
	// available to enforce just one layer.
	RequireAuth      bool // LOCALAI_DISTRIBUTED_REQUIRE_AUTH
	AutoApproveNodes bool // --auto-approve-nodes / LOCALAI_AUTO_APPROVE_NODES (skip admin approval for new workers)
	// SharedModels asserts that every node (frontend and workers) mounts the
	// SAME models directory at the SAME path (e.g. a shared volume, as in
	// docker-compose.distributed.yaml). When true, the router skips staging
	// model files to workers entirely: the frontend's absolute model paths are
	// already valid on the worker, so re-uploading them into a per-model
	// subdirectory only re-downloads what is already present (#10556). Default
	// false preserves the historical per-node staging behavior.
	SharedModels bool // --distributed-shared-models / LOCALAI_DISTRIBUTED_SHARED_MODELS

	// NATS JWT auth (optional; see pkg/natsauth and docs/features/distributed-mode.md)
	NatsAccountSeed  string        // LOCALAI_NATS_ACCOUNT_SEED — account signing seed to mint per-node worker JWTs
	NatsServiceJWT   string        // LOCALAI_NATS_SERVICE_JWT — user JWT for frontends / agent workers
	NatsServiceSeed  string        // LOCALAI_NATS_SERVICE_SEED — signing seed paired with service JWT
	NatsWorkerJWTTTL time.Duration // LOCALAI_NATS_WORKER_JWT_TTL — minted worker JWT lifetime (default 24h)
	NatsRequireAuth  bool          // LOCALAI_NATS_REQUIRE_AUTH — fail startup if NATS credentials are missing
	NatsTLSCA        string        // LOCALAI_NATS_TLS_CA — PEM file for private CA (server verify)
	NatsTLSCert      string        // LOCALAI_NATS_TLS_CERT — client cert for NATS mTLS
	NatsTLSKey       string        // LOCALAI_NATS_TLS_KEY — client key paired with NatsTLSCert

	// S3 configuration (used when StorageURL is set)
	StorageBucket    string // --storage-bucket / LOCALAI_STORAGE_BUCKET
	StorageRegion    string // --storage-region / LOCALAI_STORAGE_REGION
	StorageAccessKey string // --storage-access-key / LOCALAI_STORAGE_ACCESS_KEY
	StorageSecretKey string // --storage-secret-key / LOCALAI_STORAGE_SECRET_KEY

	// Timeout configuration (all have sensible defaults — zero means use default)
	MCPToolTimeout      time.Duration // MCP tool execution timeout (default 360s)
	MCPDiscoveryTimeout time.Duration // MCP discovery timeout (default 60s)
	WorkerWaitTimeout   time.Duration // Max wait for healthy worker at startup (default 5m)
	DrainTimeout        time.Duration // Time to wait for in-flight requests during drain (default 30s)
	HealthCheckInterval time.Duration // Health monitor check interval (default 15s)
	StaleNodeThreshold  time.Duration // Time before a node is considered stale (default 60s)
	// DisablePerModelHealthCheck turns off the health monitor's per-model
	// gRPC probe. When enabled (the default), the monitor pings each model's
	// gRPC address and removes stale node_models rows whose backend has
	// crashed even though the worker's node-level heartbeat is still arriving.
	// Without per-model probing, /embeddings and /completions can be dispatched
	// to a backend that silently returns garbage (see also the cascading
	// model-row cleanup on MarkUnhealthy / MarkDraining).
	DisablePerModelHealthCheck bool

	MCPCIJobTimeout time.Duration // MCP CI job execution timeout (default 10m)

	BackendInstallTimeout time.Duration // NATS round-trip timeout for backend.install (default 15m)
	BackendUpgradeTimeout time.Duration // NATS round-trip timeout for backend.upgrade (default 15m)

	MaxUploadSize int64 // Maximum upload body size in bytes (default 50 GB)

	AgentWorkerConcurrency int `yaml:"agent_worker_concurrency" json:"agent_worker_concurrency" env:"LOCALAI_AGENT_WORKER_CONCURRENCY"`
	JobWorkerConcurrency   int `yaml:"job_worker_concurrency" json:"job_worker_concurrency" env:"LOCALAI_JOB_WORKER_CONCURRENCY"`

	// PrefixCacheDisabled turns off prefix-cache-aware routing, falling back to
	// round-robin (the floor). Prefix-cache routing is ON by default in
	// distributed mode; this flag exists so operators can opt out. The CLI
	// surfaces a default-true --distributed-prefix-cache enable flag and sets
	// this when the operator passes --distributed-prefix-cache=false.
	PrefixCacheDisabled bool
	// PrefixCacheTTL is the idle-timeout for prefix-cache index entries and
	// drives the background eviction cadence (eviction runs every TTL/2). Zero
	// means use the prefixcache package default (5m).
	PrefixCacheTTL time.Duration
	// ModelSchedulingJSON is an inline JSON list of per-model scheduling configs
	// applied authoritatively at startup (LOCALAI_MODEL_SCHEDULING).
	ModelSchedulingJSON string
	// ModelSchedulingConfigPath is a path to a YAML file with the same list
	// (LOCALAI_MODEL_SCHEDULING_CONFIG).
	ModelSchedulingConfigPath string
}

// Validate checks that the distributed configuration is internally consistent.
// It returns nil if distributed mode is disabled.
func (c DistributedConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.NatsURL == "" {
		return fmt.Errorf("distributed mode requires --nats-url / LOCALAI_NATS_URL")
	}
	// S3 credentials must be paired
	if (c.StorageAccessKey != "" && c.StorageSecretKey == "") ||
		(c.StorageAccessKey == "" && c.StorageSecretKey != "") {
		return fmt.Errorf("storage-access-key and storage-secret-key must both be set or both empty")
	}
	// The registration token guards both the node HTTP register/heartbeat
	// endpoints and the worker file-transfer server (which fails open on an
	// empty token). Enforce it when registration auth is required (the granular
	// flag or the umbrella); otherwise warn.
	if c.RegistrationToken == "" {
		if c.RegistrationAuthRequired() {
			return fmt.Errorf("registration auth is required (LOCALAI_REGISTRATION_REQUIRE_AUTH or LOCALAI_DISTRIBUTED_REQUIRE_AUTH) but LOCALAI_REGISTRATION_TOKEN is empty")
		}
		xlog.Warn("distributed mode running without registration token — node endpoints and the worker file-transfer server are unprotected; set LOCALAI_REGISTRATION_TOKEN, or LOCALAI_DISTRIBUTED_REQUIRE_AUTH=true to fail closed")
	}
	if err := c.NatsAuthConfig().Validate(); err != nil {
		return err
	}
	if err := c.NatsTLSFiles().Validate(); err != nil {
		return err
	}
	c.NatsAuthConfig().WarnIfInsecure(true)
	// Check for negative durations
	for name, d := range map[string]time.Duration{
		FlagMCPToolTimeout:        c.MCPToolTimeout,
		FlagMCPDiscoveryTimeout:   c.MCPDiscoveryTimeout,
		FlagWorkerWaitTimeout:     c.WorkerWaitTimeout,
		FlagDrainTimeout:          c.DrainTimeout,
		FlagHealthCheckInterval:   c.HealthCheckInterval,
		FlagStaleNodeThreshold:    c.StaleNodeThreshold,
		FlagMCPCIJobTimeout:       c.MCPCIJobTimeout,
		FlagBackendInstallTimeout: c.BackendInstallTimeout,
		FlagBackendUpgradeTimeout: c.BackendUpgradeTimeout,
	} {
		if d < 0 {
			return fmt.Errorf("%s must not be negative", name)
		}
	}
	return nil
}

// Distributed config options

var EnableDistributed = func(o *ApplicationConfig) {
	o.Distributed.Enabled = true
}

func WithDistributedInstanceID(id string) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.InstanceID = id
	}
}

func WithNatsURL(url string) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.NatsURL = url
	}
}

func WithRegistrationToken(token string) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.RegistrationToken = token
	}
}

func WithNatsAccountSeed(seed string) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.NatsAccountSeed = seed
	}
}

func WithNatsServiceJWT(jwt string) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.NatsServiceJWT = jwt
	}
}

func WithNatsServiceSeed(seed string) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.NatsServiceSeed = seed
	}
}

func WithNatsWorkerJWTTTL(d time.Duration) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.NatsWorkerJWTTTL = d
	}
}

var EnableNatsRequireAuth = func(o *ApplicationConfig) {
	o.Distributed.NatsRequireAuth = true
}

// EnableRegistrationRequireAuth makes an empty registration token a hard error
// in distributed mode (see DistributedConfig.RegistrationRequireAuth).
var EnableRegistrationRequireAuth = func(o *ApplicationConfig) {
	o.Distributed.RegistrationRequireAuth = true
}

// EnableDistributedRequireAuth is the umbrella switch implying both
// NatsRequireAuth and RegistrationRequireAuth (see DistributedConfig.RequireAuth).
var EnableDistributedRequireAuth = func(o *ApplicationConfig) {
	o.Distributed.RequireAuth = true
}

// RegistrationAuthRequired reports whether an empty registration token must be
// treated as a fatal misconfiguration — the granular flag or the umbrella.
func (c DistributedConfig) RegistrationAuthRequired() bool {
	return c.RegistrationRequireAuth || c.RequireAuth
}

// NatsAuthRequired reports whether NATS JWT credentials must be present — the
// granular flag or the umbrella.
func (c DistributedConfig) NatsAuthRequired() bool {
	return c.NatsRequireAuth || c.RequireAuth
}

func WithNatsTLSCA(path string) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.NatsTLSCA = path
	}
}

func WithNatsTLSCert(path string) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.NatsTLSCert = path
	}
}

func WithNatsTLSKey(path string) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.NatsTLSKey = path
	}
}

func WithStorageURL(url string) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.StorageURL = url
	}
}

func WithStorageBucket(bucket string) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.StorageBucket = bucket
	}
}

func WithStorageRegion(region string) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.StorageRegion = region
	}
}

func WithStorageAccessKey(key string) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.StorageAccessKey = key
	}
}

func WithStorageSecretKey(key string) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.StorageSecretKey = key
	}
}

func WithBackendInstallTimeout(d time.Duration) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.BackendInstallTimeout = d
	}
}

func WithBackendUpgradeTimeout(d time.Duration) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.BackendUpgradeTimeout = d
	}
}

var EnableAutoApproveNodes = func(o *ApplicationConfig) {
	o.Distributed.AutoApproveNodes = true
}

// EnableDistributedSharedModels marks the cluster as sharing one models
// directory across all nodes, so the router skips staging model files to
// workers (see DistributedConfig.SharedModels).
var EnableDistributedSharedModels = func(o *ApplicationConfig) {
	o.Distributed.SharedModels = true
}

// DisablePrefixCache turns off prefix-cache-aware routing (falls back to
// round-robin). Prefix-cache routing is enabled by default in distributed mode.
var DisablePrefixCache = func(o *ApplicationConfig) {
	o.Distributed.PrefixCacheDisabled = true
}

// WithPrefixCacheTTL sets the prefix-cache index idle-timeout (and the
// background eviction cadence, which runs every TTL/2).
func WithPrefixCacheTTL(d time.Duration) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.PrefixCacheTTL = d
	}
}

// WithModelSchedulingJSON sets the inline-JSON declarative scheduling config.
func WithModelSchedulingJSON(s string) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.ModelSchedulingJSON = s
	}
}

// WithModelSchedulingConfigPath sets the path to a YAML declarative scheduling
// config file.
func WithModelSchedulingConfigPath(path string) AppOption {
	return func(o *ApplicationConfig) {
		o.Distributed.ModelSchedulingConfigPath = path
	}
}

// Flag names for distributed timeout / interval configuration. These are
// the kebab-case identifiers kong derives from the matching RunCMD struct
// fields; they appear in Validate error messages and any other operator-
// facing surface that needs to reference a specific knob by name. Keeping
// them as constants prevents the string from drifting from the actual
// flag a future rename would produce.
const (
	FlagMCPToolTimeout        = "mcp-tool-timeout"
	FlagMCPDiscoveryTimeout   = "mcp-discovery-timeout"
	FlagWorkerWaitTimeout     = "worker-wait-timeout"
	FlagDrainTimeout          = "drain-timeout"
	FlagHealthCheckInterval   = "health-check-interval"
	FlagStaleNodeThreshold    = "stale-node-threshold"
	FlagMCPCIJobTimeout       = "mcp-ci-job-timeout"
	FlagBackendInstallTimeout = "backend-install-timeout"
	FlagBackendUpgradeTimeout = "backend-upgrade-timeout"
)

// Defaults for distributed timeouts.
const (
	DefaultMCPToolTimeout        = 360 * time.Second
	DefaultMCPDiscoveryTimeout   = 60 * time.Second
	DefaultWorkerWaitTimeout     = 5 * time.Minute
	DefaultDrainTimeout          = 30 * time.Second
	DefaultHealthCheckInterval   = 15 * time.Second
	DefaultStaleNodeThreshold    = 60 * time.Second
	DefaultMCPCIJobTimeout       = 10 * time.Minute
	DefaultBackendInstallTimeout = 15 * time.Minute
	DefaultBackendUpgradeTimeout = 15 * time.Minute
)

// DefaultMaxUploadSize is the default maximum upload body size (50 GB).
const DefaultMaxUploadSize int64 = 50 << 30

// NatsTLSFiles returns NATS TLS/mTLS PEM paths for the messaging client.
func (c DistributedConfig) NatsTLSFiles() messaging.TLSFiles {
	return messaging.TLSFiles{
		CA:   c.NatsTLSCA,
		Cert: c.NatsTLSCert,
		Key:  c.NatsTLSKey,
	}
}

// NatsMessagingOptions builds messaging client options (JWT + TLS) for distributed components.
// Pass explicit userJWT/userSeed when set (e.g. worker overrides); empty uses service JWT from config.
func (c DistributedConfig) NatsMessagingOptions(userJWT, userSeed string) []messaging.Option {
	var opts []messaging.Option
	jwt, seed := userJWT, userSeed
	if jwt == "" && seed == "" {
		auth := c.NatsAuthConfig()
		jwt, seed = auth.ServiceUserJWT, auth.ServiceUserSeed
	}
	if jwt != "" && seed != "" {
		opts = append(opts, messaging.WithUserJWT(jwt, seed))
	}
	if tls := c.NatsTLSFiles(); tls.Enabled() {
		opts = append(opts, messaging.WithTLS(tls))
	}
	return opts
}

// NatsAuthConfig builds pkg/natsauth settings from distributed configuration.
func (c DistributedConfig) NatsAuthConfig() natsauth.Config {
	return natsauth.Config{
		AccountSeed:     c.NatsAccountSeed,
		ServiceUserJWT:  c.NatsServiceJWT,
		ServiceUserSeed: c.NatsServiceSeed,
		WorkerJWTTTL:    c.NatsWorkerJWTTTL,
		RequireAuth:     c.NatsAuthRequired(),
	}
}

// BackendInstallTimeoutOrDefault returns the configured timeout or the default.
func (c DistributedConfig) BackendInstallTimeoutOrDefault() time.Duration {
	return cmp.Or(c.BackendInstallTimeout, DefaultBackendInstallTimeout)
}

// BackendUpgradeTimeoutOrDefault returns the configured timeout or the default.
func (c DistributedConfig) BackendUpgradeTimeoutOrDefault() time.Duration {
	return cmp.Or(c.BackendUpgradeTimeout, DefaultBackendUpgradeTimeout)
}

// MCPToolTimeoutOrDefault returns the configured timeout or the default.
func (c DistributedConfig) MCPToolTimeoutOrDefault() time.Duration {
	return cmp.Or(c.MCPToolTimeout, DefaultMCPToolTimeout)
}

// MCPDiscoveryTimeoutOrDefault returns the configured timeout or the default.
func (c DistributedConfig) MCPDiscoveryTimeoutOrDefault() time.Duration {
	return cmp.Or(c.MCPDiscoveryTimeout, DefaultMCPDiscoveryTimeout)
}

// WorkerWaitTimeoutOrDefault returns the configured timeout or the default.
func (c DistributedConfig) WorkerWaitTimeoutOrDefault() time.Duration {
	return cmp.Or(c.WorkerWaitTimeout, DefaultWorkerWaitTimeout)
}

// DrainTimeoutOrDefault returns the configured timeout or the default.
func (c DistributedConfig) DrainTimeoutOrDefault() time.Duration {
	return cmp.Or(c.DrainTimeout, DefaultDrainTimeout)
}

// HealthCheckIntervalOrDefault returns the configured interval or the default.
func (c DistributedConfig) HealthCheckIntervalOrDefault() time.Duration {
	return cmp.Or(c.HealthCheckInterval, DefaultHealthCheckInterval)
}

// StaleNodeThresholdOrDefault returns the configured threshold or the default.
func (c DistributedConfig) StaleNodeThresholdOrDefault() time.Duration {
	return cmp.Or(c.StaleNodeThreshold, DefaultStaleNodeThreshold)
}

// MCPCIJobTimeoutOrDefault returns the configured MCP CI job timeout or the default.
func (c DistributedConfig) MCPCIJobTimeoutOrDefault() time.Duration {
	return cmp.Or(c.MCPCIJobTimeout, DefaultMCPCIJobTimeout)
}

// MaxUploadSizeOrDefault returns the configured max upload size or the default.
func (c DistributedConfig) MaxUploadSizeOrDefault() int64 {
	return cmp.Or(c.MaxUploadSize, DefaultMaxUploadSize)
}
