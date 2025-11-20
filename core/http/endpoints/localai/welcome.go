package localai

import (
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/model"
)

func WelcomeEndpoint(appConfig *config.ApplicationConfig,
	cl *config.ModelConfigLoader, ml *model.ModelLoader, opcache *services.OpCache) echo.HandlerFunc {
	return func(c echo.Context) error {
		modelConfigs := cl.GetAllModelsConfigs()
		galleryConfigs := map[string]*gallery.ModelConfig{}

		installedBackends, err := gallery.ListSystemBackends(appConfig.SystemState)
		if err != nil {
			return err
		}

		for _, m := range modelConfigs {
			cfg, err := gallery.GetLocalModelConfiguration(ml.ModelPath, m.Name)
			if err != nil {
				continue
			}
			galleryConfigs[m.Name] = cfg
		}

		loadedModels := ml.ListLoadedModels()
		loadedModelsMap := map[string]bool{}
		for _, m := range loadedModels {
			loadedModelsMap[m.ID] = true
		}

		modelsWithoutConfig, _ := services.ListModels(cl, ml, config.NoFilterFn, services.LOOSE_ONLY)

		// Get model statuses to display in the UI the operation in progress
		processingModels, taskTypes := opcache.GetStatus()

		summary := map[string]interface{}{
			"Title":                  "LocalAI API - " + internal.PrintableVersion(),
			"Version":                internal.PrintableVersion(),
			"BaseURL":                middleware.BaseURL(c),
			"Models":                 modelsWithoutConfig,
			"ModelsConfig":           modelConfigs,
			"GalleryConfig":          galleryConfigs,
			"ApplicationConfig":      appConfig,
			"ProcessingModels":       processingModels,
			"TaskTypes":              taskTypes,
			"LoadedModels":           loadedModelsMap,
			"InstalledBackends":      installedBackends,
			"DisableRuntimeSettings": appConfig.DisableRuntimeSettings,
		}

		contentType := c.Request().Header.Get("Content-Type")
		accept := c.Request().Header.Get("Accept")
		// Default to HTML if Accept header is empty (browser behavior)
		// Only return JSON if explicitly requested or Content-Type is application/json
		if strings.Contains(contentType, "application/json") || (accept != "" && !strings.Contains(accept, "text/html")) {
			// The client expects a JSON response
			return c.JSON(200, summary)
		} else {
			// Check if this is the manage route
			templateName := "views/index"
			if strings.HasSuffix(c.Request().URL.Path, "/manage") || c.Request().URL.Path == "/manage" {
				templateName = "views/manage"
			}
			// Render appropriate template
			return c.Render(200, templateName, summary)
		}
	}
}
