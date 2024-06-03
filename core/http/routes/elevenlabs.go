package routes

import (
	"github.com/go-skynet/LocalAI/core/backend"
	"github.com/go-skynet/LocalAI/core/http/endpoints/elevenlabs"
	"github.com/go-skynet/LocalAI/core/http/middleware"
	"github.com/gofiber/fiber/v2"
)

func RegisterElevenLabsRoutes(app *fiber.App,
	ttsbs *backend.TextToSpeechBackendService,
	requestExtractor *middleware.RequestExtractor,
	auth func(*fiber.Ctx) error) {

	// Elevenlabs
	app.Post("/v1/text-to-speech/:voice-id", auth, requestExtractor.SetModelName, elevenlabs.TTSEndpoint(ttsbs))

}
