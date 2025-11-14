package localai

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/rs/zerolog/log"

	"github.com/mudler/LocalAI/pkg/model"
)

// TODO: This is not yet in use. Needs middleware rework, since it is not referenced.

// TokenMetricsEndpoint is an endpoint to get TokensProcessed Per Second for Active SlotID
//
//	@Summary	Get TokenMetrics for Active Slot.
//	@Accept json
//	@Produce audio/x-wav
//	@Success	200		{string}	binary				"generated audio/wav file"
//	@Router		/v1/tokenMetrics [get]
//	@Router		/tokenMetrics [get]
func TokenMetricsEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {

		input := new(schema.TokenMetricsRequest)

		// Get input data from the request body
		if err := c.Bind(input); err != nil {
			return err
		}

		modelFile, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_NAME).(string)
		if !ok || modelFile != "" {
			modelFile = input.Model
			log.Warn().Msgf("Model not found in context: %s", input.Model)
		}

		cfg, err := cl.LoadModelConfigFileByNameDefaultOptions(modelFile, appConfig)

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
		return c.JSON(200, response)
	}
}
