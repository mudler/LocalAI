package routes

import (
	"github.com/go-skynet/LocalAI/core"
	"github.com/go-skynet/LocalAI/core/http/endpoints/localai"
	"github.com/go-skynet/LocalAI/core/http/middleware"
	"github.com/go-skynet/LocalAI/internal"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/swagger"
)

func RegisterLocalAIRoutes(app *fiber.App,
	application *core.Application,
	requestExtractor *middleware.RequestExtractor,
	auth fiber.Handler) {

	app.Get("/swagger/*", swagger.HandlerDefault) // default

	// LocalAI API endpoints

	modelGalleryEndpointService := localai.CreateModelGalleryEndpointService(application.ApplicationConfig.Galleries, application.ApplicationConfig.ModelPath, application.GalleryService)
	app.Post("/models/apply", auth, modelGalleryEndpointService.ApplyModelGalleryEndpoint())
	app.Post("/models/delete/:name", auth, modelGalleryEndpointService.DeleteModelGalleryEndpoint())

	app.Get("/models/available", auth, modelGalleryEndpointService.ListModelFromGalleryEndpoint())
	app.Get("/models/galleries", auth, modelGalleryEndpointService.ListModelGalleriesEndpoint())
	app.Post("/models/galleries", auth, modelGalleryEndpointService.AddModelGalleryEndpoint())
	app.Delete("/models/galleries", auth, modelGalleryEndpointService.RemoveModelGalleryEndpoint())
	app.Get("/models/jobs/:uuid", auth, modelGalleryEndpointService.GetOpStatusEndpoint())
	app.Get("/models/jobs", auth, modelGalleryEndpointService.GetAllStatusEndpoint())

	app.Post("/tts", auth, requestExtractor.SetModelName, localai.TTSEndpoint(application.TextToSpeechBackendService))

	// Stores : TODO IS THIS REALLY A SERVICE? OR IS IT PURELY WEB API FEATURE?
	app.Post("/stores/set", auth, localai.StoresSetEndpoint(application.StoresLoader, application.ApplicationConfig))
	app.Post("/stores/delete", auth, localai.StoresDeleteEndpoint(application.StoresLoader, application.ApplicationConfig))
	app.Post("/stores/get", auth, localai.StoresGetEndpoint(application.StoresLoader, application.ApplicationConfig))
	app.Post("/stores/find", auth, localai.StoresFindEndpoint(application.StoresLoader, application.ApplicationConfig))

	// Kubernetes health checks
	ok := func(c *fiber.Ctx) error {
		return c.SendStatus(200)
	}

	app.Get("/healthz", ok)
	app.Get("/readyz", ok)

	app.Get("/metrics", auth, localai.LocalAIMetricsEndpoint())

	app.Get("/backend/monitor", auth, localai.BackendMonitorEndpoint(application.BackendMonitorService))
	app.Post("/backend/shutdown", auth, localai.BackendShutdownEndpoint(application.BackendMonitorService))

	app.Get("/version", auth, func(c *fiber.Ctx) error {
		return c.JSON(struct {
			Version string `json:"version"`
		}{Version: internal.PrintableVersion()})
	})

}
