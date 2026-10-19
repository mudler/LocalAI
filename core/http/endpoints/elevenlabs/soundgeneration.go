package elevenlabs

import (
	"net/http"
	"path/filepath"
	"slices"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/audio"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// SoundGenerationEndpoint is the ElevenLabs SoundGeneration endpoint https://elevenlabs.io/docs/api-reference/sound-generation
// @Summary Generates audio from the input text.
// @Tags audio
// @Param request body schema.ElevenLabsSoundGenerationRequest true "query params"
// @Success 200 {string} binary	 "Response"
// @Router /v1/sound-generation [post]
func SoundGenerationEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig, authDB *gorm.DB) echo.HandlerFunc {
	return func(c echo.Context) error {

		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.ElevenLabsSoundGenerationRequest)
		if !ok || input.ModelID == "" {
			return echo.ErrBadRequest
		}

		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}

		if user := auth.GetUser(c); authDB != nil && user != nil && user.Role != auth.RoleAdmin {
			perm, err := auth.GetCachedUserPermissions(c, authDB, user.ID)
			if err != nil {
				return c.JSON(http.StatusInternalServerError, schema.ErrorResponse{Error: &schema.APIError{
					Message: "failed to check permissions", Code: http.StatusInternalServerError, Type: "server_error",
				}})
			}
			models := []string{cfg.Name}
			if input.ExpansionModel != nil {
				models = append(models, *input.ExpansionModel)
			}
			for _, name := range models {
				if perm.AllowedModels.Enabled && !slices.Contains(perm.AllowedModels.Models, name) {
					return c.JSON(http.StatusForbidden, schema.ErrorResponse{Error: &schema.APIError{
						Message: "access denied to model: " + name, Code: http.StatusForbidden, Type: "authorization_error",
					}})
				}
			}
		}

		expandedInput, err := backend.ExpandSoundRequest(c.Request().Context(), *input, cl, ml, appConfig)
		if err != nil {
			return echo.NewHTTPError(400, err.Error()).SetInternal(err)
		}
		input = &expandedInput

		xlog.Debug("Sound Generation Request about to be sent to backend", "modelFile", "modelFile", "backend", cfg.Backend)

		language := input.Language
		if language == "" {
			language = input.VocalLanguage
		}
		var bpm *int32
		if input.BPM != nil {
			b := int32(*input.BPM)
			bpm = &b
		}
		filePath, _, err := backend.SoundGeneration(
			c.Request().Context(),
			input.Text, input.Duration, input.Temperature, input.DoSample,
			nil, nil,
			input.Think, input.Caption, input.Lyrics, bpm, input.Keyscale,
			language, input.Timesignature,
			input.Instrumental, input.Params,
			ml, appConfig, *cfg, input.ExpansionModel != nil)
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
