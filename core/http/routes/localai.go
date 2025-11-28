package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services"
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
	galleryService *services.GalleryService,
	opcache *services.OpCache,
	evaluator *templates.Evaluator,
	app *application.Application) {

	router.GET("/swagger/*", echoswagger.WrapHandler) // default

	// LocalAI API endpoints
	if !appConfig.DisableGalleryEndpoint {
		// Import model page
		router.GET("/import-model", func(c echo.Context) error {
			return c.Render(200, "views/model-editor", map[string]interface{}{
				"Title":   "LocalAI - Import Model",
				"BaseURL": middleware.BaseURL(c),
				"Version": internal.PrintableVersion(),
			})
		})

		// Edit model page
		router.GET("/models/edit/:name", localai.GetEditModelPage(cl, appConfig))
		modelGalleryEndpointService := localai.CreateModelGalleryEndpointService(appConfig.Galleries, appConfig.BackendGalleries, appConfig.SystemState, galleryService)
		router.POST("/models/apply", modelGalleryEndpointService.ApplyModelGalleryEndpoint())
		router.POST("/models/delete/:name", modelGalleryEndpointService.DeleteModelGalleryEndpoint())

		router.GET("/models/available", modelGalleryEndpointService.ListModelFromGalleryEndpoint(appConfig.SystemState))
		router.GET("/models/galleries", modelGalleryEndpointService.ListModelGalleriesEndpoint())
		router.GET("/models/jobs/:uuid", modelGalleryEndpointService.GetOpStatusEndpoint())
		router.GET("/models/jobs", modelGalleryEndpointService.GetAllStatusEndpoint())

		backendGalleryEndpointService := localai.CreateBackendEndpointService(
			appConfig.BackendGalleries,
			appConfig.SystemState,
			galleryService)
		router.POST("/backends/apply", backendGalleryEndpointService.ApplyBackendEndpoint())
		router.POST("/backends/delete/:name", backendGalleryEndpointService.DeleteBackendEndpoint())
		router.GET("/backends", backendGalleryEndpointService.ListBackendsEndpoint(appConfig.SystemState))
		router.GET("/backends/available", backendGalleryEndpointService.ListAvailableBackendsEndpoint(appConfig.SystemState))
		router.GET("/backends/galleries", backendGalleryEndpointService.ListBackendGalleriesEndpoint())
		router.GET("/backends/jobs/:uuid", backendGalleryEndpointService.GetOpStatusEndpoint())
		// Custom model import endpoint
		router.POST("/models/import", localai.ImportModelEndpoint(cl, appConfig))

		// URI model import endpoint
		router.POST("/models/import-uri", localai.ImportModelURIEndpoint(cl, appConfig, galleryService, opcache))

		// Custom model edit endpoint
		router.POST("/models/edit/:name", localai.EditModelEndpoint(cl, appConfig))

		// Reload models endpoint
		router.POST("/models/reload", localai.ReloadModelsEndpoint(cl, appConfig))
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
		router.GET("/metrics", localai.LocalAIMetricsEndpoint())
	}

	videoHandler := localai.VideoEndpoint(cl, ml, appConfig)
	router.POST("/video",
		videoHandler,
		requestExtractor.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_VIDEO)),
		requestExtractor.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.VideoRequest) }))

	// Backend Statistics Module
	// TODO: Should these use standard middlewares? Refactor later, they are extremely simple.
	backendMonitorService := services.NewBackendMonitorService(ml, cl, appConfig) // Split out for now
	router.GET("/backend/monitor", localai.BackendMonitorEndpoint(backendMonitorService))
	router.POST("/backend/shutdown", localai.BackendShutdownEndpoint(backendMonitorService))
	// The v1/* urls are exactly the same as above - makes local e2e testing easier if they are registered.
	router.GET("/v1/backend/monitor", localai.BackendMonitorEndpoint(backendMonitorService))
	router.POST("/v1/backend/shutdown", localai.BackendShutdownEndpoint(backendMonitorService))

	// p2p
	router.GET("/api/p2p", localai.ShowP2PNodes(appConfig))
	router.GET("/api/p2p/token", localai.ShowP2PToken(appConfig))

	router.GET("/version", func(c echo.Context) error {
		return c.JSON(200, struct {
			Version string `json:"version"`
		}{Version: internal.PrintableVersion()})
	})

	router.GET("/system", localai.SystemInformations(ml, appConfig))

	// misc
	tokenizeHandler := localai.TokenizeEndpoint(cl, ml, appConfig)
	router.POST("/v1/tokenize",
		tokenizeHandler,
		requestExtractor.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_TOKENIZE)),
		requestExtractor.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.TokenizeRequest) }))

	// MCP Stream endpoint
	if evaluator != nil {
		mcpStreamHandler := localai.MCPStreamEndpoint(cl, ml, evaluator, appConfig)
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
	}

	// Agent job routes
	if app != nil && app.AgentJobService() != nil {
		router.POST("/api/agent/tasks", localai.CreateTaskEndpoint(app))
		router.PUT("/api/agent/tasks/:id", localai.UpdateTaskEndpoint(app))
		router.DELETE("/api/agent/tasks/:id", localai.DeleteTaskEndpoint(app))
		router.GET("/api/agent/tasks", localai.ListTasksEndpoint(app))
		router.GET("/api/agent/tasks/:id", localai.GetTaskEndpoint(app))

		router.POST("/api/agent/jobs/execute", localai.ExecuteJobEndpoint(app))
		router.GET("/api/agent/jobs/:id", localai.GetJobEndpoint(app))
		router.GET("/api/agent/jobs", localai.ListJobsEndpoint(app))
		router.POST("/api/agent/jobs/:id/cancel", localai.CancelJobEndpoint(app))
		router.DELETE("/api/agent/jobs/:id", localai.DeleteJobEndpoint(app))

		router.POST("/api/agent/tasks/:name/execute", localai.ExecuteTaskByNameEndpoint(app))
	}

}
