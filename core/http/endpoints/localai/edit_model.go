package localai

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/config"
	httpUtils "github.com/mudler/LocalAI/core/http/utils"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/utils"

	"gopkg.in/yaml.v3"
)

// GetEditModelPage renders the edit model page with current configuration
func GetEditModelPage(cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig) fiber.Handler {
	return func(c *fiber.Ctx) error {
		modelName := c.Params("name")
		if modelName == "" {
			response := ModelResponse{
				Success: false,
				Error:   "Model name is required",
			}
			return c.Status(400).JSON(response)
		}

		modelConfig, exists := cl.GetModelConfig(modelName)
		if !exists {
			response := ModelResponse{
				Success: false,
				Error:   "Model configuration not found",
			}
			return c.Status(404).JSON(response)
		}

		configData, err := yaml.Marshal(modelConfig)
		if err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to marshal configuration: " + err.Error(),
			}
			return c.Status(500).JSON(response)
		}

		// Marshal the config to JSON for the template
		configJSON, err := json.Marshal(modelConfig)
		if err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to marshal configuration: " + err.Error(),
			}
			return c.Status(500).JSON(response)
		}

		// Render the edit page with the current configuration
		templateData := struct {
			Title      string
			ModelName  string
			Config     *config.ModelConfig
			ConfigJSON string
			ConfigYAML string
			BaseURL    string
			Version    string
		}{
			Title:      "LocalAI - Edit Model " + modelName,
			ModelName:  modelName,
			Config:     &modelConfig,
			ConfigJSON: string(configJSON),
			ConfigYAML: string(configData),
			BaseURL:    httpUtils.BaseURL(c),
			Version:    internal.PrintableVersion(),
		}

		return c.Render("views/model-editor", templateData)
	}
}

// EditModelEndpoint handles updating existing model configurations
func EditModelEndpoint(cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig) fiber.Handler {
	return func(c *fiber.Ctx) error {
		modelName := c.Params("name")
		if modelName == "" {
			response := ModelResponse{
				Success: false,
				Error:   "Model name is required",
			}
			return c.Status(400).JSON(response)
		}

		// Get the raw body
		body := c.Body()
		if len(body) == 0 {
			response := ModelResponse{
				Success: false,
				Error:   "Request body is empty",
			}
			return c.Status(400).JSON(response)
		}

		// Check content type to determine how to parse
		contentType := string(c.Context().Request.Header.ContentType())
		var req config.ModelConfig
		var err error

		if strings.Contains(contentType, "application/json") {
			// Parse JSON
			if err := json.Unmarshal(body, &req); err != nil {
				response := ModelResponse{
					Success: false,
					Error:   "Failed to parse JSON: " + err.Error(),
				}
				return c.Status(400).JSON(response)
			}
		} else if strings.Contains(contentType, "application/x-yaml") || strings.Contains(contentType, "text/yaml") {
			// Parse YAML
			if err := yaml.Unmarshal(body, &req); err != nil {
				response := ModelResponse{
					Success: false,
					Error:   "Failed to parse YAML: " + err.Error(),
				}
				return c.Status(400).JSON(response)
			}
		} else {
			// Try to auto-detect format
			if strings.TrimSpace(string(body))[0] == '{' {
				// Looks like JSON
				if err := json.Unmarshal(body, &req); err != nil {
					response := ModelResponse{
						Success: false,
						Error:   "Failed to parse JSON: " + err.Error(),
					}
					return c.Status(400).JSON(response)
				}
			} else {
				// Assume YAML
				if err := yaml.Unmarshal(body, &req); err != nil {
					response := ModelResponse{
						Success: false,
						Error:   "Failed to parse YAML: " + err.Error(),
					}
					return c.Status(400).JSON(response)
				}
			}
		}

		// Validate required fields
		if req.Name == "" {
			response := ModelResponse{
				Success: false,
				Error:   "Name is required",
			}
			return c.Status(400).JSON(response)
		}

		// Load the existing configuration
		configPath := filepath.Join(appConfig.SystemState.Model.ModelsPath, modelName+".yaml")
		if err := utils.VerifyPath(modelName+".yaml", appConfig.SystemState.Model.ModelsPath); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Model configuration not trusted: " + err.Error(),
			}
			return c.Status(404).JSON(response)
		}

		// Set defaults
		req.SetDefaults()

		// Validate the configuration
		if !req.Validate() {
			response := ModelResponse{
				Success: false,
				Error:   "Validation failed",
				Details: []string{"Configuration validation failed. Please check your YAML syntax and required fields."},
			}
			return c.Status(400).JSON(response)
		}

		// Create the YAML file
		yamlData, err := yaml.Marshal(req)
		if err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to marshal configuration: " + err.Error(),
			}
			return c.Status(500).JSON(response)
		}

		// Write to file
		if err := os.WriteFile(configPath, yamlData, 0644); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to write configuration file: " + err.Error(),
			}
			return c.Status(500).JSON(response)
		}

		// Reload configurations
		if err := cl.LoadModelConfigsFromPath(appConfig.SystemState.Model.ModelsPath); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to reload configurations: " + err.Error(),
			}
			return c.Status(500).JSON(response)
		}

		// Preload the model
		if err := cl.Preload(appConfig.SystemState.Model.ModelsPath); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to preload model: " + err.Error(),
			}
			return c.Status(500).JSON(response)
		}

		// Return success response
		response := ModelResponse{
			Success:  true,
			Message:  fmt.Sprintf("Model '%s' updated successfully", modelName),
			Filename: configPath,
			Config:   req,
		}
		return c.JSON(response)
	}
}

// ReloadModelsEndpoint handles reloading model configurations from disk
func ReloadModelsEndpoint(cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Reload configurations
		if err := cl.LoadModelConfigsFromPath(appConfig.SystemState.Model.ModelsPath); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to reload configurations: " + err.Error(),
			}
			return c.Status(500).JSON(response)
		}

		// Preload the models
		if err := cl.Preload(appConfig.SystemState.Model.ModelsPath); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to preload models: " + err.Error(),
			}
			return c.Status(500).JSON(response)
		}

		// Return success response
		response := ModelResponse{
			Success: true,
			Message: "Model configurations reloaded successfully",
		}
		return c.Status(fiber.StatusOK).JSON(response)
	}
}
