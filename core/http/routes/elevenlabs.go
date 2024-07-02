package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/elevenlabs"
	"github.com/mudler/LocalAI/core/http/middleware"
)

func RegisterElevenLabsRoutes(app *fiber.App, requestExtractor *middleware.RequestExtractor, application *core.Application) {

	// Elevenlabs TTS
	app.Post("/v1/text-to-speech/:voice-id", requestExtractor.SetModelName,
		requestExtractor.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_TTS)),
		elevenlabs.TTSEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig),
	)

}
