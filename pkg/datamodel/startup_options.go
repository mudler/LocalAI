package datamodel

import (
	"context"
	"embed"
	"encoding/json"
	"time"

	"github.com/go-skynet/LocalAI/pkg/gallery"
	"github.com/rs/zerolog/log"
)

type StartupOptions struct {
	Context                             context.Context
	ConfigFile                          string
	ModelPath                           string
	UploadLimitMB, Threads, ContextSize int
	F16                                 bool
	Debug, DisableMessage               bool
	ImageDir                            string
	AudioDir                            string
	CORS                                bool
	PreloadJSONModels                   string
	PreloadModelsFromPath               string
	CORSAllowOrigins                    string
	ApiKeys                             []string
	Metrics                             *LocalAIMetrics

	Galleries []gallery.Gallery

	BackendAssets     embed.FS
	AssetsDestination string

	ExternalGRPCBackends map[string]string

	AutoloadGalleries bool

	SingleBackend           bool
	ParallelBackendRequests bool

	WatchDogIdle                             bool
	WatchDogBusy                             bool
	WatchDog                                 bool
	WatchDogBusyTimeout, WatchDogIdleTimeout time.Duration

	LocalAIConfigDir string
}

type AppOption func(*StartupOptions)

func NewStartupOptions(o ...AppOption) *StartupOptions {
	opt := &StartupOptions{
		Context:        context.Background(),
		UploadLimitMB:  15,
		Threads:        1,
		ContextSize:    512,
		Debug:          true,
		DisableMessage: true,
	}
	for _, oo := range o {
		oo(opt)
	}
	return opt
}

func WithCors(b bool) AppOption {
	return func(o *StartupOptions) {
		o.CORS = b
	}
}

var EnableWatchDog = func(o *StartupOptions) {
	o.WatchDog = true
}

var EnableWatchDogIdleCheck = func(o *StartupOptions) {
	o.WatchDog = true
	o.WatchDogIdle = true
}

var EnableWatchDogBusyCheck = func(o *StartupOptions) {
	o.WatchDog = true
	o.WatchDogBusy = true
}

func SetWatchDogBusyTimeout(t time.Duration) AppOption {
	return func(o *StartupOptions) {
		o.WatchDogBusyTimeout = t
	}
}

func SetWatchDogIdleTimeout(t time.Duration) AppOption {
	return func(o *StartupOptions) {
		o.WatchDogIdleTimeout = t
	}
}

var EnableSingleBackend = func(o *StartupOptions) {
	o.SingleBackend = true
}

var EnableParallelBackendRequests = func(o *StartupOptions) {
	o.ParallelBackendRequests = true
}

var EnableGalleriesAutoload = func(o *StartupOptions) {
	o.AutoloadGalleries = true
}

func WithExternalBackend(name string, uri string) AppOption {
	return func(o *StartupOptions) {
		if o.ExternalGRPCBackends == nil {
			o.ExternalGRPCBackends = make(map[string]string)
		}
		o.ExternalGRPCBackends[name] = uri
	}
}

func WithCorsAllowOrigins(b string) AppOption {
	return func(o *StartupOptions) {
		o.CORSAllowOrigins = b
	}
}

func WithBackendAssetsOutput(out string) AppOption {
	return func(o *StartupOptions) {
		o.AssetsDestination = out
	}
}

func WithBackendAssets(f embed.FS) AppOption {
	return func(o *StartupOptions) {
		o.BackendAssets = f
	}
}

func WithStringGalleries(galls string) AppOption {
	return func(o *StartupOptions) {
		if galls == "" {
			log.Debug().Msgf("no galleries to load")
			o.Galleries = []gallery.Gallery{}
			return
		}
		var galleries []gallery.Gallery
		if err := json.Unmarshal([]byte(galls), &galleries); err != nil {
			log.Error().Msgf("failed loading galleries: %s", err.Error())
		}
		o.Galleries = append(o.Galleries, galleries...)
	}
}

func WithGalleries(galleries []gallery.Gallery) AppOption {
	return func(o *StartupOptions) {
		o.Galleries = append(o.Galleries, galleries...)
	}
}

func WithContext(ctx context.Context) AppOption {
	return func(o *StartupOptions) {
		o.Context = ctx
	}
}

func WithYAMLConfigPreload(configFile string) AppOption {
	return func(o *StartupOptions) {
		o.PreloadModelsFromPath = configFile
	}
}

func WithJSONStringPreload(configFile string) AppOption {
	return func(o *StartupOptions) {
		o.PreloadJSONModels = configFile
	}
}
func WithConfigFile(configFile string) AppOption {
	return func(o *StartupOptions) {
		o.ConfigFile = configFile
	}
}

func WithModelPath(path string) AppOption {
	return func(o *StartupOptions) {
		o.ModelPath = path
	}
}

func WithUploadLimitMB(limit int) AppOption {
	return func(o *StartupOptions) {
		o.UploadLimitMB = limit
	}
}

func WithThreads(threads int) AppOption {
	return func(o *StartupOptions) {
		o.Threads = threads
	}
}

func WithContextSize(ctxSize int) AppOption {
	return func(o *StartupOptions) {
		o.ContextSize = ctxSize
	}
}

func WithF16(f16 bool) AppOption {
	return func(o *StartupOptions) {
		o.F16 = f16
	}
}

func WithDebug(debug bool) AppOption {
	return func(o *StartupOptions) {
		o.Debug = debug
	}
}

func WithDisableMessage(disableMessage bool) AppOption {
	return func(o *StartupOptions) {
		o.DisableMessage = disableMessage
	}
}

func WithAudioDir(audioDir string) AppOption {
	return func(o *StartupOptions) {
		o.AudioDir = audioDir
	}
}

func WithImageDir(imageDir string) AppOption {
	return func(o *StartupOptions) {
		o.ImageDir = imageDir
	}
}

func WithApiKeys(apiKeys []string) AppOption {
	return func(o *StartupOptions) {
		o.ApiKeys = apiKeys
	}
}

func WithMetrics(metrics *LocalAIMetrics) AppOption {
	return func(o *StartupOptions) {
		o.Metrics = metrics
	}
}

func WithLocalAIConfigDir(configDir string) AppOption {
	return func(o *StartupOptions) {
		o.LocalAIConfigDir = configDir
	}
}
