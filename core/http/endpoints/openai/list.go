package openai

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services"
)

func ListModelsEndpoint(lms *services.ListModels) func(ctx *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		// If blank, no filter is applied.
		filter := c.Query("filter")

		// By default, exclude any loose files that are already referenced by a configuration file.
		excludeConfigured := c.QueryBool("excludeConfigured", true)

		filterFn, err := config.BuildNameFilterFn(filter)
		if err != nil {
			return err
		}

		var policy services.LooseFilePolicy
		if excludeConfigured {
			policy = services.SKIP_IF_CONFIGURED
		} else {
			policy = services.ALWAYS_INCLUDE
		}

		dataModels, err := lms.ListModels(filterFn, policy)
		if err != nil {
			return err
		}
		return c.JSON(struct {
			Object string               `json:"object"`
			Data   []schema.OpenAIModel `json:"data"`
		}{
			Object: "list",
			Data:   dataModels,
		})
	}
}
