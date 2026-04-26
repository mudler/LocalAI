package localai

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/voicerecognition"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// VoiceRegisterEndpoint enrolls a speaker into the 1:N identification store.
// @Summary Register a speaker for 1:N identification.
// @Tags voice-recognition
// @Param request body schema.VoiceRegisterRequest true "query params"
// @Success 200 {object} schema.VoiceRegisterResponse "Response"
// @Router /v1/voice/register [post]
func VoiceRegisterEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig, registry voicerecognition.Registry) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.VoiceRegisterRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}
		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}
		if input.Name == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "name is required")
		}

		audio, cleanup, err := decodeAudioInput(input.Audio)
		if err != nil {
			return err
		}
		defer cleanup()

		xlog.Debug("VoiceRegister", "model", cfg.Name, "name", input.Name)
		res, err := backend.VoiceEmbed(audio, ml, appConfig, *cfg)
		if err != nil {
			return mapBackendError(err)
		}

		stored, err := registry.Register(c.Request().Context(), res.GetEmbedding(), voicerecognition.Metadata{
			Name:   input.Name,
			Labels: input.Labels,
		})
		if err != nil {
			return err
		}
		return c.JSON(http.StatusOK, schema.VoiceRegisterResponse{
			ID:           stored.ID,
			Name:         stored.Name,
			RegisteredAt: stored.RegisteredAt,
		})
	}
}
