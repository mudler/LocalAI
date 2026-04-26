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

// VoiceVerifyEndpoint compares two audio clips and reports whether they were
// spoken by the same person.
// @Summary Verify that two audio clips were spoken by the same person.
// @Tags voice-recognition
// @Param request body schema.VoiceVerifyRequest true "query params"
// @Success 200 {object} schema.VoiceVerifyResponse "Response"
// @Router /v1/voice/verify [post]
func VoiceVerifyEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.VoiceVerifyRequest)
		if !ok || input.Model == "" {
			return echo.ErrBadRequest
		}
		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}

		audio1, cleanup1, err := decodeAudioInput(input.Audio1)
		if err != nil {
			return err
		}
		defer cleanup1()
		audio2, cleanup2, err := decodeAudioInput(input.Audio2)
		if err != nil {
			return err
		}
		defer cleanup2()

		xlog.Debug("VoiceVerify", "model", cfg.Name, "backend", cfg.Backend)
		res, err := backend.VoiceVerify(audio1, audio2, input.Threshold, input.AntiSpoofing, ml, appConfig, *cfg)
		if err != nil {
			return mapBackendError(err)
		}

		return c.JSON(http.StatusOK, schema.VoiceVerifyResponse{
			Verified:         res.GetVerified(),
			Distance:         res.GetDistance(),
			Threshold:        res.GetThreshold(),
			Confidence:       res.GetConfidence(),
			Model:            res.GetModel(),
			ProcessingTimeMs: res.GetProcessingTimeMs(),
		})
	}
}
