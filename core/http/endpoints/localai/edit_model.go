package localai

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/downloader"
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
		return c.Render("views/edit-model", fiber.Map{
			"ModelName": modelName,
			"Config":    backendConfig,
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

		var req ModelRequest
		if err := c.BodyParser(&req); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to parse request: " + err.Error(),
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

		var existingConfig config.BackendConfig
		if err := yaml.Unmarshal(configData, &existingConfig); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to parse existing configuration: " + err.Error(),
			}
			return c.Status(500).JSON(response)
		}

		// Update the configuration with new values
		backendConfig := &existingConfig
		backendConfig.Backend = req.Backend
		backendConfig.Description = req.Description
		backendConfig.Usage = req.Usage
		backendConfig.Model = req.Model
		backendConfig.MMProj = req.MMProj

		// Set template configuration
		if req.ChatTemplate != "" || req.CompletionTemplate != "" {
			backendConfig.TemplateConfig = config.TemplateConfig{
				Chat:       req.ChatTemplate,
				Completion: req.CompletionTemplate,
			}
		}

		// Set system prompt
		if req.SystemPrompt != "" {
			backendConfig.SystemPrompt = req.SystemPrompt
		}

		// Set prediction options
		if temp, err := strconv.ParseFloat(req.Temperature, 32); err == nil {
			backendConfig.Temperature = &temp
		}
		if topP, err := strconv.ParseFloat(req.TopP, 32); err == nil {
			backendConfig.TopP = &topP
		}
		if topK, err := strconv.Atoi(req.TopK); err == nil {
			backendConfig.TopK = &topK
		}
		if ctxSize, err := strconv.Atoi(req.ContextSize); err == nil {
			backendConfig.ContextSize = &ctxSize
		}
		if threads, err := strconv.Atoi(req.Threads); err == nil {
			backendConfig.Threads = &threads
		}
		if seed, err := strconv.Atoi(req.Seed); err == nil {
			backendConfig.Seed = &seed
		}

		// Set feature flags
		backendConfig.F16 = &req.F16
		backendConfig.CUDA = req.CUDA
		backendConfig.Embeddings = &req.Embeddings
		backendConfig.Debug = &req.Debug
		backendConfig.MMap = &req.MMap
		backendConfig.MMlock = &req.MMlock

		// Set backend-specific configurations
		switch req.Backend {
		case "llama.cpp":
			if req.LoraAdapter != "" {
				backendConfig.LoraAdapter = req.LoraAdapter
			}
			if req.Grammar != "" {
				backendConfig.Grammar = req.Grammar
			}
		case "diffusers":
			if req.PipelineType != "" {
				backendConfig.Diffusers.PipelineType = req.PipelineType
			}
			if req.SchedulerType != "" {
				backendConfig.Diffusers.SchedulerType = req.SchedulerType
			}
		case "whisper":
			if req.AudioPath != "" {
				backendConfig.AudioPath = req.AudioPath
			}
		case "bark-cpp":
			if req.Voice != "" {
				backendConfig.Voice = req.Voice
			}
		}

		// Set defaults
		backendConfig.SetDefaults()

		// Check if model file is a URL and needs downloading
		if strings.HasPrefix(req.Model, "http") {
			// Add to download files
			backendConfig.DownloadFiles = append(backendConfig.DownloadFiles, config.File{
				URI: downloader.URI(req.Model),
			})
		}

		if req.MMProj != "" && strings.HasPrefix(req.MMProj, "http") {
			// Add MMProj to download files
			backendConfig.DownloadFiles = append(backendConfig.DownloadFiles, config.File{
				URI: downloader.URI(req.MMProj),
			})
		}

		// Validate the configuration
		if !backendConfig.Validate() {
			// Provide more specific validation error messages
			var validationErrors []string

			if backendConfig.Backend == "" {
				validationErrors = append(validationErrors, "Backend name is required")
			} else {
				// Check if backend name contains invalid characters
				re := regexp.MustCompile(`^[a-zA-Z0-9-_\.]+$`)
				if !re.MatchString(backendConfig.Backend) {
					validationErrors = append(validationErrors, "Backend name contains invalid characters (only letters, numbers, hyphens, underscores, and dots are allowed)")
				}
			}

			if backendConfig.Model == "" {
				validationErrors = append(validationErrors, "Model file/URL is required")
			}

			// Check for path traversal attempts
			if strings.Contains(backendConfig.Model, "..") || strings.Contains(backendConfig.Model, string(os.PathSeparator)) {
				validationErrors = append(validationErrors, "Model path contains invalid characters")
			}

			if backendConfig.MMProj != "" {
				if strings.Contains(backendConfig.MMProj, "..") || strings.Contains(backendConfig.MMProj, string(os.PathSeparator)) {
					validationErrors = append(validationErrors, "MMProj path contains invalid characters")
				}
			}

			if len(validationErrors) == 0 {
				validationErrors = append(validationErrors, "Configuration validation failed")
			}

			response := ModelResponse{
				Success: false,
				Error:   "Validation failed",
				Details: validationErrors,
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
