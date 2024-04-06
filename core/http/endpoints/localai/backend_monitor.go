package localai

import (
	"github.com/go-skynet/LocalAI/core/schema"
	"github.com/go-skynet/LocalAI/core/services"
	"github.com/gofiber/fiber/v2"
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
