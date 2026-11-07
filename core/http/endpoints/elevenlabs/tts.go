package elevenlabs

import (
	"net/http"
	"path/filepath"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/voiceprofile"
	"github.com/mudler/LocalAI/pkg/audio"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// TTSEndpoint is the OpenAI Speech API endpoint https://platform.openai.com/docs/api-reference/audio/createSpeech
// @Summary Generates audio from the input text.
// @Tags audio
// @Param  voice-id	path string	true	"Account ID"
// @Param request body schema.TTSRequest true "query params"
// @Success 200 {string} binary	 "Response"
// @Router /v1/text-to-speech/{voice-id} [post]
func TTSEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {

		voiceID := c.Param("voice-id")
		// The TTS backends that clone from a reference clip take the voice as
		// a file name: only an administrator, or a server without accounts,
		// may pass a path.
		if u := auth.GetUser(c); u != nil && u.Role != auth.RoleAdmin && !voiceprofile.IsReference(voiceID) && voiceprofile.UnsafeVoice(voiceID) {
			return echo.NewHTTPError(http.StatusBadRequest, "voice must be a voice profile or a voice of the model, not a path")
		}

		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.ElevenLabsTTSRequest)
		if !ok || input.ModelID == "" {
			return echo.ErrBadRequest
		}

		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}

		xlog.Debug("elevenlabs TTS request received", "modelName", input.ModelID)

		filePath, _, err := backend.ModelTTS(c.Request().Context(), input.Text, voiceID, input.LanguageCode, "", nil, ml, appConfig, *cfg)
		if err != nil {
			return err
		}
		filePath, contentType := audio.NormalizeAudioFile(filePath)
		if contentType != "" {
			c.Response().Header().Set("Content-Type", contentType)
		}
		return c.Attachment(filePath, filepath.Base(filePath))
	}
}
