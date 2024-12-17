package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/swagger"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/p2p"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/model"
)

func RegisterLocalAIRoutes(router *fiber.App,
	cl *config.BackendConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	galleryService *services.GalleryService) {

	router.Get("/swagger/*", swagger.HandlerDefault) // default

	// LocalAI API endpoints
	if !appConfig.DisableGalleryEndpoint {
		modelGalleryEndpointService := localai.CreateModelGalleryEndpointService(appConfig.Galleries, appConfig.ModelPath, galleryService)
		router.Post("/models/apply", modelGalleryEndpointService.ApplyModelGalleryEndpoint())
		router.Post("/models/delete/:name", modelGalleryEndpointService.DeleteModelGalleryEndpoint())

		router.Get("/models/available", modelGalleryEndpointService.ListModelFromGalleryEndpoint())
		router.Get("/models/galleries", modelGalleryEndpointService.ListModelGalleriesEndpoint())
		router.Post("/models/galleries", modelGalleryEndpointService.AddModelGalleryEndpoint())
		router.Delete("/models/galleries", modelGalleryEndpointService.RemoveModelGalleryEndpoint())
		router.Get("/models/jobs/:uuid", modelGalleryEndpointService.GetOpStatusEndpoint())
		router.Get("/models/jobs", modelGalleryEndpointService.GetAllStatusEndpoint())
	}

	router.Post("/tts", localai.TTSEndpoint(cl, ml, appConfig))
	router.Post("/vad", localai.VADEndpoint(cl, ml, appConfig))

	// Stores
	sl := model.NewModelLoader("")
	router.Post("/stores/set", localai.StoresSetEndpoint(sl, appConfig))
	router.Post("/stores/delete", localai.StoresDeleteEndpoint(sl, appConfig))
	router.Post("/stores/get", localai.StoresGetEndpoint(sl, appConfig))
	router.Post("/stores/find", localai.StoresFindEndpoint(sl, appConfig))

	if !appConfig.DisableMetrics {
		router.Get("/metrics", localai.LocalAIMetricsEndpoint())
	}

	// Experimental Backend Statistics Module
	backendMonitorService := services.NewBackendMonitorService(ml, cl, appConfig) // Split out for now
	router.Get("/backend/monitor", localai.BackendMonitorEndpoint(backendMonitorService))
	router.Post("/backend/shutdown", localai.BackendShutdownEndpoint(backendMonitorService))

	// p2p
	if p2p.IsP2PEnabled() {
		router.Get("/api/p2p", localai.ShowP2PNodes(appConfig))
		router.Get("/api/p2p/token", localai.ShowP2PToken(appConfig))
	}

	router.Get("/version", func(c *fiber.Ctx) error {
		return c.JSON(struct {
			Version string `json:"version"`
		}{Version: internal.PrintableVersion()})
	})

	router.Get("/system", localai.SystemInformations(ml, appConfig))

	// misc
	router.Post("/v1/tokenize", localai.TokenizeEndpoint(cl, ml, appConfig))

}
