package startup

import (
	"encoding/json"
	"fmt"
	"os"
	"path"

	"github.com/fsnotify/fsnotify"
	"github.com/go-skynet/LocalAI/core/config"
	"github.com/imdario/mergo"
	"github.com/rs/zerolog/log"
)

type WatchConfigDirectoryCloser func() error

func ReadApiKeysJson(configDir string, appConfig *config.ApplicationConfig) error {
	fileContent, err := os.ReadFile(path.Join(configDir, "api_keys.json"))
	if err == nil {
		// Parse JSON content from the file
		var fileKeys []string
		err := json.Unmarshal(fileContent, &fileKeys)
		if err == nil {
			appConfig.ApiKeys = append(appConfig.ApiKeys, fileKeys...)
			return nil
		}
		return err
	}
	return err
}

func ReadExternalBackendsJson(configDir string, appConfig *config.ApplicationConfig) error {
	fileContent, err := os.ReadFile(path.Join(configDir, "external_backends.json"))
	if err != nil {
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
		log.Fatal().Msgf("Unable to create a watcher for the LocalAI Configuration Directory: %+v", err)
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
				if event.Has(fsnotify.Write) {
					for targetName, watchFn := range CONFIG_FILE_UPDATES {
						if event.Name == targetName {
							err := watchFn(configDir, appConfig)
							log.Warn().Msgf("WatchConfigDirectory goroutine for %s: failed to update options: %+v", targetName, err)
						}
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
		return ret, fmt.Errorf("unable to establish watch on the LocalAI Configuration Directory: %+v", err)
	}

	return ret, nil
}
