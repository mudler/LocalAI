package localai

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	httpUtils "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/utils"

	"gopkg.in/yaml.v3"
)

// GetEditModelPage renders the edit model page with current configuration
func GetEditModelPage(cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		modelName := c.Param("name")
		if decoded, err := url.PathUnescape(modelName); err == nil {
			modelName = decoded
		}
		if modelName == "" {
			response := ModelResponse{
				Success: false,
				Error:   "Model name is required",
			}
			return c.JSON(http.StatusBadRequest, response)
		}

		modelConfig, exists := cl.GetModelConfig(modelName)
		if !exists {
			response := ModelResponse{
				Success: false,
				Error:   "Model configuration not found",
			}
			return c.JSON(http.StatusNotFound, response)
		}

		modelConfigFile := modelConfig.GetModelConfigFile()
		if modelConfigFile == "" {
			response := ModelResponse{
				Success: false,
				Error:   "Model configuration file not found",
			}
			return c.JSON(http.StatusNotFound, response)
		}
		configData, err := os.ReadFile(modelConfigFile)
		if err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to read configuration file: " + err.Error(),
			}
			return c.JSON(http.StatusInternalServerError, response)
		}

		// Render the edit page with the current configuration
		templateData := struct {
			Title                  string
			ModelName              string
			Config                 *config.ModelConfig
			ConfigJSON             string
			ConfigYAML             string
			BaseURL                string
			Version                string
			DisableRuntimeSettings bool
		}{
			Title:                  "LocalAI - Edit Model " + modelName,
			ModelName:              modelName,
			Config:                 &modelConfig,
			ConfigYAML:             string(configData),
			BaseURL:                httpUtils.BaseURL(c),
			Version:                internal.PrintableVersion(),
			DisableRuntimeSettings: appConfig.DisableRuntimeSettings,
		}

		return c.Render(http.StatusOK, "views/model-editor", templateData)
	}
}

// EditModelEndpoint handles updating existing model configurations
func EditModelEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		modelName := c.Param("name")
		if decoded, err := url.PathUnescape(modelName); err == nil {
			modelName = decoded
		}
		if modelName == "" {
			response := ModelResponse{
				Success: false,
				Error:   "Model name is required",
			}
			return c.JSON(http.StatusBadRequest, response)
		}

		modelConfig, exists := cl.GetModelConfig(modelName)
		if !exists {
			response := ModelResponse{
				Success: false,
				Error:   "Existing model configuration not found",
			}
			return c.JSON(http.StatusNotFound, response)
		}

		// Get the raw body
		body, err := io.ReadAll(c.Request().Body)
		if err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to read request body: " + err.Error(),
			}
			return c.JSON(http.StatusBadRequest, response)
		}
		if len(body) == 0 {
			response := ModelResponse{
				Success: false,
				Error:   "Request body is empty",
			}
			return c.JSON(http.StatusBadRequest, response)
		}

		// Check content to see if it's a valid model config
		var req config.ModelConfig

		// Parse YAML
		if err := yaml.Unmarshal(body, &req); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to parse YAML: " + err.Error(),
			}
			return c.JSON(http.StatusBadRequest, response)
		}

		// Validate required fields
		if req.Name == "" {
			response := ModelResponse{
				Success: false,
				Error:   "Name is required",
			}
			return c.JSON(http.StatusBadRequest, response)
		}

		// Validate the configuration
		if valid, _ := req.Validate(); !valid {
			response := ModelResponse{
				Success: false,
				Error:   "Validation failed",
				Details: []string{"Configuration validation failed. Please check your YAML syntax and required fields."},
			}
			return c.JSON(http.StatusBadRequest, response)
		}

		// Load the existing configuration
		configPath := modelConfig.GetModelConfigFile()
		modelsPath := appConfig.SystemState.Model.ModelsPath
		if err := utils.VerifyPath(configPath, modelsPath); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Model configuration not trusted: " + err.Error(),
			}
			return c.JSON(http.StatusNotFound, response)
		}

		// Detect a rename: the URL param (old name) differs from the name field
		// in the posted YAML. When that happens we must rename the on-disk file
		// so that <name>.yaml stays in sync with the internal name field —
		// otherwise a subsequent config reload indexes the file under the new
		// name while the old key lingers in memory, producing duplicates in the UI.
		renamed := req.Name != modelName
		if renamed {
			if strings.ContainsRune(req.Name, os.PathSeparator) || strings.Contains(req.Name, "/") || strings.Contains(req.Name, "\\") {
				response := ModelResponse{
					Success: false,
					Error:   "Model name must not contain path separators",
				}
				return c.JSON(http.StatusBadRequest, response)
			}
			if _, exists := cl.GetModelConfig(req.Name); exists {
				response := ModelResponse{
					Success: false,
					Error:   fmt.Sprintf("A model named %q already exists", req.Name),
				}
				return c.JSON(http.StatusConflict, response)
			}
			newConfigPath := filepath.Join(modelsPath, req.Name+".yaml")
			if err := utils.VerifyPath(newConfigPath, modelsPath); err != nil {
				response := ModelResponse{
					Success: false,
					Error:   "New model configuration path not trusted: " + err.Error(),
				}
				return c.JSON(http.StatusBadRequest, response)
			}
			if _, err := os.Stat(newConfigPath); err == nil {
				response := ModelResponse{
					Success: false,
					Error:   fmt.Sprintf("A configuration file for %q already exists on disk", req.Name),
				}
				return c.JSON(http.StatusConflict, response)
			} else if !errors.Is(err, os.ErrNotExist) {
				response := ModelResponse{
					Success: false,
					Error:   "Failed to check for existing configuration: " + err.Error(),
				}
				return c.JSON(http.StatusInternalServerError, response)
			}

			if err := os.WriteFile(newConfigPath, body, 0644); err != nil {
				response := ModelResponse{
					Success: false,
					Error:   "Failed to write configuration file: " + err.Error(),
				}
				return c.JSON(http.StatusInternalServerError, response)
			}
			if configPath != newConfigPath {
				if err := os.Remove(configPath); err != nil && !errors.Is(err, os.ErrNotExist) {
					fmt.Printf("Warning: Failed to remove old configuration file %q: %v\n", configPath, err)
				}
			}

			// Rename the gallery metadata file if one exists, so the delete
			// flow (which looks up ._gallery_<name>.yaml) can still find it.
			oldGalleryPath := filepath.Join(modelsPath, gallery.GalleryFileName(modelName))
			newGalleryPath := filepath.Join(modelsPath, gallery.GalleryFileName(req.Name))
			if _, err := os.Stat(oldGalleryPath); err == nil {
				if err := os.Rename(oldGalleryPath, newGalleryPath); err != nil {
					fmt.Printf("Warning: Failed to rename gallery metadata from %q to %q: %v\n", oldGalleryPath, newGalleryPath, err)
				}
			}

			// Drop the stale in-memory entry before the reload so we don't
			// surface both names to callers between the reload scan steps.
			cl.RemoveModelConfig(modelName)
			configPath = newConfigPath
		} else {
			// Write new content to file
			if err := os.WriteFile(configPath, body, 0644); err != nil {
				response := ModelResponse{
					Success: false,
					Error:   "Failed to write configuration file: " + err.Error(),
				}
				return c.JSON(http.StatusInternalServerError, response)
			}
		}

		// Reload configurations
		if err := cl.LoadModelConfigsFromPath(modelsPath, appConfig.ToConfigLoaderOptions()...); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to reload configurations: " + err.Error(),
			}
			return c.JSON(http.StatusInternalServerError, response)
		}

		// Shutdown the running model to apply new configuration (e.g., context_size)
		// The model will be reloaded on the next inference request.
		// Shutdown uses the old name because that's what the running instance
		// (if any) was started with.
		if err := ml.ShutdownModel(modelName); err != nil {
			// Log the error but don't fail the request - the config was saved successfully
			// The model can still be manually reloaded or restarted
			fmt.Printf("Warning: Failed to shutdown model '%s': %v\n", modelName, err)
		}

		// Preload the model
		if err := cl.Preload(modelsPath); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to preload model: " + err.Error(),
			}
			return c.JSON(http.StatusInternalServerError, response)
		}

		// Return success response
		msg := fmt.Sprintf("Model '%s' updated successfully. Model has been reloaded with new configuration.", req.Name)
		if renamed {
			msg = fmt.Sprintf("Model '%s' renamed to '%s' and updated successfully.", modelName, req.Name)
		}
		response := ModelResponse{
			Success:  true,
			Message:  msg,
			Filename: configPath,
			Config:   req,
		}
		return c.JSON(200, response)
	}
}

// ReloadModelsEndpoint handles reloading model configurations from disk
func ReloadModelsEndpoint(cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Reload configurations
		if err := cl.LoadModelConfigsFromPath(appConfig.SystemState.Model.ModelsPath, appConfig.ToConfigLoaderOptions()...); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to reload configurations: " + err.Error(),
			}
			return c.JSON(http.StatusInternalServerError, response)
		}

		// Preload the models
		if err := cl.Preload(appConfig.SystemState.Model.ModelsPath); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to preload models: " + err.Error(),
			}
			return c.JSON(http.StatusInternalServerError, response)
		}

		// Return success response
		response := ModelResponse{
			Success: true,
			Message: "Model configurations reloaded successfully",
		}
		return c.JSON(http.StatusOK, response)
	}
}
