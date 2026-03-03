package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/cambai"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
)

func RegisterCambAIRoutes(app *echo.Echo,
	re *middleware.RequestExtractor,
	cl *config.ModelConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig) {

	// TTS streaming (POST /apis/tts-stream)
	app.POST("/apis/tts-stream",
		cambai.TTSStreamEndpoint(cl, ml, appConfig),
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_TTS)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.CambAITTSStreamRequest) }))

	// TTS async (POST /apis/tts)
	app.POST("/apis/tts",
		cambai.TTSEndpoint(cl, ml, appConfig),
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_TTS)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.CambAITTSRequest) }))

	// TTS task status (GET /apis/tts/:task_id)
	app.GET("/apis/tts/:task_id", cambai.TTSTaskStatusEndpoint())

	// Translated TTS (POST /apis/translated-tts)
	app.POST("/apis/translated-tts",
		cambai.TranslatedTTSEndpoint(cl, ml, appConfig),
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_CHAT)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.CambAITranslatedTTSRequest) }))

	// Translation (POST /apis/translate)
	app.POST("/apis/translate",
		cambai.TranslationEndpoint(cl, ml, appConfig),
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_CHAT)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.CambAITranslationRequest) }))

	// Translation streaming (POST /apis/translation/stream)
	app.POST("/apis/translation/stream",
		cambai.TranslationStreamEndpoint(cl, ml, appConfig),
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_CHAT)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.CambAITranslationStreamRequest) }))

	// Transcription (POST /apis/transcribe)
	app.POST("/apis/transcribe",
		cambai.TranscriptionEndpoint(cl, ml, appConfig),
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_TRANSCRIPT)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.CambAITranscriptionRequest) }))

	// Text-to-sound (POST /apis/text-to-sound)
	app.POST("/apis/text-to-sound",
		cambai.SoundGenerationEndpoint(cl, ml, appConfig),
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_SOUND_GENERATION)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.CambAITextToSoundRequest) }))

	// List voices (GET /apis/list-voices)
	app.GET("/apis/list-voices", cambai.ListVoicesEndpoint(cl, ml, appConfig))

	// Create custom voice (POST /apis/create-custom-voice)
	app.POST("/apis/create-custom-voice",
		cambai.CreateCustomVoiceEndpoint(cl, ml, appConfig))

	// Audio separation stub (POST /apis/audio-separation)
	app.POST("/apis/audio-separation", cambai.AudioSeparationEndpoint())
}
