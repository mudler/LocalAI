package localai

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	fiberContext "github.com/mudler/LocalAI/core/http/ctx"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/rs/zerolog/log"

	"github.com/mudler/LocalAI/pkg/model"
)

// TokenMetricsEndpoint is an endpoint to get TokensProcessed Per Second for Active SlotID
//
//	@Summary	Get TokenMetrics for Active Slot.
//	@Accept json
//	@Produce audio/x-wav
//	@Success	200		{string}	binary				"generated audio/wav file"
//	@Router		/v1/tokenMetrics [get]
//	@Router		/tokenMetrics [get]
func TokenMetricsEndpoint(cl *config.BackendConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {

		input := new(schema.TokenMetricsRequest)

		// Get input data from the request body
		if err := c.BodyParser(input); err != nil {
			return err
		}

		modelFile, err := fiberContext.ModelFromContext(c, cl, ml, input.Model, false)
		if err != nil {
			modelFile = input.Model
			log.Warn().Msgf("Model not found in context: %s", input.Model)
		}

		cfg, err := cl.LoadBackendConfigFileByName(modelFile, appConfig.ModelPath,
			config.LoadOptionDebug(appConfig.Debug),
			config.LoadOptionThreads(appConfig.Threads),
			config.LoadOptionContextSize(appConfig.ContextSize),
			config.LoadOptionF16(appConfig.F16),
		)

		if err != nil {
			log.Err(err)
			modelFile = input.Model
			log.Warn().Msgf("Model not found in context: %s", input.Model)
		} else {
			modelFile = cfg.Model
		}
		log.Debug().Msgf("Token Metrics for model: %s", modelFile)

		response, err := backend.TokenMetrics(modelFile, ml, appConfig, *cfg)
		if err != nil {
			return err
		}
		return c.JSON(response)
	}
}
