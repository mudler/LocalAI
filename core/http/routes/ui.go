package routes

import (
	"fmt"
	"html/template"
	"sort"
	"strings"

	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/http/elements"
	"github.com/go-skynet/LocalAI/core/http/endpoints/localai"
	"github.com/go-skynet/LocalAI/core/services"
	"github.com/go-skynet/LocalAI/internal"
	"github.com/go-skynet/LocalAI/pkg/gallery"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/go-skynet/LocalAI/pkg/xsync"
	"github.com/rs/zerolog/log"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func RegisterUIRoutes(app *fiber.App,
	cl *config.BackendConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	galleryService *services.GalleryService,
	auth func(*fiber.Ctx) error) {

	// keeps the state of models that are being installed from the UI
	var processingModels = xsync.NewSyncedMap[string, string]()

	// modelStatus returns the current status of the models being processed (installation or deletion)
	// it is called asynchonously from the UI
	modelStatus := func() (map[string]string, map[string]string) {
		processingModelsData := processingModels.Map()

		taskTypes := map[string]string{}

		for k, v := range processingModelsData {
			status := galleryService.GetStatus(v)
			taskTypes[k] = "Installation"
			if status != nil && status.Deletion {
				taskTypes[k] = "Deletion"
			} else if status == nil {
				taskTypes[k] = "Waiting"
			}
		}

		return processingModelsData, taskTypes
	}

	app.Get("/", auth, localai.WelcomeEndpoint(appConfig, cl, ml, modelStatus))

	// Show the Models page (all models)
	app.Get("/browse", auth, func(c *fiber.Ctx) error {
		term := c.Query("term")

		models, _ := gallery.AvailableGalleryModels(appConfig.Galleries, appConfig.ModelPath)

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
			"Models":           template.HTML(elements.ListModels(models, processingModels, galleryService)),
			"Repositories":     appConfig.Galleries,
			"AllTags":          tags,
			"ProcessingModels": processingModelsData,
			"AvailableModels":  len(models),
			"TaskTypes":        taskTypes,
			//	"ApplicationConfig": appConfig,
		}

		// Render index
		return c.Render("views/models", summary)
	})

	// Show the models, filtered from the user input
	// https://htmx.org/examples/active-search/
	app.Post("/browse/search/models", auth, func(c *fiber.Ctx) error {
		form := struct {
			Search string `form:"search"`
		}{}
		if err := c.BodyParser(&form); err != nil {
			return c.Status(fiber.StatusBadRequest).SendString(err.Error())
		}

		models, _ := gallery.AvailableGalleryModels(appConfig.Galleries, appConfig.ModelPath)

		return c.SendString(elements.ListModels(gallery.GalleryModels(models).Search(form.Search), processingModels, galleryService))
	})

	/*

		Install routes

	*/

	// This route is used when the "Install" button is pressed, we submit here a new job to the gallery service
	// https://htmx.org/examples/progress-bar/
	app.Post("/browse/install/model/:id", auth, func(c *fiber.Ctx) error {
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
			Galleries:        appConfig.Galleries,
		}
		go func() {
			galleryService.C <- op
		}()

		return c.SendString(elements.StartProgressBar(uid, "0", "Installation"))
	})

	// This route is used when the "Install" button is pressed, we submit here a new job to the gallery service
	// https://htmx.org/examples/progress-bar/
	app.Post("/browse/delete/model/:id", auth, func(c *fiber.Ctx) error {
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
			galleryService.C <- op
			cl.RemoveBackendConfig(galleryName)
		}()

		return c.SendString(elements.StartProgressBar(uid, "0", "Deletion"))
	})

	// Display the job current progress status
	// If the job is done, we trigger the /browse/job/:uid route
	// https://htmx.org/examples/progress-bar/
	app.Get("/browse/job/progress/:uid", auth, func(c *fiber.Ctx) error {
		jobUID := strings.Clone(c.Params("uid")) // note: strings.Clone is required for multiple requests!

		status := galleryService.GetStatus(jobUID)
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
	app.Get("/browse/job/:uid", auth, func(c *fiber.Ctx) error {
		jobUID := strings.Clone(c.Params("uid")) // note: strings.Clone is required for multiple requests!

		status := galleryService.GetStatus(jobUID)

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
	app.Get("/chat/:model", auth, func(c *fiber.Ctx) error {
		backendConfigs := cl.GetAllBackendConfigs()

		summary := fiber.Map{
			"Title":        "LocalAI - Chat with " + c.Params("model"),
			"ModelsConfig": backendConfigs,
			"Model":        c.Params("model"),
			"Version":      internal.PrintableVersion(),
		}

		// Render index
		return c.Render("views/chat", summary)
	})
	app.Get("/chat/", auth, func(c *fiber.Ctx) error {

		backendConfigs := cl.GetAllBackendConfigs()

		if len(backendConfigs) == 0 {
			// If no model is available redirect to the index which suggests how to install models
			return c.Redirect("/")
		}

		summary := fiber.Map{
			"Title":        "LocalAI - Chat with " + backendConfigs[0].Name,
			"ModelsConfig": backendConfigs,
			"Model":        backendConfigs[0].Name,
			"Version":      internal.PrintableVersion(),
		}

		// Render index
		return c.Render("views/chat", summary)
	})

	app.Get("/text2image/:model", auth, func(c *fiber.Ctx) error {
		backendConfigs := cl.GetAllBackendConfigs()

		summary := fiber.Map{
			"Title":        "LocalAI - Generate images with " + c.Params("model"),
			"ModelsConfig": backendConfigs,
			"Model":        c.Params("model"),
			"Version":      internal.PrintableVersion(),
		}

		// Render index
		return c.Render("views/text2image", summary)
	})

	app.Get("/text2image/", auth, func(c *fiber.Ctx) error {

		backendConfigs := cl.GetAllBackendConfigs()

		if len(backendConfigs) == 0 {
			// If no model is available redirect to the index which suggests how to install models
			return c.Redirect("/")
		}

		summary := fiber.Map{
			"Title":        "LocalAI - Generate images with " + backendConfigs[0].Name,
			"ModelsConfig": backendConfigs,
			"Model":        backendConfigs[0].Name,
			"Version":      internal.PrintableVersion(),
		}

		// Render index
		return c.Render("views/text2image", summary)
	})

	app.Get("/tts/:model", auth, func(c *fiber.Ctx) error {
		backendConfigs := cl.GetAllBackendConfigs()

		summary := fiber.Map{
			"Title":        "LocalAI - Generate images with " + c.Params("model"),
			"ModelsConfig": backendConfigs,
			"Model":        c.Params("model"),
			"Version":      internal.PrintableVersion(),
		}

		// Render index
		return c.Render("views/tts", summary)
	})

	app.Get("/tts/", auth, func(c *fiber.Ctx) error {

		backendConfigs := cl.GetAllBackendConfigs()

		if len(backendConfigs) == 0 {
			// If no model is available redirect to the index which suggests how to install models
			return c.Redirect("/")
		}

		summary := fiber.Map{
			"Title":        "LocalAI - Generate audio with " + backendConfigs[0].Name,
			"ModelsConfig": backendConfigs,
			"Model":        backendConfigs[0].Name,
			"Version":      internal.PrintableVersion(),
		}

		// Render index
		return c.Render("views/tts", summary)
	})
}
