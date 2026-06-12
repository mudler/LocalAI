package cambai

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// SoundGenerationEndpoint handles CAMB AI text-to-sound (POST /apis/text-to-sound).
func SoundGenerationEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.CambAITextToSoundRequest)
		if !ok {
			return echo.ErrBadRequest
		}

		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}

		xlog.Debug("CAMB AI text-to-sound request received", "model", input.Model)

		_, _, err := backend.SoundGeneration(
			input.Prompt, input.Duration, nil, nil,
			nil, nil,
			nil, "", "", nil, "",
			"", "",
			nil,
			ml, appConfig, *cfg)
		if err != nil {
			return err
		}

		taskID := uuid.New().String()

		return c.JSON(http.StatusOK, schema.CambAITaskResponse{
			TaskID: taskID,
			Status: "SUCCESS",
			RunID:  taskID,
		})
	}
}
