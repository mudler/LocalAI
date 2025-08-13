package localai

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/config"
	httpUtils "github.com/mudler/LocalAI/core/http/utils"
	"github.com/mudler/LocalAI/pkg/utils"

	"gopkg.in/yaml.v3"
)

// GetEditModelPage renders the edit model page with current configuration
func GetEditModelPage(cl *config.BackendConfigLoader, appConfig *config.ApplicationConfig) fiber.Handler {
	return func(c *fiber.Ctx) error {
		modelName := c.Params("name")
		if modelName == "" {
			response := ModelResponse{
				Success: false,
				Error:   "Model name is required",
			}
			return c.Status(400).JSON(response)
		}

		// Load the existing configuration
		configPath := filepath.Join(appConfig.ModelPath, modelName+".yaml")
		if err := utils.InTrustedRoot(configPath, appConfig.ModelPath); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Model configuration not trusted: " + err.Error(),
			}
			return c.Status(404).JSON(response)
		}

		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			response := ModelResponse{
				Success: false,
				Error:   "Model configuration not found",
			}
			return c.Status(404).JSON(response)
		}

		// Read and parse the existing configuration
		configData, err := os.ReadFile(configPath)
		if err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to read model configuration: " + err.Error(),
			}
			return c.Status(500).JSON(response)
		}

		var backendConfig config.BackendConfig
		if err := yaml.Unmarshal(configData, &backendConfig); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to parse model configuration: " + err.Error(),
			}
			return c.Status(500).JSON(response)
		}

		// Render the edit page with the current configuration
		return c.Render("views/model-editor", fiber.Map{
			"ModelName": modelName,
			"Config":    backendConfig,
			"BaseURL":   httpUtils.BaseURL(c),
		})
	}
}

// EditModelEndpoint handles updating existing model configurations
func EditModelEndpoint(cl *config.BackendConfigLoader, appConfig *config.ApplicationConfig) fiber.Handler {
	return func(c *fiber.Ctx) error {
		modelName := c.Params("name")
		if modelName == "" {
			return c.Status(400).JSON(fiber.Map{
				"error": "Model name is required",
			})
		}

		// Get the raw body as YAML
		body := c.Body()
		if len(body) == 0 {
			response := ModelResponse{
				Success: false,
				Error:   "Request body is empty",
			}
			return c.Status(400).JSON(response)
		}

		// Unmarshal YAML directly into BackendConfig
		var req config.BackendConfig
		if err := yaml.Unmarshal(body, &req); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to parse YAML: " + err.Error(),
			}
			return c.Status(400).JSON(response)
		}

		// Validate required fields
		if req.Backend == "" || req.Model == "" {
			response := ModelResponse{
				Success: false,
				Error:   "Backend and model are required fields",
			}
			return c.Status(400).JSON(response)
		}

		// Load the existing configuration
		configPath := filepath.Join(appConfig.ModelPath, modelName+".yaml")
		if err := utils.InTrustedRoot(configPath, appConfig.ModelPath); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Model configuration not trusted: " + err.Error(),
			}
			return c.Status(404).JSON(response)
		}

		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			response := ModelResponse{
				Success: false,
				Error:   "Model configuration not found",
			}
			return c.Status(404).JSON(response)
		}

		// Read existing config
		configData, err := os.ReadFile(configPath)
		if err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to read existing configuration: " + err.Error(),
			}
			return c.Status(500).JSON(response)
		}

		var backendConfig config.BackendConfig
		if err := yaml.Unmarshal(configData, &backendConfig); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to parse existing configuration: " + err.Error(),
			}
			return c.Status(500).JSON(response)
		}

		// Set defaults
		backendConfig.SetDefaults()

		// Validate the configuration
		if !backendConfig.Validate() {

			response := ModelResponse{
				Success: false,
				Error:   "Validation failed",
				Details: []string{"Calling backend.Config.Validate()"},
			}
			return c.Status(400).JSON(response)
		}

		// Create the YAML file
		yamlData, err := yaml.Marshal(backendConfig)
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
		if err := cl.LoadBackendConfigsFromPath(appConfig.ModelPath); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to reload configurations: " + err.Error(),
			}
			return c.Status(500).JSON(response)
		}

		// Preload the model
		if err := cl.Preload(appConfig.ModelPath); err != nil {
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
			Config:   backendConfig,
		}
		return c.JSON(response)
	}
}
