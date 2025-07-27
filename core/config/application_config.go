package config

import (
	"context"
	"encoding/json"
	"regexp"
	"time"

	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/rs/zerolog/log"
)

type ApplicationConfig struct {
	Context                             context.Context
	ConfigFile                          string
	ModelPath                           string
	BackendsPath                        string
	ExternalBackends                    []string
	UploadLimitMB, Threads, ContextSize int
	F16                                 bool
	Debug                               bool
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

	SingleBackend           bool
	ParallelBackendRequests bool

	WatchDogIdle bool
	WatchDogBusy bool
	WatchDog     bool

	ModelsURL []string

	WatchDogBusyTimeout, WatchDogIdleTimeout time.Duration

	MachineTag string
}

type AppOption func(*ApplicationConfig)

func NewApplicationConfig(o ...AppOption) *ApplicationConfig {
	opt := &ApplicationConfig{
		Context:       context.Background(),
		UploadLimitMB: 15,
		ContextSize:   512,
		Debug:         true,
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

func WithModelPath(path string) AppOption {
	return func(o *ApplicationConfig) {
		o.ModelPath = path
	}
}

func WithBackendsPath(path string) AppOption {
	return func(o *ApplicationConfig) {
		o.BackendsPath = path
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

var EnableSingleBackend = func(o *ApplicationConfig) {
	o.SingleBackend = true
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
			log.Error().Err(err).Msg("failed loading galleries")
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
			log.Error().Err(err).Msg("failed loading galleries")
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
				log.Warn().Err(err).Str("regex", epr).Msg("Error while compiling HTTP Get Exemption regex, skipping this entry.")
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
		ModelPath(o.ModelPath),
	}
}

// func WithMetrics(meter *metrics.Metrics) AppOption {
// 	return func(o *StartupOptions) {
// 		o.Metrics = meter
// 	}
// }
