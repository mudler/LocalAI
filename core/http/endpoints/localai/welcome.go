package localai

import (
	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/internal"
	"github.com/go-skynet/LocalAI/pkg/gallery"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/gofiber/fiber/v2"
)

func WelcomeEndpoint(appConfig *config.ApplicationConfig,
	cl *config.BackendConfigLoader, ml *model.ModelLoader, modelStatus func() (map[string]string, map[string]string)) func(*fiber.Ctx) error {
	return func(c *fiber.Ctx) error {
		models, _ := ml.ListModels()
		backendConfigs := cl.GetAllBackendConfigs()

		galleryConfigs := map[string]*gallery.Config{}
		for _, m := range backendConfigs {

			cfg, err := gallery.GetLocalModelConfiguration(ml.ModelPath, m.Name)
			if err != nil {
				continue
			}
			galleryConfigs[m.Name] = cfg
		}

		// Get model statuses to display in the UI the operation in progress
		processingModels, taskTypes := modelStatus()

		summary := fiber.Map{
			"Title":             "LocalAI API - " + internal.PrintableVersion(),
			"Version":           internal.PrintableVersion(),
			"Models":            models,
			"ModelsConfig":      backendConfigs,
			"GalleryConfig":     galleryConfigs,
			"ApplicationConfig": appConfig,
			"ProcessingModels":  processingModels,
			"TaskTypes":         taskTypes,
		}

		if string(c.Context().Request.Header.ContentType()) == "application/json" || len(c.Accepts("html")) == 0 {
			// The client expects a JSON response
			return c.Status(fiber.StatusOK).JSON(summary)
		} else {
			// Render index
			return c.Render("views/index", summary)
		}
	}
}
