package openai

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services"
	model "github.com/mudler/LocalAI/pkg/model"
)

// ListModelsEndpoint is the OpenAI Models API endpoint https://platform.openai.com/docs/api-reference/models
// @Summary List and describe the various models available in the API.
// @Success 200 {object} schema.ModelsDataResponse "Response"
// @Router /v1/models [get]
func ListModelsEndpoint(bcl *config.BackendConfigLoader, ml *model.ModelLoader) func(ctx *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		// If blank, no filter is applied.
		filter := c.Query("filter")

		// By default, exclude any loose files that are already referenced by a configuration file.
		excludeConfigured := c.QueryBool("excludeConfigured", true)

		dataModels, err := modelList(bcl, ml, filter, excludeConfigured)
		if err != nil {
			return err
		}
		return c.JSON(schema.ModelsDataResponse{
			Object: "list",
			Data:   dataModels,
		})
	}
}

func modelList(bcl *config.BackendConfigLoader, ml *model.ModelLoader, filter string, excludeConfigured bool) ([]schema.OpenAIModel, error) {

	models, err := services.ListModels(bcl, ml, filter, excludeConfigured)
	if err != nil {
		return nil, err
	}

	dataModels := []schema.OpenAIModel{}

	// Then iterate through the loose files:
	for _, m := range models {
		dataModels = append(dataModels, schema.OpenAIModel{ID: m, Object: "model"})
	}

	return dataModels, nil
}
