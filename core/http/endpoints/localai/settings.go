package localai

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	WatchdogIdleTimeout    *string `json:"watchdog_idle_timeout,omitempty"`
	WatchdogBusyTimeout     *string `json:"watchdog_busy_timeout,omitempty"`
	SingleBackend           *bool   `json:"single_backend,omitempty"`
	ParallelBackendRequests *bool   `json:"parallel_backend_requests,omitempty"`
}

type CurrentSettings struct {
	WatchdogEnabled         bool   `json:"watchdog_enabled"`
	WatchdogIdleEnabled     bool   `json:"watchdog_idle_enabled"`
	WatchdogBusyEnabled     bool   `json:"watchdog_busy_enabled"`
	WatchdogIdleTimeout    string `json:"watchdog_idle_timeout"`
	WatchdogBusyTimeout     string `json:"watchdog_busy_timeout"`
	SingleBackend           bool   `json:"single_backend"`
	ParallelBackendRequests bool   `json:"parallel_backend_requests"`
	Source                  string `json:"source"` // "env", "file", or "default"
}

// getEnvVarWithPrecedence checks multiple env var names and returns the first one found
func getEnvVarWithPrecedence(names ...string) string {
	for _, name := range names {
		if val := os.Getenv(name); val != "" {
			return val
		}
	}
	return ""
}

// getBoolEnvVar returns true if env var is set to "true", "1", "yes", or "on"
func getBoolEnvVar(names ...string) (bool, bool) {
	val := getEnvVarWithPrecedence(names...)
	if val == "" {
		return false, false
	}
	val = strings.ToLower(val)
	return val == "true" || val == "1" || val == "yes" || val == "on", true
}

// GetSettingsEndpoint returns current settings with precedence (env > file > defaults)
func GetSettingsEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		appConfig := app.ApplicationConfig()
		
		// Check env vars first
		envWatchdogIdle, envWatchdogIdleSet := getBoolEnvVar("LOCALAI_WATCHDOG_IDLE", "WATCHDOG_IDLE")
		envWatchdogBusy, envWatchdogBusySet := getBoolEnvVar("LOCALAI_WATCHDOG_BUSY", "WATCHDOG_BUSY")
		envWatchdogIdleTimeout := getEnvVarWithPrecedence("LOCALAI_WATCHDOG_IDLE_TIMEOUT", "WATCHDOG_IDLE_TIMEOUT")
		envWatchdogBusyTimeout := getEnvVarWithPrecedence("LOCALAI_WATCHDOG_BUSY_TIMEOUT", "WATCHDOG_BUSY_TIMEOUT")
		envSingleBackend, envSingleBackendSet := getBoolEnvVar("LOCALAI_SINGLE_ACTIVE_BACKEND", "SINGLE_ACTIVE_BACKEND")
		envParallelRequests, envParallelRequestsSet := getBoolEnvVar("LOCALAI_PARALLEL_REQUESTS", "PARALLEL_REQUESTS")

		settings := CurrentSettings{}

		// Determine source and values
		if envWatchdogIdleSet || envWatchdogBusySet {
			settings.WatchdogIdleEnabled = envWatchdogIdle
			settings.WatchdogBusyEnabled = envWatchdogBusy
			settings.WatchdogEnabled = envWatchdogIdle || envWatchdogBusy
			settings.Source = "env"
		} else {
			settings.WatchdogIdleEnabled = appConfig.WatchDogIdle
			settings.WatchdogBusyEnabled = appConfig.WatchDogBusy
			settings.WatchdogEnabled = appConfig.WatchDog
			settings.Source = "file"
		}

		if envWatchdogIdleTimeout != "" {
			settings.WatchdogIdleTimeout = envWatchdogIdleTimeout
			if settings.Source == "file" {
				settings.Source = "env"
			}
		} else {
			if appConfig.WatchDogIdleTimeout > 0 {
				settings.WatchdogIdleTimeout = appConfig.WatchDogIdleTimeout.String()
			} else {
				settings.WatchdogIdleTimeout = "15m" // default
			}
		}

		if envWatchdogBusyTimeout != "" {
			settings.WatchdogBusyTimeout = envWatchdogBusyTimeout
			if settings.Source == "file" {
				settings.Source = "env"
			}
		} else {
			if appConfig.WatchDogBusyTimeout > 0 {
				settings.WatchdogBusyTimeout = appConfig.WatchDogBusyTimeout.String()
			} else {
				settings.WatchdogBusyTimeout = "5m" // default
			}
		}

		if envSingleBackendSet {
			settings.SingleBackend = envSingleBackend
			if settings.Source == "file" {
				settings.Source = "env"
			}
		} else {
			settings.SingleBackend = appConfig.SingleBackend
		}

		if envParallelRequestsSet {
			settings.ParallelBackendRequests = envParallelRequests
			if settings.Source == "file" {
				settings.Source = "env"
			}
		} else {
			settings.ParallelBackendRequests = appConfig.ParallelBackendRequests
		}

		// If no env vars set and no file values, use defaults
		if settings.Source == "file" && !appConfig.WatchDog && !appConfig.SingleBackend && !appConfig.ParallelBackendRequests {
			settings.Source = "default"
		}

		return c.JSON(http.StatusOK, settings)
	}
}

// UpdateSettingsEndpoint updates settings, saves to file, and applies immediately
func UpdateSettingsEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		appConfig := app.ApplicationConfig()

		// Check if env vars are set - if so, reject the update
		envWatchdogIdleSet := getEnvVarWithPrecedence("LOCALAI_WATCHDOG_IDLE", "WATCHDOG_IDLE") != ""
		envWatchdogBusySet := getEnvVarWithPrecedence("LOCALAI_WATCHDOG_BUSY", "WATCHDOG_BUSY") != ""
		envWatchdogIdleTimeoutSet := getEnvVarWithPrecedence("LOCALAI_WATCHDOG_IDLE_TIMEOUT", "WATCHDOG_IDLE_TIMEOUT") != ""
		envWatchdogBusyTimeoutSet := getEnvVarWithPrecedence("LOCALAI_WATCHDOG_BUSY_TIMEOUT", "WATCHDOG_BUSY_TIMEOUT") != ""
		envSingleBackendSet := getEnvVarWithPrecedence("LOCALAI_SINGLE_ACTIVE_BACKEND", "SINGLE_ACTIVE_BACKEND") != ""
		envParallelRequestsSet := getEnvVarWithPrecedence("LOCALAI_PARALLEL_REQUESTS", "PARALLEL_REQUESTS") != ""

		if envWatchdogIdleSet || envWatchdogBusySet || envWatchdogIdleTimeoutSet || envWatchdogBusyTimeoutSet || envSingleBackendSet || envParallelRequestsSet {
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

