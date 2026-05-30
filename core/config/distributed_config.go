package config

import (
	"cmp"
	"fmt"
	"time"

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
	AutoApproveNodes  bool   // --auto-approve-nodes / LOCALAI_AUTO_APPROVE_NODES (skip admin approval for new workers)

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
	// Warn about missing registration token (not an error)
	if c.RegistrationToken == "" {
		xlog.Warn("distributed mode running without registration token — node endpoints are unprotected")
	}
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
