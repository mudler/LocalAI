package application

import (
	"fmt"
	"os"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/internal"

	coreStartup "github.com/mudler/LocalAI/core/startup"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/rs/zerolog/log"
)

func New(opts ...config.AppOption) (*Application, error) {
	options := config.NewApplicationConfig(opts...)
	application := newApplication(options)

	log.Info().Msgf("Starting LocalAI using %d threads, with models path: %s", options.Threads, options.SystemState.Model.ModelsPath)
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
	if options.SystemState.Model.ModelsPath == "" {
		return nil, fmt.Errorf("models path cannot be empty")
	}

	err = os.MkdirAll(options.SystemState.Model.ModelsPath, 0750)
	if err != nil {
		return nil, fmt.Errorf("unable to create ModelPath: %q", err)
	}
	if options.GeneratedContentDir != "" {
		err := os.MkdirAll(options.GeneratedContentDir, 0750)
		if err != nil {
			return nil, fmt.Errorf("unable to create ImageDir: %q", err)
		}
	}
	if options.UploadDir != "" {
		err := os.MkdirAll(options.UploadDir, 0750)
		if err != nil {
			return nil, fmt.Errorf("unable to create UploadDir: %q", err)
		}
	}

	if err := coreStartup.InstallModels(options.Galleries, options.BackendGalleries, options.SystemState, application.ModelLoader(), options.EnforcePredownloadScans, options.AutoloadBackendGalleries, nil, options.ModelsURL...); err != nil {
		log.Error().Err(err).Msg("error installing models")
	}

	for _, backend := range options.ExternalBackends {
		if err := coreStartup.InstallExternalBackends(options.BackendGalleries, options.SystemState, application.ModelLoader(), nil, backend, "", ""); err != nil {
			log.Error().Err(err).Msg("error installing external backend")
		}
	}

	configLoaderOpts := options.ToConfigLoaderOptions()

	if err := application.ModelConfigLoader().LoadModelConfigsFromPath(options.SystemState.Model.ModelsPath, configLoaderOpts...); err != nil {
		log.Error().Err(err).Msg("error loading config files")
	}

	if err := gallery.RegisterBackends(options.SystemState, application.ModelLoader()); err != nil {
		log.Error().Err(err).Msg("error registering external backends")
	}

	if options.ConfigFile != "" {
		if err := application.ModelConfigLoader().LoadMultipleModelConfigsSingleFile(options.ConfigFile, configLoaderOpts...); err != nil {
			log.Error().Err(err).Msg("error loading config file")
		}
	}

	if err := application.ModelConfigLoader().Preload(options.SystemState.Model.ModelsPath); err != nil {
		log.Error().Err(err).Msg("error downloading models")
	}

	if options.PreloadJSONModels != "" {
		if err := services.ApplyGalleryFromString(options.SystemState, application.ModelLoader(), options.EnforcePredownloadScans, options.AutoloadBackendGalleries, options.Galleries, options.BackendGalleries, options.PreloadJSONModels); err != nil {
			return nil, err
		}
	}

	if options.PreloadModelsFromPath != "" {
		if err := services.ApplyGalleryFromFile(options.SystemState, application.ModelLoader(), options.EnforcePredownloadScans, options.AutoloadBackendGalleries, options.Galleries, options.BackendGalleries, options.PreloadModelsFromPath); err != nil {
			return nil, err
		}
	}

	if options.Debug {
		for _, v := range application.ModelConfigLoader().GetAllModelsConfigs() {
			log.Debug().Msgf("Model: %s (config: %+v)", v.Name, v)
		}
	}

	// turn off any process that was started by GRPC if the context is canceled
	go func() {
		<-options.Context.Done()
		log.Debug().Msgf("Context canceled, shutting down")
		err := application.ModelLoader().StopAllGRPC()
		if err != nil {
			log.Error().Err(err).Msg("error while stopping all grpc backends")
		}
	}()

	if options.WatchDog {
		wd := model.NewWatchDog(
			application.ModelLoader(),
			options.WatchDogBusyTimeout,
			options.WatchDogIdleTimeout,
			options.WatchDogBusy,
			options.WatchDogIdle)
		application.ModelLoader().SetWatchDog(wd)
		go wd.Run()
		go func() {
			<-options.Context.Done()
			log.Debug().Msgf("Context canceled, shutting down")
			wd.Shutdown()
		}()
	}

	if options.LoadToMemory != nil && !options.SingleBackend {
		for _, m := range options.LoadToMemory {
			cfg, err := application.ModelConfigLoader().LoadModelConfigFileByNameDefaultOptions(m, options)
			if err != nil {
				return nil, err
			}

			log.Debug().Msgf("Auto loading model %s into memory from file: %s", m, cfg.Model)

			o := backend.ModelOptions(*cfg, options)

			var backendErr error
			_, backendErr = application.ModelLoader().Load(o...)
			if backendErr != nil {
				return nil, err
			}
		}
	}

	// Watch the configuration directory
	startWatcher(options)

	if err := application.start(); err != nil {
		return nil, err
	}

	log.Info().Msg("core/startup process completed!")
	return application, nil
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
