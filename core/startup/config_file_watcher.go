package startup

import (
	"encoding/json"
	"fmt"
	"os"
	"path"

	"github.com/fsnotify/fsnotify"
	"github.com/go-skynet/LocalAI/pkg/datamodel"
	"github.com/imdario/mergo"
	"github.com/rs/zerolog/log"
)

// TODO: Does this belong in package startup?

type WatchConfigDirectoryCloser func() error

func ReadApiKeysJson(configDir string, options *datamodel.StartupOptions) error {
	fileContent, err := os.ReadFile(path.Join(configDir, "api_keys.json"))
	if err == nil {
		// Parse JSON content from the file
		var fileKeys []string
		err := json.Unmarshal(fileContent, &fileKeys)
		if err == nil {
			options.ApiKeys = append(options.ApiKeys, fileKeys...)
			return nil
		}
		return err
	}
	return err
}

func ReadExternalBackendsJson(configDir string, options *datamodel.StartupOptions) error {
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
	err = mergo.Merge(&options.ExternalGRPCBackends, fileBackends)
	if err != nil {
		return err
	}
	return nil
}

var CONFIG_FILE_UPDATES = map[string]func(configDir string, options *datamodel.StartupOptions) error{
	"api_keys.json":          ReadApiKeysJson,
	"external_backends.json": ReadExternalBackendsJson,
}

func WatchConfigDirectory(configDir string, options *datamodel.StartupOptions) (WatchConfigDirectoryCloser, error) {
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
							err := watchFn(configDir, options)
							log.Warn().Msgf("WatchConfigDirectory goroutine for %s: failed to update options: %+v", targetName, err)
						}
					}
				}
			case _, ok := <-configWatcher.Errors:
				if !ok {
					return
				}
				log.Error().Msgf("WatchConfigDirectory goroutine error: %+v", err)
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
