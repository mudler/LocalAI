package localai

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/rs/zerolog/log"
)

// VADEndpoint is Voice-Activation-Detection endpoint
// @Summary	Detect voice fragments in an audio stream
// @Accept json
// @Param		request	body		schema.VADRequest	true	"query params"
// @Success 200 {object} proto.VADResponse "Response"
// @Router		/vad [post]
func VADEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.VADRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}

		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}

		log.Debug().Str("model", input.Model).Msg("LocalAI VAD Request received")

		resp, err := backend.VAD(input, c.Request().Context(), ml, appConfig, *cfg)

		if err != nil {
			return err
		}

		return c.JSON(200, resp)
	}
}
