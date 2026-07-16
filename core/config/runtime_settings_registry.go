package config

import (
	"slices"
	"time"

	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// fieldSpec is one runtime setting: how to snapshot it out of
// ApplicationConfig, how to apply it back, and how to decide at load time
// whether env/CLI already claimed the value. Every RuntimeSettings field is
// owned by exactly one spec (the registry completeness spec pins this), so
// GET /api/settings, POST /api/settings, boot, and the file watcher can
// never drift apart per-field again (the bug class behind #10845).
type fieldSpec struct {
	// jsonNames this spec owns; a single entry except for composite rows
	// (max_active_backends + its deprecated alias single_backend).
	jsonNames []string
	// requiresRestart marks fields whose live change makes
	// ApplyRuntimeSettings report a restart to the settings endpoint.
	requiresRestart bool
	// fileAuthoritative: at startup/watch time the persisted value applies
	// unconditionally - either no env/CLI source exists (branding) or the
	// file is defined as the owner (enable_backend_logging,
	// tracing_max_body_bytes).
	fileAuthoritative bool
	// snapshotOnly: echoed by ToRuntimeSettings but never applied by the
	// loops (api_keys: the endpoint and file watcher own the env merge).
	snapshotOnly bool
	snapshot     func(o *ApplicationConfig, s *RuntimeSettings)
	isSet        func(s *RuntimeSettings) bool
	// apply writes the (non-nil) settings value into o and reports whether
	// it was accepted (validation may reject it, e.g. a bad duration).
	apply func(o *ApplicationConfig, s *RuntimeSettings) bool
	// envSet reports whether env/CLI already set this field: the live value
	// differs from the option-less-run baseline (DefaultRuntimeBaseline).
	envSet func(current, baseline *ApplicationConfig) bool
}

type fieldOpt func(*fieldSpec)

func restartRequired() fieldOpt { return func(f *fieldSpec) { f.requiresRestart = true } }
func fromFileAlways() fieldOpt  { return func(f *fieldSpec) { f.fileAuthoritative = true } }

func field[T comparable](name string, sel func(*RuntimeSettings) **T, get func(*ApplicationConfig) T, set func(*ApplicationConfig, T), opts ...fieldOpt) fieldSpec {
	return fieldEq(name, sel, get, set, func(a, b T) bool { return a == b }, opts...)
}

func fieldEq[T any](name string, sel func(*RuntimeSettings) **T, get func(*ApplicationConfig) T, set func(*ApplicationConfig, T), eq func(a, b T) bool, opts ...fieldOpt) fieldSpec {
	f := fieldSpec{
		jsonNames: []string{name},
		snapshot:  func(o *ApplicationConfig, s *RuntimeSettings) { v := get(o); *sel(s) = &v },
		isSet:     func(s *RuntimeSettings) bool { return *sel(s) != nil },
		apply:     func(o *ApplicationConfig, s *RuntimeSettings) bool { set(o, **sel(s)); return true },
		envSet:    func(cur, base *ApplicationConfig) bool { return !eq(get(cur), get(base)) },
	}
	for _, opt := range opts {
		opt(&f)
	}
	return f
}

// durationField maps a time.Duration config member to a duration-string
// settings field. fallback is the presentation default ToRuntimeSettings
// reports while the member is unset (0). envSet compares the raw durations
// rather than the formatted strings so the fallback text cannot mask an
// env-set value that happens to equal it.
func durationField(name string, sel func(*RuntimeSettings) **string, dur func(*ApplicationConfig) *time.Duration, fallback string, opts ...fieldOpt) fieldSpec {
	f := fieldSpec{
		jsonNames: []string{name},
		snapshot: func(o *ApplicationConfig, s *RuntimeSettings) {
			v := fallback
			if d := *dur(o); d > 0 {
				v = d.String()
			}
			*sel(s) = &v
		},
		isSet: func(s *RuntimeSettings) bool { return *sel(s) != nil },
		apply: func(o *ApplicationConfig, s *RuntimeSettings) bool {
			d, err := time.ParseDuration(**sel(s))
			if err != nil {
				xlog.Warn("invalid duration in runtime settings", "field", name, "value", **sel(s), "error", err)
				return false
			}
			*dur(o) = d
			return true
		},
		envSet: func(cur, base *ApplicationConfig) bool { return *dur(cur) != *dur(base) },
	}
	for _, opt := range opts {
		opt(&f)
	}
	return f
}

// runtimeSettingsFields is THE per-field description of every runtime
// setting. ToRuntimeSettings, ApplyRuntimeSettings, and
// ApplyRuntimeSettingsAtStartup are loops over this table; adding a setting
// means adding a RuntimeSettings struct field plus one row here (the
// registry completeness spec fails until both exist).
var runtimeSettingsFields = []fieldSpec{
	// Watchdog. The live master flag is post-processed by the apply loops
	// (WatchDog follows idle||busy; see ApplyRuntimeSettings).
	field("watchdog_enabled",
		func(s *RuntimeSettings) **bool { return &s.WatchdogEnabled },
		func(o *ApplicationConfig) bool { return o.WatchDog },
		func(o *ApplicationConfig, v bool) { o.WatchDog = v },
		restartRequired()),
	field("watchdog_idle_enabled",
		func(s *RuntimeSettings) **bool { return &s.WatchdogIdleEnabled },
		func(o *ApplicationConfig) bool { return o.WatchDogIdle },
		func(o *ApplicationConfig, v bool) { o.WatchDogIdle = v },
		restartRequired()),
	field("watchdog_busy_enabled",
		func(s *RuntimeSettings) **bool { return &s.WatchdogBusyEnabled },
		func(o *ApplicationConfig) bool { return o.WatchDogBusy },
		func(o *ApplicationConfig, v bool) { o.WatchDogBusy = v },
		restartRequired()),
	durationField("watchdog_idle_timeout",
		func(s *RuntimeSettings) **string { return &s.WatchdogIdleTimeout },
		func(o *ApplicationConfig) *time.Duration { return &o.WatchDogIdleTimeout },
		"15m", restartRequired()),
	durationField("watchdog_busy_timeout",
		func(s *RuntimeSettings) **string { return &s.WatchdogBusyTimeout },
		func(o *ApplicationConfig) *time.Duration { return &o.WatchDogBusyTimeout },
		"5m", restartRequired()),
	durationField("watchdog_interval",
		func(s *RuntimeSettings) **string { return &s.WatchdogInterval },
		func(o *ApplicationConfig) *time.Duration { return &o.WatchDogInterval },
		model.DefaultWatchdogInterval.String(), restartRequired()),

	// Backend management. max_active_backends and its deprecated alias
	// single_backend are one composite row: they must stay mutually
	// consistent and max_active_backends wins when both are posted.
	{
		jsonNames:       []string{"max_active_backends", "single_backend"},
		requiresRestart: true,
		snapshot: func(o *ApplicationConfig, s *RuntimeSettings) {
			mab := o.MaxActiveBackends
			sb := o.SingleBackend
			s.MaxActiveBackends = &mab
			s.SingleBackend = &sb
		},
		isSet: func(s *RuntimeSettings) bool { return s.MaxActiveBackends != nil || s.SingleBackend != nil },
		apply: func(o *ApplicationConfig, s *RuntimeSettings) bool {
			if s.MaxActiveBackends != nil {
				o.MaxActiveBackends = *s.MaxActiveBackends
				o.SingleBackend = *s.MaxActiveBackends == 1
				return true
			}
			o.SingleBackend = *s.SingleBackend
			if *s.SingleBackend {
				o.MaxActiveBackends = 1
			} else {
				o.MaxActiveBackends = 0
			}
			return true
		},
		envSet: func(cur, base *ApplicationConfig) bool {
			return cur.MaxActiveBackends != base.MaxActiveBackends || cur.SingleBackend != base.SingleBackend
		},
	},
	field("auto_upgrade_backends",
		func(s *RuntimeSettings) **bool { return &s.AutoUpgradeBackends },
		func(o *ApplicationConfig) bool { return o.AutoUpgradeBackends },
		func(o *ApplicationConfig, v bool) { o.AutoUpgradeBackends = v }),
	field("prefer_development_backends",
		func(s *RuntimeSettings) **bool { return &s.PreferDevelopmentBackends },
		func(o *ApplicationConfig) bool { return o.PreferDevelopmentBackends },
		func(o *ApplicationConfig, v bool) { o.PreferDevelopmentBackends = v }),

	// Memory reclaimer. Enabling it forces the watchdog master flag - that
	// cross-field invariant lives in the apply loops, not here.
	field("memory_reclaimer_enabled",
		func(s *RuntimeSettings) **bool { return &s.MemoryReclaimerEnabled },
		func(o *ApplicationConfig) bool { return o.MemoryReclaimerEnabled },
		func(o *ApplicationConfig, v bool) { o.MemoryReclaimerEnabled = v },
		restartRequired()),
	{
		jsonNames:       []string{"memory_reclaimer_threshold"},
		requiresRestart: true,
		snapshot: func(o *ApplicationConfig, s *RuntimeSettings) {
			v := o.MemoryReclaimerThreshold
			s.MemoryReclaimerThreshold = &v
		},
		isSet: func(s *RuntimeSettings) bool { return s.MemoryReclaimerThreshold != nil },
		apply: func(o *ApplicationConfig, s *RuntimeSettings) bool {
			v := *s.MemoryReclaimerThreshold
			if v <= 0 || v > 1.0 {
				xlog.Warn("memory_reclaimer_threshold out of range (0,1], ignoring", "value", v)
				return false
			}
			o.MemoryReclaimerThreshold = v
			return true
		},
		envSet: func(cur, base *ApplicationConfig) bool {
			return cur.MemoryReclaimerThreshold != base.MemoryReclaimerThreshold
		},
	},

	// Eviction.
	field("force_eviction_when_busy",
		func(s *RuntimeSettings) **bool { return &s.ForceEvictionWhenBusy },
		func(o *ApplicationConfig) bool { return o.ForceEvictionWhenBusy },
		func(o *ApplicationConfig, v bool) { o.ForceEvictionWhenBusy = v }),
	field("size_aware_eviction",
		func(s *RuntimeSettings) **bool { return &s.SizeAwareEviction },
		func(o *ApplicationConfig) bool { return o.SizeAwareEviction },
		func(o *ApplicationConfig, v bool) { o.SizeAwareEviction = v }),
	field("lru_eviction_max_retries",
		func(s *RuntimeSettings) **int { return &s.LRUEvictionMaxRetries },
		func(o *ApplicationConfig) int { return o.LRUEvictionMaxRetries },
		func(o *ApplicationConfig, v int) { o.LRUEvictionMaxRetries = v }),
	durationField("lru_eviction_retry_interval",
		func(s *RuntimeSettings) **string { return &s.LRUEvictionRetryInterval },
		func(o *ApplicationConfig) *time.Duration { return &o.LRUEvictionRetryInterval },
		"1s"),

	// Performance.
	field("threads",
		func(s *RuntimeSettings) **int { return &s.Threads },
		func(o *ApplicationConfig) int { return o.Threads },
		func(o *ApplicationConfig, v int) { o.Threads = v }),
	field("context_size",
		func(s *RuntimeSettings) **int { return &s.ContextSize },
		func(o *ApplicationConfig) int { return o.ContextSize },
		func(o *ApplicationConfig, v int) { o.ContextSize = v }),
	// VRAM budget: the cap string ("80%"/"12GB"/"" = uncapped). The live
	// side effect (xsysinfo.SetDefaultVRAMBudget) is post-processing in the
	// apply loop, not here - the row only owns the config member, matching
	// how the memory-reclaimer/watchdog cross-field effects are handled.
	field("vram_budget",
		func(s *RuntimeSettings) **string { return &s.VRAMBudget },
		func(o *ApplicationConfig) string { return o.VRAMBudget },
		func(o *ApplicationConfig, v string) { o.VRAMBudget = v }),
	field("f16",
		func(s *RuntimeSettings) **bool { return &s.F16 },
		func(o *ApplicationConfig) bool { return o.F16 },
		func(o *ApplicationConfig, v bool) { o.F16 = v }),
	field("debug",
		func(s *RuntimeSettings) **bool { return &s.Debug },
		func(o *ApplicationConfig) bool { return o.Debug },
		func(o *ApplicationConfig, v bool) { o.Debug = v }),
	field("enable_tracing",
		func(s *RuntimeSettings) **bool { return &s.EnableTracing },
		func(o *ApplicationConfig) bool { return o.EnableTracing },
		func(o *ApplicationConfig, v bool) { o.EnableTracing = v }),
	field("tracing_max_items",
		func(s *RuntimeSettings) **int { return &s.TracingMaxItems },
		func(o *ApplicationConfig) int { return o.TracingMaxItems },
		func(o *ApplicationConfig, v int) { o.TracingMaxItems = v }),
	// The on-disk setting overrides the CLI/env default by design: 0 in the
	// file means "uncapped" and must not be mistaken for "unset".
	field("tracing_max_body_bytes",
		func(s *RuntimeSettings) **int { return &s.TracingMaxBodyBytes },
		func(o *ApplicationConfig) int { return o.TracingMaxBodyBytes },
		func(o *ApplicationConfig, v int) { o.TracingMaxBodyBytes = v },
		fromFileAlways()),
	// Backend logging defaults on in single mode; a persisted false is the
	// UI toggle-off and must win over that default on restart. No env/CLI
	// source exists, so the file is authoritative.
	field("enable_backend_logging",
		func(s *RuntimeSettings) **bool { return &s.EnableBackendLogging },
		func(o *ApplicationConfig) bool { return o.EnableBackendLogging },
		func(o *ApplicationConfig, v bool) { o.EnableBackendLogging = v },
		fromFileAlways()),

	// Security / CORS. The "csrf" wire field carries DisableCSRF (historic
	// naming kept for API compatibility).
	field("cors",
		func(s *RuntimeSettings) **bool { return &s.CORS },
		func(o *ApplicationConfig) bool { return o.CORS },
		func(o *ApplicationConfig, v bool) { o.CORS = v }),
	field("csrf",
		func(s *RuntimeSettings) **bool { return &s.CSRF },
		func(o *ApplicationConfig) bool { return o.DisableCSRF },
		func(o *ApplicationConfig, v bool) { o.DisableCSRF = v }),
	field("cors_allow_origins",
		func(s *RuntimeSettings) **string { return &s.CORSAllowOrigins },
		func(o *ApplicationConfig) string { return o.CORSAllowOrigins },
		func(o *ApplicationConfig, v string) { o.CORSAllowOrigins = v }),

	// P2P.
	field("p2p_token",
		func(s *RuntimeSettings) **string { return &s.P2PToken },
		func(o *ApplicationConfig) string { return o.P2PToken },
		func(o *ApplicationConfig, v string) { o.P2PToken = v }),
	field("p2p_network_id",
		func(s *RuntimeSettings) **string { return &s.P2PNetworkID },
		func(o *ApplicationConfig) string { return o.P2PNetworkID },
		func(o *ApplicationConfig, v string) { o.P2PNetworkID = v }),
	field("federated",
		func(s *RuntimeSettings) **bool { return &s.Federated },
		func(o *ApplicationConfig) bool { return o.Federated },
		func(o *ApplicationConfig, v bool) { o.Federated = v }),

	// Galleries. Gallery is comparable (string fields + a pointer), so
	// slices.Equal gives element-wise comparison against the baseline's
	// default gallery list.
	fieldEq("galleries",
		func(s *RuntimeSettings) **[]Gallery { return &s.Galleries },
		func(o *ApplicationConfig) []Gallery { return o.Galleries },
		func(o *ApplicationConfig, v []Gallery) { o.Galleries = v },
		slices.Equal),
	fieldEq("backend_galleries",
		func(s *RuntimeSettings) **[]Gallery { return &s.BackendGalleries },
		func(o *ApplicationConfig) []Gallery { return o.BackendGalleries },
		func(o *ApplicationConfig, v []Gallery) { o.BackendGalleries = v },
		slices.Equal),
	field("autoload_galleries",
		func(s *RuntimeSettings) **bool { return &s.AutoloadGalleries },
		func(o *ApplicationConfig) bool { return o.AutoloadGalleries },
		func(o *ApplicationConfig, v bool) { o.AutoloadGalleries = v }),
	field("autoload_backend_galleries",
		func(s *RuntimeSettings) **bool { return &s.AutoloadBackendGalleries },
		func(o *ApplicationConfig) bool { return o.AutoloadBackendGalleries },
		func(o *ApplicationConfig, v bool) { o.AutoloadBackendGalleries = v }),

	// API keys: echoed for the UI, but the apply loops never touch them.
	// The settings endpoint and the file watcher own the env+runtime merge
	// (MergeAPIKeys) because env keys must always survive.
	{
		jsonNames:    []string{"api_keys"},
		snapshotOnly: true,
		snapshot: func(o *ApplicationConfig, s *RuntimeSettings) {
			keys := o.ApiKeys
			s.ApiKeys = &keys
		},
		isSet: func(s *RuntimeSettings) bool { return s.ApiKeys != nil },
	},

	field("agent_job_retention_days",
		func(s *RuntimeSettings) **int { return &s.AgentJobRetentionDays },
		func(o *ApplicationConfig) int { return o.AgentJobRetentionDays },
		func(o *ApplicationConfig, v int) { o.AgentJobRetentionDays = v }),

	// Open Responses TTL: "0" or "" mean "no expiration" on the wire.
	{
		jsonNames: []string{"open_responses_store_ttl"},
		snapshot: func(o *ApplicationConfig, s *RuntimeSettings) {
			v := "0"
			if o.OpenResponsesStoreTTL > 0 {
				v = o.OpenResponsesStoreTTL.String()
			}
			s.OpenResponsesStoreTTL = &v
		},
		isSet: func(s *RuntimeSettings) bool { return s.OpenResponsesStoreTTL != nil },
		apply: func(o *ApplicationConfig, s *RuntimeSettings) bool {
			v := *s.OpenResponsesStoreTTL
			if v == "0" || v == "" {
				o.OpenResponsesStoreTTL = 0
				return true
			}
			d, err := time.ParseDuration(v)
			if err != nil {
				xlog.Warn("invalid open_responses_store_ttl in runtime settings", "value", v, "error", err)
				return false
			}
			o.OpenResponsesStoreTTL = d
			return true
		},
		envSet: func(cur, base *ApplicationConfig) bool {
			return cur.OpenResponsesStoreTTL != base.OpenResponsesStoreTTL
		},
	},

	// Agent Pool.
	field("agent_pool_enabled",
		func(s *RuntimeSettings) **bool { return &s.AgentPoolEnabled },
		func(o *ApplicationConfig) bool { return o.AgentPool.Enabled },
		func(o *ApplicationConfig, v bool) { o.AgentPool.Enabled = v },
		restartRequired()),
	field("agent_pool_default_model",
		func(s *RuntimeSettings) **string { return &s.AgentPoolDefaultModel },
		func(o *ApplicationConfig) string { return o.AgentPool.DefaultModel },
		func(o *ApplicationConfig, v string) { o.AgentPool.DefaultModel = v },
		restartRequired()),
	field("agent_pool_embedding_model",
		func(s *RuntimeSettings) **string { return &s.AgentPoolEmbeddingModel },
		func(o *ApplicationConfig) string { return o.AgentPool.EmbeddingModel },
		func(o *ApplicationConfig, v string) { o.AgentPool.EmbeddingModel = v },
		restartRequired()),
	field("agent_pool_max_chunking_size",
		func(s *RuntimeSettings) **int { return &s.AgentPoolMaxChunkingSize },
		func(o *ApplicationConfig) int { return o.AgentPool.MaxChunkingSize },
		func(o *ApplicationConfig, v int) { o.AgentPool.MaxChunkingSize = v },
		restartRequired()),
	field("agent_pool_chunk_overlap",
		func(s *RuntimeSettings) **int { return &s.AgentPoolChunkOverlap },
		func(o *ApplicationConfig) int { return o.AgentPool.ChunkOverlap },
		func(o *ApplicationConfig, v int) { o.AgentPool.ChunkOverlap = v },
		restartRequired()),
	field("agent_pool_enable_logs",
		func(s *RuntimeSettings) **bool { return &s.AgentPoolEnableLogs },
		func(o *ApplicationConfig) bool { return o.AgentPool.EnableLogs },
		func(o *ApplicationConfig, v bool) { o.AgentPool.EnableLogs = v },
		restartRequired()),
	field("agent_pool_collection_db_path",
		func(s *RuntimeSettings) **string { return &s.AgentPoolCollectionDBPath },
		func(o *ApplicationConfig) string { return o.AgentPool.CollectionDBPath },
		func(o *ApplicationConfig, v string) { o.AgentPool.CollectionDBPath = v },
		restartRequired()),
	field("agent_pool_vector_engine",
		func(s *RuntimeSettings) **string { return &s.AgentPoolVectorEngine },
		func(o *ApplicationConfig) string { return o.AgentPool.VectorEngine },
		func(o *ApplicationConfig, v string) { o.AgentPool.VectorEngine = v },
		restartRequired()),
	field("agent_pool_database_url",
		func(s *RuntimeSettings) **string { return &s.AgentPoolDatabaseURL },
		func(o *ApplicationConfig) string { return o.AgentPool.DatabaseURL },
		func(o *ApplicationConfig, v string) { o.AgentPool.DatabaseURL = v },
		restartRequired()),
	field("agent_pool_agent_hub_url",
		func(s *RuntimeSettings) **string { return &s.AgentPoolAgentHubURL },
		func(o *ApplicationConfig) string { return o.AgentPool.AgentHubURL },
		func(o *ApplicationConfig, v string) { o.AgentPool.AgentHubURL = v },
		restartRequired()),

	// LocalAI Assistant: stored as the negation for UI clarity.
	field("localai_assistant_enabled",
		func(s *RuntimeSettings) **bool { return &s.LocalAIAssistantEnabled },
		func(o *ApplicationConfig) bool { return !o.DisableLocalAIAssistant },
		func(o *ApplicationConfig, v bool) { o.DisableLocalAIAssistant = !v }),

	// Branding: the file is the only source (no env/CLI), so it is always
	// authoritative. Without fromFileAlways a restart silently drops the
	// configured instance name and asset filenames.
	field("instance_name",
		func(s *RuntimeSettings) **string { return &s.InstanceName },
		func(o *ApplicationConfig) string { return o.Branding.InstanceName },
		func(o *ApplicationConfig, v string) { o.Branding.InstanceName = v },
		fromFileAlways()),
	field("instance_tagline",
		func(s *RuntimeSettings) **string { return &s.InstanceTagline },
		func(o *ApplicationConfig) string { return o.Branding.InstanceTagline },
		func(o *ApplicationConfig, v string) { o.Branding.InstanceTagline = v },
		fromFileAlways()),
	field("logo_file",
		func(s *RuntimeSettings) **string { return &s.LogoFile },
		func(o *ApplicationConfig) string { return o.Branding.LogoFile },
		func(o *ApplicationConfig, v string) { o.Branding.LogoFile = v },
		fromFileAlways()),
	field("logo_horizontal_file",
		func(s *RuntimeSettings) **string { return &s.LogoHorizontalFile },
		func(o *ApplicationConfig) string { return o.Branding.LogoHorizontalFile },
		func(o *ApplicationConfig, v string) { o.Branding.LogoHorizontalFile = v },
		fromFileAlways()),
	field("favicon_file",
		func(s *RuntimeSettings) **string { return &s.FaviconFile },
		func(o *ApplicationConfig) string { return o.Branding.FaviconFile },
		func(o *ApplicationConfig, v string) { o.Branding.FaviconFile = v },
		fromFileAlways()),

	field("mitm_listen",
		func(s *RuntimeSettings) **string { return &s.MITMListen },
		func(o *ApplicationConfig) string { return o.MITMListen },
		func(o *ApplicationConfig, v string) { o.MITMListen = v }),

	// PII default detectors: cloned on both directions so callers can't
	// alias the live config's slice.
	fieldEq("pii_default_detectors",
		func(s *RuntimeSettings) **[]string { return &s.PIIDefaultDetectors },
		func(o *ApplicationConfig) []string { return append([]string(nil), o.PIIDefaultDetectors...) },
		func(o *ApplicationConfig, v []string) { o.PIIDefaultDetectors = append([]string(nil), v...) },
		slices.Equal),
}
