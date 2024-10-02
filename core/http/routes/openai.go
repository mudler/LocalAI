package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/endpoints/openai"
	"github.com/mudler/LocalAI/pkg/model"
)

func RegisterOpenAIRoutes(app *fiber.App,
	cl *config.BackendConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig) {
	// openAI compatible API endpoint

	// realtime
	app.Get("/v1/realtime", websocket.New(openai.RegisterRealtime(cl, ml, appConfig)))

	// chat
	app.Post("/v1/chat/completions", openai.ChatEndpoint(cl, ml, appConfig))
	app.Post("/chat/completions", openai.ChatEndpoint(cl, ml, appConfig))

	// edit
	app.Post("/v1/edits", openai.EditEndpoint(cl, ml, appConfig))
	app.Post("/edits", openai.EditEndpoint(cl, ml, appConfig))

	// assistant
	app.Get("/v1/assistants", openai.ListAssistantsEndpoint(cl, ml, appConfig))
	app.Get("/assistants", openai.ListAssistantsEndpoint(cl, ml, appConfig))
	app.Post("/v1/assistants", openai.CreateAssistantEndpoint(cl, ml, appConfig))
	app.Post("/assistants", openai.CreateAssistantEndpoint(cl, ml, appConfig))
	app.Delete("/v1/assistants/:assistant_id", openai.DeleteAssistantEndpoint(cl, ml, appConfig))
	app.Delete("/assistants/:assistant_id", openai.DeleteAssistantEndpoint(cl, ml, appConfig))
	app.Get("/v1/assistants/:assistant_id", openai.GetAssistantEndpoint(cl, ml, appConfig))
	app.Get("/assistants/:assistant_id", openai.GetAssistantEndpoint(cl, ml, appConfig))
	app.Post("/v1/assistants/:assistant_id", openai.ModifyAssistantEndpoint(cl, ml, appConfig))
	app.Post("/assistants/:assistant_id", openai.ModifyAssistantEndpoint(cl, ml, appConfig))
	app.Get("/v1/assistants/:assistant_id/files", openai.ListAssistantFilesEndpoint(cl, ml, appConfig))
	app.Get("/assistants/:assistant_id/files", openai.ListAssistantFilesEndpoint(cl, ml, appConfig))
	app.Post("/v1/assistants/:assistant_id/files", openai.CreateAssistantFileEndpoint(cl, ml, appConfig))
	app.Post("/assistants/:assistant_id/files", openai.CreateAssistantFileEndpoint(cl, ml, appConfig))
	app.Delete("/v1/assistants/:assistant_id/files/:file_id", openai.DeleteAssistantFileEndpoint(cl, ml, appConfig))
	app.Delete("/assistants/:assistant_id/files/:file_id", openai.DeleteAssistantFileEndpoint(cl, ml, appConfig))
	app.Get("/v1/assistants/:assistant_id/files/:file_id", openai.GetAssistantFileEndpoint(cl, ml, appConfig))
	app.Get("/assistants/:assistant_id/files/:file_id", openai.GetAssistantFileEndpoint(cl, ml, appConfig))

	// files
	app.Post("/v1/files", openai.UploadFilesEndpoint(cl, appConfig))
	app.Post("/files", openai.UploadFilesEndpoint(cl, appConfig))
	app.Get("/v1/files", openai.ListFilesEndpoint(cl, appConfig))
	app.Get("/files", openai.ListFilesEndpoint(cl, appConfig))
	app.Get("/v1/files/:file_id", openai.GetFilesEndpoint(cl, appConfig))
	app.Get("/files/:file_id", openai.GetFilesEndpoint(cl, appConfig))
	app.Delete("/v1/files/:file_id", openai.DeleteFilesEndpoint(cl, appConfig))
	app.Delete("/files/:file_id", openai.DeleteFilesEndpoint(cl, appConfig))
	app.Get("/v1/files/:file_id/content", openai.GetFilesContentsEndpoint(cl, appConfig))
	app.Get("/files/:file_id/content", openai.GetFilesContentsEndpoint(cl, appConfig))

	// completion
	app.Post("/v1/completions", openai.CompletionEndpoint(cl, ml, appConfig))
	app.Post("/completions", openai.CompletionEndpoint(cl, ml, appConfig))
	app.Post("/v1/engines/:model/completions", openai.CompletionEndpoint(cl, ml, appConfig))

	// embeddings
	app.Post("/v1/embeddings", openai.EmbeddingsEndpoint(cl, ml, appConfig))
	app.Post("/embeddings", openai.EmbeddingsEndpoint(cl, ml, appConfig))
	app.Post("/v1/engines/:model/embeddings", openai.EmbeddingsEndpoint(cl, ml, appConfig))

	// audio
	app.Post("/v1/audio/transcriptions", openai.TranscriptEndpoint(cl, ml, appConfig))
	app.Post("/v1/audio/speech", localai.TTSEndpoint(cl, ml, appConfig))

	// images
	app.Post("/v1/images/generations", openai.ImageEndpoint(cl, ml, appConfig))

	if appConfig.ImageDir != "" {
		app.Static("/generated-images", appConfig.ImageDir)
	}

	if appConfig.AudioDir != "" {
		app.Static("/generated-audio", appConfig.AudioDir)
	}

	// List models
	app.Get("/v1/models", openai.ListModelsEndpoint(cl, ml))
	app.Get("/models", openai.ListModelsEndpoint(cl, ml))
}
