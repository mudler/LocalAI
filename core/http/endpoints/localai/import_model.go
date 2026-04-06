package localai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/gallery/importers"
	httpUtils "github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/LocalAI/pkg/vram"

	"gopkg.in/yaml.v3"
)

// ImportModelURIEndpoint handles creating new model configurations from a URI
func ImportModelURIEndpoint(cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, galleryService *galleryop.GalleryService, opcache *galleryop.OpCache) echo.HandlerFunc {
	return func(c echo.Context) error {

		input := new(schema.ImportModelRequest)

		if err := c.Bind(input); err != nil {
			return err
		}

		modelConfig, err := importers.DiscoverModelConfig(input.URI, input.Preferences)
		if err != nil {
			return fmt.Errorf("failed to discover model config: %w", err)
		}

		resp := schema.GalleryResponse{
			StatusURL: fmt.Sprintf("%smodels/jobs/%s", httpUtils.BaseURL(c), ""),
		}

		if len(modelConfig.Files) > 0 {
			files := make([]vram.FileInput, 0, len(modelConfig.Files))
			for _, f := range modelConfig.Files {
				files = append(files, vram.FileInput{URI: f.URI, Size: 0})
			}
			estCtx, cancel := context.WithTimeout(c.Request().Context(), 5*time.Second)
			defer cancel()
			result, err := vram.EstimateModel(estCtx, vram.ModelEstimateInput{
				Files:   files,
				Options: vram.EstimateOptions{ContextLength: 8192},
			})
			if err == nil {
				if result.SizeBytes > 0 {
					resp.EstimatedSizeBytes = result.SizeBytes
					resp.EstimatedSizeDisplay = result.SizeDisplay
				}
				if result.VRAMBytes > 0 {
					resp.EstimatedVRAMBytes = result.VRAMBytes
					resp.EstimatedVRAMDisplay = result.VRAMDisplay
				}
			}
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

		galleryService.ModelGalleryChannel <- galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
			Req: gallery.GalleryModel{
				Overrides: map[string]any{},
			},
			ID:                 uuid.String(),
			GalleryElementName: galleryID,
			GalleryElement:     &modelConfig,
			BackendGalleries:   appConfig.BackendGalleries,
		}

		resp.ID = uuid.String()
		resp.StatusURL = fmt.Sprintf("%smodels/jobs/%s", httpUtils.BaseURL(c), uuid.String())
		return c.JSON(200, resp)
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

		// Detect format once and reuse for both typed and map parsing
		contentType := c.Request().Header.Get("Content-Type")
		trimmed := strings.TrimSpace(string(body))
		isJSON := strings.Contains(contentType, "application/json") ||
			(!strings.Contains(contentType, "yaml") && len(trimmed) > 0 && trimmed[0] == '{')

		var modelConfig config.ModelConfig
		if isJSON {
			if err := json.Unmarshal(body, &modelConfig); err != nil {
				return c.JSON(http.StatusBadRequest, ModelResponse{Success: false, Error: "Failed to parse JSON: " + err.Error()})
			}
		} else {
			if err := yaml.Unmarshal(body, &modelConfig); err != nil {
				return c.JSON(http.StatusBadRequest, ModelResponse{Success: false, Error: "Failed to parse YAML: " + err.Error()})
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

		// Validate without calling SetDefaults() — runtime defaults should not
		// be persisted to disk. SetDefaults() is called when loading configs
		// for inference via LoadModelConfigsFromPath().
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

		// Write only the user-provided fields to disk by parsing the original
		// body into a map (not the typed struct, which includes Go zero values).
		var bodyMap map[string]any
		if isJSON {
			_ = json.Unmarshal(body, &bodyMap)
		} else {
			_ = yaml.Unmarshal(body, &bodyMap)
		}

		var yamlData []byte
		if bodyMap != nil {
			yamlData, err = yaml.Marshal(bodyMap)
		} else {
			yamlData, err = yaml.Marshal(&modelConfig)
		}
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
