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
	"github.com/rs/zerolog/log"
)

type SettingsResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	Message string `json:"message,omitempty"`
}

type RuntimeSettings struct {
	WatchdogEnabled         *bool   `json:"watchdog_enabled,omitempty"`
	WatchdogIdleEnabled     *bool   `json:"watchdog_idle_enabled,omitempty"`
	WatchdogBusyEnabled     *bool   `json:"watchdog_busy_enabled,omitempty"`
	WatchdogIdleTimeout     *string `json:"watchdog_idle_timeout,omitempty"`
	WatchdogBusyTimeout     *string `json:"watchdog_busy_timeout,omitempty"`
	SingleBackend           *bool   `json:"single_backend,omitempty"`
	ParallelBackendRequests *bool   `json:"parallel_backend_requests,omitempty"`
}

type CurrentSettings struct {
	WatchdogEnabled         bool   `json:"watchdog_enabled"`
	WatchdogIdleEnabled     bool   `json:"watchdog_idle_enabled"`
	WatchdogBusyEnabled     bool   `json:"watchdog_busy_enabled"`
	WatchdogIdleTimeout     string `json:"watchdog_idle_timeout"`
	WatchdogBusyTimeout     string `json:"watchdog_busy_timeout"`
	SingleBackend           bool   `json:"single_backend"`
	ParallelBackendRequests bool   `json:"parallel_backend_requests"`
	Source                  string `json:"source"` // "env", "file", or "default"
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

		settings := CurrentSettings{}

		// Determine if values came from env vars by comparing with startup config
		// If current values match startup values, they came from env vars (or defaults)
		// If current values differ from startup, file changed them (so not from env var)
		envWatchdogIdle := appConfig.WatchDogIdle == startupConfig.WatchDogIdle
		envWatchdogBusy := appConfig.WatchDogBusy == startupConfig.WatchDogBusy
		envWatchdogIdleTimeout := appConfig.WatchDogIdleTimeout == startupConfig.WatchDogIdleTimeout
		envWatchdogBusyTimeout := appConfig.WatchDogBusyTimeout == startupConfig.WatchDogBusyTimeout
		envSingleBackend := appConfig.SingleBackend == startupConfig.SingleBackend
		envParallelRequests := appConfig.ParallelBackendRequests == startupConfig.ParallelBackendRequests

		// Determine source: if any setting matches startup config, it's from env (or default)
		// If any setting differs from startup, it's from file
		settings.WatchdogIdleEnabled = appConfig.WatchDogIdle
		settings.WatchdogBusyEnabled = appConfig.WatchDogBusy
		settings.WatchdogEnabled = appConfig.WatchDog
		settings.SingleBackend = appConfig.SingleBackend
		settings.ParallelBackendRequests = appConfig.ParallelBackendRequests

		if appConfig.WatchDogIdleTimeout > 0 {
			settings.WatchdogIdleTimeout = appConfig.WatchDogIdleTimeout.String()
		} else {
			settings.WatchdogIdleTimeout = "15m" // default
		}

		if appConfig.WatchDogBusyTimeout > 0 {
			settings.WatchdogBusyTimeout = appConfig.WatchDogBusyTimeout.String()
		} else {
			settings.WatchdogBusyTimeout = "5m" // default
		}

		// Determine overall source: if all settings match startup, it's "env" or "default"
		// If any setting differs, it's "file"
		if envWatchdogIdle && envWatchdogBusy && envWatchdogIdleTimeout && envWatchdogBusyTimeout && envSingleBackend && envParallelRequests {
			// All match startup - check if they're at defaults
			if !appConfig.WatchDog && !appConfig.SingleBackend && !appConfig.ParallelBackendRequests && appConfig.WatchDogIdleTimeout == 0 && appConfig.WatchDogBusyTimeout == 0 {
				settings.Source = "default"
			} else {
				settings.Source = "env"
			}
		} else {
			settings.Source = "file"
		}

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

		// Check if env vars are set by comparing with startup config
		// If current values match startup values, they came from env vars (or defaults)
		// If current values differ from startup, file changed them (so not from env var)
		envWatchdogIdle := appConfig.WatchDogIdle == startupConfig.WatchDogIdle
		envWatchdogBusy := appConfig.WatchDogBusy == startupConfig.WatchDogBusy
		envWatchdogIdleTimeout := appConfig.WatchDogIdleTimeout == startupConfig.WatchDogIdleTimeout
		envWatchdogBusyTimeout := appConfig.WatchDogBusyTimeout == startupConfig.WatchDogBusyTimeout
		envSingleBackend := appConfig.SingleBackend == startupConfig.SingleBackend
		envParallelRequests := appConfig.ParallelBackendRequests == startupConfig.ParallelBackendRequests

		if envWatchdogIdle || envWatchdogBusy || envWatchdogIdleTimeout || envWatchdogBusyTimeout || envSingleBackend || envParallelRequests {
			return c.JSON(http.StatusBadRequest, SettingsResponse{
				Success: false,
				Error:   "Cannot update settings: environment variables are set and take precedence. Please unset environment variables first.",
			})
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

		// Apply settings immediately
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

		// Restart watchdog if settings changed
		if watchdogChanged {
			if err := app.RestartWatchdog(); err != nil {
				log.Error().Err(err).Msg("Failed to restart watchdog")
				return c.JSON(http.StatusInternalServerError, SettingsResponse{
					Success: false,
					Error:   "Settings saved but failed to restart watchdog: " + err.Error(),
				})
			}
		}

		return c.JSON(http.StatusOK, SettingsResponse{
			Success: true,
			Message: "Settings updated successfully",
		})
	}
}
