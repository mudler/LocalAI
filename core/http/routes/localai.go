package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/swagger"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/model"
)

func RegisterLocalAIRoutes(router *fiber.App,
	requestExtractor *middleware.RequestExtractor,
	cl *config.BackendConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	galleryService *services.GalleryService) {

	router.Get("/swagger/*", swagger.HandlerDefault) // default

	// LocalAI API endpoints
	if !appConfig.DisableGalleryEndpoint {
		modelGalleryEndpointService := localai.CreateModelGalleryEndpointService(appConfig.Galleries, appConfig.BackendGalleries, appConfig.ModelPath, galleryService)
		router.Post("/models/apply", modelGalleryEndpointService.ApplyModelGalleryEndpoint())
		router.Post("/models/delete/:name", modelGalleryEndpointService.DeleteModelGalleryEndpoint())

		router.Get("/models/available", modelGalleryEndpointService.ListModelFromGalleryEndpoint())
		router.Get("/models/galleries", modelGalleryEndpointService.ListModelGalleriesEndpoint())
		router.Get("/models/jobs/:uuid", modelGalleryEndpointService.GetOpStatusEndpoint())
		router.Get("/models/jobs", modelGalleryEndpointService.GetAllStatusEndpoint())

		backendGalleryEndpointService := localai.CreateBackendEndpointService(appConfig.BackendGalleries, appConfig.BackendsPath, galleryService)
		router.Post("/backends/apply", backendGalleryEndpointService.ApplyBackendEndpoint())
		router.Post("/backends/delete/:name", backendGalleryEndpointService.DeleteBackendEndpoint())
		router.Get("/backends", backendGalleryEndpointService.ListBackendsEndpoint())
		router.Get("/backends/available", backendGalleryEndpointService.ListAvailableBackendsEndpoint())
		router.Get("/backends/galleries", backendGalleryEndpointService.ListBackendGalleriesEndpoint())
		router.Get("/backends/jobs/:uuid", backendGalleryEndpointService.GetOpStatusEndpoint())
	}

	router.Post("/v1/detection",
		requestExtractor.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_DETECTION)),
		requestExtractor.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.DetectionRequest) }),
		localai.DetectionEndpoint(cl, ml, appConfig))

	router.Post("/tts",
		requestExtractor.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_TTS)),
		requestExtractor.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.TTSRequest) }),
		localai.TTSEndpoint(cl, ml, appConfig))

	vadChain := []fiber.Handler{
		requestExtractor.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_VAD)),
		requestExtractor.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.VADRequest) }),
		localai.VADEndpoint(cl, ml, appConfig),
	}
	router.Post("/vad", vadChain...)
	router.Post("/v1/vad", vadChain...)

	// Stores
	router.Post("/stores/set", localai.StoresSetEndpoint(ml, appConfig))
	router.Post("/stores/delete", localai.StoresDeleteEndpoint(ml, appConfig))
	router.Post("/stores/get", localai.StoresGetEndpoint(ml, appConfig))
	router.Post("/stores/find", localai.StoresFindEndpoint(ml, appConfig))

	if !appConfig.DisableMetrics {
		router.Get("/metrics", localai.LocalAIMetricsEndpoint())
	}

	router.Post("/video",
		requestExtractor.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_VIDEO)),
		requestExtractor.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.VideoRequest) }),
		localai.VideoEndpoint(cl, ml, appConfig))

	// Backend Statistics Module
	// TODO: Should these use standard middlewares? Refactor later, they are extremely simple.
	backendMonitorService := services.NewBackendMonitorService(ml, cl, appConfig) // Split out for now
	router.Get("/backend/monitor", localai.BackendMonitorEndpoint(backendMonitorService))
	router.Post("/backend/shutdown", localai.BackendShutdownEndpoint(backendMonitorService))
	// The v1/* urls are exactly the same as above - makes local e2e testing easier if they are registered.
	router.Get("/v1/backend/monitor", localai.BackendMonitorEndpoint(backendMonitorService))
	router.Post("/v1/backend/shutdown", localai.BackendShutdownEndpoint(backendMonitorService))

	// p2p
	router.Get("/api/p2p", localai.ShowP2PNodes(appConfig))
	router.Get("/api/p2p/token", localai.ShowP2PToken(appConfig))

	router.Get("/version", func(c *fiber.Ctx) error {
		return c.JSON(struct {
			Version string `json:"version"`
		}{Version: internal.PrintableVersion()})
	})

	router.Get("/system", localai.SystemInformations(ml, appConfig))

	// misc
	router.Post("/v1/tokenize",
		requestExtractor.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_TOKENIZE)),
		requestExtractor.SetModelAndConfig(func() schema.LocalAIRequest { return new(schema.TokenizeRequest) }),
		localai.TokenizeEndpoint(cl, ml, appConfig))

}
