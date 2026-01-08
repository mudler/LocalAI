package localai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/gallery/importers"
	httpUtils "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/pkg/utils"

	"gopkg.in/yaml.v3"
)

// ImportModelURIEndpoint handles creating new model configurations from a URI
func ImportModelURIEndpoint(cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, galleryService *services.GalleryService, opcache *services.OpCache) echo.HandlerFunc {
	return func(c echo.Context) error {

		input := new(schema.ImportModelRequest)

		if err := c.Bind(input); err != nil {
			return err
		}

		modelConfig, err := importers.DiscoverModelConfig(input.URI, input.Preferences)
		if err != nil {
			return fmt.Errorf("failed to discover model config: %w", err)
		}

		uuid, err := uuid.NewUUID()
		if err != nil {
			return err
		}

		// Determine gallery ID for tracking - use model name if available, otherwise use URI
		galleryID := input.URI
		if modelConfig.Name != "" {
			galleryID = modelConfig.Name
		}

		// Register operation in opcache if available (for UI progress tracking)
		if opcache != nil {
			opcache.Set(galleryID, uuid.String())
		}

		galleryService.ModelGalleryChannel <- services.GalleryOp[gallery.GalleryModel, gallery.ModelConfig]{
			Req: gallery.GalleryModel{
				Overrides: map[string]interface{}{},
			},
			ID:                 uuid.String(),
			GalleryElementName: galleryID,
			GalleryElement:     &modelConfig,
			BackendGalleries:   appConfig.BackendGalleries,
		}

		return c.JSON(200, schema.GalleryResponse{
			ID:        uuid.String(),
			StatusURL: fmt.Sprintf("%smodels/jobs/%s", httpUtils.BaseURL(c), uuid.String()),
		})
	}
}

// ImportModelEndpoint handles creating new model configurations
func ImportModelEndpoint(cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
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

		// Check content type to determine how to parse
		contentType := c.Request().Header.Get("Content-Type")
		var modelConfig config.ModelConfig

		if strings.Contains(contentType, "application/json") {
			// Parse JSON
			if err := json.Unmarshal(body, &modelConfig); err != nil {
				response := ModelResponse{
					Success: false,
					Error:   "Failed to parse JSON: " + err.Error(),
				}
				return c.JSON(http.StatusBadRequest, response)
			}
		} else if strings.Contains(contentType, "application/x-yaml") || strings.Contains(contentType, "text/yaml") {
			// Parse YAML
			if err := yaml.Unmarshal(body, &modelConfig); err != nil {
				response := ModelResponse{
					Success: false,
					Error:   "Failed to parse YAML: " + err.Error(),
				}
				return c.JSON(http.StatusBadRequest, response)
			}
		} else {
			// Try to auto-detect format
			if len(body) > 0 && strings.TrimSpace(string(body))[0] == '{' {
				// Looks like JSON
				if err := json.Unmarshal(body, &modelConfig); err != nil {
					response := ModelResponse{
						Success: false,
						Error:   "Failed to parse JSON: " + err.Error(),
					}
					return c.JSON(http.StatusBadRequest, response)
				}
			} else {
				// Assume YAML
				if err := yaml.Unmarshal(body, &modelConfig); err != nil {
					response := ModelResponse{
						Success: false,
						Error:   "Failed to parse YAML: " + err.Error(),
					}
					return c.JSON(http.StatusBadRequest, response)
				}
			}
		}

		// Validate required fields
		if modelConfig.Name == "" {
			response := ModelResponse{
				Success: false,
				Error:   "Name is required",
			}
			return c.JSON(http.StatusBadRequest, response)
		}

		// Set defaults
		modelConfig.SetDefaults(appConfig.ToConfigLoaderOptions()...)

		// Validate the configuration
		if valid, _ := modelConfig.Validate(); !valid {
			response := ModelResponse{
				Success: false,
				Error:   "Invalid configuration",
			}
			return c.JSON(http.StatusBadRequest, response)
		}

		// Create the configuration file
		configPath := filepath.Join(appConfig.SystemState.Model.ModelsPath, modelConfig.Name+".yaml")
		if err := utils.VerifyPath(modelConfig.Name+".yaml", appConfig.SystemState.Model.ModelsPath); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Model path not trusted: " + err.Error(),
			}
			return c.JSON(http.StatusBadRequest, response)
		}

		// Marshal to YAML for storage
		yamlData, err := yaml.Marshal(&modelConfig)
		if err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to marshal configuration: " + err.Error(),
			}
			return c.JSON(http.StatusInternalServerError, response)
		}

		// Write the file
		if err := os.WriteFile(configPath, yamlData, 0644); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to write configuration file: " + err.Error(),
			}
			return c.JSON(http.StatusInternalServerError, response)
		}
		// Reload configurations
		if err := cl.LoadModelConfigsFromPath(appConfig.SystemState.Model.ModelsPath, appConfig.ToConfigLoaderOptions()...); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to reload configurations: " + err.Error(),
			}
			return c.JSON(http.StatusInternalServerError, response)
		}

		// Preload the model
		if err := cl.Preload(appConfig.SystemState.Model.ModelsPath); err != nil {
			response := ModelResponse{
				Success: false,
				Error:   "Failed to preload model: " + err.Error(),
			}
			return c.JSON(http.StatusInternalServerError, response)
		}
		// Return success response
		response := ModelResponse{
			Success:  true,
			Message:  "Model configuration created successfully",
			Filename: filepath.Base(configPath),
		}
		return c.JSON(200, response)
	}
}
