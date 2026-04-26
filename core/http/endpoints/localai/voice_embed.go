package localai

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// VoiceEmbedEndpoint extracts a speaker embedding vector from an audio clip.
//
// Distinct from /v1/embeddings, which is OpenAI-compatible and text-only
// by contract. Use this endpoint when you need a speaker-encoder output
// (typically 192-d for ECAPA-TDNN, 256-d for ResNet/WeSpeaker).
//
// @Summary Extract a speaker embedding from an audio clip.
// @Tags voice-recognition
// @Param request body schema.VoiceEmbedRequest true "query params"
// @Success 200 {object} schema.VoiceEmbedResponse "Response"
// @Router /v1/voice/embed [post]
func VoiceEmbedEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.VoiceEmbedRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}
		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}

		audio, cleanup, err := decodeAudioInput(input.Audio)
		if err != nil {
			return err
		}
		defer cleanup()

		xlog.Debug("VoiceEmbed", "model", cfg.Name, "backend", cfg.Backend)
		res, err := backend.VoiceEmbed(audio, ml, appConfig, *cfg)
		if err != nil {
			return mapBackendError(err)
		}
		return c.JSON(http.StatusOK, schema.VoiceEmbedResponse{
			Embedding: res.GetEmbedding(),
			Dim:       len(res.GetEmbedding()),
			Model:     res.GetModel(),
		})
	}
}
