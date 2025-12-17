package config

import (
	"context"
	"encoding/json"
	"regexp"
	"time"

	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/mudler/xlog"
)

type ApplicationConfig struct {
	Context                             context.Context
	ConfigFile                          string
	SystemState                         *system.SystemState
	ExternalBackends                    []string
	UploadLimitMB, Threads, ContextSize int
	F16                                 bool
	Debug                               bool
	EnableTracing                       bool
	TracingMaxItems                     int
	GeneratedContentDir                 string

	UploadDir string

	DynamicConfigsDir             string
	DynamicConfigsDirPollInterval time.Duration
	CORS                          bool
	CSRF                          bool
	PreloadJSONModels             string
	PreloadModelsFromPath         string
	CORSAllowOrigins              string
	ApiKeys                       []string
	P2PToken                      string
	P2PNetworkID                  string
	Federated                     bool

	DisableWebUI                       bool
	EnforcePredownloadScans            bool
	OpaqueErrors                       bool
	UseSubtleKeyComparison             bool
	DisableApiKeyRequirementForHttpGet bool
	DisableMetrics                     bool
	HttpGetExemptedEndpoints           []*regexp.Regexp
	DisableGalleryEndpoint             bool
	LoadToMemory                       []string

	Galleries        []Gallery
	BackendGalleries []Gallery

	ExternalGRPCBackends map[string]string

	AutoloadGalleries, AutoloadBackendGalleries bool

	SingleBackend           bool // Deprecated: use MaxActiveBackends = 1 instead
	MaxActiveBackends       int  // Maximum number of active backends (0 = unlimited, 1 = single backend mode)
	ParallelBackendRequests bool

	WatchDogIdle bool
	WatchDogBusy bool
	WatchDog     bool

	// Memory Reclaimer settings (works with GPU if available, otherwise RAM)
	MemoryReclaimerEnabled   bool    // Enable memory threshold monitoring
	MemoryReclaimerThreshold float64 // Threshold 0.0-1.0 (e.g., 0.95 = 95%)

	// Eviction settings
	ForceEvictionWhenBusy    bool          // Force eviction even when models have active API calls (default: false for safety)
	LRUEvictionMaxRetries    int           // Maximum number of retries when waiting for busy models to become idle (default: 30)
	LRUEvictionRetryInterval time.Duration // Interval between retries when waiting for busy models (default: 1s)

	ModelsURL []string

	WatchDogBusyTimeout, WatchDogIdleTimeout time.Duration
	WatchDogInterval                         time.Duration // Interval between watchdog checks

	MachineTag string

	APIAddress string

	TunnelCallback func(tunnels []string)

	DisableRuntimeSettings bool

	AgentJobRetentionDays int // Default: 30 days

	PathWithoutAuth []string
}

type AppOption func(*ApplicationConfig)

func NewApplicationConfig(o ...AppOption) *ApplicationConfig {
	opt := &ApplicationConfig{
		Context:                  context.Background(),
		UploadLimitMB:            15,
		Debug:                    true,
		AgentJobRetentionDays:    30,              // Default: 30 days
		LRUEvictionMaxRetries:    30,              // Default: 30 retries
		LRUEvictionRetryInterval: 1 * time.Second, // Default: 1 second
		TracingMaxItems:       1024,
		PathWithoutAuth: []string{
			"/static/",
			"/generated-audio/",
			"/generated-images/",
			"/generated-videos/",
			"/favicon.svg",
			"/readyz",
			"/healthz",
		},
	}
	for _, oo := range o {
		oo(opt)
	}
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

func WithCsrf(b bool) AppOption {
	return func(o *ApplicationConfig) {
		o.CSRF = b
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

var EnableWatchDogIdleCheck = func(o *ApplicationConfig) {
	o.WatchDog = true
	o.WatchDogIdle = true
}

var DisableGalleryEndpoint = func(o *ApplicationConfig) {
	o.DisableGalleryEndpoint = true
}

var EnableWatchDogBusyCheck = func(o *ApplicationConfig) {
	o.WatchDog = true
	o.WatchDogBusy = true
}

var DisableWebUI = func(o *ApplicationConfig) {
	o.DisableWebUI = true
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

// WithForceEvictionWhenBusy sets whether to force eviction even when models have active API calls
func WithForceEvictionWhenBusy(enabled bool) AppOption {
	return func(o *ApplicationConfig) {
		o.ForceEvictionWhenBusy = enabled
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

var EnableParallelBackendRequests = func(o *ApplicationConfig) {
	o.ParallelBackendRequests = true
}

var EnableGalleriesAutoload = func(o *ApplicationConfig) {
	o.AutoloadGalleries = true
}

var EnableBackendGalleriesAutoload = func(o *ApplicationConfig) {
	o.AutoloadBackendGalleries = true
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

func WithThreads(threads int) AppOption {
	return func(o *ApplicationConfig) {
		if threads == 0 { // 0 is not allowed
			threads = xsysinfo.CPUPhysicalCores()
		}
		o.Threads = threads
	}
}

func WithContextSize(ctxSize int) AppOption {
	return func(o *ApplicationConfig) {
		o.ContextSize = ctxSize
	}
}

func WithTunnelCallback(callback func(tunnels []string)) AppOption {
	return func(o *ApplicationConfig) {
		o.TunnelCallback = callback
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

func WithAgentJobRetentionDays(days int) AppOption {
	return func(o *ApplicationConfig) {
		o.AgentJobRetentionDays = days
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
	}
}

// ToRuntimeSettings converts ApplicationConfig to RuntimeSettings for API responses and JSON serialization.
// This provides a single source of truth - ApplicationConfig holds the live values,
// and this method creates a RuntimeSettings snapshot for external consumption.
func (o *ApplicationConfig) ToRuntimeSettings() RuntimeSettings {
	// Create local copies for pointer fields
	watchdogEnabled := o.WatchDog
	watchdogIdle := o.WatchDogIdle
	watchdogBusy := o.WatchDogBusy
	singleBackend := o.SingleBackend
	maxActiveBackends := o.MaxActiveBackends
	parallelBackendRequests := o.ParallelBackendRequests
	memoryReclaimerEnabled := o.MemoryReclaimerEnabled
	memoryReclaimerThreshold := o.MemoryReclaimerThreshold
	forceEvictionWhenBusy := o.ForceEvictionWhenBusy
	lruEvictionMaxRetries := o.LRUEvictionMaxRetries
	threads := o.Threads
	contextSize := o.ContextSize
	f16 := o.F16
	debug := o.Debug
	tracingMaxItems := o.TracingMaxItems
	enableTracing := o.EnableTracing
	cors := o.CORS
	csrf := o.CSRF
	corsAllowOrigins := o.CORSAllowOrigins
	p2pToken := o.P2PToken
	p2pNetworkID := o.P2PNetworkID
	federated := o.Federated
	galleries := o.Galleries
	backendGalleries := o.BackendGalleries
	autoloadGalleries := o.AutoloadGalleries
	autoloadBackendGalleries := o.AutoloadBackendGalleries
	apiKeys := o.ApiKeys
	agentJobRetentionDays := o.AgentJobRetentionDays

	// Format timeouts as strings
	var idleTimeout, busyTimeout, watchdogInterval string
	if o.WatchDogIdleTimeout > 0 {
		idleTimeout = o.WatchDogIdleTimeout.String()
	} else {
		idleTimeout = "15m" // default
	}
	if o.WatchDogBusyTimeout > 0 {
		busyTimeout = o.WatchDogBusyTimeout.String()
	} else {
		busyTimeout = "5m" // default
	}
	if o.WatchDogInterval > 0 {
		watchdogInterval = o.WatchDogInterval.String()
	} else {
		watchdogInterval = "2s" // default
	}
	var lruEvictionRetryInterval string
	if o.LRUEvictionRetryInterval > 0 {
		lruEvictionRetryInterval = o.LRUEvictionRetryInterval.String()
	} else {
		lruEvictionRetryInterval = "1s" // default
	}

	return RuntimeSettings{
		WatchdogEnabled:          &watchdogEnabled,
		WatchdogIdleEnabled:      &watchdogIdle,
		WatchdogBusyEnabled:      &watchdogBusy,
		WatchdogIdleTimeout:      &idleTimeout,
		WatchdogBusyTimeout:      &busyTimeout,
		WatchdogInterval:         &watchdogInterval,
		SingleBackend:            &singleBackend,
		MaxActiveBackends:        &maxActiveBackends,
		ParallelBackendRequests:  &parallelBackendRequests,
		MemoryReclaimerEnabled:   &memoryReclaimerEnabled,
		MemoryReclaimerThreshold: &memoryReclaimerThreshold,
		ForceEvictionWhenBusy:    &forceEvictionWhenBusy,
		LRUEvictionMaxRetries:    &lruEvictionMaxRetries,
		LRUEvictionRetryInterval: &lruEvictionRetryInterval,
		Threads:                  &threads,
		ContextSize:              &contextSize,
		F16:                      &f16,
		Debug:                    &debug,
		TracingMaxItems:          &tracingMaxItems,
		EnableTracing:            &enableTracing,
		CORS:                     &cors,
		CSRF:                     &csrf,
		CORSAllowOrigins:         &corsAllowOrigins,
		P2PToken:                 &p2pToken,
		P2PNetworkID:             &p2pNetworkID,
		Federated:                &federated,
		Galleries:                &galleries,
		BackendGalleries:         &backendGalleries,
		AutoloadGalleries:        &autoloadGalleries,
		AutoloadBackendGalleries: &autoloadBackendGalleries,
		ApiKeys:                  &apiKeys,
		AgentJobRetentionDays:    &agentJobRetentionDays,
	}
}

// ApplyRuntimeSettings applies RuntimeSettings to ApplicationConfig.
// Only non-nil fields in RuntimeSettings are applied.
// Returns true if watchdog-related settings changed (requiring restart).
func (o *ApplicationConfig) ApplyRuntimeSettings(settings *RuntimeSettings) (requireRestart bool) {
	if settings == nil {
		return false
	}

	if settings.WatchdogEnabled != nil {
		o.WatchDog = *settings.WatchdogEnabled
		requireRestart = true
	}
	if settings.WatchdogIdleEnabled != nil {
		o.WatchDogIdle = *settings.WatchdogIdleEnabled
		if o.WatchDogIdle {
			o.WatchDog = true
		}
		requireRestart = true
	}
	if settings.WatchdogBusyEnabled != nil {
		o.WatchDogBusy = *settings.WatchdogBusyEnabled
		if o.WatchDogBusy {
			o.WatchDog = true
		}
		requireRestart = true
	}
	if settings.WatchdogIdleTimeout != nil {
		if dur, err := time.ParseDuration(*settings.WatchdogIdleTimeout); err == nil {
			o.WatchDogIdleTimeout = dur
			requireRestart = true
		}
	}
	if settings.WatchdogBusyTimeout != nil {
		if dur, err := time.ParseDuration(*settings.WatchdogBusyTimeout); err == nil {
			o.WatchDogBusyTimeout = dur
			requireRestart = true
		}
	}
	if settings.WatchdogInterval != nil {
		if dur, err := time.ParseDuration(*settings.WatchdogInterval); err == nil {
			o.WatchDogInterval = dur
			requireRestart = true
		}
	}
	if settings.MaxActiveBackends != nil {
		o.MaxActiveBackends = *settings.MaxActiveBackends
		o.SingleBackend = (*settings.MaxActiveBackends == 1)
		requireRestart = true
	} else if settings.SingleBackend != nil {
		o.SingleBackend = *settings.SingleBackend
		if *settings.SingleBackend {
			o.MaxActiveBackends = 1
		} else {
			o.MaxActiveBackends = 0
		}
		requireRestart = true
	}
	if settings.ParallelBackendRequests != nil {
		o.ParallelBackendRequests = *settings.ParallelBackendRequests
	}
	if settings.MemoryReclaimerEnabled != nil {
		o.MemoryReclaimerEnabled = *settings.MemoryReclaimerEnabled
		if *settings.MemoryReclaimerEnabled {
			o.WatchDog = true
		}
		requireRestart = true
	}
	if settings.MemoryReclaimerThreshold != nil {
		if *settings.MemoryReclaimerThreshold > 0 && *settings.MemoryReclaimerThreshold <= 1.0 {
			o.MemoryReclaimerThreshold = *settings.MemoryReclaimerThreshold
			requireRestart = true
		}
	}
	if settings.ForceEvictionWhenBusy != nil {
		o.ForceEvictionWhenBusy = *settings.ForceEvictionWhenBusy
		// This setting doesn't require restart, can be updated dynamically
	}
	if settings.LRUEvictionMaxRetries != nil {
		o.LRUEvictionMaxRetries = *settings.LRUEvictionMaxRetries
		// This setting doesn't require restart, can be updated dynamically
	}
	if settings.LRUEvictionRetryInterval != nil {
		if dur, err := time.ParseDuration(*settings.LRUEvictionRetryInterval); err == nil {
			o.LRUEvictionRetryInterval = dur
			// This setting doesn't require restart, can be updated dynamically
		}
	}
	if settings.Threads != nil {
		o.Threads = *settings.Threads
	}
	if settings.ContextSize != nil {
		o.ContextSize = *settings.ContextSize
	}
	if settings.F16 != nil {
		o.F16 = *settings.F16
	}
	if settings.Debug != nil {
		o.Debug = *settings.Debug
	}
	if settings.EnableTracing != nil {
		o.EnableTracing = *settings.EnableTracing
	}
	if settings.TracingMaxItems != nil {
		o.TracingMaxItems = *settings.TracingMaxItems
	}
	if settings.CORS != nil {
		o.CORS = *settings.CORS
	}
	if settings.CSRF != nil {
		o.CSRF = *settings.CSRF
	}
	if settings.CORSAllowOrigins != nil {
		o.CORSAllowOrigins = *settings.CORSAllowOrigins
	}
	if settings.P2PToken != nil {
		o.P2PToken = *settings.P2PToken
	}
	if settings.P2PNetworkID != nil {
		o.P2PNetworkID = *settings.P2PNetworkID
	}
	if settings.Federated != nil {
		o.Federated = *settings.Federated
	}
	if settings.Galleries != nil {
		o.Galleries = *settings.Galleries
	}
	if settings.BackendGalleries != nil {
		o.BackendGalleries = *settings.BackendGalleries
	}
	if settings.AutoloadGalleries != nil {
		o.AutoloadGalleries = *settings.AutoloadGalleries
	}
	if settings.AutoloadBackendGalleries != nil {
		o.AutoloadBackendGalleries = *settings.AutoloadBackendGalleries
	}
	if settings.AgentJobRetentionDays != nil {
		o.AgentJobRetentionDays = *settings.AgentJobRetentionDays
	}
	// Note: ApiKeys requires special handling (merging with startup keys) - handled in caller

	return requireRestart
}

// func WithMetrics(meter *metrics.Metrics) AppOption {
// 	return func(o *StartupOptions) {
// 		o.Metrics = meter
// 	}
// }
