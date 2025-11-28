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
	err = c.Register("runtime_settings.json", readRuntimeSettingsJson(*appConfig), true)
	if err != nil {
		log.Error().Err(err).Str("file", "runtime_settings.json").Msg("unable to register config file handler")
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

type runtimeSettings struct {
	WatchdogEnabled          *bool             `json:"watchdog_enabled,omitempty"`
	WatchdogIdleEnabled      *bool             `json:"watchdog_idle_enabled,omitempty"`
	WatchdogBusyEnabled      *bool             `json:"watchdog_busy_enabled,omitempty"`
	WatchdogIdleTimeout      *string           `json:"watchdog_idle_timeout,omitempty"`
	WatchdogBusyTimeout      *string           `json:"watchdog_busy_timeout,omitempty"`
	SingleBackend            *bool             `json:"single_backend,omitempty"`
	ParallelBackendRequests  *bool             `json:"parallel_backend_requests,omitempty"`
	Threads                  *int              `json:"threads,omitempty"`
	ContextSize              *int              `json:"context_size,omitempty"`
	F16                      *bool             `json:"f16,omitempty"`
	Debug                    *bool             `json:"debug,omitempty"`
	CORS                     *bool             `json:"cors,omitempty"`
	CSRF                     *bool             `json:"csrf,omitempty"`
	CORSAllowOrigins         *string           `json:"cors_allow_origins,omitempty"`
	P2PToken                 *string           `json:"p2p_token,omitempty"`
	P2PNetworkID             *string           `json:"p2p_network_id,omitempty"`
	Federated                *bool             `json:"federated,omitempty"`
	Galleries                *[]config.Gallery `json:"galleries,omitempty"`
	BackendGalleries         *[]config.Gallery `json:"backend_galleries,omitempty"`
	AutoloadGalleries        *bool             `json:"autoload_galleries,omitempty"`
	AutoloadBackendGalleries *bool             `json:"autoload_backend_galleries,omitempty"`
	ApiKeys                  *[]string         `json:"api_keys,omitempty"`
	AgentJobRetentionDays    *int              `json:"agent_job_retention_days,omitempty"`
}

func readRuntimeSettingsJson(startupAppConfig config.ApplicationConfig) fileHandler {
	handler := func(fileContent []byte, appConfig *config.ApplicationConfig) error {
		log.Debug().Msg("processing runtime_settings.json")

		// Determine if settings came from env vars by comparing with startup config
		// startupAppConfig contains the original values set from env vars at startup.
		// If current values match startup values, they came from env vars (or defaults).
		// We apply file settings only if current values match startup values (meaning not from env vars).
		envWatchdogIdle := appConfig.WatchDogIdle == startupAppConfig.WatchDogIdle
		envWatchdogBusy := appConfig.WatchDogBusy == startupAppConfig.WatchDogBusy
		envWatchdogIdleTimeout := appConfig.WatchDogIdleTimeout == startupAppConfig.WatchDogIdleTimeout
		envWatchdogBusyTimeout := appConfig.WatchDogBusyTimeout == startupAppConfig.WatchDogBusyTimeout
		envSingleBackend := appConfig.SingleBackend == startupAppConfig.SingleBackend
		envParallelRequests := appConfig.ParallelBackendRequests == startupAppConfig.ParallelBackendRequests
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

		if len(fileContent) > 0 {
			var settings runtimeSettings
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
					log.Warn().Err(err).Str("timeout", *settings.WatchdogIdleTimeout).Msg("invalid watchdog idle timeout in runtime_settings.json")
				}
			}
			if settings.WatchdogBusyTimeout != nil && !envWatchdogBusyTimeout {
				dur, err := time.ParseDuration(*settings.WatchdogBusyTimeout)
				if err == nil {
					appConfig.WatchDogBusyTimeout = dur
				} else {
					log.Warn().Err(err).Str("timeout", *settings.WatchdogBusyTimeout).Msg("invalid watchdog busy timeout in runtime_settings.json")
				}
			}
			if settings.SingleBackend != nil && !envSingleBackend {
				appConfig.SingleBackend = *settings.SingleBackend
			}
			if settings.ParallelBackendRequests != nil && !envParallelRequests {
				appConfig.ParallelBackendRequests = *settings.ParallelBackendRequests
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
		log.Debug().Msg("runtime settings loaded from runtime_settings.json")
		return nil
	}
	return handler
}
