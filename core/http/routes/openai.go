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
		openai.ChatEndpoint(application.BackendLoader(), application.ModelLoader(), application.TemplatesEvaluator(), application.ApplicationConfig()),
	}
	app.Post("/v1/chat/completions", chatChain...)
	app.Post("/chat/completions", chatChain...)

	// edit
	editChain := []fiber.Handler{
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_EDIT)),
		re.BuildConstantDefaultModelNameMiddleware("gpt-4o"),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		re.SetOpenAIRequest,
		openai.EditEndpoint(application.BackendLoader(), application.ModelLoader(), application.TemplatesEvaluator(), application.ApplicationConfig()),
	}
	app.Post("/v1/edits", editChain...)
	app.Post("/edits", editChain...)

	// completion
	completionChain := []fiber.Handler{
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_COMPLETION)),
		re.BuildConstantDefaultModelNameMiddleware("gpt-4o"),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		re.SetOpenAIRequest,
		openai.CompletionEndpoint(application.BackendLoader(), application.ModelLoader(), application.TemplatesEvaluator(), application.ApplicationConfig()),
	}
	app.Post("/v1/completions", completionChain...)
	app.Post("/completions", completionChain...)
	app.Post("/v1/engines/:model/completions", completionChain...)

	// assistant
	app.Get("/v1/assistants", openai.ListAssistantsEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))
	app.Get("/assistants", openai.ListAssistantsEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))
	app.Post("/v1/assistants", openai.CreateAssistantEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))
	app.Post("/assistants", openai.CreateAssistantEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))
	app.Delete("/v1/assistants/:assistant_id", openai.DeleteAssistantEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))
	app.Delete("/assistants/:assistant_id", openai.DeleteAssistantEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))
	app.Get("/v1/assistants/:assistant_id", openai.GetAssistantEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))
	app.Get("/assistants/:assistant_id", openai.GetAssistantEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))
	app.Post("/v1/assistants/:assistant_id", openai.ModifyAssistantEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))
	app.Post("/assistants/:assistant_id", openai.ModifyAssistantEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))
	app.Get("/v1/assistants/:assistant_id/files", openai.ListAssistantFilesEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))
	app.Get("/assistants/:assistant_id/files", openai.ListAssistantFilesEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))
	app.Post("/v1/assistants/:assistant_id/files", openai.CreateAssistantFileEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))
	app.Post("/assistants/:assistant_id/files", openai.CreateAssistantFileEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))
	app.Delete("/v1/assistants/:assistant_id/files/:file_id", openai.DeleteAssistantFileEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))
	app.Delete("/assistants/:assistant_id/files/:file_id", openai.DeleteAssistantFileEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))
	app.Get("/v1/assistants/:assistant_id/files/:file_id", openai.GetAssistantFileEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))
	app.Get("/assistants/:assistant_id/files/:file_id", openai.GetAssistantFileEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))

	// files
	app.Post("/v1/files", openai.UploadFilesEndpoint(application.BackendLoader(), application.ApplicationConfig()))
	app.Post("/files", openai.UploadFilesEndpoint(application.BackendLoader(), application.ApplicationConfig()))
	app.Get("/v1/files", openai.ListFilesEndpoint(application.BackendLoader(), application.ApplicationConfig()))
	app.Get("/files", openai.ListFilesEndpoint(application.BackendLoader(), application.ApplicationConfig()))
	app.Get("/v1/files/:file_id", openai.GetFilesEndpoint(application.BackendLoader(), application.ApplicationConfig()))
	app.Get("/files/:file_id", openai.GetFilesEndpoint(application.BackendLoader(), application.ApplicationConfig()))
	app.Delete("/v1/files/:file_id", openai.DeleteFilesEndpoint(application.BackendLoader(), application.ApplicationConfig()))
	app.Delete("/files/:file_id", openai.DeleteFilesEndpoint(application.BackendLoader(), application.ApplicationConfig()))
	app.Get("/v1/files/:file_id/content", openai.GetFilesContentsEndpoint(application.BackendLoader(), application.ApplicationConfig()))
	app.Get("/files/:file_id/content", openai.GetFilesContentsEndpoint(application.BackendLoader(), application.ApplicationConfig()))

	// embeddings
	embeddingChain := []fiber.Handler{
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_EMBEDDINGS)),
		re.BuildConstantDefaultModelNameMiddleware("gpt-4o"),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		re.SetOpenAIRequest,
		openai.EmbeddingsEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()),
	}
	app.Post("/v1/embeddings", embeddingChain...)
	app.Post("/embeddings", embeddingChain...)
	app.Post("/v1/engines/:model/embeddings", embeddingChain...)

	// audio
	app.Post("/v1/audio/transcriptions",
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_TRANSCRIPT)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		re.SetOpenAIRequest,
		openai.TranscriptEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()),
	)

	app.Post("/v1/audio/speech",
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_TTS)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.TTSRequest) }),
		localai.TTSEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))

	// images
	app.Post("/v1/images/generations",
		re.BuildConstantDefaultModelNameMiddleware("stablediffusion"),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		re.SetOpenAIRequest,
		openai.ImageEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))

	// List models
	app.Get("/v1/models", openai.ListModelsEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))
	app.Get("/models", openai.ListModelsEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))
}
