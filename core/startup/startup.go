package startup

import (
	"fmt"
	"os"

	"github.com/mudler/LocalAI/core"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/assets"
	"github.com/mudler/LocalAI/pkg/library"
	"github.com/mudler/LocalAI/pkg/model"
	pkgStartup "github.com/mudler/LocalAI/pkg/startup"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/rs/zerolog/log"
)

func Startup(opts ...config.AppOption) (*config.BackendConfigLoader, *model.ModelLoader, *config.ApplicationConfig, error) {
	options := config.NewApplicationConfig(opts...)

	log.Info().Msgf("Starting LocalAI using %d threads, with models path: %s", options.Threads, options.ModelPath)
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
	if options.ModelPath == "" {
		return nil, nil, nil, fmt.Errorf("options.ModelPath cannot be empty")
	}
	err = os.MkdirAll(options.ModelPath, 0750)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("unable to create ModelPath: %q", err)
	}
	if options.ImageDir != "" {
		err := os.MkdirAll(options.ImageDir, 0750)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("unable to create ImageDir: %q", err)
		}
	}
	if options.AudioDir != "" {
		err := os.MkdirAll(options.AudioDir, 0750)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("unable to create AudioDir: %q", err)
		}
	}
	if options.UploadDir != "" {
		err := os.MkdirAll(options.UploadDir, 0750)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("unable to create UploadDir: %q", err)
		}
	}

	if err := pkgStartup.InstallModels(options.Galleries, options.ModelLibraryURL, options.ModelPath, options.EnforcePredownloadScans, nil, options.ModelsURL...); err != nil {
		log.Error().Err(err).Msg("error installing models")
	}

	cl := config.NewBackendConfigLoader(options.ModelPath)
	ml := model.NewModelLoader(options.ModelPath)

	configLoaderOpts := options.ToConfigLoaderOptions()

	if err := cl.LoadBackendConfigsFromPath(options.ModelPath, configLoaderOpts...); err != nil {
		log.Error().Err(err).Msg("error loading config files")
	}

	if options.ConfigFile != "" {
		if err := cl.LoadMultipleBackendConfigsSingleFile(options.ConfigFile, configLoaderOpts...); err != nil {
			log.Error().Err(err).Msg("error loading config file")
		}
	}

	if err := cl.Preload(options.ModelPath); err != nil {
		log.Error().Err(err).Msg("error downloading models")
	}

	if options.PreloadJSONModels != "" {
		if err := services.ApplyGalleryFromString(options.ModelPath, options.PreloadJSONModels, options.EnforcePredownloadScans, options.Galleries); err != nil {
			return nil, nil, nil, err
		}
	}

	if options.PreloadModelsFromPath != "" {
		if err := services.ApplyGalleryFromFile(options.ModelPath, options.PreloadModelsFromPath, options.EnforcePredownloadScans, options.Galleries); err != nil {
			return nil, nil, nil, err
		}
	}

	if options.Debug {
		for _, v := range cl.GetAllBackendConfigs() {
			log.Debug().Msgf("Model: %s (config: %+v)", v.Name, v)
		}
	}

	if options.AssetsDestination != "" {
		// Extract files from the embedded FS
		err := assets.ExtractFiles(options.BackendAssets, options.AssetsDestination)
		log.Debug().Msgf("Extracting backend assets files to %s", options.AssetsDestination)
		if err != nil {
			log.Warn().Msgf("Failed extracting backend assets files: %s (might be required for some backends to work properly, like gpt4all)", err)
		}
	}

	if options.LibPath != "" {
		// If there is a lib directory, set LD_LIBRARY_PATH to include it
		err := library.LoadExternal(options.LibPath)
		if err != nil {
			log.Error().Err(err).Str("LibPath", options.LibPath).Msg("Error while loading external libraries")
		}
	}

	// turn off any process that was started by GRPC if the context is canceled
	go func() {
		<-options.Context.Done()
		log.Debug().Msgf("Context canceled, shutting down")
		err := ml.StopAllGRPC()
		if err != nil {
			log.Error().Err(err).Msg("error while stopping all grpc backends")
		}
	}()

	if options.WatchDog {
		wd := model.NewWatchDog(
			ml,
			options.WatchDogBusyTimeout,
			options.WatchDogIdleTimeout,
			options.WatchDogBusy,
			options.WatchDogIdle)
		ml.SetWatchDog(wd)
		go wd.Run()
		go func() {
			<-options.Context.Done()
			log.Debug().Msgf("Context canceled, shutting down")
			wd.Shutdown()
		}()
	}

	// Watch the configuration directory
	startWatcher(options)

	log.Info().Msg("core/startup process completed!")
	return cl, ml, options, nil
}

func startWatcher(options *config.ApplicationConfig) {
	if options.DynamicConfigsDir == "" {
		// No need to start the watcher if the directory is not set
		return
	}

	if _, err := os.Stat(options.DynamicConfigsDir); err != nil {
		if os.IsNotExist(err) {
			// We try to create the directory if it does not exist and was specified
			if err := os.MkdirAll(options.DynamicConfigsDir, 0700); err != nil {
				log.Error().Err(err).Msg("failed creating DynamicConfigsDir")
			}
		} else {
			// something else happened, we log the error and don't start the watcher
			log.Error().Err(err).Msg("failed to read DynamicConfigsDir, watcher will not be started")
			return
		}
	}

	configHandler := newConfigFileHandler(options)
	if err := configHandler.Watch(); err != nil {
		log.Error().Err(err).Msg("failed creating watcher")
	}
}

// In Lieu of a proper DI framework, this function wires up the Application manually.
// This is in core/startup rather than core/state.go to keep package references clean!
func createApplication(appConfig *config.ApplicationConfig) *core.Application {
	app := &core.Application{
		ApplicationConfig:   appConfig,
		BackendConfigLoader: config.NewBackendConfigLoader(appConfig.ModelPath),
		ModelLoader:         model.NewModelLoader(appConfig.ModelPath),
	}

	var err error

	// app.EmbeddingsBackendService = backend.NewEmbeddingsBackendService(app.ModelLoader, app.BackendConfigLoader, app.ApplicationConfig)
	// app.ImageGenerationBackendService = backend.NewImageGenerationBackendService(app.ModelLoader, app.BackendConfigLoader, app.ApplicationConfig)
	// app.LLMBackendService = backend.NewLLMBackendService(app.ModelLoader, app.BackendConfigLoader, app.ApplicationConfig)
	// app.TranscriptionBackendService = backend.NewTranscriptionBackendService(app.ModelLoader, app.BackendConfigLoader, app.ApplicationConfig)
	// app.TextToSpeechBackendService = backend.NewTextToSpeechBackendService(app.ModelLoader, app.BackendConfigLoader, app.ApplicationConfig)

	app.BackendMonitorService = services.NewBackendMonitorService(app.ModelLoader, app.BackendConfigLoader, app.ApplicationConfig)
	app.GalleryService = services.NewGalleryService(app.ApplicationConfig)
	// app.OpenAIService = services.NewOpenAIService(app.ModelLoader, app.BackendConfigLoader, app.ApplicationConfig, app.LLMBackendService)

	app.LocalAIMetricsService, err = services.NewLocalAIMetricsService()
	if err != nil {
		log.Error().Err(err).Msg("encountered an error initializing metrics service, startup will continue but metrics will not be tracked.")
	}

	return app
}
