package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/endpoints/openai"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/model"
)

func RegisterOpenAIRoutes(app *fiber.App,
	re *middleware.RequestExtractor,
	cl *config.BackendConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig) {
	// openAI compatible API endpoint

	// chat
	chatChain := []fiber.Handler{
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_CHAT)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		re.SetOpenAIRequest,
		openai.ChatEndpoint(cl, ml, appConfig),
	}
	app.Post("/v1/chat/completions", chatChain...)
	app.Post("/chat/completions", chatChain...)

	// edit
	editChain := []fiber.Handler{
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_EDIT)),
		re.BuildConstantDefaultModelNameMiddleware("gpt-4o"),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		re.SetOpenAIRequest,
		openai.EditEndpoint(cl, ml, appConfig),
	}
	app.Post("/v1/edits", editChain...)
	app.Post("/edits", editChain...)

	// completion
	completionChain := []fiber.Handler{
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_COMPLETION)),
		re.BuildConstantDefaultModelNameMiddleware("gpt-4o"),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		re.SetOpenAIRequest,
		openai.CompletionEndpoint(cl, ml, appConfig),
	}
	app.Post("/v1/completions", completionChain...)
	app.Post("/completions", completionChain...)
	app.Post("/v1/engines/:model/completions", completionChain...)

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

	// embeddings
	embeddingChain := []fiber.Handler{
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_EMBEDDINGS)),
		re.BuildConstantDefaultModelNameMiddleware("gpt-4o"),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		re.SetOpenAIRequest,
		openai.EmbeddingsEndpoint(cl, ml, appConfig),
	}
	app.Post("/v1/embeddings", embeddingChain...)
	app.Post("/embeddings", embeddingChain...)
	app.Post("/v1/engines/:model/embeddings", embeddingChain...)

	// audio
	app.Post("/v1/audio/transcriptions",
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_TRANSCRIPT)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		re.SetOpenAIRequest,
		openai.TranscriptEndpoint(cl, ml, appConfig),
	)

	app.Post("/v1/audio/speech",
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_TTS)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.TTSRequest) }),
		localai.TTSEndpoint(cl, ml, appConfig))

	// images
	app.Post("/v1/images/generations",
		re.BuildConstantDefaultModelNameMiddleware(model.StableDiffusionBackend),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		re.SetOpenAIRequest,
		openai.ImageEndpoint(cl, ml, appConfig))

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
