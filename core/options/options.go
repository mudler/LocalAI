package options

import (
	"context"
	"embed"
	"encoding/json"
	"time"

	"github.com/go-skynet/LocalAI/metrics"
	"github.com/go-skynet/LocalAI/pkg/gallery"
	model "github.com/go-skynet/LocalAI/pkg/model"
	"github.com/rs/zerolog/log"
)

type Option struct {
	Context                             context.Context
	ConfigFile                          string
	Loader                              *model.ModelLoader
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
	Metrics                             *metrics.Metrics

	ModelLibraryURL string

	Galleries []gallery.Gallery

	BackendAssets     embed.FS
	AssetsDestination string

	ExternalGRPCBackends map[string]string

	AutoloadGalleries bool

	SingleBackend           bool
	ParallelBackendRequests bool

	WatchDogIdle bool
	WatchDogBusy bool
	WatchDog     bool

	ModelsURL []string

	WatchDogBusyTimeout, WatchDogIdleTimeout time.Duration
}

type AppOption func(*Option)

func NewOptions(o ...AppOption) *Option {
	opt := &Option{
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

func WithModelsURL(urls ...string) AppOption {
	return func(o *Option) {
		o.ModelsURL = urls
	}
}

func WithCors(b bool) AppOption {
	return func(o *Option) {
		o.CORS = b
	}
}

func WithModelLibraryURL(url string) AppOption {
	return func(o *Option) {
		o.ModelLibraryURL = url
	}
}

var EnableWatchDog = func(o *Option) {
	o.WatchDog = true
}

var EnableWatchDogIdleCheck = func(o *Option) {
	o.WatchDog = true
	o.WatchDogIdle = true
}

var EnableWatchDogBusyCheck = func(o *Option) {
	o.WatchDog = true
	o.WatchDogBusy = true
}

func SetWatchDogBusyTimeout(t time.Duration) AppOption {
	return func(o *Option) {
		o.WatchDogBusyTimeout = t
	}
}

func SetWatchDogIdleTimeout(t time.Duration) AppOption {
	return func(o *Option) {
		o.WatchDogIdleTimeout = t
	}
}

var EnableSingleBackend = func(o *Option) {
	o.SingleBackend = true
}

var EnableParallelBackendRequests = func(o *Option) {
	o.ParallelBackendRequests = true
}

var EnableGalleriesAutoload = func(o *Option) {
	o.AutoloadGalleries = true
}

func WithExternalBackend(name string, uri string) AppOption {
	return func(o *Option) {
		if o.ExternalGRPCBackends == nil {
			o.ExternalGRPCBackends = make(map[string]string)
		}
		o.ExternalGRPCBackends[name] = uri
	}
}

func WithCorsAllowOrigins(b string) AppOption {
	return func(o *Option) {
		o.CORSAllowOrigins = b
	}
}

func WithBackendAssetsOutput(out string) AppOption {
	return func(o *Option) {
		o.AssetsDestination = out
	}
}

func WithBackendAssets(f embed.FS) AppOption {
	return func(o *Option) {
		o.BackendAssets = f
	}
}

func WithStringGalleries(galls string) AppOption {
	return func(o *Option) {
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
	return func(o *Option) {
		o.Galleries = append(o.Galleries, galleries...)
	}
}

func WithContext(ctx context.Context) AppOption {
	return func(o *Option) {
		o.Context = ctx
	}
}

func WithYAMLConfigPreload(configFile string) AppOption {
	return func(o *Option) {
		o.PreloadModelsFromPath = configFile
	}
}

func WithJSONStringPreload(configFile string) AppOption {
	return func(o *Option) {
		o.PreloadJSONModels = configFile
	}
}
func WithConfigFile(configFile string) AppOption {
	return func(o *Option) {
		o.ConfigFile = configFile
	}
}

func WithModelLoader(loader *model.ModelLoader) AppOption {
	return func(o *Option) {
		o.Loader = loader
	}
}

func WithUploadLimitMB(limit int) AppOption {
	return func(o *Option) {
		o.UploadLimitMB = limit
	}
}

func WithThreads(threads int) AppOption {
	return func(o *Option) {
		o.Threads = threads
	}
}

func WithContextSize(ctxSize int) AppOption {
	return func(o *Option) {
		o.ContextSize = ctxSize
	}
}

func WithF16(f16 bool) AppOption {
	return func(o *Option) {
		o.F16 = f16
	}
}

func WithDebug(debug bool) AppOption {
	return func(o *Option) {
		o.Debug = debug
	}
}

func WithDisableMessage(disableMessage bool) AppOption {
	return func(o *Option) {
		o.DisableMessage = disableMessage
	}
}

func WithAudioDir(audioDir string) AppOption {
	return func(o *Option) {
		o.AudioDir = audioDir
	}
}

func WithImageDir(imageDir string) AppOption {
	return func(o *Option) {
		o.ImageDir = imageDir
	}
}

func WithApiKeys(apiKeys []string) AppOption {
	return func(o *Option) {
		o.ApiKeys = apiKeys
	}
}

func WithMetrics(meter *metrics.Metrics) AppOption {
	return func(o *Option) {
		o.Metrics = meter
	}
}
