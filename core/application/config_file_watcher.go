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
	"github.com/mudler/xlog"
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
		xlog.Error("unable to register config file handler", "error", err, "file", "api_keys.json")
	}
	err = c.Register("external_backends.json", readExternalBackendsJson(*appConfig), true)
	if err != nil {
		xlog.Error("unable to register config file handler", "error", err, "file", "external_backends.json")
	}
	err = c.Register("runtime_settings.json", readRuntimeSettingsJson(*appConfig), true)
	if err != nil {
		xlog.Error("unable to register config file handler", "error", err, "file", "runtime_settings.json")
	}
	// Note: agent_tasks.json and agent_jobs.json are handled by AgentJobService directly
	// The service watches and reloads these files internally
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
	xlog.Debug("reading file for dynamic config update", "filename", rootedFilePath)
	fileContent, err := os.ReadFile(rootedFilePath)
	if err != nil && !os.IsNotExist(err) {
		xlog.Error("could not read file", "error", err, "filename", rootedFilePath)
	}

	if err = handler(fileContent, c.appConfig); err != nil {
		xlog.Error("WatchConfigDirectory goroutine failed to update options", "error", err)
	}
}

func (c *configFileHandler) Watch() error {
	configWatcher, err := fsnotify.NewWatcher()
	c.watcher = configWatcher
	if err != nil {
		return err
	}

	if c.appConfig.DynamicConfigsDirPollInterval > 0 {
		xlog.Debug("Poll interval set, falling back to polling for configuration changes")
		ticker := time.NewTicker(c.appConfig.DynamicConfigsDirPollInterval)
		go func() {
			for {
				<-ticker.C
				for file, handler := range c.handlers {
					xlog.Debug("polling config file", "file", file)
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
				xlog.Error("config watcher error received", "error", err)
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
		xlog.Debug("processing api keys runtime update", "numKeys", len(startupAppConfig.ApiKeys))

		if len(fileContent) > 0 {
			// Parse JSON content from the file
			var fileKeys []string
			err := json.Unmarshal(fileContent, &fileKeys)
			if err != nil {
				return err
			}

			xlog.Debug("discovered API keys from api keys dynamic config file", "numKeys", len(fileKeys))

			appConfig.ApiKeys = append(startupAppConfig.ApiKeys, fileKeys...)
		} else {
			xlog.Debug("no API keys discovered from dynamic config file")
			appConfig.ApiKeys = startupAppConfig.ApiKeys
		}
		xlog.Debug("total api keys after processing", "numKeys", len(appConfig.ApiKeys))
		return nil
	}

	return handler
}

func readExternalBackendsJson(startupAppConfig config.ApplicationConfig) fileHandler {
	handler := func(fileContent []byte, appConfig *config.ApplicationConfig) error {
		xlog.Debug("processing external_backends.json")

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
		xlog.Debug("external backends loaded from external_backends.json")
		return nil
	}
	return handler
}

func readRuntimeSettingsJson(startupAppConfig config.ApplicationConfig) fileHandler {
	return func(fileContent []byte, appConfig *config.ApplicationConfig) error {
		xlog.Debug("processing runtime_settings.json")
		if len(fileContent) == 0 {
			return nil
		}
		var settings config.RuntimeSettings
		if err := json.Unmarshal(fileContent, &settings); err != nil {
			return err
		}
		// Same merge semantics as boot: env/CLI-claimed fields win, the
		// file supplies the rest. This replaces the old inverted guard
		// (apply only when live != startup snapshot) which skipped genuine
		// manual edits of any field still at its boot value. Trade-off: a
		// field previously changed via the API looks env-set to the
		// baseline comparison, so a manual file edit of that field lands
		// on the next restart instead of hot-applying.
		appConfig.ApplyRuntimeSettingsAtStartup(&settings)
		if settings.ApiKeys != nil {
			appConfig.ApiKeys = config.MergeAPIKeys(startupAppConfig.ApiKeys, *settings.ApiKeys)
		}
		return nil
	}
}
