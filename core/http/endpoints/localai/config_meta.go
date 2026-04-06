package localai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"

	"dario.cat/mergo"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/config/meta"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/xlog"
	"gopkg.in/yaml.v3"
)

// ConfigMetadataEndpoint returns field metadata for config fields.
// Without ?section, returns just the section index (lightweight).
// With ?section=<id>, returns fields for that section only.
// With ?section=all, returns all fields grouped by section.
// @Summary List model configuration field metadata
// @Description Returns config field metadata. Use ?section=<id> to filter by section, or omit for a section index.
// @Tags config
// @Produce json
// @Param section query string false "Section ID to filter (e.g. 'general', 'llm', 'parameters') or 'all' for everything"
// @Success 200 {object} map[string]any "Section index or filtered field metadata"
// @Router /api/models/config-metadata [get]
func ConfigMetadataEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		sectionParam := c.QueryParam("section")

		// No section param: return lightweight section index.
		if sectionParam == "" {
			sections := meta.DefaultSections()
			type sectionInfo struct {
				ID    string `json:"id"`
				Label string `json:"label"`
				URL   string `json:"url"`
			}
			index := make([]sectionInfo, len(sections))
			for i, s := range sections {
				index[i] = sectionInfo{
					ID:    s.ID,
					Label: s.Label,
					URL:   "/api/models/config-metadata?section=" + s.ID,
				}
			}
			return c.JSON(http.StatusOK, map[string]any{
				"hint":     "Fetch a section URL to see its fields. Use ?section=all for everything.",
				"sections": index,
			})
		}

		md := meta.BuildConfigMetadata(reflect.TypeOf(config.ModelConfig{}))

		// section=all: return everything.
		if sectionParam == "all" {
			return c.JSON(http.StatusOK, md)
		}

		// Filter to requested section.
		var filtered []meta.FieldMeta
		for _, f := range md.Fields {
			if f.Section == sectionParam {
				filtered = append(filtered, f)
			}
		}
		if len(filtered) == 0 {
			return c.JSON(http.StatusNotFound, map[string]any{"error": "unknown section: " + sectionParam})
		}
		return c.JSON(http.StatusOK, filtered)
	}
}

// AutocompleteEndpoint handles dynamic autocomplete lookups for config fields.
// Static option lists (quantizations, cache types, diffusers pipelines/schedulers)
// are embedded directly in the field metadata Options; only truly dynamic values
// that require runtime lookup are served here.
// @Summary Get dynamic autocomplete values for a config field
// @Description Returns runtime-resolved values for dynamic providers (backends, models)
// @Tags config
// @Produce json
// @Param provider path string true "Provider name (backends, models, models:chat, models:tts, models:transcript, models:vad)"
// @Success 200 {object} map[string]any "values array"
// @Router /api/models/config-metadata/autocomplete/{provider} [get]
func AutocompleteEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		provider := c.Param("provider")
		var values []string

		switch {
		case provider == meta.ProviderBackends:
			installedBackends, err := gallery.ListSystemBackends(appConfig.SystemState)
			if err == nil {
				for name := range installedBackends {
					values = append(values, name)
				}
			}
			sort.Strings(values)

		case provider == meta.ProviderModels:
			modelConfigs := cl.GetAllModelsConfigs()
			for _, cfg := range modelConfigs {
				values = append(values, cfg.Name)
			}
			modelsWithoutConfig, _ := galleryop.ListModels(cl, ml, config.NoFilterFn, galleryop.LOOSE_ONLY)
			values = append(values, modelsWithoutConfig...)
			sort.Strings(values)

		case strings.HasPrefix(provider, "models:"):
			capability := strings.TrimPrefix(provider, "models:")
			var filterFn config.ModelConfigFilterFn
			switch capability {
			case config.UsecaseChat:
				filterFn = config.BuildUsecaseFilterFn(config.FLAG_CHAT)
			case config.UsecaseTTS:
				filterFn = config.BuildUsecaseFilterFn(config.FLAG_TTS)
			case config.UsecaseVAD:
				filterFn = config.BuildUsecaseFilterFn(config.FLAG_VAD)
			case config.UsecaseTranscript:
				filterFn = config.BuildUsecaseFilterFn(config.FLAG_TRANSCRIPT)
			default:
				filterFn = config.NoFilterFn
			}
			filteredConfigs := cl.GetModelConfigsByFilter(filterFn)
			for _, cfg := range filteredConfigs {
				values = append(values, cfg.Name)
			}
			sort.Strings(values)

		default:
			return c.JSON(http.StatusNotFound, map[string]any{"error": "unknown provider: " + provider})
		}

		return c.JSON(http.StatusOK, map[string]any{"values": values})
	}
}

// PatchConfigEndpoint handles PATCH requests to partially update a model config
// using nested JSON merge.
// @Summary Partially update a model configuration
// @Description Deep-merges the JSON patch body into the existing model config
// @Tags config
// @Accept json
// @Produce json
// @Param name path string true "Model name"
// @Success 200 {object} map[string]any "success message"
// @Router /api/models/config-json/{name} [patch]
func PatchConfigEndpoint(cl *config.ModelConfigLoader, _ *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		modelName := c.Param("name")
		if decoded, err := url.PathUnescape(modelName); err == nil {
			modelName = decoded
		}
		if modelName == "" {
			return c.JSON(http.StatusBadRequest, map[string]any{"error": "model name is required"})
		}

		modelConfig, exists := cl.GetModelConfig(modelName)
		if !exists {
			return c.JSON(http.StatusNotFound, map[string]any{"error": "model configuration not found"})
		}

		patchBody, err := io.ReadAll(c.Request().Body)
		if err != nil || len(patchBody) == 0 {
			return c.JSON(http.StatusBadRequest, map[string]any{"error": "request body is empty or unreadable"})
		}

		var patchMap map[string]any
		if err := json.Unmarshal(patchBody, &patchMap); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON: " + err.Error()})
		}

		existingJSON, err := json.Marshal(modelConfig)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]any{"error": "failed to marshal existing config"})
		}

		var existingMap map[string]any
		if err := json.Unmarshal(existingJSON, &existingMap); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]any{"error": "failed to parse existing config"})
		}

		if err := mergo.Merge(&existingMap, patchMap, mergo.WithOverride); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]any{"error": "failed to merge configs: " + err.Error()})
		}

		mergedJSON, err := json.Marshal(existingMap)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]any{"error": "failed to marshal merged config"})
		}

		var updatedConfig config.ModelConfig
		if err := json.Unmarshal(mergedJSON, &updatedConfig); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{"error": "merged config is invalid: " + err.Error()})
		}

		if valid, err := updatedConfig.Validate(); !valid {
			errMsg := "validation failed"
			if err != nil {
				errMsg = err.Error()
			}
			return c.JSON(http.StatusBadRequest, map[string]any{"error": errMsg})
		}

		configPath := modelConfig.GetModelConfigFile()
		if err := utils.VerifyPath(configPath, appConfig.SystemState.Model.ModelsPath); err != nil {
			return c.JSON(http.StatusForbidden, map[string]any{"error": "config path not trusted: " + err.Error()})
		}

		yamlData, err := yaml.Marshal(updatedConfig)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]any{"error": "failed to marshal YAML"})
		}

		if err := os.WriteFile(configPath, yamlData, 0644); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]any{"error": "failed to write config file"})
		}

		if err := cl.LoadModelConfigsFromPath(appConfig.SystemState.Model.ModelsPath, appConfig.ToConfigLoaderOptions()...); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]any{"error": "failed to reload configs: " + err.Error()})
		}

		if err := cl.Preload(appConfig.SystemState.Model.ModelsPath); err != nil {
			xlog.Warn("Failed to preload after PATCH", "error", err)
		}

		return c.JSON(http.StatusOK, map[string]any{
			"success": true,
			"message": fmt.Sprintf("Model '%s' updated successfully", modelName),
		})
	}
}
