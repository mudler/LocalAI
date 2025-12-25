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
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/xlog"
)

// GetSettingsEndpoint returns current settings with precedence (env > file > defaults)
func GetSettingsEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		appConfig := app.ApplicationConfig()
		settings := appConfig.ToRuntimeSettings()
		return c.JSON(http.StatusOK, settings)
	}
}

// UpdateSettingsEndpoint updates settings, saves to file, and applies immediately
func UpdateSettingsEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		appConfig := app.ApplicationConfig()
		startupConfig := app.StartupConfig()

		if startupConfig == nil {
			startupConfig = appConfig
		}

		body, err := io.ReadAll(c.Request().Body)
		if err != nil {
			return c.JSON(http.StatusBadRequest, schema.SettingsResponse{
				Success: false,
				Error:   "Failed to read request body: " + err.Error(),
			})
		}

		var settings config.RuntimeSettings
		if err := json.Unmarshal(body, &settings); err != nil {
			return c.JSON(http.StatusBadRequest, schema.SettingsResponse{
				Success: false,
				Error:   "Failed to parse JSON: " + err.Error(),
			})
		}

		// Validate timeouts if provided
		if settings.WatchdogIdleTimeout != nil {
			if _, err := time.ParseDuration(*settings.WatchdogIdleTimeout); err != nil {
				return c.JSON(http.StatusBadRequest, schema.SettingsResponse{
					Success: false,
					Error:   "Invalid watchdog_idle_timeout format: " + err.Error(),
				})
			}
		}
		if settings.WatchdogBusyTimeout != nil {
			if _, err := time.ParseDuration(*settings.WatchdogBusyTimeout); err != nil {
				return c.JSON(http.StatusBadRequest, schema.SettingsResponse{
					Success: false,
					Error:   "Invalid watchdog_busy_timeout format: " + err.Error(),
				})
			}
		}
		if settings.WatchdogInterval != nil {
			if _, err := time.ParseDuration(*settings.WatchdogInterval); err != nil {
				return c.JSON(http.StatusBadRequest, schema.SettingsResponse{
					Success: false,
					Error:   "Invalid watchdog_interval format: " + err.Error(),
				})
			}
		}
		if settings.LRUEvictionRetryInterval != nil {
			if _, err := time.ParseDuration(*settings.LRUEvictionRetryInterval); err != nil {
				return c.JSON(http.StatusBadRequest, schema.SettingsResponse{
					Success: false,
					Error:   "Invalid lru_eviction_retry_interval format: " + err.Error(),
				})
			}
		}

		// Save to file
		if appConfig.DynamicConfigsDir == "" {
			return c.JSON(http.StatusBadRequest, schema.SettingsResponse{
				Success: false,
				Error:   "DynamicConfigsDir is not set",
			})
		}

		settingsFile := filepath.Join(appConfig.DynamicConfigsDir, "runtime_settings.json")
		settingsJSON, err := json.MarshalIndent(settings, "", "  ")
		if err != nil {
			return c.JSON(http.StatusInternalServerError, schema.SettingsResponse{
				Success: false,
				Error:   "Failed to marshal settings: " + err.Error(),
			})
		}

		if err := os.WriteFile(settingsFile, settingsJSON, 0600); err != nil {
			return c.JSON(http.StatusInternalServerError, schema.SettingsResponse{
				Success: false,
				Error:   "Failed to write settings file: " + err.Error(),
			})
		}

		// Apply settings using centralized method
		watchdogChanged := appConfig.ApplyRuntimeSettings(&settings)

		// Handle API keys specially (merge with startup keys)
		if settings.ApiKeys != nil {
			envKeys := startupConfig.ApiKeys
			runtimeKeys := *settings.ApiKeys
			appConfig.ApiKeys = append(envKeys, runtimeKeys...)
		}

		// Update watchdog dynamically for settings that don't require restart
		if settings.ForceEvictionWhenBusy != nil {
			currentWD := app.ModelLoader().GetWatchDog()
			if currentWD != nil {
				currentWD.SetForceEvictionWhenBusy(*settings.ForceEvictionWhenBusy)
				xlog.Info("Updated watchdog force eviction when busy setting", "forceEvictionWhenBusy", *settings.ForceEvictionWhenBusy)
			}
		}

		// Update ModelLoader LRU eviction retry settings dynamically
		maxRetries := appConfig.LRUEvictionMaxRetries
		retryInterval := appConfig.LRUEvictionRetryInterval
		if settings.LRUEvictionMaxRetries != nil {
			maxRetries = *settings.LRUEvictionMaxRetries
		}
		if settings.LRUEvictionRetryInterval != nil {
			if dur, err := time.ParseDuration(*settings.LRUEvictionRetryInterval); err == nil {
				retryInterval = dur
			}
		}
		if settings.LRUEvictionMaxRetries != nil || settings.LRUEvictionRetryInterval != nil {
			app.ModelLoader().SetLRUEvictionRetrySettings(maxRetries, retryInterval)
			xlog.Info("Updated LRU eviction retry settings", "maxRetries", maxRetries, "retryInterval", retryInterval)
		}

		// Check if agent job retention changed
		agentJobChanged := settings.AgentJobRetentionDays != nil

		// Restart watchdog if settings changed
		if watchdogChanged {
			if settings.WatchdogEnabled != nil && !*settings.WatchdogEnabled {
				if err := app.StopWatchdog(); err != nil {
					xlog.Error("Failed to stop watchdog", "error", err)
					return c.JSON(http.StatusInternalServerError, schema.SettingsResponse{
						Success: false,
						Error:   "Settings saved but failed to stop watchdog: " + err.Error(),
					})
				}
			} else {
				if err := app.RestartWatchdog(); err != nil {
					xlog.Error("Failed to restart watchdog", "error", err)
					return c.JSON(http.StatusInternalServerError, schema.SettingsResponse{
						Success: false,
						Error:   "Settings saved but failed to restart watchdog: " + err.Error(),
					})
				}
			}
		}

		// Restart agent job service if retention days changed
		if agentJobChanged {
			if err := app.RestartAgentJobService(); err != nil {
				xlog.Error("Failed to restart agent job service", "error", err)
				return c.JSON(http.StatusInternalServerError, schema.SettingsResponse{
					Success: false,
					Error:   "Settings saved but failed to restart agent job service: " + err.Error(),
				})
			}
		}

		// Restart P2P if P2P settings changed
		p2pChanged := settings.P2PToken != nil || settings.P2PNetworkID != nil || settings.Federated != nil
		if p2pChanged {
			if settings.P2PToken != nil && *settings.P2PToken == "" {
				if err := app.StopP2P(); err != nil {
					xlog.Error("Failed to stop P2P", "error", err)
					return c.JSON(http.StatusInternalServerError, schema.SettingsResponse{
						Success: false,
						Error:   "Settings saved but failed to stop P2P: " + err.Error(),
					})
				}
			} else {
				if settings.P2PToken != nil && *settings.P2PToken == "0" {
					token := p2p.GenerateToken(60, 60)
					settings.P2PToken = &token
					appConfig.P2PToken = token
				}
				if err := app.RestartP2P(); err != nil {
					xlog.Error("Failed to restart P2P", "error", err)
					return c.JSON(http.StatusInternalServerError, schema.SettingsResponse{
						Success: false,
						Error:   "Settings saved but failed to restart P2P: " + err.Error(),
					})
				}
			}
		}

		return c.JSON(http.StatusOK, schema.SettingsResponse{
			Success: true,
			Message: "Settings updated successfully",
		})
	}
}
