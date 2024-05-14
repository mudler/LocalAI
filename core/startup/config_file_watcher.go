package startup

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/go-skynet/LocalAI/core/config"
	"github.com/imdario/mergo"
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

	err := c.Register("roles.json", readRolesJson(*appConfig), true)
	if err != nil {
		log.Error().Err(err).Str("file", "roles.json").Msg("unable to register config file handler")
	}
	err = c.Register("api_keys.json", readApiKeysJson(*appConfig), true)
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
		log.Fatal().Err(err).Str("configdir", c.appConfig.DynamicConfigsDir).Msg("unable to create a watcher for configuration directory")

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
		return fmt.Errorf("unable to establish watch on the LocalAI Configuration Directory: %+v", err)
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
			var fileKeys map[string][]string
			err := json.Unmarshal(fileContent, &fileKeys)
			if err != nil {
				// Try to deserialize the old, flat list format
				var oldFileFormat []string
				err := json.Unmarshal(fileContent, &oldFileFormat)
				if err != nil {
					log.Error().Err(err).Msg("unable to parse api_keys.json as any known format")
					return err
				}
				log.Warn().Msg("unable to parse api_keys.json in modern format, defaulting all api keys to [\"ui\", \"user\"]")
				for _, k := range oldFileFormat {
					fileKeys[k] = []string{"ui", "user"}
				}
			}

			appConfig.ApiKeys = startupAppConfig.ApiKeys
			if appConfig.ApiKeys == nil {
				appConfig.ApiKeys = map[string][]string{}
			}

			log.Trace().Int("numKeys", len(fileKeys)).Msg("discovered API keys from api keys dynamic config dile")
			for key, rawFileEndpoints := range fileKeys {
				appConfig.ApiKeys[key] = append(startupAppConfig.ApiKeys[key], rawFileEndpoints...)
			}
		} else {
			log.Trace().Msg("no API keys discovered from dynamic config file")
			appConfig.ApiKeys = startupAppConfig.ApiKeys
		}

		// next, clean and process the ApiKeys for roles, duplicates, and *
		// This is registered to run at startup, so will evaluate roles passed in as startupAppConfig
		// quick version for now, this can be improved later
		for key, endpoints := range appConfig.ApiKeys {
			// Check if the starting point is enough to know the final answer
			if slices.Contains(endpoints, "*") {
				appConfig.ApiKeys[key] = []string{"*"}
				continue
			}

			for { // We loop around here a second time if we make a change -- this ensures we unroll nested roles
				isClean := true
				for role, roleEndpoints := range appConfig.Roles {
					index := slices.Index(appConfig.ApiKeys[key], role)
					if index != -1 {
						appConfig.ApiKeys[key] = slices.Replace(appConfig.ApiKeys[key], index, index+1, roleEndpoints...)
						isClean = false
					}
				}
				if isClean {
					break
				}
			}
			// Check if we have a "*"" yet
			if slices.Contains(appConfig.ApiKeys[key], "*") {
				appConfig.ApiKeys[key] = []string{"*"}
				continue
			}
			// At this point, Sort+Compact is a simple way to deduplicate the endpoint list, no matter how the roles overlap
			slices.Sort(appConfig.ApiKeys[key])
			appConfig.ApiKeys[key] = slices.Compact(appConfig.ApiKeys[key])
		}

		log.Trace().Int("numKeys", len(appConfig.ApiKeys)).Msg("total api keys after processing")
		return nil
	}

	return handler
}

func readRolesJson(startupAppConfig config.ApplicationConfig) fileHandler {
	handler := func(fileContent []byte, appConfig *config.ApplicationConfig) error {
		log.Debug().Msg("processing roles runtime update")
		log.Trace().Int("numRoles", len(startupAppConfig.Roles)).Msg("roles provided at startup")

		if len(fileContent) > 0 {
			// Parse JSON content from the file
			var fileRoles map[string][]string // Roles is a simple "shortcut" mapping a name to a list of endpoints
			err := json.Unmarshal(fileContent, &fileRoles)
			if err != nil {
				return err
			}

			log.Trace().Int("numRoles", len(fileRoles)).Msg("discovered roles from roles dynamic config dile")

			appConfig.Roles = fileRoles
		} else {
			log.Trace().Msg("no roles discovered from dynamic config file")
			appConfig.Roles = startupAppConfig.Roles
		}
		log.Trace().Int("numRoles", len(appConfig.Roles)).Msg("total roles after processing")
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
