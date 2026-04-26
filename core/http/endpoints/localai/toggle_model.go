package localai

import (
	"fmt"
	"net/http"
	"net/url"
	"os"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/utils"

	"gopkg.in/yaml.v3"
)

// ToggleModelEndpoint handles enabling or disabling a model from being loaded on demand.
// When disabled, the model remains in the collection but will not be loaded when requested.
//
// @Summary      Toggle model enabled/disabled status
// @Description  Enable or disable a model from being loaded on demand. Disabled models remain installed but cannot be loaded.
// @Tags         config
// @Param        name  path  string  true  "Model name"
// @Param        action  path  string  true  "Action: 'enable' or 'disable'"
// @Success      200  {object}  ModelResponse
// @Failure      400  {object}  ModelResponse
// @Failure      404  {object}  ModelResponse
// @Failure      500  {object}  ModelResponse
// @Router       /api/models/{name}/{action} [put]
func ToggleStateModelEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
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
		if action != "enable" && action != "disable" {
			return c.JSON(http.StatusBadRequest, ModelResponse{
				Success: false,
				Error:   "Action must be 'enable' or 'disable'",
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

		// Update the disabled field
		disabled := action == "disable"
		if disabled {
			configMap["disabled"] = true
		} else {
			// Remove the disabled key entirely when enabling (clean YAML)
			delete(configMap, "disabled")
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

		// If disabling, also shutdown the model if it's currently running
		if disabled {
			if err := ml.ShutdownModel(modelName); err != nil {
				// Log but don't fail - the config was saved successfully
				fmt.Printf("Warning: Failed to shutdown model '%s' during disable: %v\n", modelName, err)
			}
		}

		msg := fmt.Sprintf("Model '%s' has been %sd successfully.", modelName, action)
		if disabled {
			msg += " The model will not be loaded on demand until re-enabled."
		}

		return c.JSON(http.StatusOK, ModelResponse{
			Success:  true,
			Message:  msg,
			Filename: configPath,
		})
	}
}
