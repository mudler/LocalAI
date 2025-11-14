package openai

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services"
	model "github.com/mudler/LocalAI/pkg/model"
)

// ListModelsEndpoint is the OpenAI Models API endpoint https://platform.openai.com/docs/api-reference/models
// @Summary List and describe the various models available in the API.
// @Success 200 {object} schema.ModelsDataResponse "Response"
// @Router /v1/models [get]
func ListModelsEndpoint(bcl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		// If blank, no filter is applied.
		filter := c.QueryParam("filter")

		// By default, exclude any loose files that are already referenced by a configuration file.
		var policy services.LooseFilePolicy
		excludeConfigured := c.QueryParam("excludeConfigured")
		if excludeConfigured == "" || excludeConfigured == "true" {
			policy = services.SKIP_IF_CONFIGURED
		} else {
			policy = services.ALWAYS_INCLUDE // This replicates current behavior. TODO: give more options to the user?
		}

		filterFn, err := config.BuildNameFilterFn(filter)
		if err != nil {
			return err
		}

		modelNames, err := services.ListModels(bcl, ml, filterFn, policy)
		if err != nil {
			return err
		}

		// Map from a slice of names to a slice of OpenAIModel response objects
		dataModels := []schema.OpenAIModel{}
		for _, m := range modelNames {
			dataModels = append(dataModels, schema.OpenAIModel{ID: m, Object: "model"})
		}

		return c.JSON(200, schema.ModelsDataResponse{
			Object: "list",
			Data:   dataModels,
		})
	}
}
