package openai

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	model "github.com/mudler/LocalAI/pkg/model"
	"gorm.io/gorm"
)

// ListModelCapabilitiesEndpoint is a LocalAI-specific extension of the OpenAI
// models listing. It returns the same set of models as /v1/models but enriches
// each entry with the capabilities and input/output modalities the model
// supports, so clients can decide whether an image/audio/video attachment can be
// handed to a given model directly (or must be converted/transcribed first).
//
// It is purely additive: clients that don't know about it keep using /v1/models
// and see no change.
// @Summary List available models enriched with capabilities and input/output modalities.
// @Tags models
// @Success 200 {object} schema.ModelCapabilitiesResponse "Response"
// @Router /v1/models/capabilities [get]
func ListModelCapabilitiesEndpoint(bcl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig, db ...*gorm.DB) echo.HandlerFunc {
	var authDB *gorm.DB
	if len(db) > 0 {
		authDB = db[0]
	}
	return func(c echo.Context) error {
		modelNames, err := listVisibleModelNames(c, bcl, ml, authDB)
		if err != nil {
			return err
		}

		dataModels := []schema.ModelCapabilities{}
		for _, m := range modelNames {
			entry := schema.ModelCapabilities{ID: m, Object: "model"}
			if cfg, ok := modelConfigFor(bcl, m); ok {
				if (cfg.ContextSize == nil || *cfg.ContextSize <= 0) && appConfig != nil && appConfig.ContextSize > 0 {
					cfg.ContextSize = &appConfig.ContextSize
				}
				entry.Capabilities = cfg.Capabilities()
				entry.ThreeDOperations = cfg.ThreeDOperations()
				entry.InputModalities = cfg.InputModalities()
				entry.OutputModalities = cfg.OutputModalities()
				if ctx := backend.EffectiveRequestContextSize(cfg); ctx > 0 {
					entry.ContextSize = ctx
				}
			}
			dataModels = append(dataModels, entry)
		}

		return c.JSON(200, schema.ModelCapabilitiesResponse{
			Object: "list",
			Data:   dataModels,
		})
	}
}

// modelConfigFor returns the config that describes what a listed model can
// do. An alias is a pure redirect whose own config carries no backend, so its
// capabilities, modalities and context_size would all be empty defaults;
// report the target's instead, since that is the model a request for the
// alias reaches. A dangling or chained alias returns false: the endpoint then
// reports the entry without enrichment rather than advertise defaults no
// model runs with.
func modelConfigFor(bcl *config.ModelConfigLoader, name string) (config.ModelConfig, bool) {
	cfg, ok := bcl.GetModelConfig(name)
	if !ok {
		return config.ModelConfig{}, false
	}
	resolved, _, err := bcl.ResolveAlias(&cfg)
	if err != nil {
		return config.ModelConfig{}, false
	}
	return *resolved, true
}
