package startup

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"github.com/go-skynet/LocalAI/core/config"
	"github.com/imdario/mergo"
	"github.com/rs/zerolog/log"
)

type WatchConfigDirectoryCloser func() error

func ReadApiKeysJson(configDir string, appConfig *config.ApplicationConfig) error {
	fileContent, err := os.ReadFile(filepath.Join(configDir, "api_keys.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	// Parse JSON content from the file
	var fileKeys []string
	err = json.Unmarshal(fileContent, &fileKeys)
	if err == nil {
		appConfig.ApiKeys = append(appConfig.ApiKeys, fileKeys...)
		return nil
	}
	return err

}

func ReadExternalBackendsJson(configDir string, appConfig *config.ApplicationConfig) error {
	fileContent, err := os.ReadFile(filepath.Join(configDir, "external_backends.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	// Parse JSON content from the file
	var fileBackends map[string]string
	err = json.Unmarshal(fileContent, &fileBackends)
	if err != nil {
		return err
	}
	err = mergo.Merge(&appConfig.ExternalGRPCBackends, fileBackends)
	if err != nil {
		return err
	}
	return nil
}

var CONFIG_FILE_UPDATES = map[string]func(configDir string, appConfig *config.ApplicationConfig) error{
	"api_keys.json":          ReadApiKeysJson,
	"external_backends.json": ReadExternalBackendsJson,
}

func WatchConfigDirectory(configDir string, appConfig *config.ApplicationConfig) (WatchConfigDirectoryCloser, error) {
	if len(configDir) == 0 {
		return nil, fmt.Errorf("configDir blank")
	}
	configWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal().Err(err).Msg("Unable to create a watcher for the LocalAI Configuration Directory")
	}
	ret := func() error {
		configWatcher.Close()
		return nil
	}

	// Start listening for events.
	go func() {
		for {
			select {
			case event, ok := <-configWatcher.Events:
				if !ok {
					return
				}
				if event.Has(fsnotify.Write | fsnotify.Create | fsnotify.Rename) {
					watchFn, ok := CONFIG_FILE_UPDATES[path.Base(event.Name)]
					if !ok {
						log.Warn().Str("filename", event.Name).Msg("no configuration file handler found for file")
						continue
					}

					err := watchFn(configDir, appConfig)
					if err != nil {
						log.Warn().Err(err).Str("filename", event.Name).Msg("WatchConfigDirectory goroutine failed to update options")
					}

				}
			case _, ok := <-configWatcher.Errors:
				if !ok {
					return
				}
				log.Error().Err(err).Msg("error encountered while watching config directory")
			}
		}
	}()

	// Add a path.
	err = configWatcher.Add(configDir)
	if err != nil {
		return ret, fmt.Errorf("unable to establish watch on the LocalAI Configuration Directory: %w", err)
	}

	for name, watchFn := range CONFIG_FILE_UPDATES {
		err := watchFn(configDir, appConfig)
		if err != nil {
			log.Warn().Err(err).Str("filename", name).Msg("could not process file")
		}
	}

	return ret, nil
}
