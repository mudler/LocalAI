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
	SingleBackend             *bool `json:"single_backend,omitempty"`              // Deprecated: use MaxActiveBackends = 1 instead
	MaxActiveBackends         *int  `json:"max_active_backends,omitempty"`         // Maximum number of active backends (0 = unlimited, 1 = single backend mode)
	OfficialLibraries         *bool `json:"official_libraries,omitempty"`          // Read the shipped default library sources of add-ons, backends and models
	AutoUpgradeBackends       *bool `json:"auto_upgrade_backends,omitempty"`       // Automatically upgrade backends when new versions are detected
	AutoUpgradeAddons         *bool `json:"auto_upgrade_addons,omitempty"`         // Automatically upgrade installed add-ons when their library lists a newer version
	PreferDevelopmentBackends *bool `json:"prefer_development_backends,omitempty"` // Display/download preference; installed alias resolver changes require restart
	// Memory Reclaimer settings (works with GPU if available, otherwise RAM)
	MemoryReclaimerEnabled   *bool    `json:"memory_reclaimer_enabled,omitempty"`   // Enable memory threshold monitoring
	MemoryReclaimerThreshold *float64 `json:"memory_reclaimer_threshold,omitempty"` // Threshold 0.0-1.0 (e.g., 0.95 = 95%)

	// Eviction settings
	ForceEvictionWhenBusy    *bool   `json:"force_eviction_when_busy,omitempty"`    // Force eviction even when models have active API calls (default: false for safety)
	SizeAwareEviction        *bool   `json:"size_aware_eviction,omitempty"`         // Evict largest models first rather than least-recently-used (default: false)
	LRUEvictionMaxRetries    *int    `json:"lru_eviction_max_retries,omitempty"`    // Maximum number of retries when waiting for busy models to become idle (default: 30)
	LRUEvictionRetryInterval *string `json:"lru_eviction_retry_interval,omitempty"` // Interval between retries when waiting for busy models (e.g., 1s, 2s) (default: 1s)

	// Performance settings
	Threads                     *int `json:"threads,omitempty"`
	ContextSize                 *int `json:"context_size,omitempty"`
	ArtifactDownloadConcurrency *int `json:"artifact_download_concurrency,omitempty"`

	MaxConcurrentBackendOperations *int `json:"max_concurrent_backend_operations,omitempty"`

	VRAMBudget           *string `json:"vram_budget,omitempty"` // Cap VRAM for allocation ("80%" or "12GB"; "" = no cap)
	F16                  *bool   `json:"f16,omitempty"`
	Debug                *bool   `json:"debug,omitempty"`
	EnableTracing        *bool   `json:"enable_tracing,omitempty"`
	TracingMaxItems      *int    `json:"tracing_max_items,omitempty"`
	TracingMaxBodyBytes  *int    `json:"tracing_max_body_bytes,omitempty"` // Per-body cap in bytes; 0 disables the cap
	EnableBackendLogging *bool   `json:"enable_backend_logging,omitempty"`

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
	VRAMPersistentCache      *bool      `json:"vram_persistent_cache,omitempty"`

	// API keys - No omitempty as we need to save empty arrays to clear keys
	ApiKeys *[]string `json:"api_keys"`

	// Open Responses settings
	OpenResponsesStoreTTL *string `json:"open_responses_store_ttl,omitempty"` // TTL for stored responses (e.g., "1h", "30m", "0" = no expiration)

	// Collections settings
	CollectionsEnabled         *bool   `json:"collections_enabled,omitempty"`
	CollectionsDefaultModel    *string `json:"collections_default_model,omitempty"`
	ChatsVisionModel           *string `json:"chats_vision_model,omitempty"`        // model that describes images for the chats
	ChatsTranscriptionModel    *string `json:"chats_transcription_model,omitempty"` // model that transcribes audio for the chats
	CollectionsEmbeddingModel  *string `json:"collections_embedding_model,omitempty"`
	CollectionsMaxChunkingSize *int    `json:"collections_max_chunking_size,omitempty"`
	CollectionsChunkOverlap    *int    `json:"collections_chunk_overlap,omitempty"`
	CollectionsDBPath          *string `json:"collections_db_path,omitempty"`
	CollectionsVectorEngine    *string `json:"collections_vector_engine,omitempty"` // chromem | postgres
	CollectionsDatabaseURL     *string `json:"collections_database_url,omitempty"`  // PostgreSQL DSN when vector engine is postgres

	// Distributed-mode settings. Read LIVE by the SmartRouter on each
	// scheduling decision, so toggling takes effect on the next placement
	// without a restart. Stored as the negation of
	// DistributedConfig.DiskHeadroomDisabled for UI clarity.
	DistributedDiskHeadroomCheck *bool `json:"distributed_disk_headroom_check,omitempty"` // Reject nodes without room to store the model (default: true)

	// MCP connectors that users define in the chats. Read at each connection, so
	// flipping it takes effect without a restart.
	MCPConnectorsAllowPrivate *bool `json:"mcp_connectors_allow_private,omitempty"` // let them reach private network addresses (default: false)

	// The built-in web tools of the chats. Read at each turn, so a change
	// applies at the next turn.
	ChatsWebSearch       *bool   `json:"chats_web_search,omitempty"`        // offer web_search (default: true)
	ChatsWebFetch        *bool   `json:"chats_web_fetch,omitempty"`         // offer web_fetch (default: true)
	ChatsWebSearXNGURL   *string `json:"chats_web_searxng_url,omitempty"`   // search with this SearXNG instance; empty: DuckDuckGo
	ChatsWebAllowPrivate *bool   `json:"chats_web_allow_private,omitempty"` // let web_fetch reach private network addresses (default: false)
	ChatsWebPorts        *string `json:"chats_web_ports,omitempty"`         // ports web_fetch may use, comma separated (default: 80,443)
	// Let a chat that switches it on call the user's saved agents as
	// sub-agents (default: true). Read at each turn.
	ChatsSavedAgents *bool `json:"chats_saved_agents,omitempty"`

	// Offers from the assistant in the chats, for users who did not choose.
	// Read at each turn, so a change applies at once.
	ChatsSuggestions *bool `json:"chats_suggestions,omitempty"` // negation of DisableChatSuggestions (default: true)

	// LocalAI Assistant settings — read live by the chat handler at request
	// entry, so flipping the toggle takes effect on the next request.
	LocalAIAssistantEnabled *bool `json:"localai_assistant_enabled,omitempty"` // negation of DisableLocalAIAssistant for UI clarity

	// Pages of shared folders — read live by the pages handlers on every
	// request, so switching them off stops open pages at their next call.
	SharedPagesEnabled *bool `json:"shared_pages_enabled,omitempty"` // negation of DisableSharedPages

	// Built-in chat tools (Settings > Tools) — read live at every turn.
	// ChatsHostExec is who gets the built-in bash that runs commands on this
	// machine with no isolation: off, ask or on; empty is the default (off
	// with accounts, ask without). ChatsToolProviders pins, for a built-in tool
	// that several add-ons replace, the add-on that serves it.
	ChatsHostExec      *string            `json:"chats_host_exec,omitempty"`
	ChatsToolProviders *map[string]string `json:"chats_tool_providers,omitempty"`

	// Branding / whitelabeling. Text fields are user-facing; *File fields hold
	// just the basename of an uploaded asset under {DynamicConfigsDir}/branding/.
	// All optional — empty values fall back to bundled LocalAI defaults.
	InstanceName       *string `json:"instance_name,omitempty"`
	InstanceTagline    *string `json:"instance_tagline,omitempty"`
	LogoFile           *string `json:"logo_file,omitempty"`
	LogoHorizontalFile *string `json:"logo_horizontal_file,omitempty"`
	FaviconFile        *string `json:"favicon_file,omitempty"`

	// Cloud-proxy MITM listener. MITMCADir is intentionally NOT
	// exposed at runtime — the CA dir is a startup-only path and
	// changing it after the CA has been generated would orphan
	// trusted clients.
	MITMListen *string `json:"mitm_listen,omitempty"`

	// PIIDefaultDetectors are the token-classification detector models applied
	// to any PII-enabled model that names no detectors of its own (so
	// cloud-proxy/MITM redaction works without per-model config). No omitempty:
	// an empty array must round-trip so the operator can clear it from the UI.
	PIIDefaultDetectors *[]string `json:"pii_default_detectors"`
}
