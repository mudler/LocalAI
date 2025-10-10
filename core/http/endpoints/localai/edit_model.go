package localai

import (
	"fmt"
	"os"

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

		modelConfigFile := modelConfig.GetModelConfigFile()
		if modelConfigFile == "" {
			response := ModelResponse{
				Success: false,
				Error:   "Model configuration file not found",
			}
			return c.Status(404).JSON(response)
		}
		configData, err := os.ReadFile(modelConfigFile)
		if err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to read configuration file: " + err.Error(),
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

		modelConfig, exists := cl.GetModelConfig(modelName)
		if !exists {
			response := ModelResponse{
				Success: false,
				Error:   "Existing model configuration not found",
			}
			return c.Status(404).JSON(response)
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

		// Check content to see if it's a valid model config
		var req config.ModelConfig

		// Parse YAML
		if err := yaml.Unmarshal(body, &req); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to parse YAML: " + err.Error(),
			}
			return c.Status(400).JSON(response)
		}

		// Validate required fields
		if req.Name == "" {
			response := ModelResponse{
				Success: false,
				Error:   "Name is required",
			}
			return c.Status(400).JSON(response)
		}

		// Validate the configuration
		if !req.Validate() {
			response := ModelResponse{
				Success: false,
				Error:   "Validation failed",
				Details: []string{"Configuration validation failed. Please check your YAML syntax and required fields."},
			}
			return c.Status(400).JSON(response)
		}

		// Load the existing configuration
		configPath := modelConfig.GetModelConfigFile()
		if err := utils.VerifyPath(configPath, appConfig.SystemState.Model.ModelsPath); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Model configuration not trusted: " + err.Error(),
			}
			return c.Status(404).JSON(response)
		}

		// Write new content to file
		if err := os.WriteFile(configPath, body, 0644); err != nil {
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
