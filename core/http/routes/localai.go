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

func RegisterLocalAIRoutes(app *fiber.App,
	cl *config.BackendConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	galleryService *services.GalleryService) {

	app.Get("/swagger/*", swagger.HandlerDefault) // default

	// LocalAI API endpoints
	if !appConfig.DisableGalleryEndpoint {
		modelGalleryEndpointService := localai.CreateModelGalleryEndpointService(appConfig.Galleries, appConfig.ModelPath, galleryService)
		app.Post("/models/apply", modelGalleryEndpointService.ApplyModelGalleryEndpoint())
		app.Post("/models/delete/:name", modelGalleryEndpointService.DeleteModelGalleryEndpoint())

		app.Get("/models/available", modelGalleryEndpointService.ListModelFromGalleryEndpoint())
		app.Get("/models/galleries", modelGalleryEndpointService.ListModelGalleriesEndpoint())
		app.Post("/models/galleries", modelGalleryEndpointService.AddModelGalleryEndpoint())
		app.Delete("/models/galleries", modelGalleryEndpointService.RemoveModelGalleryEndpoint())
		app.Get("/models/jobs/:uuid", modelGalleryEndpointService.GetOpStatusEndpoint())
		app.Get("/models/jobs", modelGalleryEndpointService.GetAllStatusEndpoint())
	}

	app.Post("/tts", localai.TTSEndpoint(cl, ml, appConfig))

	// Stores
	sl := model.NewModelLoader("")
	app.Post("/stores/set", localai.StoresSetEndpoint(sl, appConfig))
	app.Post("/stores/delete", localai.StoresDeleteEndpoint(sl, appConfig))
	app.Post("/stores/get", localai.StoresGetEndpoint(sl, appConfig))
	app.Post("/stores/find", localai.StoresFindEndpoint(sl, appConfig))

	// Kubernetes health checks
	ok := func(c *fiber.Ctx) error {
		return c.SendStatus(200)
	}

	app.Get("/healthz", ok)
	app.Get("/readyz", ok)

	app.Get("/metrics", localai.LocalAIMetricsEndpoint())

	// Experimental Backend Statistics Module
	backendMonitorService := services.NewBackendMonitorService(ml, cl, appConfig) // Split out for now
	app.Get("/backend/monitor", localai.BackendMonitorEndpoint(backendMonitorService))
	app.Post("/backend/shutdown", localai.BackendShutdownEndpoint(backendMonitorService))

	// p2p
	if p2p.IsP2PEnabled() {
		app.Get("/api/p2p", localai.ShowP2PNodes(appConfig))
		app.Get("/api/p2p/token", localai.ShowP2PToken(appConfig))
	}

	app.Get("/version", func(c *fiber.Ctx) error {
		return c.JSON(struct {
			Version string `json:"version"`
		}{Version: internal.PrintableVersion()})
	})

}
