package localai

import (
	"context"
	"encoding/json"
	"errors"
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
			var amb *importers.AmbiguousImportError
			if errors.As(err, &amb) {
				// Fall back to empty slice so JSON always renders an array,
				// not null — keeps the UI code a bit simpler.
				candidates := amb.Candidates
				if candidates == nil {
					candidates = []string{}
				}
				return c.JSON(http.StatusBadRequest, map[string]any{
					"error":      "ambiguous import",
					"detail":     amb.Error(),
					"modality":   amb.Modality,
					"candidates": candidates,
					"hint":       "Pass preferences.backend to pick one of the candidates.",
				})
			}
			if errors.Is(err, importers.ErrAmbiguousImport) {
				return c.JSON(http.StatusBadRequest, map[string]any{
					"error":      "ambiguous import",
					"detail":     err.Error(),
					"modality":   "",
					"candidates": []string{},
					"hint":       "Pass preferences.backend to pick one of the candidates.",
				})
			}
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
			result, err := vram.EstimateModelMultiContext(estCtx, vram.ModelEstimateInput{
				Files: files,
			}, []uint32{8192})
			if err == nil {
				if result.SizeBytes > 0 {
					resp.EstimatedSizeBytes = result.SizeBytes
					resp.EstimatedSizeDisplay = result.SizeDisplay
				}
				if v := result.VRAMForContext(8192); v > 0 {
					resp.EstimatedVRAMBytes = v
					resp.EstimatedVRAMDisplay = vram.FormatBytes(v)
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

		galleryService.EnqueueModelOp(galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
			Req: gallery.GalleryModel{
				Overrides: map[string]any{},
			},
			ID:                 uuid.String(),
			GalleryElementName: galleryID,
			GalleryElement:     &modelConfig,
			BackendGalleries:   appConfig.BackendGalleries,
		})

		resp.ID = uuid.String()
		resp.StatusURL = fmt.Sprintf("%smodels/jobs/%s", httpUtils.BaseURL(c), uuid.String())
		return c.JSON(200, resp)
	}
}

// ImportModelEndpoint handles creating new model configurations
// @Summary Import a model from a raw YAML or JSON configuration
// @Description Writes the posted model configuration. A config referencing
// @Description remote assets (download_files entries, URI-valued model/mmproj)
// @Description is enqueued on the gallery job queue and answered with a job id
// @Description plus status URL to poll, like a gallery install; a config with
// @Description no downloads is written synchronously and answered with a
// @Description plain success response.
// @Tags models
// @Accept json
// @Produce json
// @Param request body object true "model configuration (YAML or JSON body)"
// @Success 200 {object} schema.GalleryResponse "remote assets present: job id and status URL"
// @Failure 400 {object} localai.ModelResponse "invalid or incomplete configuration"
// @Router /models/import [post]
func ImportModelEndpoint(cl *config.ModelConfigLoader, gs *galleryop.GalleryService, appConfig *config.ApplicationConfig, opcache *galleryop.OpCache) echo.HandlerFunc {
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
		if valid, vErr := modelConfig.Validate(); !valid {
			msg := "Invalid configuration"
			if vErr != nil {
				msg = vErr.Error()
			}
			return c.JSON(http.StatusBadRequest, ModelResponse{Success: false, Error: msg})
		}

		// Reject aliases whose target is missing, chained, or disabled so a
		// dangling alias can't be persisted and surface as a runtime error later.
		if err := cl.ValidateAliasTarget(&modelConfig); err != nil {
			return c.JSON(http.StatusBadRequest, ModelResponse{Success: false, Error: err.Error()})
		}

		// Reject failover chains whose targets are missing or are themselves
		// chains, for the same reason.
		if err := cl.ValidateFailoverTargets(&modelConfig); err != nil {
			return c.JSON(http.StatusBadRequest, ModelResponse{Success: false, Error: err.Error()})
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

		// A config whose assets are not on disk yet is a download, not just
		// a file write: route it through the gallery op queue so the UI
		// tracks it like a gallery install (job UUID + /models/jobs/{uuid})
		// instead of acquiring synchronously inside this request. The op
		// writes the config file and reloads once acquisition finishes;
		// configs needing no acquisition keep the fast synchronous path.
		//
		// WHICH acquisition is the artifact resolver's call, not a string
		// test: a managed artifact (explicit artifacts:, an hf:// repo or
		// bare owner/repo on a directory-consuming backend) must enqueue
		// with NO files, so the worker's InstallModel runs the same binding
		// and repository materializer as a gallery install — flattening a
		// repository into file entries would send it to the single-file
		// downloader and skip the materializer. Plain remote files
		// (download_files, URL model/mmproj on single-file backends) ride
		// as file entries through the same downloader preload would use.
		// (A URL mmproj on an artifact config stays preload's job, as it
		// was before queued imports.)
		_, _, hasArtifact, err := modelConfig.PrimaryArtifactSpec(appConfig.SystemState.Model.ModelsPath)
		if err != nil {
			return c.JSON(http.StatusBadRequest, ModelResponse{Success: false, Error: "Invalid model reference: " + err.Error()})
		}
		var queueFiles []gallery.File
		if !hasArtifact {
			queueFiles = remoteAssetFiles(&modelConfig)
		}
		if gs != nil && (hasArtifact || len(queueFiles) > 0) {
			jobUUID, err := uuid.NewUUID()
			if err != nil {
				return err
			}
			if opcache != nil {
				opcache.Set(modelConfig.Name, jobUUID.String())
			}
			gs.EnqueueModelOp(galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
				Req:                gallery.GalleryModel{Overrides: map[string]any{}},
				ID:                 jobUUID.String(),
				GalleryElementName: modelConfig.Name,
				GalleryElement: &gallery.ModelConfig{
					Name:       modelConfig.Name,
					ConfigFile: string(yamlData),
					// nil for artifact configs: InstallModel's empty-Files
					// branch re-resolves and binds the artifact itself.
					Files: queueFiles,
				},
				BackendGalleries: appConfig.BackendGalleries,
			})
			return c.JSON(200, schema.GalleryResponse{
				ID:        jobUUID.String(),
				StatusURL: fmt.Sprintf("%smodels/jobs/%s", httpUtils.BaseURL(c), jobUUID.String()),
			})
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
		// Tell peer replicas to load the newly-created config from the shared
		// models dir: this endpoint only reloaded the local loader. No-op in
		// standalone mode.
		if gs != nil {
			gs.BroadcastModelsChanged(modelConfig.Name, "install")
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

// remoteAssetFiles lists the downloads a config implies: explicit
// download_files entries, and URI-valued model/mmproj fields under the
// same on-disk names the config loader's preload would give them — so
// the later preload finds the files present and the config needs no
// rewriting.
func remoteAssetFiles(c *config.ModelConfig) []gallery.File {
	var files []gallery.File
	for _, f := range c.DownloadFiles {
		files = append(files, gallery.File{Filename: f.Filename, SHA256: f.SHA256, URI: string(f.URI)})
	}
	if c.IsModelURL() {
		files = append(files, gallery.File{Filename: c.ModelFileName(), URI: c.Model})
	}
	if c.IsMMProjURL() {
		files = append(files, gallery.File{Filename: c.MMProjFileName(), URI: c.MMProj})
	}
	return files
}
