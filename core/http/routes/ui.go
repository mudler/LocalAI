package routes

import (
	"fmt"
	"html/template"
	"strings"

	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/http/elements"
	"github.com/go-skynet/LocalAI/core/http/endpoints/localai"
	"github.com/go-skynet/LocalAI/core/services"
	"github.com/go-skynet/LocalAI/internal"
	"github.com/go-skynet/LocalAI/pkg/gallery"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/go-skynet/LocalAI/pkg/xsync"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func RegisterUIRoutes(app *fiber.App,
	cl *config.BackendConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	galleryService *services.GalleryService,
	auth func(*fiber.Ctx) error) {

	app.Get("/", auth, localai.WelcomeEndpoint(appConfig, cl, ml))

	// keeps the state of models that are being installed from the UI
	var installingModels = xsync.NewSyncedMap[string, string]()

	// Show the Models page (all models)
	app.Get("/browse", auth, func(c *fiber.Ctx) error {
		models, _ := gallery.AvailableGalleryModels(appConfig.Galleries, appConfig.ModelPath)

		summary := fiber.Map{
			"Title":        "LocalAI - Models",
			"Version":      internal.PrintableVersion(),
			"Models":       template.HTML(elements.ListModels(models, installingModels)),
			"Repositories": appConfig.Galleries,
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

		filteredModels := []*gallery.GalleryModel{}
		for _, m := range models {
			if strings.Contains(m.Name, form.Search) ||
				strings.Contains(m.Description, form.Search) ||
				strings.Contains(m.Gallery.Name, form.Search) ||
				strings.Contains(strings.Join(m.Tags, ","), form.Search) {
				filteredModels = append(filteredModels, m)
			}
		}

		return c.SendString(elements.ListModels(filteredModels, installingModels))
	})

	/*

		Install routes

	*/

	// This route is used when the "Install" button is pressed, we submit here a new job to the gallery service
	// https://htmx.org/examples/progress-bar/
	app.Post("/browse/install/model/:id", auth, func(c *fiber.Ctx) error {
		galleryID := strings.Clone(c.Params("id")) // note: strings.Clone is required for multiple requests!

		id, err := uuid.NewUUID()
		if err != nil {
			return err
		}

		uid := id.String()

		installingModels.Set(galleryID, uid)

		op := gallery.GalleryOp{
			Id:          uid,
			GalleryName: galleryID,
			Galleries:   appConfig.Galleries,
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

		id, err := uuid.NewUUID()
		if err != nil {
			return err
		}

		uid := id.String()

		installingModels.Set(galleryID, uid)

		op := gallery.GalleryOp{
			Id:          uid,
			Delete:      true,
			GalleryName: galleryID,
		}
		go func() {
			galleryService.C <- op
		}()

		return c.SendString(elements.StartProgressBar(uid, "0", "Deletion"))
	})

	// Display the job current progress status
	// If the job is done, we trigger the /browse/job/:uid route
	// https://htmx.org/examples/progress-bar/
	app.Get("/browse/job/progress/:uid", auth, func(c *fiber.Ctx) error {
		jobUID := c.Params("uid")

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
			return c.SendString(elements.ErrorProgress(status.Error.Error()))
		}

		return c.SendString(elements.ProgressBar(fmt.Sprint(status.Progress)))
	})

	// this route is hit when the job is done, and we display the
	// final state (for now just displays "Installation completed")
	app.Get("/browse/job/:uid", auth, func(c *fiber.Ctx) error {

		status := galleryService.GetStatus(c.Params("uid"))

		for _, k := range installingModels.Keys() {
			if installingModels.Get(k) == c.Params("uid") {
				installingModels.Delete(k)
			}
		}

		displayText := "Installation completed"
		if status.Deletion {
			displayText = "Deletion completed"
		}

		return c.SendString(elements.DoneProgress(c.Params("uid"), displayText))
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
			return c.SendString("No models available")
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
			return c.SendString("No models available")
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
			return c.SendString("No models available")
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
