package localai

import (
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/utils"

	"gopkg.in/yaml.v3"
)

// TogglePinnedModelEndpoint handles pinning or unpinning a model.
// Pinned models are excluded from idle unloading, LRU eviction, and memory-pressure eviction.
//
// @Summary      Toggle model pinned status
// @Description  Pin or unpin a model. Pinned models stay loaded and are excluded from automatic eviction.
// @Tags         config
// @Param        name    path  string  true  "Model name"
// @Param        action  path  string  true  "Action: 'pin' or 'unpin'"
// @Success      200  {object}  ModelResponse
// @Failure      400  {object}  ModelResponse
// @Failure      404  {object}  ModelResponse
// @Failure      500  {object}  ModelResponse
// @Router       /api/models/toggle-pinned/{name}/{action} [put]
func TogglePinnedModelEndpoint(cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, syncPinnedFn func()) echo.HandlerFunc {
	return func(c echo.Context) error {
		modelName := c.Param("name")
		if decoded, err := url.PathUnescape(modelName); err == nil {
			modelName = decoded
		}
		if modelName == "" {
			return c.JSON(http.StatusBadRequest, ModelResponse{
				Success: false,
				Error:   "Model name is required",
			})
		}

		action := c.Param("action")
		if action != "pin" && action != "unpin" {
			return c.JSON(http.StatusBadRequest, ModelResponse{
				Success: false,
				Error:   "Action must be 'pin' or 'unpin'",
			})
		}

		// Get existing model config
		modelConfig, exists := cl.GetModelConfig(modelName)
		if !exists {
			return c.JSON(http.StatusNotFound, ModelResponse{
				Success: false,
				Error:   "Model configuration not found",
			})
		}

		// Get the config file path
		configPath := modelConfig.GetModelConfigFile()
		if configPath == "" {
			return c.JSON(http.StatusNotFound, ModelResponse{
				Success: false,
				Error:   "Model configuration file not found",
			})
		}

		// Verify the path is trusted
		if err := utils.VerifyPath(configPath, appConfig.SystemState.Model.ModelsPath); err != nil {
			return c.JSON(http.StatusForbidden, ModelResponse{
				Success: false,
				Error:   "Model configuration not trusted: " + err.Error(),
			})
		}

		// Read the existing config file
		configData, err := os.ReadFile(configPath)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, ModelResponse{
				Success: false,
				Error:   "Failed to read configuration file: " + err.Error(),
			})
		}

		// Parse the YAML config as a generic map to preserve all fields
		var configMap map[string]interface{}
		if err := yaml.Unmarshal(configData, &configMap); err != nil {
			return c.JSON(http.StatusInternalServerError, ModelResponse{
				Success: false,
				Error:   "Failed to parse configuration file: " + err.Error(),
			})
		}

		// Update the pinned field
		pinned := action == "pin"
		if pinned {
			configMap["pinned"] = true
		} else {
			// Remove the pinned key entirely when unpinning (clean YAML)
			delete(configMap, "pinned")
		}

		// Marshal back to YAML
		updatedData, err := yaml.Marshal(configMap)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, ModelResponse{
				Success: false,
				Error:   "Failed to serialize configuration: " + err.Error(),
			})
		}

		// Write updated config back to file
		if err := os.WriteFile(configPath, updatedData, 0644); err != nil {
			return c.JSON(http.StatusInternalServerError, ModelResponse{
				Success: false,
				Error:   "Failed to write configuration file: " + err.Error(),
			})
		}

		// Reload model configurations from disk
		if err := cl.LoadModelConfigsFromPath(appConfig.SystemState.Model.ModelsPath, appConfig.ToConfigLoaderOptions()...); err != nil {
			return c.JSON(http.StatusInternalServerError, ModelResponse{
				Success: false,
				Error:   "Failed to reload configurations: " + err.Error(),
			})
		}

		// Sync pinned models to the watchdog
		if syncPinnedFn != nil {
			syncPinnedFn()
		}

		msg := fmt.Sprintf("Model '%s' has been %sned successfully.", modelName, action)
		if pinned {
			msg += " The model will be excluded from automatic eviction."
		}

		return c.JSON(http.StatusOK, ModelResponse{
			Success:  true,
			Message:  msg,
			Filename: configPath,
		})
	}
}
