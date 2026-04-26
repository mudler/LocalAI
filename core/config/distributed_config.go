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
	PerModelHealthCheck bool          // Enable per-model backend health checking (default false)
	MCPCIJobTimeout     time.Duration // MCP CI job execution timeout (default 10m)

	MaxUploadSize int64 // Maximum upload body size in bytes (default 50 GB)

	AgentWorkerConcurrency int `yaml:"agent_worker_concurrency" json:"agent_worker_concurrency" env:"LOCALAI_AGENT_WORKER_CONCURRENCY"`
	JobWorkerConcurrency   int `yaml:"job_worker_concurrency" json:"job_worker_concurrency" env:"LOCALAI_JOB_WORKER_CONCURRENCY"`
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
		"mcp-tool-timeout":      c.MCPToolTimeout,
		"mcp-discovery-timeout": c.MCPDiscoveryTimeout,
		"worker-wait-timeout":   c.WorkerWaitTimeout,
		"drain-timeout":         c.DrainTimeout,
		"health-check-interval": c.HealthCheckInterval,
		"stale-node-threshold":  c.StaleNodeThreshold,
		"mcp-ci-job-timeout":    c.MCPCIJobTimeout,
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

var EnableAutoApproveNodes = func(o *ApplicationConfig) {
	o.Distributed.AutoApproveNodes = true
}

// Defaults for distributed timeouts.
const (
	DefaultMCPToolTimeout      = 360 * time.Second
	DefaultMCPDiscoveryTimeout = 60 * time.Second
	DefaultWorkerWaitTimeout   = 5 * time.Minute
	DefaultDrainTimeout        = 30 * time.Second
	DefaultHealthCheckInterval = 15 * time.Second
	DefaultStaleNodeThreshold  = 60 * time.Second
	DefaultMCPCIJobTimeout     = 10 * time.Minute
)

// DefaultMaxUploadSize is the default maximum upload body size (50 GB).
const DefaultMaxUploadSize int64 = 50 << 30

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
