package localai

import (
	"github.com/gofiber/fiber/v2"
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
func VADEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		input, ok := c.Locals(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.VADRequest)
		if !ok || input.Model == "" {
			return fiber.ErrBadRequest
		}

		cfg, ok := c.Locals(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return fiber.ErrBadRequest
		}

		log.Debug().Str("model", input.Model).Msg("LocalAI VAD Request received")

		resp, err := backend.VAD(input, c.Context(), ml, appConfig, *cfg)

		if err != nil {
			return err
		}

		return c.JSON(resp)
	}
}
