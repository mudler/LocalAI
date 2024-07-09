package routes

import (
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/swagger"
	"github.com/mudler/LocalAI/core"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/p2p"
	"github.com/mudler/LocalAI/internal"
)

func RegisterLocalAIRoutes(app *fiber.App, requestExtractor *middleware.RequestExtractor, application *core.Application) {

	app.Get("/swagger/*", swagger.HandlerDefault) // default

	// LocalAI API endpoints

	modelGalleryEndpointService := localai.CreateModelGalleryEndpointService(application.ApplicationConfig.Galleries, application.ApplicationConfig.ModelPath, application.GalleryService)
	app.Post("/models/apply", modelGalleryEndpointService.ApplyModelGalleryEndpoint())
	app.Post("/models/delete/:name", modelGalleryEndpointService.DeleteModelGalleryEndpoint())

	app.Get("/models/available", modelGalleryEndpointService.ListModelFromGalleryEndpoint())
	app.Get("/models/galleries", modelGalleryEndpointService.ListModelGalleriesEndpoint())
	app.Post("/models/galleries", modelGalleryEndpointService.AddModelGalleryEndpoint())
	app.Delete("/models/galleries", modelGalleryEndpointService.RemoveModelGalleryEndpoint())
	app.Get("/models/jobs/:uuid", modelGalleryEndpointService.GetOpStatusEndpoint())
	app.Get("/models/jobs", modelGalleryEndpointService.GetAllStatusEndpoint())

	app.Post("/tts", requestExtractor.SetModelName,
		requestExtractor.BuildFilteredFirstAvailableDefaultModel(config.BuildUsecaseFilterFn(config.FLAG_TTS)),
		localai.TTSEndpoint(application.BackendConfigLoader, application.ModelLoader, application.ApplicationConfig),
	)

	// Stores
	app.Post("/stores/set", localai.StoresSetEndpoint(application.StoresLoader, application.ApplicationConfig))
	app.Post("/stores/delete", localai.StoresDeleteEndpoint(application.StoresLoader, application.ApplicationConfig))
	app.Post("/stores/get", localai.StoresGetEndpoint(application.StoresLoader, application.ApplicationConfig))
	app.Post("/stores/find", localai.StoresFindEndpoint(application.StoresLoader, application.ApplicationConfig))

	// Kubernetes health checks
	ok := func(c *fiber.Ctx) error {
		return c.SendStatus(200)
	}

	app.Get("/healthz", ok)
	app.Get("/readyz", ok)

	app.Get("/metrics", localai.LocalAIMetricsEndpoint())

	app.Get("/backend/monitor", localai.BackendMonitorEndpoint(application.BackendMonitorService))
	app.Post("/backend/shutdown", localai.BackendShutdownEndpoint(application.BackendMonitorService))

	// p2p
	if p2p.IsP2PEnabled() {
		app.Get("/api/p2p", func(c *fiber.Ctx) error {
			// Render index
			return c.JSON(map[string]interface{}{
				"Nodes":          p2p.GetAvailableNodes(""),
				"FederatedNodes": p2p.GetAvailableNodes(p2p.FederatedID),
			})
		})
		app.Get("/api/p2p/token", func(c *fiber.Ctx) error {
			return c.Send([]byte(application.ApplicationConfig.P2PToken))
		})
	}

	app.Get("/version", func(c *fiber.Ctx) error {
		return c.JSON(struct {
			Version string `json:"version"`
		}{Version: internal.PrintableVersion()})
	})

}
