package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/monitoring"
	"github.com/mudler/LocalAI/core/templates"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/model"
	echoswagger "github.com/swaggo/echo-swagger"
)

func RegisterLocalAIRoutes(router *echo.Echo,
	requestExtractor *middleware.RequestExtractor,
	cl *config.ModelConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	galleryService *galleryop.GalleryService,
	opcache *galleryop.OpCache,
	evaluator *templates.Evaluator,
	app *application.Application,
	adminMiddleware echo.MiddlewareFunc,
	mcpJobsMw echo.MiddlewareFunc,
	mcpMw echo.MiddlewareFunc) {

	router.GET("/swagger/*", echoswagger.WrapHandler) // default

	// LocalAI API endpoints
	if !appConfig.DisableGalleryEndpoint {
		// Import model page
		router.GET("/import-model", func(c echo.Context) error {
			return c.Render(200, "views/model-editor", map[string]interface{}{
				"Title":                  "LocalAI - Import Model",
				"BaseURL":                middleware.BaseURL(c),
				"Version":                internal.PrintableVersion(),
				"DisableRuntimeSettings": appConfig.DisableRuntimeSettings,
			})
		}, adminMiddleware)

		// Edit model page
		router.GET("/models/edit/:name", localai.GetEditModelPage(cl, appConfig), adminMiddleware)
		modelGalleryEndpointService := localai.CreateModelGalleryEndpointService(appConfig.Galleries, appConfig.BackendGalleries, appConfig.SystemState, galleryService, cl)
		router.POST("/models/apply", modelGalleryEndpointService.ApplyModelGalleryEndpoint(), adminMiddleware)
		router.POST("/models/delete/:name", modelGalleryEndpointService.DeleteModelGalleryEndpoint(), adminMiddleware)

		router.GET("/models/available", modelGalleryEndpointService.ListModelFromGalleryEndpoint(appConfig.SystemState), adminMiddleware)
		router.GET("/models/galleries", modelGalleryEndpointService.ListModelGalleriesEndpoint(), adminMiddleware)
		router.GET("/models/jobs/:uuid", modelGalleryEndpointService.GetOpStatusEndpoint(), adminMiddleware)
		router.GET("/models/jobs", modelGalleryEndpointService.GetAllStatusEndpoint(), adminMiddleware)

		backendGalleryEndpointService := localai.CreateBackendEndpointService(
			appConfig.BackendGalleries,
			appConfig.SystemState,
			galleryService)
		router.POST("/backends/apply", backendGalleryEndpointService.ApplyBackendEndpoint(), adminMiddleware)
		router.POST("/backends/delete/:name", backendGalleryEndpointService.DeleteBackendEndpoint(), adminMiddleware)
		router.GET("/backends", backendGalleryEndpointService.ListBackendsEndpoint(), adminMiddleware)
		router.GET("/backends/available", backendGalleryEndpointService.ListAvailableBackendsEndpoint(appConfig.SystemState), adminMiddleware)
		router.GET("/backends/galleries", backendGalleryEndpointService.ListBackendGalleriesEndpoint(), adminMiddleware)
		router.GET("/backends/jobs/:uuid", backendGalleryEndpointService.GetOpStatusEndpoint(), adminMiddleware)
		// Custom model import endpoint
		router.POST("/models/import", localai.ImportModelEndpoint(cl, appConfig), adminMiddleware)

		// URI model import endpoint
		router.POST("/models/import-uri", localai.ImportModelURIEndpoint(cl, appConfig, galleryService, opcache), adminMiddleware)

		// Custom model edit endpoint
		router.POST("/models/edit/:name", localai.EditModelEndpoint(cl, ml, appConfig), adminMiddleware)

		// Reload models endpoint
		router.POST("/models/reload", localai.ReloadModelsEndpoint(cl, appConfig), adminMiddleware)
	}

	detectionHandler := localai.DetectionEndpoint(cl, ml, appConfig)
	router.POST("/v1/detection",
		detectionHandler,
		requestExtractor.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_DETECTION)),
		requestExtractor.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.DetectionRequest) }))

	ttsHandler := localai.TTSEndpoint(cl, ml, appConfig)
	router.POST("/tts",
		ttsHandler,
		requestExtractor.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_TTS)),
		requestExtractor.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.TTSRequest) }))

	vadHandler := localai.VADEndpoint(cl, ml, appConfig)
	router.POST("/vad",
		vadHandler,
		requestExtractor.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_VAD)),
		requestExtractor.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.VADRequest) }))
	router.POST("/v1/vad",
		vadHandler,
		requestExtractor.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_VAD)),
		requestExtractor.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.VADRequest) }))

	// Stores
	router.POST("/stores/set", localai.StoresSetEndpoint(ml, appConfig))
	router.POST("/stores/delete", localai.StoresDeleteEndpoint(ml, appConfig))
	router.POST("/stores/get", localai.StoresGetEndpoint(ml, appConfig))
	router.POST("/stores/find", localai.StoresFindEndpoint(ml, appConfig))

	if !appConfig.DisableMetrics {
		router.GET("/metrics", localai.LocalAIMetricsEndpoint(), adminMiddleware)
	}

	videoHandler := localai.VideoEndpoint(cl, ml, appConfig)
	router.POST("/video",
		videoHandler,
		requestExtractor.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_VIDEO)),
		requestExtractor.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.VideoRequest) }))

	// Backend Statistics Module
	// TODO: Should these use standard middlewares? Refactor later, they are extremely simple.
	backendMonitorService := monitoring.NewBackendMonitorService(ml, cl, appConfig) // Split out for now
	router.GET("/backend/monitor", localai.BackendMonitorEndpoint(backendMonitorService), adminMiddleware)
	router.POST("/backend/shutdown", localai.BackendShutdownEndpoint(backendMonitorService), adminMiddleware)
	// The v1/* urls are exactly the same as above - makes local e2e testing easier if they are registered.
	router.GET("/v1/backend/monitor", localai.BackendMonitorEndpoint(backendMonitorService), adminMiddleware)
	router.POST("/v1/backend/shutdown", localai.BackendShutdownEndpoint(backendMonitorService), adminMiddleware)

	// p2p
	router.GET("/api/p2p", localai.ShowP2PNodes(appConfig), adminMiddleware)
	router.GET("/api/p2p/token", localai.ShowP2PToken(appConfig), adminMiddleware)

	router.GET("/version", func(c echo.Context) error {
		return c.JSON(200, struct {
			Version string `json:"version"`
		}{Version: internal.PrintableVersion()})
	})

	router.GET("/api/features", func(c echo.Context) error {
		return c.JSON(200, map[string]bool{
			"agents":       appConfig.AgentPool.Enabled,
			"mcp":          !appConfig.DisableMCP,
			"fine_tuning":  true,
			"quantization": true,
			"distributed":  appConfig.Distributed.Enabled,
		})
	})

	router.GET("/system", localai.SystemInformations(ml, appConfig), adminMiddleware)

	// misc
	tokenizeHandler := localai.TokenizeEndpoint(cl, ml, appConfig)
	router.POST("/v1/tokenize",
		tokenizeHandler,
		requestExtractor.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_TOKENIZE)),
		requestExtractor.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.TokenizeRequest) }))

	// MCP endpoint - supports both streaming and non-streaming modes
	// Note: streaming mode is NOT compatible with the OpenAI apis. We have a set which streams more states.
	if evaluator != nil && !appConfig.DisableMCP {
		var mcpNATS mcpTools.MCPNATSClient
		if d := app.Distributed(); d != nil {
			mcpNATS = d.Nats
		}
		mcpStreamHandler := localai.MCPEndpoint(cl, ml, evaluator, appConfig, mcpNATS)
		mcpStreamMiddleware := []echo.MiddlewareFunc{
			requestExtractor.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_CHAT)),
			requestExtractor.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.OpenAIRequest) }),
			func(next echo.HandlerFunc) echo.HandlerFunc {
				return func(c echo.Context) error {
					if err := requestExtractor.SetOpenAIRequest(c); err != nil {
						return err
					}
					return next(c)
				}
			},
		}
		router.POST("/v1/mcp/chat/completions", mcpStreamHandler, mcpStreamMiddleware...)
		router.POST("/mcp/v1/chat/completions", mcpStreamHandler, mcpStreamMiddleware...)
		router.POST("/mcp/chat/completions", mcpStreamHandler, mcpStreamMiddleware...)

		// MCP server listing endpoint
		router.GET("/v1/mcp/servers/:model", localai.MCPServersEndpoint(cl, appConfig, mcpNATS), mcpMw)

		// MCP prompts endpoints
		router.GET("/v1/mcp/prompts/:model", localai.MCPPromptsEndpoint(cl, appConfig), mcpMw)
		router.POST("/v1/mcp/prompts/:model/:prompt", localai.MCPGetPromptEndpoint(cl, appConfig), mcpMw)

		// MCP resources endpoints
		router.GET("/v1/mcp/resources/:model", localai.MCPResourcesEndpoint(cl, appConfig), mcpMw)
		router.POST("/v1/mcp/resources/:model/read", localai.MCPReadResourceEndpoint(cl, appConfig), mcpMw)

		// CORS proxy for client-side MCP connections
		router.GET("/api/cors-proxy", localai.CORSProxyEndpoint(appConfig), mcpMw)
		router.POST("/api/cors-proxy", localai.CORSProxyEndpoint(appConfig), mcpMw)
		router.OPTIONS("/api/cors-proxy", localai.CORSProxyOptionsEndpoint())
	}

	// Agent job routes (MCP CI Jobs — requires MCP to be enabled)
	if app != nil && app.AgentJobService() != nil && !appConfig.DisableMCP {
		router.POST("/api/agent/tasks", localai.CreateTaskEndpoint(app), mcpJobsMw)
		router.PUT("/api/agent/tasks/:id", localai.UpdateTaskEndpoint(app), mcpJobsMw)
		router.DELETE("/api/agent/tasks/:id", localai.DeleteTaskEndpoint(app), mcpJobsMw)
		router.GET("/api/agent/tasks", localai.ListTasksEndpoint(app), mcpJobsMw)
		router.GET("/api/agent/tasks/:id", localai.GetTaskEndpoint(app), mcpJobsMw)

		router.POST("/api/agent/jobs/execute", localai.ExecuteJobEndpoint(app), mcpJobsMw)
		router.GET("/api/agent/jobs/:id", localai.GetJobEndpoint(app), mcpJobsMw)
		router.GET("/api/agent/jobs", localai.ListJobsEndpoint(app), mcpJobsMw)
		router.POST("/api/agent/jobs/:id/cancel", localai.CancelJobEndpoint(app), mcpJobsMw)
		router.DELETE("/api/agent/jobs/:id", localai.DeleteJobEndpoint(app), mcpJobsMw)

		router.POST("/api/agent/tasks/:name/execute", localai.ExecuteTaskByNameEndpoint(app), mcpJobsMw)
	}

}
