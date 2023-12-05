package api

// TODO: Someday, refactor outer package name to reflect this layer being added?

import (
	config "github.com/go-skynet/LocalAI/api/config"
	"github.com/go-skynet/LocalAI/api/localai"
	"github.com/go-skynet/LocalAI/api/options"
	"github.com/go-skynet/LocalAI/internal"
	"github.com/go-skynet/LocalAI/pkg/assets"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func Startup(opts ...options.AppOption) (*options.Option, *config.ConfigLoader, error) {
	options := options.NewOptions(opts...)

	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if options.Debug {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	log.Info().Msgf("Starting LocalAI using %d threads, with models path: %s", options.Threads, options.Loader.ModelPath)
	log.Info().Msgf("LocalAI version: %s", internal.PrintableVersion())

	cl := config.NewConfigLoader()
	if err := cl.LoadConfigs(options.Loader.ModelPath); err != nil {
		log.Error().Msgf("error loading config files: %s", err.Error())
	}

	if options.ConfigFile != "" {
		if err := cl.LoadConfigFile(options.ConfigFile); err != nil {
			log.Error().Msgf("error loading config file: %s", err.Error())
		}
	}

	if options.Debug {
		for _, v := range cl.ListConfigs() {
			cfg, _ := cl.GetConfig(v)
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

	if options.PreloadJSONModels != "" {
		if err := localai.ApplyGalleryFromString(options.Loader.ModelPath, options.PreloadJSONModels, cl, options.Galleries); err != nil {
			return nil, nil, err
		}
	}

	if options.PreloadModelsFromPath != "" {
		if err := localai.ApplyGalleryFromFile(options.Loader.ModelPath, options.PreloadModelsFromPath, cl, options.Galleries); err != nil {
			return nil, nil, err
		}
	}

	// turn off any process that was started by GRPC if the context is canceled
	go func() {
		<-options.Context.Done()
		log.Debug().Msgf("Context canceled, shutting down")
		options.Loader.StopAllGRPC()
	}()

	if options.WatchDog {
		wd := model.NewWatchDog(
			options.Loader,
			options.WatchDogBusyTimeout,
			options.WatchDogIdleTimeout,
			options.WatchDogBusy,
			options.WatchDogIdle)
		options.Loader.SetWatchDog(wd)
		go wd.Run()
		go func() {
			<-options.Context.Done()
			log.Debug().Msgf("Context canceled, shutting down")
			wd.Shutdown()
		}()
	}

	return options, cl, nil
}
