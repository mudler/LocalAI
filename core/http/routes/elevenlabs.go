package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/elevenlabs"
	"github.com/mudler/LocalAI/pkg/model"
)

func RegisterElevenLabsRoutes(app *fiber.App,
	cl *config.BackendConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig) {

	// Elevenlabs
	app.Post("/v1/text-to-speech/:voice-id", elevenlabs.TTSEndpoint(cl, ml, appConfig))

	app.Post("/v1/sound-generation", elevenlabs.SoundGenerationEndpoint(cl, ml, appConfig))

}
