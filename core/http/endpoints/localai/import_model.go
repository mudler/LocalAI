package localai

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/utils"
	"gopkg.in/yaml.v3"
)

func ImportModelEndpoint(cl *config.BackendConfigLoader, appConfig *config.ApplicationConfig) fiber.Handler {
	return func(c *fiber.Ctx) error {
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
		var backendConfig config.BackendConfig
		if err := yaml.Unmarshal(body, &backendConfig); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to parse YAML: " + err.Error(),
			}
			return c.Status(400).JSON(response)
		}

		// Validate required fields
		if backendConfig.Name == "" || backendConfig.Backend == "" || backendConfig.Model == "" {
			response := ModelResponse{
				Success: false,
				Error:   "Name, backend, and model are required fields",
			}
			return c.Status(400).JSON(response)
		}

		// Set defaults
		backendConfig.SetDefaults()

		// Validate the configuration
		if !backendConfig.Validate() {

			response := ModelResponse{
				Success: false,
				Error:   "Validation failed",
				Details: []string{"failed to validate model"},
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
		filename := filepath.Join(appConfig.ModelPath, backendConfig.Name+".yaml")

		if err := utils.InTrustedRoot(filename, appConfig.ModelPath); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "File is not in trusted root: " + err.Error(),
			}
			return c.Status(500).JSON(response)
		}

		if err := os.WriteFile(filename, yamlData, 0644); err != nil {
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
			Message:  fmt.Sprintf("Model '%s' imported successfully", backendConfig.Name),
			Filename: filename,
			Config:   backendConfig,
		}
		return c.JSON(response)
	}
}
