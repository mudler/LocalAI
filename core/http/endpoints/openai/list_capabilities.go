package openai

import (
	"github.com/labstack/echo/v4"
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
			if cfg, ok := bcl.GetModelConfig(m); ok {
				entry.Capabilities = cfg.Capabilities()
				entry.InputModalities = cfg.InputModalities()
				entry.OutputModalities = cfg.OutputModalities()
			}
			dataModels = append(dataModels, entry)
		}

		return c.JSON(200, schema.ModelCapabilitiesResponse{
			Object: "list",
			Data:   dataModels,
		})
	}
}
