package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/endpoints/openai"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
)

func RegisterOpenAIRoutes(app *echo.Echo,
	re *middleware.RequestExtractor,
	application *application.Application) {
	// openAI compatible API endpoint

	// realtime
	// TODO: Modify/disable the API key middleware for this endpoint to allow ephemeral keys created by sessions
	app.GET("/v1/realtime", openai.Realtime(application))
	app.POST("/v1/realtime/sessions", openai.RealtimeTranscriptionSession(application))
	app.POST("/v1/realtime/transcription_session", openai.RealtimeTranscriptionSession(application))

	// chat
	chatHandler := openai.ChatEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.TemplatesEvaluator(), application.ApplicationConfig())
	chatMiddleware := []echo.MiddlewareFunc{
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_CHAT)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				if err := re.SetOpenAIRequest(c); err != nil {
					return err
				}
				return next(c)
			}
		},
	}
	app.POST("/v1/chat/completions", chatHandler, chatMiddleware...)
	app.POST("/chat/completions", chatHandler, chatMiddleware...)

	// edit
	editHandler := openai.EditEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.TemplatesEvaluator(), application.ApplicationConfig())
	editMiddleware := []echo.MiddlewareFunc{
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_EDIT)),
		re.BuildConstantDefaultModelNameMiddleware("gpt-4o"),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				if err := re.SetOpenAIRequest(c); err != nil {
					return err
				}
				return next(c)
			}
		},
	}
	app.POST("/v1/edits", editHandler, editMiddleware...)
	app.POST("/edits", editHandler, editMiddleware...)

	// completion
	completionHandler := openai.CompletionEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.TemplatesEvaluator(), application.ApplicationConfig())
	completionMiddleware := []echo.MiddlewareFunc{
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_COMPLETION)),
		re.BuildConstantDefaultModelNameMiddleware("gpt-4o"),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				if err := re.SetOpenAIRequest(c); err != nil {
					return err
				}
				return next(c)
			}
		},
	}
	app.POST("/v1/completions", completionHandler, completionMiddleware...)
	app.POST("/completions", completionHandler, completionMiddleware...)
	app.POST("/v1/engines/:model/completions", completionHandler, completionMiddleware...)

	// MCPcompletion
	mcpCompletionHandler := openai.MCPCompletionEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.TemplatesEvaluator(), application.ApplicationConfig())
	mcpCompletionMiddleware := []echo.MiddlewareFunc{
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_CHAT)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				if err := re.SetOpenAIRequest(c); err != nil {
					return err
				}
				return next(c)
			}
		},
	}
	app.POST("/mcp/v1/chat/completions", mcpCompletionHandler, mcpCompletionMiddleware...)
	app.POST("/mcp/chat/completions", mcpCompletionHandler, mcpCompletionMiddleware...)

	// embeddings
	embeddingHandler := openai.EmbeddingsEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig())
	embeddingMiddleware := []echo.MiddlewareFunc{
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_EMBEDDINGS)),
		re.BuildConstantDefaultModelNameMiddleware("gpt-4o"),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				if err := re.SetOpenAIRequest(c); err != nil {
					return err
				}
				return next(c)
			}
		},
	}
	app.POST("/v1/embeddings", embeddingHandler, embeddingMiddleware...)
	app.POST("/embeddings", embeddingHandler, embeddingMiddleware...)
	app.POST("/v1/engines/:model/embeddings", embeddingHandler, embeddingMiddleware...)

	audioHandler := openai.TranscriptEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig())
	audioMiddleware := []echo.MiddlewareFunc{
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_TRANSCRIPT)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				if err := re.SetOpenAIRequest(c); err != nil {
					return err
				}
				return next(c)
			}
		},
	}
	// audio
	app.POST("/v1/audio/transcriptions", audioHandler, audioMiddleware...)
	app.POST("/audio/transcriptions", audioHandler, audioMiddleware...)

	audioSpeechHandler := localai.TTSEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig())
	audioSpeechMiddleware := []echo.MiddlewareFunc{
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_TTS)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.TTSRequest) }),
	}

	app.POST("/v1/audio/speech", audioSpeechHandler, audioSpeechMiddleware...)
	app.POST("/audio/speech", audioSpeechHandler, audioSpeechMiddleware...)

	// images
	imageHandler := openai.ImageEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig())
	imageMiddleware := []echo.MiddlewareFunc{
		// Default: use the first available image generation model
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_IMAGE)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				if err := re.SetOpenAIRequest(c); err != nil {
					return err
				}
				return next(c)
			}
		},
	}

	app.POST("/v1/images/generations", imageHandler, imageMiddleware...)
	app.POST("/images/generations", imageHandler, imageMiddleware...)

	// inpainting endpoint (image + mask) - reuse same middleware config as images
	inpaintingHandler := openai.InpaintingEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig())
	app.POST("/v1/images/inpainting", inpaintingHandler, imageMiddleware...)
	app.POST("/images/inpainting", inpaintingHandler, imageMiddleware...)

	// videos (OpenAI-compatible endpoints mapped to LocalAI video handler)
	videoHandler := openai.VideoEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig())
	videoMiddleware := []echo.MiddlewareFunc{
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_VIDEO)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
		func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				if err := re.SetOpenAIRequest(c); err != nil {
					return err
				}
				return next(c)
			}
		},
	}

	// OpenAI-style create video endpoint
	app.POST("/v1/videos", videoHandler, videoMiddleware...)
	app.POST("/v1/videos/generations", videoHandler, videoMiddleware...)
	app.POST("/videos", videoHandler, videoMiddleware...)

	// List models
	app.GET("/v1/models", openai.ListModelsEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig()))
	app.GET("/models", openai.ListModelsEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig()))
}
