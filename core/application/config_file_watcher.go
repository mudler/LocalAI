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
	handler := func(fileContent []byte, appConfig *config.ApplicationConfig) error {
		xlog.Debug("processing runtime_settings.json")

		// Determine if settings came from env vars by comparing with startup config
		// startupAppConfig contains the original values set from env vars at startup.
		// If current values match startup values, they came from env vars (or defaults).
		// We apply file settings only if current values match startup values (meaning not from env vars).
		envWatchdogIdle := appConfig.WatchDogIdle == startupAppConfig.WatchDogIdle
		envWatchdogBusy := appConfig.WatchDogBusy == startupAppConfig.WatchDogBusy
		envWatchdogIdleTimeout := appConfig.WatchDogIdleTimeout == startupAppConfig.WatchDogIdleTimeout
		envWatchdogBusyTimeout := appConfig.WatchDogBusyTimeout == startupAppConfig.WatchDogBusyTimeout
		envSingleBackend := appConfig.SingleBackend == startupAppConfig.SingleBackend
		envMaxActiveBackends := appConfig.MaxActiveBackends == startupAppConfig.MaxActiveBackends
		envParallelRequests := appConfig.ParallelBackendRequests == startupAppConfig.ParallelBackendRequests
		envMemoryReclaimerEnabled := appConfig.MemoryReclaimerEnabled == startupAppConfig.MemoryReclaimerEnabled
		envMemoryReclaimerThreshold := appConfig.MemoryReclaimerThreshold == startupAppConfig.MemoryReclaimerThreshold
		envThreads := appConfig.Threads == startupAppConfig.Threads
		envContextSize := appConfig.ContextSize == startupAppConfig.ContextSize
		envF16 := appConfig.F16 == startupAppConfig.F16
		envDebug := appConfig.Debug == startupAppConfig.Debug
		envCORS := appConfig.CORS == startupAppConfig.CORS
		envCSRF := appConfig.CSRF == startupAppConfig.CSRF
		envCORSAllowOrigins := appConfig.CORSAllowOrigins == startupAppConfig.CORSAllowOrigins
		envP2PToken := appConfig.P2PToken == startupAppConfig.P2PToken
		envP2PNetworkID := appConfig.P2PNetworkID == startupAppConfig.P2PNetworkID
		envFederated := appConfig.Federated == startupAppConfig.Federated
		envAutoloadGalleries := appConfig.AutoloadGalleries == startupAppConfig.AutoloadGalleries
		envAutoloadBackendGalleries := appConfig.AutoloadBackendGalleries == startupAppConfig.AutoloadBackendGalleries
		envAgentJobRetentionDays := appConfig.AgentJobRetentionDays == startupAppConfig.AgentJobRetentionDays
		envForceEvictionWhenBusy := appConfig.ForceEvictionWhenBusy == startupAppConfig.ForceEvictionWhenBusy
		envLRUEvictionMaxRetries := appConfig.LRUEvictionMaxRetries == startupAppConfig.LRUEvictionMaxRetries
		envLRUEvictionRetryInterval := appConfig.LRUEvictionRetryInterval == startupAppConfig.LRUEvictionRetryInterval

		if len(fileContent) > 0 {
			var settings config.RuntimeSettings
			err := json.Unmarshal(fileContent, &settings)
			if err != nil {
				return err
			}

			// Apply file settings only if they don't match startup values (i.e., not from env vars)
			if settings.WatchdogIdleEnabled != nil && !envWatchdogIdle {
				appConfig.WatchDogIdle = *settings.WatchdogIdleEnabled
				if appConfig.WatchDogIdle {
					appConfig.WatchDog = true
				}
			}
			if settings.WatchdogBusyEnabled != nil && !envWatchdogBusy {
				appConfig.WatchDogBusy = *settings.WatchdogBusyEnabled
				if appConfig.WatchDogBusy {
					appConfig.WatchDog = true
				}
			}
			if settings.WatchdogIdleTimeout != nil && !envWatchdogIdleTimeout {
				dur, err := time.ParseDuration(*settings.WatchdogIdleTimeout)
				if err == nil {
					appConfig.WatchDogIdleTimeout = dur
				} else {
					xlog.Warn("invalid watchdog idle timeout in runtime_settings.json", "error", err, "timeout", *settings.WatchdogIdleTimeout)
				}
			}
			if settings.WatchdogBusyTimeout != nil && !envWatchdogBusyTimeout {
				dur, err := time.ParseDuration(*settings.WatchdogBusyTimeout)
				if err == nil {
					appConfig.WatchDogBusyTimeout = dur
				} else {
					xlog.Warn("invalid watchdog busy timeout in runtime_settings.json", "error", err, "timeout", *settings.WatchdogBusyTimeout)
				}
			}
			// Handle MaxActiveBackends (new) and SingleBackend (deprecated)
			if settings.MaxActiveBackends != nil && !envMaxActiveBackends {
				appConfig.MaxActiveBackends = *settings.MaxActiveBackends
				// For backward compatibility, also set SingleBackend if MaxActiveBackends == 1
				appConfig.SingleBackend = (*settings.MaxActiveBackends == 1)
			} else if settings.SingleBackend != nil && !envSingleBackend {
				// Legacy: SingleBackend maps to MaxActiveBackends = 1
				appConfig.SingleBackend = *settings.SingleBackend
				if *settings.SingleBackend {
					appConfig.MaxActiveBackends = 1
				} else {
					appConfig.MaxActiveBackends = 0
				}
			}
			if settings.ParallelBackendRequests != nil && !envParallelRequests {
				appConfig.ParallelBackendRequests = *settings.ParallelBackendRequests
			}
			if settings.MemoryReclaimerEnabled != nil && !envMemoryReclaimerEnabled {
				appConfig.MemoryReclaimerEnabled = *settings.MemoryReclaimerEnabled
				if appConfig.MemoryReclaimerEnabled {
					appConfig.WatchDog = true // Memory reclaimer requires watchdog
				}
			}
			if settings.MemoryReclaimerThreshold != nil && !envMemoryReclaimerThreshold {
				appConfig.MemoryReclaimerThreshold = *settings.MemoryReclaimerThreshold
			}
			if settings.ForceEvictionWhenBusy != nil && !envForceEvictionWhenBusy {
				appConfig.ForceEvictionWhenBusy = *settings.ForceEvictionWhenBusy
			}
			if settings.LRUEvictionMaxRetries != nil && !envLRUEvictionMaxRetries {
				appConfig.LRUEvictionMaxRetries = *settings.LRUEvictionMaxRetries
			}
			if settings.LRUEvictionRetryInterval != nil && !envLRUEvictionRetryInterval {
				dur, err := time.ParseDuration(*settings.LRUEvictionRetryInterval)
				if err == nil {
					appConfig.LRUEvictionRetryInterval = dur
				} else {
					xlog.Warn("invalid LRU eviction retry interval in runtime_settings.json", "error", err, "interval", *settings.LRUEvictionRetryInterval)
				}
			}
			if settings.Threads != nil && !envThreads {
				appConfig.Threads = *settings.Threads
			}
			if settings.ContextSize != nil && !envContextSize {
				appConfig.ContextSize = *settings.ContextSize
			}
			if settings.F16 != nil && !envF16 {
				appConfig.F16 = *settings.F16
			}
			if settings.Debug != nil && !envDebug {
				appConfig.Debug = *settings.Debug
			}
			if settings.CORS != nil && !envCORS {
				appConfig.CORS = *settings.CORS
			}
			if settings.CSRF != nil && !envCSRF {
				appConfig.CSRF = *settings.CSRF
			}
			if settings.CORSAllowOrigins != nil && !envCORSAllowOrigins {
				appConfig.CORSAllowOrigins = *settings.CORSAllowOrigins
			}
			if settings.P2PToken != nil && !envP2PToken {
				appConfig.P2PToken = *settings.P2PToken
			}
			if settings.P2PNetworkID != nil && !envP2PNetworkID {
				appConfig.P2PNetworkID = *settings.P2PNetworkID
			}
			if settings.Federated != nil && !envFederated {
				appConfig.Federated = *settings.Federated
			}
			if settings.Galleries != nil {
				appConfig.Galleries = *settings.Galleries
			}
			if settings.BackendGalleries != nil {
				appConfig.BackendGalleries = *settings.BackendGalleries
			}
			if settings.AutoloadGalleries != nil && !envAutoloadGalleries {
				appConfig.AutoloadGalleries = *settings.AutoloadGalleries
			}
			if settings.AutoloadBackendGalleries != nil && !envAutoloadBackendGalleries {
				appConfig.AutoloadBackendGalleries = *settings.AutoloadBackendGalleries
			}
			if settings.ApiKeys != nil {
				// API keys from env vars (startup) should be kept, runtime settings keys replace all runtime keys
				// If runtime_settings.json specifies ApiKeys (even if empty), it replaces all runtime keys
				// Start with env keys, then add runtime_settings.json keys (which may be empty to clear them)
				envKeys := startupAppConfig.ApiKeys
				runtimeKeys := *settings.ApiKeys
				// Replace all runtime keys with what's in runtime_settings.json
				appConfig.ApiKeys = append(envKeys, runtimeKeys...)
			}
			if settings.AgentJobRetentionDays != nil && !envAgentJobRetentionDays {
				appConfig.AgentJobRetentionDays = *settings.AgentJobRetentionDays
			}

			// If watchdog is enabled via file but not via env, ensure WatchDog flag is set
			if !envWatchdogIdle && !envWatchdogBusy {
				if settings.WatchdogEnabled != nil && *settings.WatchdogEnabled {
					appConfig.WatchDog = true
				}
			}
		}
		xlog.Debug("runtime settings loaded from runtime_settings.json")
		return nil
	}
	return handler
}
