package config

// RuntimeSettings represents runtime configuration that can be changed dynamically.
// This struct is used for:
// - API responses (GET /api/settings)
// - API requests (POST /api/settings)
// - Persisting to runtime_settings.json
// - Loading from runtime_settings.json on startup
//
// All fields are pointers to distinguish between "not set" and "set to zero/false value".
type RuntimeSettings struct {
	// Watchdog settings
	WatchdogEnabled     *bool   `json:"watchdog_enabled,omitempty"`
	WatchdogIdleEnabled *bool   `json:"watchdog_idle_enabled,omitempty"`
	WatchdogBusyEnabled *bool   `json:"watchdog_busy_enabled,omitempty"`
	WatchdogIdleTimeout *string `json:"watchdog_idle_timeout,omitempty"`
	WatchdogBusyTimeout *string `json:"watchdog_busy_timeout,omitempty"`
	WatchdogInterval    *string `json:"watchdog_interval,omitempty"` // Interval between watchdog checks (e.g., 2s, 30s)

	// Backend management
	SingleBackend           *bool `json:"single_backend,omitempty"`      // Deprecated: use MaxActiveBackends = 1 instead
	MaxActiveBackends       *int  `json:"max_active_backends,omitempty"` // Maximum number of active backends (0 = unlimited, 1 = single backend mode)
	ParallelBackendRequests *bool `json:"parallel_backend_requests,omitempty"`

	// Memory Reclaimer settings (works with GPU if available, otherwise RAM)
	MemoryReclaimerEnabled   *bool    `json:"memory_reclaimer_enabled,omitempty"`   // Enable memory threshold monitoring
	MemoryReclaimerThreshold *float64 `json:"memory_reclaimer_threshold,omitempty"` // Threshold 0.0-1.0 (e.g., 0.95 = 95%)

	// Eviction settings
	ForceEvictionWhenBusy      *bool   `json:"force_eviction_when_busy,omitempty"`      // Force eviction even when models have active API calls (default: false for safety)
	LRUEvictionMaxRetries      *int    `json:"lru_eviction_max_retries,omitempty"`      // Maximum number of retries when waiting for busy models to become idle (default: 30)
	LRUEvictionRetryInterval   *string `json:"lru_eviction_retry_interval,omitempty"`   // Interval between retries when waiting for busy models (e.g., 1s, 2s) (default: 1s)

	// Performance settings
	Threads         *int  `json:"threads,omitempty"`
	ContextSize     *int  `json:"context_size,omitempty"`
	F16             *bool `json:"f16,omitempty"`
	Debug           *bool `json:"debug,omitempty"`
	EnableTracing   *bool `json:"enable_tracing,omitempty"`
	TracingMaxItems *int  `json:"tracing_max_items,omitempty"`

	// Security/CORS settings
	CORS             *bool   `json:"cors,omitempty"`
	CSRF             *bool   `json:"csrf,omitempty"`
	CORSAllowOrigins *string `json:"cors_allow_origins,omitempty"`

	// P2P settings
	P2PToken     *string `json:"p2p_token,omitempty"`
	P2PNetworkID *string `json:"p2p_network_id,omitempty"`
	Federated    *bool   `json:"federated,omitempty"`

	// Gallery settings
	Galleries                *[]Gallery `json:"galleries,omitempty"`
	BackendGalleries         *[]Gallery `json:"backend_galleries,omitempty"`
	AutoloadGalleries        *bool      `json:"autoload_galleries,omitempty"`
	AutoloadBackendGalleries *bool      `json:"autoload_backend_galleries,omitempty"`

	// API keys - No omitempty as we need to save empty arrays to clear keys
	ApiKeys *[]string `json:"api_keys"`

	// Agent settings
	AgentJobRetentionDays *int `json:"agent_job_retention_days,omitempty"`
}
