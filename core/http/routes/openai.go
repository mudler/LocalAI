package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/http/endpoints/openai"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/pii"
	"github.com/mudler/LocalAI/core/services/routing/piiadapter"
	"github.com/mudler/LocalAI/core/services/routing/router"
)

func RegisterOpenAIRoutes(app *echo.Echo,
	re *middleware.RequestExtractor,
	application *application.Application,
) {
	// openAI compatible API endpoint
	traceMiddleware := middleware.TraceMiddleware(application)
	usageMiddleware := middleware.UsageMiddleware(application.StatsRecorder(), application.FallbackUser())
	// X-LocalAI-Node attribution middleware: wraps the response writer and
	// stamps the header on first write when --expose-node-header is on. No-op
	// otherwise. Applied to every inference path that routes through
	// ml.Load (chat, completion, embeddings, audio transcriptions/speech,
	// image generation/inpainting) so distributed-mode operators can observe
	// which worker served each request.
	nodeHeaderMiddleware := middleware.ExposeNodeHeader(application.ApplicationConfig())

	// realtime
	// TODO: Modify/disable the API key middleware for this endpoint to allow ephemeral keys created by sessions
	app.GET("/v1/realtime", openai.Realtime(application))
	app.POST("/v1/realtime/sessions", openai.RealtimeTranscriptionSession(application), traceMiddleware)
	app.POST("/v1/realtime/transcription_session", openai.RealtimeTranscriptionSession(application), traceMiddleware)
	app.POST("/v1/realtime/calls", openai.RealtimeCalls(application), traceMiddleware)

	// NATS client for distributed MCP tool routing (nil when not in distributed mode)
	var natsClient mcpTools.MCPNATSClient
	if d := application.Distributed(); d != nil {
		natsClient = d.Nats
	}

	// chat
	chatHandler := openai.ChatEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.TemplatesEvaluator(), application.ApplicationConfig(), natsClient, application.LocalAIAssistant())
	chatMiddleware := []echo.MiddlewareFunc{
		nodeHeaderMiddleware,
		usageMiddleware,
		traceMiddleware,
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
		// RouteModel runs AFTER the schema-specific request parser so
		// the classifier sees a populated *schema.OpenAIRequest. When
		// the resolved model has a Router config, the middleware
		// rewrites input.Model to the chosen candidate, swaps
		// MODEL_CONFIG, and stamps RequestedModel/ServedModel for the
		// usage log. Models without a Router pass through.
		middleware.RouteModel(
			application.ModelConfigLoader(),
			application.ApplicationConfig(),
			application.RouterDecisions(),
			application.FallbackUser(),
			middleware.OpenAIProbe,
			router.SourceChat,
			middleware.ClassifierDeps{
				Scorer:       application.Scorer,
				TokenCounter: application.TokenCounter,
				Embedder:     application.Embedder,
				VectorStore:  application.VectorStore,
				Reranker:     application.Reranker,
				ModelLookup:  application.ModelConfigLookup(),
				Registry:     application.RouterClassifierRegistry(),
				Evaluator:    application.TemplatesEvaluator(),
			},
		),
		// Admission control runs after RouteModel so the SERVED
		// model's limits apply — a router fanout that lands on a
		// saturated downstream gets rejected even when the requested
		// router-model has slack.
		middleware.AdmissionControl(application.AdmissionLimiter(), application.PIIEvents()),
		// PII redaction runs INNERMOST, after RouteModel has resolved
		// the actual served model. This is what makes per-model PII
		// configs honour the routed target (e.g., a router fans out to
		// claude-strict; that model's pii block applies, not the
		// router model's).
		pii.RequestMiddleware(application.PIIRedactor(), application.PIIEvents(), piiadapter.OpenAI(), application.FallbackUser(), pii.WithNERResolver(application.PIINERResolver()), pii.WithPolicyResolver(application.PIIPolicyResolver())),
	}
	app.POST("/v1/chat/completions", chatHandler, chatMiddleware...)
	app.POST("/chat/completions", chatHandler, chatMiddleware...)

	// edit
	editHandler := openai.EditEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.TemplatesEvaluator(), application.ApplicationConfig())
	editMiddleware := []echo.MiddlewareFunc{
		usageMiddleware,
		traceMiddleware,
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
		pii.RequestMiddleware(application.PIIRedactor(), application.PIIEvents(), piiadapter.OpenAICompletion(), application.FallbackUser(), pii.WithNERResolver(application.PIINERResolver()), pii.WithPolicyResolver(application.PIIPolicyResolver())),
	}
	app.POST("/v1/edits", editHandler, editMiddleware...)
	app.POST("/edits", editHandler, editMiddleware...)

	// completion
	completionHandler := openai.CompletionEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.TemplatesEvaluator(), application.ApplicationConfig())
	completionMiddleware := []echo.MiddlewareFunc{
		nodeHeaderMiddleware,
		usageMiddleware,
		traceMiddleware,
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
		pii.RequestMiddleware(application.PIIRedactor(), application.PIIEvents(), piiadapter.OpenAICompletion(), application.FallbackUser(), pii.WithNERResolver(application.PIINERResolver()), pii.WithPolicyResolver(application.PIIPolicyResolver())),
	}
	app.POST("/v1/completions", completionHandler, completionMiddleware...)
	app.POST("/completions", completionHandler, completionMiddleware...)
	app.POST("/v1/engines/:model/completions", completionHandler, completionMiddleware...)

	// embeddings
	embeddingHandler := openai.EmbeddingsEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig())
	embeddingMiddleware := []echo.MiddlewareFunc{
		nodeHeaderMiddleware,
		usageMiddleware,
		traceMiddleware,
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
		pii.RequestMiddleware(application.PIIRedactor(), application.PIIEvents(), piiadapter.OpenAICompletion(), application.FallbackUser(), pii.WithNERResolver(application.PIINERResolver()), pii.WithPolicyResolver(application.PIIPolicyResolver())),
	}
	app.POST("/v1/embeddings", embeddingHandler, embeddingMiddleware...)
	app.POST("/embeddings", embeddingHandler, embeddingMiddleware...)
	app.POST("/v1/engines/:model/embeddings", embeddingHandler, embeddingMiddleware...)

	audioHandler := openai.TranscriptEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig())
	audioMiddleware := []echo.MiddlewareFunc{
		nodeHeaderMiddleware,
		traceMiddleware,
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

	diarizationHandler := openai.DiarizationEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig())
	diarizationMiddleware := []echo.MiddlewareFunc{
		traceMiddleware,
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_DIARIZATION)),
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
	app.POST("/v1/audio/diarization", diarizationHandler, diarizationMiddleware...)
	app.POST("/audio/diarization", diarizationHandler, diarizationMiddleware...)

	soundClassificationHandler := openai.SoundClassificationEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig())
	soundClassificationMiddleware := []echo.MiddlewareFunc{
		traceMiddleware,
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_SOUND_CLASSIFICATION)),
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
	app.POST("/v1/audio/classification", soundClassificationHandler, soundClassificationMiddleware...)
	app.POST("/audio/classification", soundClassificationHandler, soundClassificationMiddleware...)

	audioSpeechHandler := localai.TTSEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig())
	audioSpeechMiddleware := []echo.MiddlewareFunc{
		nodeHeaderMiddleware,
		traceMiddleware,
		re.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_TTS)),
		re.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.TTSRequest) }),
	}

	app.POST("/v1/audio/speech", audioSpeechHandler, audioSpeechMiddleware...)
	app.POST("/audio/speech", audioSpeechHandler, audioSpeechMiddleware...)

	// images
	imageHandler := openai.ImageEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig())
	imageMiddleware := []echo.MiddlewareFunc{
		nodeHeaderMiddleware,
		traceMiddleware,
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

	// upscale endpoint - reuse same middleware config as images
	upscaleHandler := openai.UpscaleEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig())
	app.POST("/v1/images/upscale", upscaleHandler, imageMiddleware...)
	app.POST("/images/upscale", upscaleHandler, imageMiddleware...)

	// List models
	app.GET("/v1/models", openai.ListModelsEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig(), application.AuthDB()))
	app.GET("/models", openai.ListModelsEndpoint(application.ModelConfigLoader(), application.ModelLoader(), application.ApplicationConfig(), application.AuthDB()))
}
