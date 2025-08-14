package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/elevenlabs"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
)

func RegisterElevenLabsRoutes(app *fiber.App,
	re *middleware.RequestExtractor,
	cl *config.ModelConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig) {

	// Elevenlabs
	app.Post("/v1/text-to-speech/:voice-id",
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_TTS)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.ElevenLabsTTSRequest) }),
		elevenlabs.TTSEndpoint(cl, ml, appConfig))

	app.Post("/v1/sound-generation",
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_SOUND_GENERATION)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.ElevenLabsSoundGenerationRequest) }),
		elevenlabs.SoundGenerationEndpoint(cl, ml, appConfig))

}
