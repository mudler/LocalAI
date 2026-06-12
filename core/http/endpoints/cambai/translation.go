package cambai

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

func buildTranslationPrompt(text, sourceLang, targetLang string) string {
	return fmt.Sprintf(
		"Translate the following text from %s to %s. Output ONLY the translation, nothing else.\n\n%s",
		sourceLang, targetLang, text,
	)
}

// TranslationEndpoint handles CAMB AI translation (POST /apis/translate).
// Uses an LLM chat backend to perform translation.
func TranslationEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.CambAITranslationRequest)
		if !ok {
			return echo.ErrBadRequest
		}

		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}

		xlog.Debug("CAMB AI translation request received", "model", input.Model)

		sourceLang := schema.CambAILanguageCodeFromID(input.SourceLanguageID)
		targetLang := schema.CambAILanguageCodeFromID(input.TargetLanguageID)

		var translations []string
		for _, text := range input.Texts {
			prompt := buildTranslationPrompt(text, sourceLang, targetLang)

			fn, err := backend.ModelInference(
				c.Request().Context(), prompt, nil, nil, nil, nil,
				ml, cfg, cl, appConfig, nil, "", "", nil, nil, nil,
			)
			if err != nil {
				return err
			}

			resp, err := fn()
			if err != nil {
				return err
			}

			translations = append(translations, strings.TrimSpace(resp.Response))
		}

		taskID := uuid.New().String()

		return c.JSON(http.StatusOK, schema.CambAITaskStatusResponse{
			Status: "SUCCESS",
			RunID:  taskID,
			Output: schema.CambAITranslationResponse{
				Translation: translations,
				SourceLang:  input.SourceLanguageID,
				TargetLang:  input.TargetLanguageID,
			},
		})
	}
}

// TranslationStreamEndpoint handles CAMB AI streaming translation (POST /apis/translation/stream).
func TranslationStreamEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.CambAITranslationStreamRequest)
		if !ok {
			return echo.ErrBadRequest
		}

		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}

		xlog.Debug("CAMB AI translation stream request received", "model", input.Model)

		sourceLang := schema.CambAILanguageCodeFromID(input.SourceLanguageID)
		targetLang := schema.CambAILanguageCodeFromID(input.TargetLanguageID)
		prompt := buildTranslationPrompt(input.Text, sourceLang, targetLang)

		fn, err := backend.ModelInference(
			context.Background(), prompt, nil, nil, nil, nil,
			ml, cfg, cl, appConfig, nil, "", "", nil, nil, nil,
		)
		if err != nil {
			return err
		}

		resp, err := fn()
		if err != nil {
			return err
		}

		return c.JSON(http.StatusOK, map[string]any{
			"translation":     strings.TrimSpace(resp.Response),
			"source_language": input.SourceLanguageID,
			"target_language": input.TargetLanguageID,
		})
	}
}

// TranslatedTTSEndpoint handles CAMB AI translated TTS (POST /apis/translated-tts).
// First translates text via LLM, then synthesizes speech from the translation.
func TranslatedTTSEndpoint(cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		input, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_LOCALAI_REQUEST).(*schema.CambAITranslatedTTSRequest)
		if !ok {
			return echo.ErrBadRequest
		}

		cfg, ok := c.Get(middleware.CONTEXT_LOCALS_KEY_MODEL_CONFIG).(*config.ModelConfig)
		if !ok || cfg == nil {
			return echo.ErrBadRequest
		}

		xlog.Debug("CAMB AI translated TTS request received", "model", input.Model)

		sourceLang := schema.CambAILanguageCodeFromID(input.SourceLanguageID)
		targetLang := schema.CambAILanguageCodeFromID(input.TargetLanguageID)
		prompt := buildTranslationPrompt(input.Text, sourceLang, targetLang)

		// Step 1: Translate
		fn, err := backend.ModelInference(
			c.Request().Context(), prompt, nil, nil, nil, nil,
			ml, cfg, cl, appConfig, nil, "", "", nil, nil, nil,
		)
		if err != nil {
			return err
		}

		resp, err := fn()
		if err != nil {
			return err
		}

		translatedText := strings.TrimSpace(resp.Response)

		// Step 2: TTS on translated text
		// Find a TTS model from config
		ttsConfigs := cl.GetModelConfigsByFilter(config.BuildUsecaseFilterFn(config.FLAG_TTS))
		if len(ttsConfigs) == 0 {
			return c.JSON(http.StatusServiceUnavailable, schema.CambAIErrorResponse{
				Detail: "No TTS model configured. Configure a TTS model to use translated TTS.",
			})
		}
		ttsCfg := ttsConfigs[0]

		voice := fmt.Sprintf("%d", input.VoiceID)
		language := targetLang

		filePath, _, err := backend.ModelTTS(translatedText, voice, language, ml, appConfig, ttsCfg)
		if err != nil {
			return err
		}

		taskID := uuid.New().String()
		ttsTaskResults.Store(taskID, filePath)

		return c.JSON(http.StatusOK, schema.CambAITaskResponse{
			TaskID: taskID,
			Status: "SUCCESS",
			RunID:  taskID,
		})
	}
}
