package localai

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/p2p"
	"github.com/rs/zerolog/log"
)

type SettingsResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
}

type RuntimeSettings struct {
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
	ApiKeys                  *[]string         `json:"api_keys"` // No omitempty - we need to save empty arrays to clear keys
	AgentJobRetentionDays    *int              `json:"agent_job_retention_days,omitempty"`
}

// GetSettingsEndpoint returns current settings with precedence (env > file > defaults)
func GetSettingsEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		appConfig := app.ApplicationConfig()
		startupConfig := app.StartupConfig()

		if startupConfig == nil {
			// Fallback if startup config not available
			startupConfig = appConfig
		}

		settings := RuntimeSettings{}

		// Set all current values (using pointers for RuntimeSettings)
		watchdogIdle := appConfig.WatchDogIdle
		watchdogBusy := appConfig.WatchDogBusy
		watchdogEnabled := appConfig.WatchDog
		singleBackend := appConfig.SingleBackend
		parallelBackendRequests := appConfig.ParallelBackendRequests
		threads := appConfig.Threads
		contextSize := appConfig.ContextSize
		f16 := appConfig.F16
		debug := appConfig.Debug
		cors := appConfig.CORS
		csrf := appConfig.CSRF
		corsAllowOrigins := appConfig.CORSAllowOrigins
		p2pToken := appConfig.P2PToken
		p2pNetworkID := appConfig.P2PNetworkID
		federated := appConfig.Federated
		galleries := appConfig.Galleries
		backendGalleries := appConfig.BackendGalleries
		autoloadGalleries := appConfig.AutoloadGalleries
		autoloadBackendGalleries := appConfig.AutoloadBackendGalleries
		apiKeys := appConfig.ApiKeys
		agentJobRetentionDays := appConfig.AgentJobRetentionDays

		settings.WatchdogIdleEnabled = &watchdogIdle
		settings.WatchdogBusyEnabled = &watchdogBusy
		settings.WatchdogEnabled = &watchdogEnabled
		settings.SingleBackend = &singleBackend
		settings.ParallelBackendRequests = &parallelBackendRequests
		settings.Threads = &threads
		settings.ContextSize = &contextSize
		settings.F16 = &f16
		settings.Debug = &debug
		settings.CORS = &cors
		settings.CSRF = &csrf
		settings.CORSAllowOrigins = &corsAllowOrigins
		settings.P2PToken = &p2pToken
		settings.P2PNetworkID = &p2pNetworkID
		settings.Federated = &federated
		settings.Galleries = &galleries
		settings.BackendGalleries = &backendGalleries
		settings.AutoloadGalleries = &autoloadGalleries
		settings.AutoloadBackendGalleries = &autoloadBackendGalleries
		settings.ApiKeys = &apiKeys
		settings.AgentJobRetentionDays = &agentJobRetentionDays

		var idleTimeout, busyTimeout string
		if appConfig.WatchDogIdleTimeout > 0 {
			idleTimeout = appConfig.WatchDogIdleTimeout.String()
		} else {
			idleTimeout = "15m" // default
		}
		if appConfig.WatchDogBusyTimeout > 0 {
			busyTimeout = appConfig.WatchDogBusyTimeout.String()
		} else {
			busyTimeout = "5m" // default
		}
		settings.WatchdogIdleTimeout = &idleTimeout
		settings.WatchdogBusyTimeout = &busyTimeout
		return c.JSON(http.StatusOK, settings)
	}
}

// UpdateSettingsEndpoint updates settings, saves to file, and applies immediately
func UpdateSettingsEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		appConfig := app.ApplicationConfig()
		startupConfig := app.StartupConfig()

		if startupConfig == nil {
			// Fallback if startup config not available
			startupConfig = appConfig
		}

		body, err := io.ReadAll(c.Request().Body)
		if err != nil {
			return c.JSON(http.StatusBadRequest, SettingsResponse{
				Success: false,
				Error:   "Failed to read request body: " + err.Error(),
			})
		}

		var settings RuntimeSettings
		if err := json.Unmarshal(body, &settings); err != nil {
			return c.JSON(http.StatusBadRequest, SettingsResponse{
				Success: false,
				Error:   "Failed to parse JSON: " + err.Error(),
			})
		}

		// Validate timeouts if provided
		if settings.WatchdogIdleTimeout != nil {
			_, err := time.ParseDuration(*settings.WatchdogIdleTimeout)
			if err != nil {
				return c.JSON(http.StatusBadRequest, SettingsResponse{
					Success: false,
					Error:   "Invalid watchdog_idle_timeout format: " + err.Error(),
				})
			}
		}
		if settings.WatchdogBusyTimeout != nil {
			_, err := time.ParseDuration(*settings.WatchdogBusyTimeout)
			if err != nil {
				return c.JSON(http.StatusBadRequest, SettingsResponse{
					Success: false,
					Error:   "Invalid watchdog_busy_timeout format: " + err.Error(),
				})
			}
		}

		// Save to file
		if appConfig.DynamicConfigsDir == "" {
			return c.JSON(http.StatusBadRequest, SettingsResponse{
				Success: false,
				Error:   "DynamicConfigsDir is not set",
			})
		}

		settingsFile := filepath.Join(appConfig.DynamicConfigsDir, "runtime_settings.json")
		settingsJSON, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return c.JSON(http.StatusInternalServerError, SettingsResponse{
				Success: false,
				Error:   "Failed to marshal settings: " + err.Error(),
			})
		}

		if err := os.WriteFile(settingsFile, settingsJSON, 0600); err != nil {
			return c.JSON(http.StatusInternalServerError, SettingsResponse{
				Success: false,
				Error:   "Failed to write settings file: " + err.Error(),
			})
		}

		// Apply settings immediately, checking env var overrides per field
		watchdogChanged := false
		if settings.WatchdogEnabled != nil {
			appConfig.WatchDog = *settings.WatchdogEnabled
			watchdogChanged = true
		}
		if settings.WatchdogIdleEnabled != nil {
			appConfig.WatchDogIdle = *settings.WatchdogIdleEnabled
			if appConfig.WatchDogIdle {
				appConfig.WatchDog = true
			}
			watchdogChanged = true
		}
		if settings.WatchdogBusyEnabled != nil {
			appConfig.WatchDogBusy = *settings.WatchdogBusyEnabled
			if appConfig.WatchDogBusy {
				appConfig.WatchDog = true
			}
			watchdogChanged = true
		}
		if settings.WatchdogIdleTimeout != nil {
			dur, _ := time.ParseDuration(*settings.WatchdogIdleTimeout)
			appConfig.WatchDogIdleTimeout = dur
			watchdogChanged = true
		}
		if settings.WatchdogBusyTimeout != nil {
			dur, _ := time.ParseDuration(*settings.WatchdogBusyTimeout)
			appConfig.WatchDogBusyTimeout = dur
			watchdogChanged = true
		}
		if settings.SingleBackend != nil {
			appConfig.SingleBackend = *settings.SingleBackend
		}
		if settings.ParallelBackendRequests != nil {
			appConfig.ParallelBackendRequests = *settings.ParallelBackendRequests
		}
		if settings.Threads != nil {
			appConfig.Threads = *settings.Threads
		}
		if settings.ContextSize != nil {
			appConfig.ContextSize = *settings.ContextSize
		}
		if settings.F16 != nil {
			appConfig.F16 = *settings.F16
		}
		if settings.Debug != nil {
			appConfig.Debug = *settings.Debug
		}
		if settings.CORS != nil {
			appConfig.CORS = *settings.CORS
		}
		if settings.CSRF != nil {
			appConfig.CSRF = *settings.CSRF
		}
		if settings.CORSAllowOrigins != nil {
			appConfig.CORSAllowOrigins = *settings.CORSAllowOrigins
		}
		if settings.P2PToken != nil {
			appConfig.P2PToken = *settings.P2PToken
		}
		if settings.P2PNetworkID != nil {
			appConfig.P2PNetworkID = *settings.P2PNetworkID
		}
		if settings.Federated != nil {
			appConfig.Federated = *settings.Federated
		}
		if settings.Galleries != nil {
			appConfig.Galleries = *settings.Galleries
		}
		if settings.BackendGalleries != nil {
			appConfig.BackendGalleries = *settings.BackendGalleries
		}
		if settings.AutoloadGalleries != nil {
			appConfig.AutoloadGalleries = *settings.AutoloadGalleries
		}
		if settings.AutoloadBackendGalleries != nil {
			appConfig.AutoloadBackendGalleries = *settings.AutoloadBackendGalleries
		}
		agentJobChanged := false
		if settings.AgentJobRetentionDays != nil {
			appConfig.AgentJobRetentionDays = *settings.AgentJobRetentionDays
			agentJobChanged = true
		}
		if settings.ApiKeys != nil {
			// API keys from env vars (startup) should be kept, runtime settings keys are added
			// Combine startup keys (env vars) with runtime settings keys
			envKeys := startupConfig.ApiKeys
			runtimeKeys := *settings.ApiKeys
			// Merge: env keys first (they take precedence), then runtime keys
			appConfig.ApiKeys = append(envKeys, runtimeKeys...)

			// Note: We only save to runtime_settings.json (not api_keys.json) to avoid duplication
			// The runtime_settings.json is the unified config file. If api_keys.json exists,
			// it will be loaded first, but runtime_settings.json takes precedence and deduplicates.
		}

		// Restart watchdog if settings changed
		if watchdogChanged {
			if settings.WatchdogEnabled != nil && !*settings.WatchdogEnabled || settings.WatchdogEnabled == nil {
				if err := app.StopWatchdog(); err != nil {
					log.Error().Err(err).Msg("Failed to stop watchdog")
					return c.JSON(http.StatusInternalServerError, SettingsResponse{
						Success: false,
						Error:   "Settings saved but failed to stop watchdog: " + err.Error(),
					})
				}
			} else {
				if err := app.RestartWatchdog(); err != nil {
					log.Error().Err(err).Msg("Failed to restart watchdog")
					return c.JSON(http.StatusInternalServerError, SettingsResponse{
						Success: false,
						Error:   "Settings saved but failed to restart watchdog: " + err.Error(),
					})
				}
			}
		}

		// Restart agent job service if retention days changed
		if agentJobChanged {
			if err := app.RestartAgentJobService(); err != nil {
				log.Error().Err(err).Msg("Failed to restart agent job service")
				return c.JSON(http.StatusInternalServerError, SettingsResponse{
					Success: false,
					Error:   "Settings saved but failed to restart agent job service: " + err.Error(),
				})
			}
		}

		// Restart P2P if P2P settings changed
		p2pChanged := settings.P2PToken != nil || settings.P2PNetworkID != nil || settings.Federated != nil
		if p2pChanged {
			if settings.P2PToken != nil && *settings.P2PToken == "" {
				// stop P2P
				if err := app.StopP2P(); err != nil {
					log.Error().Err(err).Msg("Failed to stop P2P")
					return c.JSON(http.StatusInternalServerError, SettingsResponse{
						Success: false,
						Error:   "Settings saved but failed to stop P2P: " + err.Error(),
					})
				}
			} else {
				if settings.P2PToken != nil && *settings.P2PToken == "0" {
					// generate a token if users sets 0 (disabled)
					token := p2p.GenerateToken(60, 60)
					settings.P2PToken = &token
					appConfig.P2PToken = token
				}
				// Stop existing P2P
				if err := app.RestartP2P(); err != nil {
					log.Error().Err(err).Msg("Failed to stop P2P")
					return c.JSON(http.StatusInternalServerError, SettingsResponse{
						Success: false,
						Error:   "Settings saved but failed to stop P2P: " + err.Error(),
					})
				}
			}
		}

		return c.JSON(http.StatusOK, SettingsResponse{
			Success: true,
			Message: "Settings updated successfully",
		})
	}
}
