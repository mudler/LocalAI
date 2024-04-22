package routes

import (
	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/http/endpoints/elevenlabs"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/gofiber/fiber/v2"
)

func RegisterElevenLabsRoutes(app *fiber.App,
	cl *config.BackendConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	auth func(*fiber.Ctx) error) {

	// Elevenlabs
	app.Post("/v1/text-to-speech/:voice-id", auth, elevenlabs.TTSEndpoint(cl, ml, appConfig))

}
