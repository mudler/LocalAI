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
		var policy services.LooseFilePolicy
		if c.QueryBool("excludeConfigured", true) {
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

		return c.JSON(schema.ModelsDataResponse{
			Object: "list",
			Data:   dataModels,
		})
	}
}
