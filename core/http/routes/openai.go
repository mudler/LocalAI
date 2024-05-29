package routes

import (
	"github.com/go-skynet/LocalAI/core"
	"github.com/go-skynet/LocalAI/core/http/endpoints/localai"
	"github.com/go-skynet/LocalAI/core/http/endpoints/openai"
	"github.com/go-skynet/LocalAI/core/http/middleware"
	"github.com/gofiber/fiber/v2"
)

func RegisterOpenAIRoutes(app *fiber.App,
	application *core.Application,
	requestExtractor *middleware.RequestExtractor,
	auth fiber.Handler) {

	requestExtractorMiddleware := middleware.NewRequestExtractor(application.ModelLoader, application.ApplicationConfig)
	// openAI compatible API endpoint

	// chat
	app.Post("/v1/chat/completions", auth, openai.ChatEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Post("/chat/completions", auth, openai.ChatEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))

	// edit
	app.Post("/v1/edits", auth, openai.EditEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Post("/edits", auth, openai.EditEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))

	// assistant
	app.Get("/v1/assistants", auth, openai.ListAssistantsEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Get("/assistants", auth, openai.ListAssistantsEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Post("/v1/assistants", auth, openai.CreateAssistantEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Post("/assistants", auth, openai.CreateAssistantEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Delete("/v1/assistants/:assistant_id", auth, openai.DeleteAssistantEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Delete("/assistants/:assistant_id", auth, openai.DeleteAssistantEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Get("/v1/assistants/:assistant_id", auth, openai.GetAssistantEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Get("/assistants/:assistant_id", auth, openai.GetAssistantEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Post("/v1/assistants/:assistant_id", auth, openai.ModifyAssistantEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Post("/assistants/:assistant_id", auth, openai.ModifyAssistantEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Get("/v1/assistants/:assistant_id/files", auth, openai.ListAssistantFilesEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Get("/assistants/:assistant_id/files", auth, openai.ListAssistantFilesEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Post("/v1/assistants/:assistant_id/files", auth, openai.CreateAssistantFileEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Post("/assistants/:assistant_id/files", auth, openai.CreateAssistantFileEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Delete("/v1/assistants/:assistant_id/files/:file_id", auth, openai.DeleteAssistantFileEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Delete("/assistants/:assistant_id/files/:file_id", auth, openai.DeleteAssistantFileEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Get("/v1/assistants/:assistant_id/files/:file_id", auth, openai.GetAssistantFileEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Get("/assistants/:assistant_id/files/:file_id", auth, openai.GetAssistantFileEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))

	// files
	app.Post("/v1/files", auth, openai.UploadFilesEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Post("/files", auth, openai.UploadFilesEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Get("/v1/files", auth, openai.ListFilesEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Get("/files", auth, openai.ListFilesEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Get("/v1/files/:file_id", auth, openai.GetFilesEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Get("/files/:file_id", auth, openai.GetFilesEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Delete("/v1/files/:file_id", auth, openai.DeleteFilesEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Delete("/files/:file_id", auth, openai.DeleteFilesEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Get("/v1/files/:file_id/content", auth, openai.GetFilesContentsEndpoint(application.BackendConfigLoader, application.ApplicationConfig))
	app.Get("/files/:file_id/content", auth, openai.GetFilesContentsEndpoint(application.BackendConfigLoader, application.ApplicationConfig))

	// completion
	app.Post("/v1/completions", auth, openai.CompletionEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Post("/completions", auth, openai.CompletionEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Post("/v1/engines/:model/completions", auth, openai.CompletionEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))

	// embeddings
	ee := []fiber.Handler{auth, requestExtractorMiddleware.SetModelName, requestExtractorMiddleware.SetOpenAIRequest, openai.EmbeddingsEndpoint(application.EmbeddingsBackendService)}
	app.Post("/v1/embeddings", ee...)
	app.Post("/embeddings", ee...)
	app.Post("/v1/engines/:model/embeddings", ee...)

	// audio
	app.Post("/v1/audio/transcriptions", auth, openai.TranscriptEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))
	app.Post("/v1/audio/speech", auth, requestExtractor.SetModelName, localai.TTSEndpoint(application.TextToSpeechBackendService))

	// images
	app.Post("/v1/images/generations", auth, openai.ImageEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig))

	if application.ApplicationConfig.ImageDir != "" {
		app.Static("/generated-images", application.ApplicationConfig.ImageDir)
	}

	if application.ApplicationConfig.AudioDir != "" {
		app.Static("/generated-audio", application.ApplicationConfig.AudioDir)
	}

	// models
	app.Get("/v1/models", auth, openai.ListModelsEndpoint(application.ListModelsService))
	app.Get("/models", auth, openai.ListModelsEndpoint(application.ListModelsService))
}
