package routes

import (
	"github.com/go-skynet/LocalAI/core/backend"
	"github.com/go-skynet/LocalAI/core/http/ctx"
	"github.com/go-skynet/LocalAI/core/http/endpoints/elevenlabs"
	"github.com/gofiber/fiber/v2"
)

func RegisterElevenLabsRoutes(app *fiber.App,
	ttsbs *backend.TextToSpeechBackendService,
	fce *ctx.FiberContentExtractor,
	auth func(*fiber.Ctx) error) {

	// Elevenlabs
	app.Post("/v1/text-to-speech/:voice-id", auth, elevenlabs.TTSEndpoint(ttsbs, fce))

}
