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

// VoiceAnalyzeEndpoint returns demographic attributes inferred from speech.
// @Summary Analyze demographic attributes (age, gender, emotion) from a voice clip.
// @Tags voice-recognition
// @Param request body schema.VoiceAnalyzeRequest true "query params"
// @Success 200 {object} schema.VoiceAnalyzeResponse "Response"
// @Router /v1/voice/analyze [post]
func VoiceAnalyzeEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.VoiceAnalyzeRequest)
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

		xlog.Debug("VoiceAnalyze", "model", cfg.Name, "backend", cfg.Backend, "actions", input.Actions)
		res, err := backend.VoiceAnalyze(audio, input.Actions, ml, appConfig, *cfg)
		if err != nil {
			return mapBackendError(err)
		}

		response := schema.VoiceAnalyzeResponse{
			Segments: make([]schema.VoiceAnalysis, len(res.GetSegments())),
		}
		for i, s := range res.GetSegments() {
			response.Segments[i] = schema.VoiceAnalysis{
				Start:           s.GetStart(),
				End:             s.GetEnd(),
				Age:             s.GetAge(),
				DominantGender:  s.GetDominantGender(),
				Gender:          s.GetGender(),
				DominantEmotion: s.GetDominantEmotion(),
				Emotion:         s.GetEmotion(),
			}
		}
		return c.JSON(http.StatusOK, response)
	}
}
