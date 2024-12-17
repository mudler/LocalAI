package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/endpoints/openai"
)

func RegisterOpenAIRoutes(app *fiber.App,
	application *application.Application) {
	// openAI compatible API endpoint

	// chat
	app.Post("/v1/chat/completions",
		openai.ChatEndpoint(
			application.BackendLoader(),
			application.ModelLoader(),
			application.TemplatesEvaluator(),
			application.ApplicationConfig(),
		),
	)

	app.Post("/chat/completions",
		openai.ChatEndpoint(
			application.BackendLoader(),
			application.ModelLoader(),
			application.TemplatesEvaluator(),
			application.ApplicationConfig(),
		),
	)

	// edit
	app.Post("/v1/edits",
		openai.EditEndpoint(
			application.BackendLoader(),
			application.ModelLoader(),
			application.TemplatesEvaluator(),
			application.ApplicationConfig(),
		),
	)

	app.Post("/edits",
		openai.EditEndpoint(
			application.BackendLoader(),
			application.ModelLoader(),
			application.TemplatesEvaluator(),
			application.ApplicationConfig(),
		),
	)

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

	// completion
	app.Post("/v1/completions",
		openai.CompletionEndpoint(
			application.BackendLoader(),
			application.ModelLoader(),
			application.TemplatesEvaluator(),
			application.ApplicationConfig(),
		),
	)

	app.Post("/completions",
		openai.CompletionEndpoint(
			application.BackendLoader(),
			application.ModelLoader(),
			application.TemplatesEvaluator(),
			application.ApplicationConfig(),
		),
	)

	app.Post("/v1/engines/:model/completions",
		openai.CompletionEndpoint(
			application.BackendLoader(),
			application.ModelLoader(),
			application.TemplatesEvaluator(),
			application.ApplicationConfig(),
		),
	)

	// embeddings
	app.Post("/v1/embeddings", openai.EmbeddingsEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))
	app.Post("/embeddings", openai.EmbeddingsEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))
	app.Post("/v1/engines/:model/embeddings", openai.EmbeddingsEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))

	// audio
	app.Post("/v1/audio/transcriptions", openai.TranscriptEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))
	app.Post("/v1/audio/speech", localai.TTSEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))

	// images
	app.Post("/v1/images/generations", openai.ImageEndpoint(application.BackendLoader(), application.ModelLoader(), application.ApplicationConfig()))

	if application.ApplicationConfig().ImageDir != "" {
		app.Static("/generated-images", application.ApplicationConfig().ImageDir)
	}

	if application.ApplicationConfig().AudioDir != "" {
		app.Static("/generated-audio", application.ApplicationConfig().AudioDir)
	}

	// List models
	app.Get("/v1/models", openai.ListModelsEndpoint(application.BackendLoader(), application.ModelLoader()))
	app.Get("/models", openai.ListModelsEndpoint(application.BackendLoader(), application.ModelLoader()))
}
