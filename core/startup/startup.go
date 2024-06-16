package startup

import (
	"fmt"
	"os"

	"github.com/go-skynet/LocalAI/core"
	"github.com/go-skynet/LocalAI/core/backend"
	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/services"
	"github.com/go-skynet/LocalAI/internal"
	"github.com/go-skynet/LocalAI/pkg/assets"
	"github.com/go-skynet/LocalAI/pkg/library"
	"github.com/go-skynet/LocalAI/pkg/model"
	pkgStartup "github.com/go-skynet/LocalAI/pkg/startup"
	"github.com/go-skynet/LocalAI/pkg/xsysinfo"
	"github.com/rs/zerolog/log"
)

func Startup(opts ...config.AppOption) (*core.Application, error) {
	appConfig := config.NewApplicationConfig(opts...)

	log.Info().Msgf("Starting LocalAI using %d threads, with models path: %s", appConfig.Threads, appConfig.ModelPath)
	log.Info().Msgf("LocalAI version: %s", internal.PrintableVersion())
	caps, err := xsysinfo.CPUCapabilities()
	if err == nil {
		log.Debug().Msgf("CPU capabilities: %v", caps)
	}
	gpus, err := xsysinfo.GPUs()
	if err == nil {
		log.Debug().Msgf("GPU count: %d", len(gpus))
		for _, gpu := range gpus {
			log.Debug().Msgf("GPU: %s", gpu.String())
		}
	}

	// Make sure directories exists
	if appConfig.ModelPath == "" {
		return nil, fmt.Errorf("options.ModelPath cannot be empty")
	}

	err = os.MkdirAll(appConfig.ModelPath, 0750)

	if err != nil {
		return nil, fmt.Errorf("unable to create ModelPath: %q", err)
	}
	if appConfig.ImageDir != "" {
		err := os.MkdirAll(appConfig.ImageDir, 0750)
		if err != nil {
			return nil, fmt.Errorf("unable to create ImageDir: %q", err)
		}
	}
	if appConfig.AudioDir != "" {
		err := os.MkdirAll(appConfig.AudioDir, 0750)
		if err != nil {
			return nil, fmt.Errorf("unable to create AudioDir: %q", err)
		}
	}
	if appConfig.UploadDir != "" {
		err := os.MkdirAll(appConfig.UploadDir, 0750)
		if err != nil {
			return nil, fmt.Errorf("unable to create UploadDir: %q", err)
		}
	}

	if err := pkgStartup.InstallModels(appConfig.Galleries, appConfig.ModelLibraryURL, appConfig.ModelPath, nil, appConfig.ModelsURL...); err != nil {
		log.Error().Err(err).Msg("error installing models")
	}

	app := createApplication(appConfig)

	configLoaderOpts := appConfig.ToConfigLoaderOptions()

	if err := app.BackendConfigLoader.LoadBackendConfigsFromPath(appConfig.ModelPath, configLoaderOpts...); err != nil {
		log.Error().Err(err).Msg("error loading config files")
	}

	if appConfig.ConfigFile != "" {
		if err := app.BackendConfigLoader.LoadMultipleBackendConfigsSingleFile(appConfig.ConfigFile, configLoaderOpts...); err != nil {
			log.Error().Err(err).Msg("error loading config file")
		}
	}

	if err := app.BackendConfigLoader.Preload(appConfig.ModelPath); err != nil {
		log.Error().Err(err).Msg("error downloading models")
	}

	if appConfig.PreloadJSONModels != "" {
		if err := services.ApplyGalleryFromString(appConfig.ModelPath, appConfig.PreloadJSONModels, appConfig.Galleries); err != nil {
			return nil, err
		}
	}

	if appConfig.PreloadModelsFromPath != "" {
		if err := services.ApplyGalleryFromFile(appConfig.ModelPath, appConfig.PreloadModelsFromPath, appConfig.Galleries); err != nil {
			return nil, err
		}
	}

	if appConfig.Debug {
		for _, v := range app.BackendConfigLoader.GetAllBackendConfigs() {
			log.Debug().Msgf("Model: %s (config: %+v)", v.Name, v)
		}
	}

	if appConfig.AssetsDestination != "" {
		// Extract files from the embedded FS
		err := assets.ExtractFiles(appConfig.BackendAssets, appConfig.AssetsDestination)
		log.Debug().Msgf("Extracting backend assets files to %s", appConfig.AssetsDestination)
		if err != nil {
			log.Warn().Msgf("Failed extracting backend assets files: %s (might be required for some backends to work properly, like gpt4all)", err)
		}
	}

	if options.LibPath != "" {
		// If there is a lib directory, set LD_LIBRARY_PATH to include it
		library.LoadExternal(options.LibPath)
	}

	// turn off any process that was started by GRPC if the context is canceled
	go func() {
		<-appConfig.Context.Done()
		log.Debug().Msgf("Context canceled, shutting down")
		err := app.ModelLoader.StopAllGRPC()
		if err != nil {
			log.Error().Err(err).Msg("error while stopping all grpc backends")
		}
	}()

	if appConfig.WatchDog {
		wd := model.NewWatchDog(
			app.ModelLoader,
			appConfig.WatchDogBusyTimeout,
			appConfig.WatchDogIdleTimeout,
			appConfig.WatchDogBusy,
			appConfig.WatchDogIdle)
		app.ModelLoader.SetWatchDog(wd)
		go wd.Run()
		go func() {
			<-appConfig.Context.Done()
			log.Debug().Msgf("Context canceled, shutting down")
			wd.Shutdown()
		}()
	}

	// Watch the configuration directory
	// If the directory does not exist, we don't watch it
	configHandler := newConfigFileHandler(appConfig)
	err = configHandler.Watch()
	if err != nil {
		log.Error().Err(err).Msg("error establishing configuration directory watcher")
	}

	log.Info().Msg("core/startup process completed!")
	return app, nil
}

// In Lieu of a proper DI framework, this function wires up the Application manually.
// This is in core/startup rather than core/state.go to keep package references clean!
func createApplication(appConfig *config.ApplicationConfig) *core.Application {
	app := &core.Application{
		ApplicationConfig:   appConfig,
		BackendConfigLoader: config.NewBackendConfigLoader(appConfig.ModelPath),
		ModelLoader:         model.NewModelLoader(appConfig.ModelPath),
		StoresLoader:        model.NewModelLoader(""),
	}

	var err error

	app.EmbeddingsBackendService = backend.NewEmbeddingsBackendService(app.ModelLoader, app.BackendConfigLoader, app.ApplicationConfig)
	// app.ImageGenerationBackendService = backend.NewImageGenerationBackendService(app.ModelLoader, app.BackendConfigLoader, app.ApplicationConfig)
	// app.LLMBackendService = backend.NewLLMBackendService(app.ModelLoader, app.BackendConfigLoader, app.ApplicationConfig)
	// app.TranscriptionBackendService = backend.NewTranscriptionBackendService(app.ModelLoader, app.BackendConfigLoader, app.ApplicationConfig)
	app.TextToSpeechBackendService = backend.NewTextToSpeechBackendService(app.ModelLoader, app.BackendConfigLoader, app.ApplicationConfig)
	// app.RerankBackendService = backend.NewRerankBackendService(app.ModelLoader, app.BackendConfigLoader, app.ApplicationConfig)

	app.BackendMonitorService = services.NewBackendMonitorService(app.ModelLoader, app.BackendConfigLoader, app.ApplicationConfig)

	app.GalleryService = services.NewGalleryService(app.ApplicationConfig)
	app.GalleryService.Start(app.ApplicationConfig.Context, app.BackendConfigLoader)

	app.ListModelsService = services.NewListModelsService(app.ModelLoader, app.BackendConfigLoader, app.ApplicationConfig)
	// app.OpenAIService = services.NewOpenAIService(app.ModelLoader, app.BackendConfigLoader, app.ApplicationConfig, app.LLMBackendService)

	app.LocalAIMetricsService, err = services.NewLocalAIMetricsService()
	if err != nil {
		log.Error().Err(err).Msg("encountered an error initializing metrics service, startup will continue but metrics will not be tracked.")
	}

	return app
}
