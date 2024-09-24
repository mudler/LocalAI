package localai

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/rs/zerolog/log"
)

// GetMetricsEndpoint exposes the GetMetrics method via an HTTP endpoint
func GetMetricsEndpoint(cl *config.BackendConfigLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		// Assuming you have logic in the backend for fetching the metrics
		metrics, err := backend.TokenMetrics(loader *model.ModelLoader, appConfig *config.ApplicationConfig, backendConfig config.BackendConfig)(cl *config.BackendConfigLoader, appConfig *config.ApplicationConfig).. // Call your backend method
		if err != nil {
			log.Err(err).Msg("Failed to get metrics")
			return fiber.NewError(fiber.StatusInternalServerError, "Failed to get metrics")
		}

		// Return metrics as a JSON response
		return c.JSON(metrics)
	}
}
