package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/endpoints/openai"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/pkg/model"
)

// openAI compatible API endpoints
func RegisterOpenAIRoutes(app *fiber.App, requestExtractor *middleware.RequestExtractor, application *core.Application) {

	// chat
	chatChain := []fiber.Handler{
		requestExtractor.SetModelName,
		requestExtractor.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_CHAT)),
		requestExtractor.SetOpenAIRequest,
		openai.ChatEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig),
	}
	app.Post("/v1/chat/completions", chatChain...)
	app.Post("/chat/completions", chatChain...)

	// edit
	editChain := []fiber.Handler{
		requestExtractor.SetModelName,
		requestExtractor.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_CHAT)),
		requestExtractor.SetOpenAIRequest,
		openai.EditEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig),
	}
	app.Post("/v1/edits", editChain...)
	app.Post("/edits", editChain...)

	// assistant
	app.Get("/v1/assistants", openai.ListAssistantsEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Get("/assistants", openai.ListAssistantsEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Post("/v1/assistants", openai.CreateAssistantEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Post("/assistants", openai.CreateAssistantEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Delete("/v1/assistants/:assistant_id", openai.DeleteAssistantEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Delete("/assistants/:assistant_id", openai.DeleteAssistantEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Get("/v1/assistants/:assistant_id", openai.GetAssistantEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Get("/assistants/:assistant_id", openai.GetAssistantEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Post("/v1/assistants/:assistant_id", openai.ModifyAssistantEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Post("/assistants/:assistant_id", openai.ModifyAssistantEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Get("/v1/assistants/:assistant_id/files", openai.ListAssistantFilesEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Get("/assistants/:assistant_id/files", openai.ListAssistantFilesEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Post("/v1/assistants/:assistant_id/files", openai.CreateAssistantFileEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Post("/assistants/:assistant_id/files", openai.CreateAssistantFileEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Delete("/v1/assistants/:assistant_id/files/:file_id", openai.DeleteAssistantFileEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Delete("/assistants/:assistant_id/files/:file_id", openai.DeleteAssistantFileEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Get("/v1/assistants/:assistant_id/files/:file_id", openai.GetAssistantFileEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Get("/assistants/:assistant_id/files/:file_id", openai.GetAssistantFileEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))

	// files
	app.Post("/v1/files", openai.UploadFilesEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Post("/files", openai.UploadFilesEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Get("/v1/files", openai.ListFilesEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Get("/files", openai.ListFilesEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Get("/v1/files/:file_id", openai.GetFilesEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Get("/files/:file_id", openai.GetFilesEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Delete("/v1/files/:file_id", openai.DeleteFilesEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Delete("/files/:file_id", openai.DeleteFilesEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Get("/v1/files/:file_id/content", openai.GetFilesContentsEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Get("/files/:file_id/content", openai.GetFilesContentsEndpoint(application.BackendConfigLoader, application.ApplicationConfig))

	// completion
	completionChain := []fiber.Handler{
		requestExtractor.SetModelName,
		requestExtractor.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_COMPLETION)),
		requestExtractor.SetOpenAIRequest,
		openai.CompletionEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig),
	}
	app.Post("/v1/completions", completionChain...)
	app.Post("/completions", completionChain...)
	app.Post("/v1/engines/:model/completions", completionChain...)

	// embeddings
	embeddingChain := []fiber.Handler{
		requestExtractor.SetModelName,
		requestExtractor.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_EMBEDDINGS)),
		requestExtractor.SetOpenAIRequest,
		openai.EmbeddingsEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig),
	}
	app.Post("/v1/embeddings", embeddingChain...)
	app.Post("/embeddings", embeddingChain...)
	app.Post("/v1/engines/:model/embeddings", embeddingChain...)

	// audio
	app.Post("/v1/audio/transcriptions", requestExtractor.SetModelName,
		requestExtractor.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_TRANSCRIPT)),
		requestExtractor.SetOpenAIRequest,
		openai.TranscriptEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig),
	)
	app.Post("/v1/audio/speech", requestExtractor.SetModelName,
		requestExtractor.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_TTS)),
		localai.TTSEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig),
	)

	// images
	app.Post("/v1/images/generations", requestExtractor.SetModelName,
		requestExtractor.BuildConstantDefaultModelNameMiddleware(model.StableDiffusionBackend),
		requestExtractor.SetOpenAIRequest,
		openai.ImageEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))

	if application.ApplicationConfig.ImageDir != "" {
		app.Static("/generated-images", application.ApplicationConfig.ImageDir)
	}

	if application.ApplicationConfig.AudioDir != "" {
		app.Static("/generated-audio", application.ApplicationConfig.AudioDir)
	}

	app.Get("/v1/models", openai.ListModelsEndpoint(application.ListModelsService))
	app.Get("/models", openai.ListModelsEndpoint(application.ListModelsService))
}
