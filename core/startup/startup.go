package startup

import (
	"fmt"
	"os"

	"github.com/go-skynet/LocalAI/core"
	"github.com/go-skynet/LocalAI/core/backend"
	"github.com/go-skynet/LocalAI/core/config"
	openaiendpoint "github.com/go-skynet/LocalAI/core/http/endpoints/openai" // TODO: This is dubious. Fix this when splitting assistant api up.
	"github.com/go-skynet/LocalAI/core/services"
	"github.com/go-skynet/LocalAI/internal"
	"github.com/go-skynet/LocalAI/pkg/assets"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/go-skynet/LocalAI/pkg/utils"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// (*config.BackendConfigLoader, *model.ModelLoader, *config.ApplicationConfig, error) {
func Startup(opts ...config.AppOption) (*core.Application, error) {
	options := config.NewApplicationConfig(opts...)

	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if options.Debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	log.Info().Msgf("Starting LocalAI using %d threads, with models path: %s", options.Threads, options.ModelPath)
	log.Info().Msgf("LocalAI version: %s", internal.PrintableVersion())

	// Make sure directories exists
	if options.ModelPath == "" {
		return nil, fmt.Errorf("options.ModelPath cannot be empty")
	}
	err := os.MkdirAll(options.ModelPath, 0755)
	if err != nil {
		return nil, fmt.Errorf("unable to create ModelPath: %q", err)
	}
	if options.ImageDir != "" {
		err := os.MkdirAll(options.ImageDir, 0755)
		if err != nil {
			return nil, fmt.Errorf("unable to create ImageDir: %q", err)
		}
	}
	if options.AudioDir != "" {
		err := os.MkdirAll(options.AudioDir, 0755)
		if err != nil {
			return nil, fmt.Errorf("unable to create AudioDir: %q", err)
		}
	}
	if options.UploadDir != "" {
		err := os.MkdirAll(options.UploadDir, 0755)
		if err != nil {
			return nil, fmt.Errorf("unable to create UploadDir: %q", err)
		}
	}
	if options.ConfigsDir != "" {
		err := os.MkdirAll(options.ConfigsDir, 0755)
		if err != nil {
			return nil, fmt.Errorf("unable to create ConfigsDir: %q", err)
		}
	}

	// Load config jsons
	utils.LoadConfig(options.UploadDir, openaiendpoint.UploadedFilesFile, &openaiendpoint.UploadedFiles)
	utils.LoadConfig(options.ConfigsDir, openaiendpoint.AssistantsConfigFile, &openaiendpoint.Assistants)
	utils.LoadConfig(options.ConfigsDir, openaiendpoint.AssistantsFileConfigFile, &openaiendpoint.AssistantFiles)

	app := createApplication(options)

	services.PreloadModelsConfigurations(options.ModelLibraryURL, options.ModelPath, options.ModelsURL...)

	if err := app.BackendConfigLoader.LoadBackendConfigsFromPath(options.ModelPath, app.ApplicationConfig.ToConfigLoaderOptions()...); err != nil {
		log.Error().Err(err).Msg("error loading config files")
	}

	if options.ConfigFile != "" {
		if err := app.BackendConfigLoader.LoadBackendConfigFile(options.ConfigFile, app.ApplicationConfig.ToConfigLoaderOptions()...); err != nil {
			log.Error().Err(err).Msg("error loading config file")
		}
	}

	if err := app.BackendConfigLoader.Preload(options.ModelPath); err != nil {
		log.Error().Err(err).Msg("error downloading models")
	}

	if options.PreloadJSONModels != "" {
		if err := services.ApplyGalleryFromString(options.ModelPath, options.PreloadJSONModels, app.BackendConfigLoader, options.Galleries); err != nil {
			return nil, err
		}
	}

	if options.PreloadModelsFromPath != "" {
		if err := services.ApplyGalleryFromFile(options.ModelPath, options.PreloadModelsFromPath, app.BackendConfigLoader, options.Galleries); err != nil {
			return nil, err
		}
	}

	if options.Debug {
		for _, v := range app.BackendConfigLoader.ListBackendConfigs() {
			cfg, _ := app.BackendConfigLoader.GetBackendConfig(v)
			log.Debug().Msgf("Model: %s (config: %+v)", v, cfg)
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

	// turn off any process that was started by GRPC if the context is canceled
	go func() {
		<-options.Context.Done()
		log.Debug().Msgf("Context canceled, shutting down")
		app.ModelLoader.StopAllGRPC()
	}()

	if options.WatchDog {
		wd := model.NewWatchDog(
			app.ModelLoader,
			options.WatchDogBusyTimeout,
			options.WatchDogIdleTimeout,
			options.WatchDogBusy,
			options.WatchDogIdle)
		app.ModelLoader.SetWatchDog(wd)
		go wd.Run()
		go func() {
			<-options.Context.Done()
			log.Debug().Msgf("Context canceled, shutting down")
			wd.Shutdown()
		}()
	}

	log.Info().Msg("core/startup process completed!")
	return app, nil
}

// In Lieu of a proper DI framework, this function wires up the Application manually.
// This is in core/startup rather than core/state.go to keep package references clean!
func createApplication(appConfig *config.ApplicationConfig) *core.Application {
	app := &core.Application{
		ApplicationConfig:   appConfig,
		BackendConfigLoader: config.NewBackendConfigLoader(),
		ModelLoader:         model.NewModelLoader(appConfig.ModelPath),
	}

	var err error

	app.EmbeddingsBackendService = backend.NewEmbeddingsBackendService(app.ModelLoader, app.BackendConfigLoader, app.ApplicationConfig)
	app.ImageGenerationBackendService = backend.NewImageGenerationBackendService(app.ModelLoader, app.BackendConfigLoader, app.ApplicationConfig)
	app.LLMBackendService = backend.NewLLMBackendService(app.ModelLoader, app.BackendConfigLoader, app.ApplicationConfig)
	app.TranscriptionBackendService = backend.NewTranscriptionBackendService(app.ModelLoader, app.BackendConfigLoader, app.ApplicationConfig)
	app.TextToSpeechBackendService = backend.NewTextToSpeechBackendService(app.ModelLoader, app.BackendConfigLoader, app.ApplicationConfig)

	app.BackendMonitorService = services.NewBackendMonitorService(app.ModelLoader, app.BackendConfigLoader, app.ApplicationConfig)
	app.GalleryService = services.NewGalleryService(app.ApplicationConfig.ModelPath)
	app.ListModelsService = services.NewListModelsService(app.ModelLoader, app.BackendConfigLoader, app.ApplicationConfig)
	app.OpenAIService = services.NewOpenAIService(app.ModelLoader, app.BackendConfigLoader, app.ApplicationConfig, app.LLMBackendService)

	app.LocalAIMetricsService, err = services.NewLocalAIMetricsService()
	if err != nil {
		log.Warn().Msg("Unable to initialize LocalAIMetricsService - non-fatal, optional service")
	}

	return app
}
