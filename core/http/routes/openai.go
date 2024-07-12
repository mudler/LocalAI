package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/endpoints/openai"
	"github.com/mudler/LocalAI/pkg/model"
)

func RegisterOpenAIRoutes(app *fiber.App,
	cl *config.BackendConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	auth func(*fiber.Ctx) error) {
	// openAI compatible API endpoint

	// chat
	app.Post("/v1/chat/completions", auth, openai.ChatEndpoint(cl, ml, appConfig))
	app.Post("/chat/completions", auth, openai.ChatEndpoint(cl, ml, appConfig))

	// edit
	app.Post("/v1/edits", auth, openai.EditEndpoint(cl, ml, appConfig))
	app.Post("/edits", auth, openai.EditEndpoint(cl, ml, appConfig))

	// assistant
	app.Get("/v1/assistants", auth, openai.ListAssistantsEndpoint(cl, ml, appConfig))
	app.Get("/assistants", auth, openai.ListAssistantsEndpoint(cl, ml, appConfig))
	app.Post("/v1/assistants", auth, openai.CreateAssistantEndpoint(cl, ml, appConfig))
	app.Post("/assistants", auth, openai.CreateAssistantEndpoint(cl, ml, appConfig))
	app.Delete("/v1/assistants/:assistant_id", auth, openai.DeleteAssistantEndpoint(cl, ml, appConfig))
	app.Delete("/assistants/:assistant_id", auth, openai.DeleteAssistantEndpoint(cl, ml, appConfig))
	app.Get("/v1/assistants/:assistant_id", auth, openai.GetAssistantEndpoint(cl, ml, appConfig))
	app.Get("/assistants/:assistant_id", auth, openai.GetAssistantEndpoint(cl, ml, appConfig))
	app.Post("/v1/assistants/:assistant_id", auth, openai.ModifyAssistantEndpoint(cl, ml, appConfig))
	app.Post("/assistants/:assistant_id", auth, openai.ModifyAssistantEndpoint(cl, ml, appConfig))
	app.Get("/v1/assistants/:assistant_id/files", auth, openai.ListAssistantFilesEndpoint(cl, ml, appConfig))
	app.Get("/assistants/:assistant_id/files", auth, openai.ListAssistantFilesEndpoint(cl, ml, appConfig))
	app.Post("/v1/assistants/:assistant_id/files", auth, openai.CreateAssistantFileEndpoint(cl, ml, appConfig))
	app.Post("/assistants/:assistant_id/files", auth, openai.CreateAssistantFileEndpoint(cl, ml, appConfig))
	app.Delete("/v1/assistants/:assistant_id/files/:file_id", auth, openai.DeleteAssistantFileEndpoint(cl, ml, appConfig))
	app.Delete("/assistants/:assistant_id/files/:file_id", auth, openai.DeleteAssistantFileEndpoint(cl, ml, appConfig))
	app.Get("/v1/assistants/:assistant_id/files/:file_id", auth, openai.GetAssistantFileEndpoint(cl, ml, appConfig))
	app.Get("/assistants/:assistant_id/files/:file_id", auth, openai.GetAssistantFileEndpoint(cl, ml, appConfig))

	// files
	app.Post("/v1/files", auth, openai.UploadFilesEndpoint(cl, appConfig))
	app.Post("/files", auth, openai.UploadFilesEndpoint(cl, appConfig))
	app.Get("/v1/files", auth, openai.ListFilesEndpoint(cl, appConfig))
	app.Get("/files", auth, openai.ListFilesEndpoint(cl, appConfig))
	app.Get("/v1/files/:file_id", auth, openai.GetFilesEndpoint(cl, appConfig))
	app.Get("/files/:file_id", auth, openai.GetFilesEndpoint(cl, appConfig))
	app.Delete("/v1/files/:file_id", auth, openai.DeleteFilesEndpoint(cl, appConfig))
	app.Delete("/files/:file_id", auth, openai.DeleteFilesEndpoint(cl, appConfig))
	app.Get("/v1/files/:file_id/content", auth, openai.GetFilesContentsEndpoint(cl, appConfig))
	app.Get("/files/:file_id/content", auth, openai.GetFilesContentsEndpoint(cl, appConfig))

	// completion
	app.Post("/v1/completions", auth, openai.CompletionEndpoint(cl, ml, appConfig))
	app.Post("/completions", auth, openai.CompletionEndpoint(cl, ml, appConfig))
	app.Post("/v1/engines/:model/completions", auth, openai.CompletionEndpoint(cl, ml, appConfig))

	// embeddings
	app.Post("/v1/embeddings", auth, openai.EmbeddingsEndpoint(cl, ml, appConfig))
	app.Post("/embeddings", auth, openai.EmbeddingsEndpoint(cl, ml, appConfig))
	app.Post("/v1/engines/:model/embeddings", auth, openai.EmbeddingsEndpoint(cl, ml, appConfig))

	// audio
	app.Post("/v1/audio/transcriptions", auth, openai.TranscriptEndpoint(cl, ml, appConfig))
	app.Post("/v1/audio/speech", auth, localai.TTSEndpoint(cl, ml, appConfig))

	// images
	app.Post("/v1/images/generations", auth, openai.ImageEndpoint(cl, ml, appConfig))

	if appConfig.ImageDir != "" {
		app.Static("/generated-images", appConfig.ImageDir)
	}

	if appConfig.AudioDir != "" {
		app.Static("/generated-audio", appConfig.AudioDir)
	}

	// List models
	app.Get("/v1/models", auth, openai.ListModelsEndpoint(cl, ml))
	app.Get("/models", auth, openai.ListModelsEndpoint(cl, ml))
}
