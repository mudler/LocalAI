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
	galleryService *services.GalleryService,
	auth func(*fiber.Ctx) error) {

	app.Get("/swagger/*", swagger.HandlerDefault) // default

	// LocalAI API endpoints

	modelGalleryEndpointService := localai.CreateModelGalleryEndpointService(appConfig.Galleries, appConfig.ModelPath, galleryService)
	app.Post("/models/apply", auth, modelGalleryEndpointService.ApplyModelGalleryEndpoint())
	app.Post("/models/delete/:name", auth, modelGalleryEndpointService.DeleteModelGalleryEndpoint())

	app.Get("/models/available", auth, modelGalleryEndpointService.ListModelFromGalleryEndpoint())
	app.Get("/models/galleries", auth, modelGalleryEndpointService.ListModelGalleriesEndpoint())
	app.Post("/models/galleries", auth, modelGalleryEndpointService.AddModelGalleryEndpoint())
	app.Delete("/models/galleries", auth, modelGalleryEndpointService.RemoveModelGalleryEndpoint())
	app.Get("/models/jobs/:uuid", auth, modelGalleryEndpointService.GetOpStatusEndpoint())
	app.Get("/models/jobs", auth, modelGalleryEndpointService.GetAllStatusEndpoint())

	app.Post("/tts", auth, localai.TTSEndpoint(cl, ml, appConfig))

	// Stores
	sl := model.NewModelLoader("")
	app.Post("/stores/set", auth, localai.StoresSetEndpoint(sl, appConfig))
	app.Post("/stores/delete", auth, localai.StoresDeleteEndpoint(sl, appConfig))
	app.Post("/stores/get", auth, localai.StoresGetEndpoint(sl, appConfig))
	app.Post("/stores/find", auth, localai.StoresFindEndpoint(sl, appConfig))

	// Kubernetes health checks
	ok := func(c *fiber.Ctx) error {
		return c.SendStatus(200)
	}

	app.Get("/healthz", ok)
	app.Get("/readyz", ok)

	app.Get("/metrics", auth, localai.LocalAIMetricsEndpoint())

	// Experimental Backend Statistics Module
	backendMonitorService := services.NewBackendMonitorService(ml, cl, appConfig) // Split out for now
	app.Get("/backend/monitor", auth, localai.BackendMonitorEndpoint(backendMonitorService))
	app.Post("/backend/shutdown", auth, localai.BackendShutdownEndpoint(backendMonitorService))

	// p2p
	if p2p.IsP2PEnabled() {
		app.Get("/api/p2p", auth, localai.ShowP2PNodes)
		app.Get("/api/p2p/token", auth, localai.ShowP2PToken(appConfig))
	}

	app.Get("/version", auth, func(c *fiber.Ctx) error {
		return c.JSON(struct {
			Version string `json:"version"`
		}{Version: internal.PrintableVersion()})
	})

}
