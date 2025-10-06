package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/endpoints/openai"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
)

func RegisterOpenAIRoutes(app *fiber.App,
	re *middleware.RequestExtractor,
	application *application.Application) {
	// openAI compatible API endpoint

	// realtime
	// TODO: Modify/disable the API key middleware for this endpoint to allow ephemeral keys created by sessions
	app.Get("/v1/realtime", openai.Realtime(application))
	app.Post("/v1/realtime/sessions", openai.RealtimeTranscriptionSession(application))
	app.Post("/v1/realtime/transcription_session", openai.RealtimeTranscriptionSession(application))

	// chat
	chatChain := []fiber.Handler{
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_CHAT)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		re.SetOpenAIRequest,
		openai.ChatEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.TemplatesEvaluator(), application.ApplicationConfig()),
	}
	app.Post("/v1/chat/completions", chatChain...)
	app.Post("/chat/completions", chatChain...)

	// edit
	editChain := []fiber.Handler{
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_EDIT)),
		re.BuildConstantDefaultModelNameMiddleware("gpt-4o"),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		re.SetOpenAIRequest,
		openai.EditEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.TemplatesEvaluator(), application.ApplicationConfig()),
	}
	app.Post("/v1/edits", editChain...)
	app.Post("/edits", editChain...)

	// completion
	completionChain := []fiber.Handler{
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_COMPLETION)),
		re.BuildConstantDefaultModelNameMiddleware("gpt-4o"),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		re.SetOpenAIRequest,
		openai.CompletionEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.TemplatesEvaluator(), application.ApplicationConfig()),
	}
	app.Post("/v1/completions", completionChain...)
	app.Post("/completions", completionChain...)
	app.Post("/v1/engines/:model/completions", completionChain...)

	// MCPcompletion
	mcpCompletionChain := []fiber.Handler{
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_CHAT)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		re.SetOpenAIRequest,
		openai.MCPCompletionEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.TemplatesEvaluator(), application.ApplicationConfig()),
	}
	app.Post("/mcp/v1/chat/completions", mcpCompletionChain...)
	app.Post("/mcp/chat/completions", mcpCompletionChain...)

	// embeddings
	embeddingChain := []fiber.Handler{
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_EMBEDDINGS)),
		re.BuildConstantDefaultModelNameMiddleware("gpt-4o"),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		re.SetOpenAIRequest,
		openai.EmbeddingsEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig()),
	}
	app.Post("/v1/embeddings", embeddingChain...)
	app.Post("/embeddings", embeddingChain...)
	app.Post("/v1/engines/:model/embeddings", embeddingChain...)

	audioChain := []fiber.Handler{
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_TRANSCRIPT)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		re.SetOpenAIRequest,
		openai.TranscriptEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig()),
	}
	// audio
	app.Post("/v1/audio/transcriptions", audioChain...)
	app.Post("/audio/transcriptions", audioChain...)

	audioSpeechChain := []fiber.Handler{
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_TTS)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.TTSRequest) }),
		localai.TTSEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig()),
	}

	app.Post("/v1/audio/speech",
		audioSpeechChain...)
	app.Post("/audio/speech", audioSpeechChain...)

	// images
	imageChain := []fiber.Handler{
		re.BuildConstantDefaultModelNameMiddleware("stablediffusion"),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		re.SetOpenAIRequest,
		openai.ImageEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig()),
	}

	app.Post("/v1/images/generations",
		imageChain...)
	app.Post("/images/generations", imageChain...)

	// List models
	app.Get("/v1/models", openai.ListModelsEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig()))
	app.Get("/models", openai.ListModelsEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig()))
}
