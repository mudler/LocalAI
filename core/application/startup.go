package application

import (
	"fmt"
	"os"

	"github.com/mudler/LocalAI/core/backend"
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

func New(opts ...config.AppOption) (*Application, error) {
	options := config.NewApplicationConfig(opts...)
	application := newApplication(options)

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
		return nil, fmt.Errorf("options.ModelPath cannot be empty")
	}
	err = os.MkdirAll(options.ModelPath, 0750)
	if err != nil {
		return nil, fmt.Errorf("unable to create ModelPath: %q", err)
	}
	if options.ImageDir != "" {
		err := os.MkdirAll(options.ImageDir, 0750)
		if err != nil {
			return nil, fmt.Errorf("unable to create ImageDir: %q", err)
		}
	}
	if options.AudioDir != "" {
		err := os.MkdirAll(options.AudioDir, 0750)
		if err != nil {
			return nil, fmt.Errorf("unable to create AudioDir: %q", err)
		}
	}
	if options.UploadDir != "" {
		err := os.MkdirAll(options.UploadDir, 0750)
		if err != nil {
			return nil, fmt.Errorf("unable to create UploadDir: %q", err)
		}
	}

	if err := pkgStartup.InstallModels(options.Galleries, options.ModelLibraryURL, options.ModelPath, options.EnforcePredownloadScans, nil, options.ModelsURL...); err != nil {
		log.Error().Err(err).Msg("error installing models")
	}

	configLoaderOpts := options.ToConfigLoaderOptions()

	if err := application.BackendLoader().LoadBackendConfigsFromPath(options.ModelPath, configLoaderOpts...); err != nil {
		log.Error().Err(err).Msg("error loading config files")
	}

	if options.ConfigFile != "" {
		if err := application.BackendLoader().LoadMultipleBackendConfigsSingleFile(options.ConfigFile, configLoaderOpts...); err != nil {
			log.Error().Err(err).Msg("error loading config file")
		}
	}

	if err := application.BackendLoader().Preload(options.ModelPath); err != nil {
		log.Error().Err(err).Msg("error downloading models")
	}

	if options.PreloadJSONModels != "" {
		if err := services.ApplyGalleryFromString(options.ModelPath, options.PreloadJSONModels, options.EnforcePredownloadScans, options.Galleries); err != nil {
			return nil, err
		}
	}

	if options.PreloadModelsFromPath != "" {
		if err := services.ApplyGalleryFromFile(options.ModelPath, options.PreloadModelsFromPath, options.EnforcePredownloadScans, options.Galleries); err != nil {
			return nil, err
		}
	}

	if options.Debug {
		for _, v := range application.BackendLoader().GetAllBackendConfigs() {
			log.Debug().Msgf("Model: %s (config: %+v)", v.Name, v)
		}
	}

	if options.AssetsDestination != "" {
		// Extract files from the embedded FS
		err := assets.ExtractFiles(options.BackendAssets, options.AssetsDestination)
		log.Debug().Msgf("Extracting backend assets files to %s", options.AssetsDestination)
		if err != nil {
			log.Warn().Msgf("Failed extracting backend assets files: %s (might be required for some backends to work properly)", err)
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

	if options.LoadToMemory != nil {
		for _, m := range options.LoadToMemory {
			cfg, err := application.BackendLoader().LoadBackendConfigFileByName(m, options.ModelPath,
				config.LoadOptionDebug(options.Debug),
				config.LoadOptionThreads(options.Threads),
				config.LoadOptionContextSize(options.ContextSize),
				config.LoadOptionF16(options.F16),
				config.ModelPath(options.ModelPath),
			)
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
