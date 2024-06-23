package localai

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services"
)

func BackendMonitorEndpoint(bm *services.BackendMonitorService) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {

		input := new(schema.BackendMonitorRequest)
		// Get input data from the request body
		if err := c.BodyParser(input); err != nil {
			return err
		}

		resp, err := bm.CheckAndSample(input.Model)
		if err != nil {
			return err
		}
		return c.JSON(resp)
	}
}

func BackendShutdownEndpoint(bm *services.BackendMonitorService) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		input := new(schema.BackendMonitorRequest)
		// Get input data from the request body
		if err := c.BodyParser(input); err != nil {
			return err
		}

		return bm.ShutdownModel(input.Model)
	}
}
