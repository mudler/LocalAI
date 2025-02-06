package application

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"time"

	"dario.cat/mergo"
	"github.com/fsnotify/fsnotify"
	"github.com/mudler/LocalAI/core/config"
	"github.com/rs/zerolog/log"
)

type fileHandler func(fileContent []byte, appConfig *config.ApplicationConfig) error

type configFileHandler struct {
	handlers map[string]fileHandler

	watcher *fsnotify.Watcher

	appConfig *config.ApplicationConfig
}

// TODO: This should be a singleton eventually so other parts of the code can register config file handlers,
// then we can export it to other packages
func newConfigFileHandler(appConfig *config.ApplicationConfig) configFileHandler {
	c := configFileHandler{
		handlers:  make(map[string]fileHandler),
		appConfig: appConfig,
	}
	err := c.Register("api_keys.json", readApiKeysJson(*appConfig), true)
	if err != nil {
		log.Error().Err(err).Str("file", "api_keys.json").Msg("unable to register config file handler")
	}
	err = c.Register("external_backends.json", readExternalBackendsJson(*appConfig), true)
	if err != nil {
		log.Error().Err(err).Str("file", "external_backends.json").Msg("unable to register config file handler")
	}
	return c
}

func (c *configFileHandler) Register(filename string, handler fileHandler, runNow bool) error {
	_, ok := c.handlers[filename]
	if ok {
		return fmt.Errorf("handler already registered for file %s", filename)
	}
	c.handlers[filename] = handler
	if runNow {
		c.callHandler(filename, handler)
	}
	return nil
}

func (c *configFileHandler) callHandler(filename string, handler fileHandler) {
	rootedFilePath := filepath.Join(c.appConfig.DynamicConfigsDir, filepath.Clean(filename))
	log.Trace().Str("filename", rootedFilePath).Msg("reading file for dynamic config update")
	fileContent, err := os.ReadFile(rootedFilePath)
	if err != nil && !os.IsNotExist(err) {
		log.Error().Err(err).Str("filename", rootedFilePath).Msg("could not read file")
	}

	if err = handler(fileContent, c.appConfig); err != nil {
		log.Error().Err(err).Msg("WatchConfigDirectory goroutine failed to update options")
	}
}

func (c *configFileHandler) Watch() error {
	configWatcher, err := fsnotify.NewWatcher()
	c.watcher = configWatcher
	if err != nil {
		return err
	}

	if c.appConfig.DynamicConfigsDirPollInterval > 0 {
		log.Debug().Msg("Poll interval set, falling back to polling for configuration changes")
		ticker := time.NewTicker(c.appConfig.DynamicConfigsDirPollInterval)
		go func() {
			for {
				<-ticker.C
				for file, handler := range c.handlers {
					log.Debug().Str("file", file).Msg("polling config file")
					c.callHandler(file, handler)
				}
			}
		}()
	}

	// Start listening for events.
	go func() {
		for {
			select {
			case event, ok := <-c.watcher.Events:
				if !ok {
					return
				}
				if event.Has(fsnotify.Write | fsnotify.Create | fsnotify.Remove) {
					handler, ok := c.handlers[path.Base(event.Name)]
					if !ok {
						continue
					}

					c.callHandler(filepath.Base(event.Name), handler)
				}
			case err, ok := <-c.watcher.Errors:
				log.Error().Err(err).Msg("config watcher error received")
				if !ok {
					return
				}
			}
		}
	}()

	// Add a path.
	err = c.watcher.Add(c.appConfig.DynamicConfigsDir)
	if err != nil {
		return fmt.Errorf("unable to create a watcher on the configuration directory: %+v", err)
	}

	return nil
}

// TODO: When we institute graceful shutdown, this should be called
func (c *configFileHandler) Stop() error {
	return c.watcher.Close()
}

func readApiKeysJson(startupAppConfig config.ApplicationConfig) fileHandler {
	handler := func(fileContent []byte, appConfig *config.ApplicationConfig) error {
		log.Debug().Msg("processing api keys runtime update")
		log.Trace().Int("numKeys", len(startupAppConfig.ApiKeys)).Msg("api keys provided at startup")

		if len(fileContent) > 0 {
			// Parse JSON content from the file
			var fileKeys []string
			err := json.Unmarshal(fileContent, &fileKeys)
			if err != nil {
				return err
			}

			log.Trace().Int("numKeys", len(fileKeys)).Msg("discovered API keys from api keys dynamic config dile")

			appConfig.ApiKeys = append(startupAppConfig.ApiKeys, fileKeys...)
		} else {
			log.Trace().Msg("no API keys discovered from dynamic config file")
			appConfig.ApiKeys = startupAppConfig.ApiKeys
		}
		log.Trace().Int("numKeys", len(appConfig.ApiKeys)).Msg("total api keys after processing")
		return nil
	}

	return handler
}

func readExternalBackendsJson(startupAppConfig config.ApplicationConfig) fileHandler {
	handler := func(fileContent []byte, appConfig *config.ApplicationConfig) error {
		log.Debug().Msg("processing external_backends.json")

		if len(fileContent) > 0 {
			// Parse JSON content from the file
			var fileBackends map[string]string
			err := json.Unmarshal(fileContent, &fileBackends)
			if err != nil {
				return err
			}
			appConfig.ExternalGRPCBackends = startupAppConfig.ExternalGRPCBackends
			err = mergo.Merge(&appConfig.ExternalGRPCBackends, &fileBackends)
			if err != nil {
				return err
			}
		} else {
			appConfig.ExternalGRPCBackends = startupAppConfig.ExternalGRPCBackends
		}
		log.Debug().Msg("external backends loaded from external_backends.json")
		return nil
	}
	return handler
}
