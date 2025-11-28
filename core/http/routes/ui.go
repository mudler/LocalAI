package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/model"
)

func RegisterUIRoutes(app *echo.Echo,
	cl *config.ModelConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	galleryService *services.GalleryService) {

	// keeps the state of ops that are started from the UI
	var processingOps = services.NewOpCache(galleryService)

	app.GET("/", localai.WelcomeEndpoint(appConfig, cl, ml, processingOps))
	app.GET("/manage", localai.WelcomeEndpoint(appConfig, cl, ml, processingOps))

	if !appConfig.DisableRuntimeSettings {
		// Settings page
		app.GET("/settings", func(c echo.Context) error {
			summary := map[string]interface{}{
				"Title":   "LocalAI - Settings",
				"BaseURL": middleware.BaseURL(c),
			}
			return c.Render(200, "views/settings", summary)
		})
	}

	// Agent Jobs pages
	app.GET("/agent-jobs", func(c echo.Context) error {
		modelConfigs := cl.GetAllModelsConfigs()
		summary := map[string]interface{}{
			"Title":        "LocalAI - Agent Jobs",
			"BaseURL":      middleware.BaseURL(c),
			"Version":      internal.PrintableVersion(),
			"ModelsConfig": modelConfigs,
		}
		return c.Render(200, "views/agent-jobs", summary)
	})

	app.GET("/agent-jobs/tasks/new", func(c echo.Context) error {
		modelConfigs := cl.GetAllModelsConfigs()
		summary := map[string]interface{}{
			"Title":        "LocalAI - Create Task",
			"BaseURL":      middleware.BaseURL(c),
			"Version":      internal.PrintableVersion(),
			"ModelsConfig": modelConfigs,
		}
		return c.Render(200, "views/agent-task-details", summary)
	})

	// More specific route must come first
	app.GET("/agent-jobs/tasks/:id/edit", func(c echo.Context) error {
		modelConfigs := cl.GetAllModelsConfigs()
		summary := map[string]interface{}{
			"Title":        "LocalAI - Edit Task",
			"BaseURL":      middleware.BaseURL(c),
			"Version":      internal.PrintableVersion(),
			"ModelsConfig": modelConfigs,
		}
		return c.Render(200, "views/agent-task-details", summary)
	})

	// Task details page (less specific, comes after edit route)
	app.GET("/agent-jobs/tasks/:id", func(c echo.Context) error {
		summary := map[string]interface{}{
			"Title":   "LocalAI - Task Details",
			"BaseURL": middleware.BaseURL(c),
			"Version": internal.PrintableVersion(),
		}
		return c.Render(200, "views/agent-task-details", summary)
	})

	app.GET("/agent-jobs/jobs/:id", func(c echo.Context) error {
		summary := map[string]interface{}{
			"Title":   "LocalAI - Job Details",
			"BaseURL": middleware.BaseURL(c),
			"Version": internal.PrintableVersion(),
		}
		return c.Render(200, "views/agent-job-details", summary)
	})

	// P2P
	app.GET("/p2p/", func(c echo.Context) error {
		summary := map[string]interface{}{
			"Title":   "LocalAI - P2P dashboard",
			"BaseURL": middleware.BaseURL(c),
			"Version": internal.PrintableVersion(),
			//"Nodes":          p2p.GetAvailableNodes(""),
			//"FederatedNodes": p2p.GetAvailableNodes(p2p.FederatedID),

			"P2PToken":  appConfig.P2PToken,
			"NetworkID": appConfig.P2PNetworkID,
		}

		// Render index
		return c.Render(200, "views/p2p", summary)
	})

	// Note: P2P UI fragment routes (/p2p/ui/*) were removed
	// P2P nodes are now fetched via JSON API at /api/p2p/workers and /api/p2p/federation

	// End P2P

	if !appConfig.DisableGalleryEndpoint {
		registerGalleryRoutes(app, cl, appConfig, galleryService, processingOps)
		registerBackendGalleryRoutes(app, appConfig, galleryService, processingOps)
	}

	app.GET("/talk/", func(c echo.Context) error {
		modelConfigs, _ := services.ListModels(cl, ml, config.NoFilterFn, services.SKIP_IF_CONFIGURED)

		if len(modelConfigs) == 0 {
			// If no model is available redirect to the index which suggests how to install models
			return c.Redirect(302, middleware.BaseURL(c))
		}

		summary := map[string]interface{}{
			"Title":        "LocalAI - Talk",
			"BaseURL":      middleware.BaseURL(c),
			"ModelsConfig": modelConfigs,
			"Model":        modelConfigs[0],

			"Version": internal.PrintableVersion(),
		}

		// Render index
		return c.Render(200, "views/talk", summary)
	})

	app.GET("/chat/", func(c echo.Context) error {
		modelConfigs := cl.GetAllModelsConfigs()
		modelsWithoutConfig, _ := services.ListModels(cl, ml, config.NoFilterFn, services.LOOSE_ONLY)

		if len(modelConfigs)+len(modelsWithoutConfig) == 0 {
			// If no model is available redirect to the index which suggests how to install models
			return c.Redirect(302, middleware.BaseURL(c))
		}
		modelThatCanBeUsed := ""
		galleryConfigs := map[string]*gallery.ModelConfig{}

		for _, m := range modelConfigs {
			cfg, err := gallery.GetLocalModelConfiguration(ml.ModelPath, m.Name)
			if err != nil {
				continue
			}
			galleryConfigs[m.Name] = cfg
		}

		title := "LocalAI - Chat"
		var modelContextSize *int

		for _, b := range modelConfigs {
			if b.HasUsecases(config.FLAG_CHAT) {
				modelThatCanBeUsed = b.Name
				title = "LocalAI - Chat with " + modelThatCanBeUsed
				if b.LLMConfig.ContextSize != nil {
					modelContextSize = b.LLMConfig.ContextSize
				}
				break
			}
		}

		summary := map[string]interface{}{
			"Title":               title,
			"BaseURL":             middleware.BaseURL(c),
			"ModelsWithoutConfig": modelsWithoutConfig,
			"GalleryConfig":       galleryConfigs,
			"ModelsConfig":        modelConfigs,
			"Model":               modelThatCanBeUsed,
			"ContextSize":         modelContextSize,
			"Version":             internal.PrintableVersion(),
		}

		// Render index
		return c.Render(200, "views/chat", summary)
	})

	// Show the Chat page
	app.GET("/chat/:model", func(c echo.Context) error {
		modelConfigs := cl.GetAllModelsConfigs()
		modelsWithoutConfig, _ := services.ListModels(cl, ml, config.NoFilterFn, services.LOOSE_ONLY)

		galleryConfigs := map[string]*gallery.ModelConfig{}
		modelName := c.Param("model")
		var modelContextSize *int

		for _, m := range modelConfigs {
			cfg, err := gallery.GetLocalModelConfiguration(ml.ModelPath, m.Name)
			if err != nil {
				continue
			}
			galleryConfigs[m.Name] = cfg
			if m.Name == modelName && m.LLMConfig.ContextSize != nil {
				modelContextSize = m.LLMConfig.ContextSize
			}
		}

		summary := map[string]interface{}{
			"Title":               "LocalAI - Chat with " + modelName,
			"BaseURL":             middleware.BaseURL(c),
			"ModelsConfig":        modelConfigs,
			"GalleryConfig":       galleryConfigs,
			"ModelsWithoutConfig": modelsWithoutConfig,
			"Model":               modelName,
			"ContextSize":         modelContextSize,
			"Version":             internal.PrintableVersion(),
		}

		// Render index
		return c.Render(200, "views/chat", summary)
	})

	app.GET("/text2image/:model", func(c echo.Context) error {
		modelConfigs := cl.GetAllModelsConfigs()
		modelsWithoutConfig, _ := services.ListModels(cl, ml, config.NoFilterFn, services.LOOSE_ONLY)

		summary := map[string]interface{}{
			"Title":               "LocalAI - Generate images with " + c.Param("model"),
			"BaseURL":             middleware.BaseURL(c),
			"ModelsConfig":        modelConfigs,
			"ModelsWithoutConfig": modelsWithoutConfig,
			"Model":               c.Param("model"),
			"Version":             internal.PrintableVersion(),
		}

		// Render index
		return c.Render(200, "views/text2image", summary)
	})

	app.GET("/text2image/", func(c echo.Context) error {
		modelConfigs := cl.GetAllModelsConfigs()
		modelsWithoutConfig, _ := services.ListModels(cl, ml, config.NoFilterFn, services.LOOSE_ONLY)

		if len(modelConfigs)+len(modelsWithoutConfig) == 0 {
			// If no model is available redirect to the index which suggests how to install models
			return c.Redirect(302, middleware.BaseURL(c))
		}

		modelThatCanBeUsed := ""
		title := "LocalAI - Generate images"

		for _, b := range modelConfigs {
			if b.HasUsecases(config.FLAG_IMAGE) {
				modelThatCanBeUsed = b.Name
				title = "LocalAI - Generate images with " + modelThatCanBeUsed
				break
			}
		}

		summary := map[string]interface{}{
			"Title":               title,
			"BaseURL":             middleware.BaseURL(c),
			"ModelsConfig":        modelConfigs,
			"ModelsWithoutConfig": modelsWithoutConfig,
			"Model":               modelThatCanBeUsed,
			"Version":             internal.PrintableVersion(),
		}

		// Render index
		return c.Render(200, "views/text2image", summary)
	})

	app.GET("/tts/:model", func(c echo.Context) error {
		modelConfigs := cl.GetAllModelsConfigs()
		modelsWithoutConfig, _ := services.ListModels(cl, ml, config.NoFilterFn, services.LOOSE_ONLY)

		summary := map[string]interface{}{
			"Title":               "LocalAI - Generate images with " + c.Param("model"),
			"BaseURL":             middleware.BaseURL(c),
			"ModelsConfig":        modelConfigs,
			"ModelsWithoutConfig": modelsWithoutConfig,
			"Model":               c.Param("model"),
			"Version":             internal.PrintableVersion(),
		}

		// Render index
		return c.Render(200, "views/tts", summary)
	})

	app.GET("/tts/", func(c echo.Context) error {
		modelConfigs := cl.GetAllModelsConfigs()
		modelsWithoutConfig, _ := services.ListModels(cl, ml, config.NoFilterFn, services.LOOSE_ONLY)

		if len(modelConfigs)+len(modelsWithoutConfig) == 0 {
			// If no model is available redirect to the index which suggests how to install models
			return c.Redirect(302, middleware.BaseURL(c))
		}

		modelThatCanBeUsed := ""
		title := "LocalAI - Generate audio"

		for _, b := range modelConfigs {
			if b.HasUsecases(config.FLAG_TTS) {
				modelThatCanBeUsed = b.Name
				title = "LocalAI - Generate audio with " + modelThatCanBeUsed
				break
			}
		}
		summary := map[string]interface{}{
			"Title":               title,
			"BaseURL":             middleware.BaseURL(c),
			"ModelsConfig":        modelConfigs,
			"ModelsWithoutConfig": modelsWithoutConfig,
			"Model":               modelThatCanBeUsed,
			"Version":             internal.PrintableVersion(),
		}

		// Render index
		return c.Render(200, "views/tts", summary)
	})
}
