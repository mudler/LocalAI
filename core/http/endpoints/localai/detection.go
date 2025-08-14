package localai

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/rs/zerolog/log"
)

// DetectionEndpoint is the LocalAI Detection endpoint https://localai.io/docs/api-reference/detection
// @Summary Detects objects in the input image.
// @Param request body schema.DetectionRequest true "query params"
// @Success 200 {object} schema.DetectionResponse "Response"
// @Router /v1/detection [post]
func DetectionEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) func(c *fiber.Ctx) error {
	return func(c *fiber.Ctx) error {

		input, ok := c.Locals(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.DetectionRequest)
		if !ok || input.Model == "" {
			return fiber.ErrBadRequest
		}

		cfg, ok := c.Locals(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return fiber.ErrBadRequest
		}

		log.Debug().Str("image", input.Image).Str("modelFile", "modelFile").Str("backend", cfg.Backend).Msg("Detection")

		image, err := utils.GetContentURIAsBase64(input.Image)
		if err != nil {
			return err
		}

		res, err := backend.Detection(image, ml, appConfig, *cfg)
		if err != nil {
			return err
		}

		response := schema.DetectionResponse{
			Detections: make([]schema.Detection, len(res.Detections)),
		}
		for i, detection := range res.Detections {
			response.Detections[i] = schema.Detection{
				X:         detection.X,
				Y:         detection.Y,
				Width:     detection.Width,
				Height:    detection.Height,
				ClassName: detection.ClassName,
			}
		}

		return c.JSON(response)
	}
}
