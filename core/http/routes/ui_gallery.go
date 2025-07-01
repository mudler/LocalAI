package routes

import (
	"fmt"
	"html/template"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/microcosm-cc/bluemonday"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/elements"
	"github.com/mudler/LocalAI/core/http/utils"
	"github.com/mudler/LocalAI/core/p2p"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/internal"
	"github.com/rs/zerolog/log"
)

func registerGalleryRoutes(app *fiber.App, cl *config.BackendConfigLoader, appConfig *config.ApplicationConfig, galleryService *services.GalleryService, opcache *services.OpCache) {

	// Show the Models page (all models)
	app.Get("/browse", func(c *fiber.Ctx) error {
		term := c.Query("term")
		page := c.Query("page")
		items := c.Query("items")

		models, err := gallery.AvailableGalleryModels(appConfig.Galleries, appConfig.ModelPath)
		if err != nil {
			log.Error().Err(err).Msg("could not list models from galleries")
			return c.Status(fiber.StatusInternalServerError).Render("views/error", fiber.Map{
				"Title":        "LocalAI - Models",
				"BaseURL":      utils.BaseURL(c),
				"Version":      internal.PrintableVersion(),
				"ErrorCode":    "500",
				"ErrorMessage": err.Error(),
			})
		}

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
			models = gallery.GalleryElements[*gallery.GalleryModel](models).Search(term)
		}

		// Get model statuses
		processingModelsData, taskTypes := opcache.GetStatus()

		summary := fiber.Map{
			"Title":            "LocalAI - Models",
			"BaseURL":          utils.BaseURL(c),
			"Version":          internal.PrintableVersion(),
			"Models":           template.HTML(elements.ListModels(models, opcache, galleryService)),
			"Repositories":     appConfig.Galleries,
			"AllTags":          tags,
			"ProcessingModels": processingModelsData,
			"AvailableModels":  len(models),
			"IsP2PEnabled":     p2p.IsP2PEnabled(),

			"TaskTypes": taskTypes,
			//	"ApplicationConfig": appConfig,
		}

		if page == "" {
			page = "1"
		}

		if page != "" {
			// return a subset of the models
			pageNum, err := strconv.Atoi(page)
			if err != nil {
				return c.Status(fiber.StatusBadRequest).SendString("Invalid page number")
			}

			if pageNum == 0 {
				return c.Render("views/models", summary)
			}

			itemsNum, err := strconv.Atoi(items)
			if err != nil {
				itemsNum = 21
			}

			totalPages := int(math.Ceil(float64(len(models)) / float64(itemsNum)))

			models = models.Paginate(pageNum, itemsNum)

			prevPage := pageNum - 1
			nextPage := pageNum + 1
			if prevPage < 1 {
				prevPage = 1
			}
			if nextPage > totalPages {
				nextPage = totalPages
			}
			if prevPage != pageNum {
				summary["PrevPage"] = prevPage
			}
			summary["NextPage"] = nextPage
			summary["TotalPages"] = totalPages
			summary["CurrentPage"] = pageNum
			summary["Models"] = template.HTML(elements.ListModels(models, opcache, galleryService))
		}

		// Render index
		return c.Render("views/models", summary)
	})

	// Show the models, filtered from the user input
	// https://htmx.org/examples/active-search/
	app.Post("/browse/search/models", func(c *fiber.Ctx) error {
		page := c.Query("page")
		items := c.Query("items")

		form := struct {
			Search string `form:"search"`
		}{}
		if err := c.BodyParser(&form); err != nil {
			return c.Status(fiber.StatusBadRequest).SendString(bluemonday.StrictPolicy().Sanitize(err.Error()))
		}

		models, _ := gallery.AvailableGalleryModels(appConfig.Galleries, appConfig.ModelPath)

		if page != "" {
			// return a subset of the models
			pageNum, err := strconv.Atoi(page)
			if err != nil {
				return c.Status(fiber.StatusBadRequest).SendString("Invalid page number")
			}

			itemsNum, err := strconv.Atoi(items)
			if err != nil {
				itemsNum = 21
			}

			models = models.Paginate(pageNum, itemsNum)
		}

		if form.Search != "" {
			models = models.Search(form.Search)
		}

		return c.SendString(elements.ListModels(models, opcache, galleryService))
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

		opcache.Set(galleryID, uid)

		op := services.GalleryOp[gallery.GalleryModel]{
			ID:                 uid,
			GalleryElementName: galleryID,
			Galleries:          appConfig.Galleries,
		}
		go func() {
			galleryService.ModelGalleryChannel <- op
		}()

		return c.SendString(elements.StartModelProgressBar(uid, "0", "Installation"))
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
		opcache.Set(galleryName, uid)
		opcache.Set(galleryID, uid)

		op := services.GalleryOp[gallery.GalleryModel]{
			ID:                 uid,
			Delete:             true,
			GalleryElementName: galleryName,
			Galleries:          appConfig.Galleries,
		}
		go func() {
			galleryService.ModelGalleryChannel <- op
			cl.RemoveBackendConfig(galleryName)
		}()

		return c.SendString(elements.StartModelProgressBar(uid, "0", "Deletion"))
	})

	// Display the job current progress status
	// If the job is done, we trigger the /browse/job/:uid route
	// https://htmx.org/examples/progress-bar/
	app.Get("/browse/job/progress/:uid", func(c *fiber.Ctx) error {
		jobUID := strings.Clone(c.Params("uid")) // note: strings.Clone is required for multiple requests!

		status := galleryService.GetStatus(jobUID)
		if status == nil {
			//fmt.Errorf("could not find any status for ID")
			return c.SendString(elements.ProgressBar("0"))
		}

		if status.Progress == 100 && status.Processed && status.Message == "completed" {
			c.Set("HX-Trigger", "done") // this triggers /browse/job/:uid (which is when the job is done)
			return c.SendString(elements.ProgressBar("100"))
		}
		if status.Error != nil {
			// TODO: instead of deleting the job, we should keep it in the cache and make it dismissable by the user
			opcache.DeleteUUID(jobUID)
			return c.SendString(elements.ErrorProgress(status.Error.Error(), status.GalleryElementName))
		}

		return c.SendString(elements.ProgressBar(fmt.Sprint(status.Progress)))
	})

	// this route is hit when the job is done, and we display the
	// final state (for now just displays "Installation completed")
	app.Get("/browse/job/:uid", func(c *fiber.Ctx) error {
		jobUID := strings.Clone(c.Params("uid")) // note: strings.Clone is required for multiple requests!

		status := galleryService.GetStatus(jobUID)

		galleryID := status.GalleryElementName
		opcache.DeleteUUID(jobUID)
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

		return c.SendString(elements.DoneModelProgress(galleryID, displayText, showDelete))
	})
}
