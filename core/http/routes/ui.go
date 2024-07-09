package routes

import (
	"fmt"
	"html/template"
	"sort"
	"strings"

	"github.com/mudler/LocalAI/core"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/elements"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/p2p"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/xsync"
	"github.com/rs/zerolog/log"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

// TODO: Convert over the remaining instances of application.BackendConfigLoader.GetAllBackendConfigs() to filtered LMS calls
func RegisterUIRoutes(app *fiber.App, application *core.Application) {

	// keeps the state of models that are being installed from the UI
	var processingModels = xsync.NewSyncedMap[string, string]()

	// modelStatus returns the current status of the models being processed (installation or deletion)
	// it is called asynchonously from the UI
	modelStatus := func() (map[string]string, map[string]string) {
		processingModelsData := processingModels.Map()

		taskTypes := map[string]string{}

		for k, v := range processingModelsData {
			status := application.GalleryService.GetStatus(v)
			taskTypes[k] = "Installation"
			if status != nil && status.Deletion {
				taskTypes[k] = "Deletion"
			} else if status == nil {
				taskTypes[k] = "Waiting"
			}
		}

		return processingModelsData, taskTypes
	}

	app.Get("/", localai.WelcomeEndpoint(application.ApplicationConfig, application.BackendConfigLoader, application.ModelLoader, modelStatus))

	if p2p.IsP2PEnabled() {
		app.Get("/p2p", func(c *fiber.Ctx) error {
			summary := fiber.Map{
				"Title":   "LocalAI - P2P dashboard",
				"Version": internal.PrintableVersion(),
				//"Nodes":          p2p.GetAvailableNodes(""),
				//"FederatedNodes": p2p.GetAvailableNodes(p2p.FederatedID),
				"IsP2PEnabled": p2p.IsP2PEnabled(),
				"P2PToken":     application.ApplicationConfig.P2PToken,
			}

			// Render index
			return c.Render("views/p2p", summary)
		})

		/* show nodes live! */
		app.Get("/p2p/ui/workers", func(c *fiber.Ctx) error {
			return c.SendString(elements.P2PNodeBoxes(p2p.GetAvailableNodes("")))
		})
		app.Get("/p2p/ui/workers-federation", func(c *fiber.Ctx) error {
			return c.SendString(elements.P2PNodeBoxes(p2p.GetAvailableNodes(p2p.FederatedID)))
		})

		app.Get("/p2p/ui/workers-stats", func(c *fiber.Ctx) error {
			return c.SendString(elements.P2PNodeStats(p2p.GetAvailableNodes("")))
		})
		app.Get("/p2p/ui/workers-federation-stats", func(c *fiber.Ctx) error {
			return c.SendString(elements.P2PNodeStats(p2p.GetAvailableNodes(p2p.FederatedID)))
		})
	}

	// Show the Models page (all models)
	app.Get("/browse", func(c *fiber.Ctx) error {
		term := c.Query("term")

		models, _ := gallery.AvailableGalleryModels(application.ApplicationConfig.Galleries, application.ApplicationConfig.ModelPath)

		// Get all available tags
		allTags := map[string]struct{}{}
		tags := []string{}
		for _, m := range models {
			for _, t := range m.Tags {
				allTags[t] = struct{}{}
			}
		}
		for t := range allTags {
			tags = append(tags, t)
		}
		sort.Strings(tags)

		if term != "" {
			models = gallery.GalleryModels(models).Search(term)
		}

		// Get model statuses
		processingModelsData, taskTypes := modelStatus()

		summary := fiber.Map{
			"Title":            "LocalAI - Models",
			"Version":          internal.PrintableVersion(),
			"Models":           template.HTML(elements.ListModels(models, processingModels, application.GalleryService)),
			"Repositories":     application.ApplicationConfig.Galleries,
			"AllTags":          tags,
			"ProcessingModels": processingModelsData,
			"AvailableModels":  len(models),
			"IsP2PEnabled":     p2p.IsP2PEnabled(),

			"TaskTypes": taskTypes,
			//	"ApplicationConfig": appConfig,
		}

		// Render index
		return c.Render("views/models", summary)
	})

	// Show the models, filtered from the user input
	// https://htmx.org/examples/active-search/
	app.Post("/browse/search/models", func(c *fiber.Ctx) error {
		form := struct {
			Search string `form:"search"`
		}{}
		if err := c.BodyParser(&form); err != nil {
			return c.Status(fiber.StatusBadRequest).SendString(err.Error())
		}

		models, _ := gallery.AvailableGalleryModels(application.ApplicationConfig.Galleries, application.ApplicationConfig.ModelPath)

		return c.SendString(elements.ListModels(gallery.GalleryModels(models).Search(form.Search), processingModels, application.GalleryService))
	})

	/*

		Install routes

	*/

	// This route is used when the "Install" button is pressed, we submit here a new job to the gallery service
	// https://htmx.org/examples/progress-bar/
	app.Post("/browse/install/model/:id", func(c *fiber.Ctx) error {
		galleryID := strings.Clone(c.Params("id")) // note: strings.Clone is required for multiple requests!
		log.Debug().Msgf("UI job submitted to install  : %+v\n", galleryID)

		id, err := uuid.NewUUID()
		if err != nil {
			return err
		}

		uid := id.String()

		processingModels.Set(galleryID, uid)

		op := gallery.GalleryOp{
			Id:               uid,
			GalleryModelName: galleryID,
			Galleries:        application.ApplicationConfig.Galleries,
		}
		go func() {
			application.GalleryService.C <- op
		}()

		return c.SendString(elements.StartProgressBar(uid, "0", "Installation"))
	})

	// This route is used when the "Install" button is pressed, we submit here a new job to the gallery service
	// https://htmx.org/examples/progress-bar/
	app.Post("/browse/delete/model/:id", func(c *fiber.Ctx) error {
		galleryID := strings.Clone(c.Params("id")) // note: strings.Clone is required for multiple requests!
		log.Debug().Msgf("UI job submitted to delete  : %+v\n", galleryID)
		var galleryName = galleryID
		if strings.Contains(galleryID, "@") {
			// if the galleryID contains a @ it means that it's a model from a gallery
			// but we want to delete it from the local models which does not need
			// a repository ID
			galleryName = strings.Split(galleryID, "@")[1]
		}

		id, err := uuid.NewUUID()
		if err != nil {
			return err
		}

		uid := id.String()

		// Track the deletion job by galleryID and galleryName
		// The GalleryID contains information about the repository,
		// while the GalleryName is ONLY the name of the model
		processingModels.Set(galleryName, uid)
		processingModels.Set(galleryID, uid)

		op := gallery.GalleryOp{
			Id:               uid,
			Delete:           true,
			GalleryModelName: galleryName,
		}
		go func() {
			application.GalleryService.C <- op
			application.BackendConfigLoader.RemoveBackendConfig(galleryName)
		}()

		return c.SendString(elements.StartProgressBar(uid, "0", "Deletion"))
	})

	// Display the job current progress status
	// If the job is done, we trigger the /browse/job/:uid route
	// https://htmx.org/examples/progress-bar/
	app.Get("/browse/job/progress/:uid", func(c *fiber.Ctx) error {
		jobUID := strings.Clone(c.Params("uid")) // note: strings.Clone is required for multiple requests!

		status := application.GalleryService.GetStatus(jobUID)
		if status == nil {
			//fmt.Errorf("could not find any status for ID")
			return c.SendString(elements.ProgressBar("0"))
		}

		if status.Progress == 100 {
			c.Set("HX-Trigger", "done") // this triggers /browse/job/:uid (which is when the job is done)
			return c.SendString(elements.ProgressBar("100"))
		}
		if status.Error != nil {
			return c.SendString(elements.ErrorProgress(status.Error.Error(), status.GalleryModelName))
		}

		return c.SendString(elements.ProgressBar(fmt.Sprint(status.Progress)))
	})

	// this route is hit when the job is done, and we display the
	// final state (for now just displays "Installation completed")
	app.Get("/browse/job/:uid", func(c *fiber.Ctx) error {
		jobUID := strings.Clone(c.Params("uid")) // note: strings.Clone is required for multiple requests!

		status := application.GalleryService.GetStatus(jobUID)

		galleryID := ""
		for _, k := range processingModels.Keys() {
			if processingModels.Get(k) == jobUID {
				galleryID = k
				processingModels.Delete(k)
			}
		}
		if galleryID == "" {
			log.Debug().Msgf("no processing model found for job : %+v\n", jobUID)
		}

		log.Debug().Msgf("JOB finished  : %+v\n", status)
		showDelete := true
		displayText := "Installation completed"
		if status.Deletion {
			showDelete = false
			displayText = "Deletion completed"
		}

		return c.SendString(elements.DoneProgress(galleryID, displayText, showDelete))
	})

	// Show the Chat page
	app.Get("/chat/:model", func(c *fiber.Ctx) error {
		backendConfigs, _ := application.ListModelsService.ListModels(config.BuildUsecaseFilterFn(config.FLAG_LLM), services.SKIP_IF_CONFIGURED)

		summary := fiber.Map{
			"Title":        "LocalAI - Chat with " + c.Params("model"),
			"ModelsConfig": backendConfigs,
			"Model":        c.Params("model"),
			"Version":      internal.PrintableVersion(),
			"IsP2PEnabled": p2p.IsP2PEnabled(),
		}

		// Render index
		return c.Render("views/chat", summary)
	})

	app.Get("/talk/", func(c *fiber.Ctx) error {
		backendConfigs, _ := application.ListModelsService.ListModels(config.BuildUsecaseFilterFn(config.FLAG_TTS), services.SKIP_IF_CONFIGURED)

		if len(backendConfigs) == 0 {
			// If no model is available redirect to the index which suggests how to install models
			return c.Redirect("/")
		}

		summary := fiber.Map{
			"Title":        "LocalAI - Talk",
			"ModelsConfig": backendConfigs,
			"Model":        backendConfigs[0].ID,
			"IsP2PEnabled": p2p.IsP2PEnabled(),
			"Version":      internal.PrintableVersion(),
		}

		// Render index
		return c.Render("views/talk", summary)
	})

	app.Get("/chat/", func(c *fiber.Ctx) error {

		backendConfigs, _ := application.ListModelsService.ListModels(config.BuildUsecaseFilterFn(config.FLAG_LLM), services.SKIP_IF_CONFIGURED)

		if len(backendConfigs) == 0 {
			// If no model is available redirect to the index which suggests how to install models
			return c.Redirect("/")
		}

		summary := fiber.Map{
			"Title":        "LocalAI - Chat with " + backendConfigs[0].ID,
			"ModelsConfig": backendConfigs,
			"Model":        backendConfigs[0].ID,
			"Version":      internal.PrintableVersion(),
			"IsP2PEnabled": p2p.IsP2PEnabled(),
		}

		// Render index
		return c.Render("views/chat", summary)
	})

	app.Get("/text2image/:model", func(c *fiber.Ctx) error {
		backendConfigs := application.BackendConfigLoader.GetAllBackendConfigs()

		summary := fiber.Map{
			"Title":        "LocalAI - Generate images with " + c.Params("model"),
			"ModelsConfig": backendConfigs,
			"Model":        c.Params("model"),
			"Version":      internal.PrintableVersion(),
			"IsP2PEnabled": p2p.IsP2PEnabled(),
		}

		// Render index
		return c.Render("views/text2image", summary)
	})

	app.Get("/text2image/", func(c *fiber.Ctx) error {

		backendConfigs := application.BackendConfigLoader.GetAllBackendConfigs()

		if len(backendConfigs) == 0 {
			// If no model is available redirect to the index which suggests how to install models
			return c.Redirect("/")
		}

		summary := fiber.Map{
			"Title":        "LocalAI - Generate images with " + backendConfigs[0].Name,
			"ModelsConfig": backendConfigs,
			"Model":        backendConfigs[0].Name,
			"Version":      internal.PrintableVersion(),
			"IsP2PEnabled": p2p.IsP2PEnabled(),
		}

		// Render index
		return c.Render("views/text2image", summary)
	})

	app.Get("/tts/:model", func(c *fiber.Ctx) error {
		backendConfigs := application.BackendConfigLoader.GetAllBackendConfigs()

		summary := fiber.Map{
			"Title":        "LocalAI - Generate images with " + c.Params("model"),
			"ModelsConfig": backendConfigs,
			"Model":        c.Params("model"),
			"Version":      internal.PrintableVersion(),
			"IsP2PEnabled": p2p.IsP2PEnabled(),
		}

		// Render index
		return c.Render("views/tts", summary)
	})

	app.Get("/tts/", func(c *fiber.Ctx) error {

		backendConfigs := application.BackendConfigLoader.GetAllBackendConfigs()

		if len(backendConfigs) == 0 {
			// If no model is available redirect to the index which suggests how to install models
			return c.Redirect("/")
		}

		summary := fiber.Map{
			"Title":        "LocalAI - Generate audio with " + backendConfigs[0].Name,
			"ModelsConfig": backendConfigs,
			"Model":        backendConfigs[0].Name,
			"IsP2PEnabled": p2p.IsP2PEnabled(),
			"Version":      internal.PrintableVersion(),
		}

		// Render index
		return c.Render("views/tts", summary)
	})
}
