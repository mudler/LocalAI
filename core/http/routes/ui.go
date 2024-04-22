package routes

import (
	"fmt"
	"html/template"
	"strings"

	"github.com/go-skynet/LocalAI/core/config"
	"github.com/go-skynet/LocalAI/core/http/elements"
	"github.com/go-skynet/LocalAI/core/services"
	"github.com/go-skynet/LocalAI/pkg/gallery"
	"github.com/go-skynet/LocalAI/pkg/model"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func RegisterUIRoutes(app *fiber.App,
	cl *config.BackendConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	galleryService *services.GalleryService,
	auth func(*fiber.Ctx) error) {

	// Show the Models page
	app.Get("/browse", auth, func(c *fiber.Ctx) error {
		models, _ := gallery.AvailableGalleryModels(appConfig.Galleries, appConfig.ModelPath)

		summary := fiber.Map{
			"Title":  "LocalAI API - Models",
			"Models": template.HTML(elements.ListModels(models)),
			//	"ApplicationConfig": appConfig,
		}

		// Render index
		return c.Render("views/models", summary)
	})

	// HTMX: return the model details
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
			if strings.Contains(m.Name, form.Search) {
				filteredModels = append(filteredModels, m)
			}
		}

		return c.SendString(elements.ListModels(filteredModels))
	})

	// https://htmx.org/examples/progress-bar/
	app.Post("/browse/install/model/:id", auth, func(c *fiber.Ctx) error {
		galleryID := c.Params("id")

		uuid, err := uuid.NewUUID()
		if err != nil {
			return err
		}

		go func() {
			galleryService.C <- gallery.GalleryOp{
				Id:          uuid.String(),
				GalleryName: galleryID,
				Galleries:   appConfig.Galleries,
			}
		}()

		return c.SendString(elements.StartProgressBar(uuid.String(), "0"))
	})

	// https://htmx.org/examples/progress-bar/
	app.Get("/browse/job/progress/:uid", auth, func(c *fiber.Ctx) error {
		jobUID := c.Params("uid")

		status := galleryService.GetStatus(jobUID)
		if status == nil {
			//fmt.Errorf("could not find any status for ID")
			return c.SendString(elements.ProgressBar("0"))
		}

		if status.Processed || status.Progress == 100 {
			c.Set("HX-Trigger", "done")
			return c.SendString(elements.ProgressBar("100"))
		}

		return c.SendString(elements.ProgressBar(fmt.Sprint(status.Progress)))
	})

	app.Get("/browse/job/:uid", auth, func(c *fiber.Ctx) error {
		return c.SendString(elements.DoneProgress(c.Params("uid")))
	})
}
