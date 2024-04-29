package openai

import (
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/go-skynet/LocalAI/core/services"
	"github.com/gofiber/fiber/v2"
)

func ListModelsEndpoint(lms *services.ListModelsService) func(ctx *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		// If blank, no filter is applied.
		filter := c.Query("filter")

		// By default, exclude any loose files that are already referenced by a configuration file.
		excludeConfigured := c.QueryBool("excludeConfigured", true)

		dataModels, err := lms.ListModels(filter, excludeConfigured)
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
