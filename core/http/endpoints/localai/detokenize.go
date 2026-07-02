package localai

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
)

// DetokenizeEndpoint exposes a REST API to convert token IDs back to text.
// @Summary Detokenize the input.
// @Tags tokenize
// @Param request body schema.DetokenizeRequest true "Request"
// @Success 200 {object} schema.DetokenizeResponse "Response"
// @Router /v1/detokenize [post]
func DetokenizeEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.DetokenizeRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}

		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}

		resp, err := backend.ModelDetokenize(input.Tokens, ml, *cfg, appConfig)
		if err != nil {
			return err
		}
		return c.JSON(200, resp)
	}
}
