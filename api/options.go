package api

import (
	"context"
	"embed"

	model "github.com/go-skynet/LocalAI/pkg/model"
)

type Option struct {
	context                         context.Context
	configFile                      string
	loader                          *model.ModelLoader
	uploadLimitMB, threads, ctxSize int
	f16                             bool
	debug, disableMessage           bool
	imageDir                        string
	cors                            bool
	preloadJSONModels               string
	preloadModelsFromPath           string
	corsAllowOrigins                string

	backendAssets     embed.FS
	assetsDestination string
}

type AppOption func(*Option)

func newOptions(o ...AppOption) *Option {
	opt := &Option{
		context:        context.Background(),
		uploadLimitMB:  15,
		threads:        1,
		ctxSize:        512,
		debug:          true,
		disableMessage: true,
	}
	for _, oo := range o {
		oo(opt)
	}
	return opt
}

func WithCors(b bool) AppOption {
	return func(o *Option) {
		o.cors = b
	}
}

func WithCorsAllowOrigins(b string) AppOption {
	return func(o *Option) {
		o.corsAllowOrigins = b
	}
}

func WithBackendAssetsOutput(out string) AppOption {
	return func(o *Option) {
		o.assetsDestination = out
	}
}

func WithBackendAssets(f embed.FS) AppOption {
	return func(o *Option) {
		o.backendAssets = f
	}
}

func WithContext(ctx context.Context) AppOption {
	return func(o *Option) {
		o.context = ctx
	}
}

func WithYAMLConfigPreload(configFile string) AppOption {
	return func(o *Option) {
		o.preloadModelsFromPath = configFile
	}
}

func WithJSONStringPreload(configFile string) AppOption {
	return func(o *Option) {
		o.preloadJSONModels = configFile
	}
}
func WithConfigFile(configFile string) AppOption {
	return func(o *Option) {
		o.configFile = configFile
	}
}

func WithModelLoader(loader *model.ModelLoader) AppOption {
	return func(o *Option) {
		o.loader = loader
	}
}

func WithUploadLimitMB(limit int) AppOption {
	return func(o *Option) {
		o.uploadLimitMB = limit
	}
}

func WithThreads(threads int) AppOption {
	return func(o *Option) {
		o.threads = threads
	}
}

func WithContextSize(ctxSize int) AppOption {
	return func(o *Option) {
		o.ctxSize = ctxSize
	}
}

func WithF16(f16 bool) AppOption {
	return func(o *Option) {
		o.f16 = f16
	}
}

func WithDebug(debug bool) AppOption {
	return func(o *Option) {
		o.debug = debug
	}
}

func WithDisableMessage(disableMessage bool) AppOption {
	return func(o *Option) {
		o.disableMessage = disableMessage
	}
}

func WithImageDir(imageDir string) AppOption {
	return func(o *Option) {
		o.imageDir = imageDir
	}
}
