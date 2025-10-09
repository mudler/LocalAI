package routes

import (
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/utils"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/model"

	"github.com/gofiber/fiber/v2"
)

func RegisterUIRoutes(app *fiber.App,
	cl *config.ModelConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	galleryService *services.GalleryService) {

	// keeps the state of ops that are started from the UI
	var processingOps = services.NewOpCache(galleryService)

	app.Get("/", localai.WelcomeEndpoint(appConfig, cl, ml, processingOps))

	// P2P
	app.Get("/p2p", func(c *fiber.Ctx) error {
		summary := fiber.Map{
			"Title":   "LocalAI - P2P dashboard",
			"BaseURL": utils.BaseURL(c),
			"Version": internal.PrintableVersion(),
			//"Nodes":          p2p.GetAvailableNodes(""),
			//"FederatedNodes": p2p.GetAvailableNodes(p2p.FederatedID),

			"P2PToken":  appConfig.P2PToken,
			"NetworkID": appConfig.P2PNetworkID,
		}

		// Render index
		return c.Render("views/p2p", summary)
	})

	// Note: P2P UI fragment routes (/p2p/ui/*) were removed
	// P2P nodes are now fetched via JSON API at /api/p2p/workers and /api/p2p/federation

	// End P2P

	if !appConfig.DisableGalleryEndpoint {
		registerGalleryRoutes(app, cl, appConfig, galleryService, processingOps)
		registerBackendGalleryRoutes(app, appConfig, galleryService, processingOps)
	}

	app.Get("/talk/", func(c *fiber.Ctx) error {
		modelConfigs, _ := services.ListModels(cl, ml, config.NoFilterFn, services.SKIP_IF_CONFIGURED)

		if len(modelConfigs) == 0 {
			// If no model is available redirect to the index which suggests how to install models
			return c.Redirect(utils.BaseURL(c))
		}

		summary := fiber.Map{
			"Title":        "LocalAI - Talk",
			"BaseURL":      utils.BaseURL(c),
			"ModelsConfig": modelConfigs,
			"Model":        modelConfigs[0],

			"Version": internal.PrintableVersion(),
		}

		// Render index
		return c.Render("views/talk", summary)
	})

	app.Get("/chat/", func(c *fiber.Ctx) error {
		modelConfigs := cl.GetAllModelsConfigs()
		modelsWithoutConfig, _ := services.ListModels(cl, ml, config.NoFilterFn, services.LOOSE_ONLY)

		if len(modelConfigs)+len(modelsWithoutConfig) == 0 {
			// If no model is available redirect to the index which suggests how to install models
			return c.Redirect(utils.BaseURL(c))
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

		for _, b := range modelConfigs {
			if b.HasUsecases(config.FLAG_CHAT) {
				modelThatCanBeUsed = b.Name
				title = "LocalAI - Chat with " + modelThatCanBeUsed
				break
			}
		}

		summary := fiber.Map{
			"Title":               title,
			"BaseURL":             utils.BaseURL(c),
			"ModelsWithoutConfig": modelsWithoutConfig,
			"GalleryConfig":       galleryConfigs,
			"ModelsConfig":        modelConfigs,
			"Model":               modelThatCanBeUsed,
			"Version":             internal.PrintableVersion(),
		}

		// Render index
		return c.Render("views/chat", summary)
	})

	// Show the Chat page
	app.Get("/chat/:model", func(c *fiber.Ctx) error {
		modelConfigs := cl.GetAllModelsConfigs()
		modelsWithoutConfig, _ := services.ListModels(cl, ml, config.NoFilterFn, services.LOOSE_ONLY)

		galleryConfigs := map[string]*gallery.ModelConfig{}

		for _, m := range modelConfigs {
			cfg, err := gallery.GetLocalModelConfiguration(ml.ModelPath, m.Name)
			if err != nil {
				continue
			}
			galleryConfigs[m.Name] = cfg
		}

		summary := fiber.Map{
			"Title":               "LocalAI - Chat with " + c.Params("model"),
			"BaseURL":             utils.BaseURL(c),
			"ModelsConfig":        modelConfigs,
			"GalleryConfig":       galleryConfigs,
			"ModelsWithoutConfig": modelsWithoutConfig,
			"Model":               c.Params("model"),
			"Version":             internal.PrintableVersion(),
		}

		// Render index
		return c.Render("views/chat", summary)
	})

	app.Get("/text2image/:model", func(c *fiber.Ctx) error {
		modelConfigs := cl.GetAllModelsConfigs()
		modelsWithoutConfig, _ := services.ListModels(cl, ml, config.NoFilterFn, services.LOOSE_ONLY)

		summary := fiber.Map{
			"Title":               "LocalAI - Generate images with " + c.Params("model"),
			"BaseURL":             utils.BaseURL(c),
			"ModelsConfig":        modelConfigs,
			"ModelsWithoutConfig": modelsWithoutConfig,
			"Model":               c.Params("model"),
			"Version":             internal.PrintableVersion(),
		}

		// Render index
		return c.Render("views/text2image", summary)
	})

	app.Get("/text2image/", func(c *fiber.Ctx) error {
		modelConfigs := cl.GetAllModelsConfigs()
		modelsWithoutConfig, _ := services.ListModels(cl, ml, config.NoFilterFn, services.LOOSE_ONLY)

		if len(modelConfigs)+len(modelsWithoutConfig) == 0 {
			// If no model is available redirect to the index which suggests how to install models
			return c.Redirect(utils.BaseURL(c))
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

		summary := fiber.Map{
			"Title":               title,
			"BaseURL":             utils.BaseURL(c),
			"ModelsConfig":        modelConfigs,
			"ModelsWithoutConfig": modelsWithoutConfig,
			"Model":               modelThatCanBeUsed,
			"Version":             internal.PrintableVersion(),
		}

		// Render index
		return c.Render("views/text2image", summary)
	})

	app.Get("/tts/:model", func(c *fiber.Ctx) error {
		modelConfigs := cl.GetAllModelsConfigs()
		modelsWithoutConfig, _ := services.ListModels(cl, ml, config.NoFilterFn, services.LOOSE_ONLY)

		summary := fiber.Map{
			"Title":               "LocalAI - Generate images with " + c.Params("model"),
			"BaseURL":             utils.BaseURL(c),
			"ModelsConfig":        modelConfigs,
			"ModelsWithoutConfig": modelsWithoutConfig,
			"Model":               c.Params("model"),
			"Version":             internal.PrintableVersion(),
		}

		// Render index
		return c.Render("views/tts", summary)
	})

	app.Get("/tts/", func(c *fiber.Ctx) error {
		modelConfigs := cl.GetAllModelsConfigs()
		modelsWithoutConfig, _ := services.ListModels(cl, ml, config.NoFilterFn, services.LOOSE_ONLY)

		if len(modelConfigs)+len(modelsWithoutConfig) == 0 {
			// If no model is available redirect to the index which suggests how to install models
			return c.Redirect(utils.BaseURL(c))
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
		summary := fiber.Map{
			"Title":               title,
			"BaseURL":             utils.BaseURL(c),
			"ModelsConfig":        modelConfigs,
			"ModelsWithoutConfig": modelsWithoutConfig,
			"Model":               modelThatCanBeUsed,
			"Version":             internal.PrintableVersion(),
		}

		// Render index
		return c.Render("views/tts", summary)
	})
}
