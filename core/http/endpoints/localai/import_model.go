package localai

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/utils"
	"gopkg.in/yaml.v3"
)

// ImportModelEndpoint handles creating new model configurations
func ImportModelEndpoint(cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig) fiber.Handler {
	return func(c *fiber.Ctx) error {
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
		var modelConfig config.ModelConfig
		var err error

		if strings.Contains(contentType, "application/json") {
			// Parse JSON
			if err := json.Unmarshal(body, &modelConfig); err != nil {
				response := ModelResponse{
					Success: false,
					Error:   "Failed to parse JSON: " + err.Error(),
				}
				return c.Status(400).JSON(response)
			}
		} else if strings.Contains(contentType, "application/x-yaml") || strings.Contains(contentType, "text/yaml") {
			// Parse YAML
			if err := yaml.Unmarshal(body, &modelConfig); err != nil {
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
				if err := json.Unmarshal(body, &modelConfig); err != nil {
					response := ModelResponse{
						Success: false,
						Error:   "Failed to parse JSON: " + err.Error(),
					}
					return c.Status(400).JSON(response)
				}
			} else {
				// Assume YAML
				if err := yaml.Unmarshal(body, &modelConfig); err != nil {
					response := ModelResponse{
						Success: false,
						Error:   "Failed to parse YAML: " + err.Error(),
					}
					return c.Status(400).JSON(response)
				}
			}
		}

		// Validate required fields
		if modelConfig.Name == "" {
			response := ModelResponse{
				Success: false,
				Error:   "Name is required",
			}
			return c.Status(400).JSON(response)
		}

		// Set defaults
		modelConfig.SetDefaults()

		// Validate the configuration
		if !modelConfig.Validate() {
			response := ModelResponse{
				Success: false,
				Error:   "Invalid configuration",
			}
			return c.Status(400).JSON(response)
		}

		// Create the configuration file
		configPath := filepath.Join(appConfig.SystemState.Model.ModelsPath, modelConfig.Name+".yaml")
		if err := utils.VerifyPath(modelConfig.Name+".yaml", appConfig.SystemState.Model.ModelsPath); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Model path not trusted: " + err.Error(),
			}
			return c.Status(400).JSON(response)
		}

		// Marshal to YAML for storage
		yamlData, err := yaml.Marshal(&modelConfig)
		if err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to marshal configuration: " + err.Error(),
			}
			return c.Status(500).JSON(response)
		}

		// Write the file
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
			Message:  "Model configuration created successfully",
			Filename: filepath.Base(configPath),
		}
		return c.JSON(response)
	}
}
