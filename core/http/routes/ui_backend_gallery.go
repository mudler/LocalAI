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

func registerBackendGalleryRoutes(app *fiber.App, appConfig *config.ApplicationConfig, galleryService *services.GalleryService, opcache *services.OpCache) {

	// Show the Backends page (all backends)
	app.Get("/browse/backends", func(c *fiber.Ctx) error {
		term := c.Query("term")
		page := c.Query("page")
		items := c.Query("items")

		backends, err := gallery.AvailableBackends(appConfig.BackendGalleries, appConfig.BackendsPath)
		if err != nil {
			log.Error().Err(err).Msg("could not list backends from galleries")
			return c.Status(fiber.StatusInternalServerError).Render("views/error", fiber.Map{
				"Title":        "LocalAI - Backends",
				"BaseURL":      utils.BaseURL(c),
				"Version":      internal.PrintableVersion(),
				"ErrorCode":    "500",
				"ErrorMessage": err.Error(),
			})
		}

		// Get all available tags
		allTags := map[string]struct{}{}
		tags := []string{}
		for _, b := range backends {
			for _, t := range b.Tags {
				allTags[t] = struct{}{}
			}
		}
		for t := range allTags {
			tags = append(tags, t)
		}
		sort.Strings(tags)

		if term != "" {
			backends = gallery.GalleryElements[*gallery.GalleryBackend](backends).Search(term)
		}

		// Get backend statuses
		processingBackendsData, taskTypes := opcache.GetStatus()

		summary := fiber.Map{
			"Title":              "LocalAI - Backends",
			"BaseURL":            utils.BaseURL(c),
			"Version":            internal.PrintableVersion(),
			"Backends":           template.HTML(elements.ListBackends(backends, opcache, galleryService)),
			"Repositories":       appConfig.BackendGalleries,
			"AllTags":            tags,
			"ProcessingBackends": processingBackendsData,
			"AvailableBackends":  len(backends),
			"TaskTypes":          taskTypes,
			"IsP2PEnabled":       p2p.IsP2PEnabled(),
		}

		if page == "" {
			page = "1"
		}

		if page != "" {
			// return a subset of the backends
			pageNum, err := strconv.Atoi(page)
			if err != nil {
				return c.Status(fiber.StatusBadRequest).SendString("Invalid page number")
			}

			if pageNum == 0 {
				return c.Render("views/backends", summary)
			}

			itemsNum, err := strconv.Atoi(items)
			if err != nil {
				itemsNum = 21
			}

			totalPages := int(math.Ceil(float64(len(backends)) / float64(itemsNum)))

			backends = backends.Paginate(pageNum, itemsNum)

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
			summary["Backends"] = template.HTML(elements.ListBackends(backends, opcache, galleryService))
		}

		// Render index
		return c.Render("views/backends", summary)
	})

	// Show the backends, filtered from the user input
	app.Post("/browse/search/backends", func(c *fiber.Ctx) error {
		page := c.Query("page")
		items := c.Query("items")

		form := struct {
			Search string `form:"search"`
		}{}
		if err := c.BodyParser(&form); err != nil {
			return c.Status(fiber.StatusBadRequest).SendString(bluemonday.StrictPolicy().Sanitize(err.Error()))
		}

		backends, _ := gallery.AvailableBackends(appConfig.BackendGalleries, appConfig.BackendsPath)

		if page != "" {
			// return a subset of the backends
			pageNum, err := strconv.Atoi(page)
			if err != nil {
				return c.Status(fiber.StatusBadRequest).SendString("Invalid page number")
			}

			itemsNum, err := strconv.Atoi(items)
			if err != nil {
				itemsNum = 21
			}

			backends = backends.Paginate(pageNum, itemsNum)
		}

		if form.Search != "" {
			backends = backends.Search(form.Search)
		}

		return c.SendString(elements.ListBackends(backends, opcache, galleryService))
	})

	// Install backend route
	app.Post("/browse/install/backend/:id", func(c *fiber.Ctx) error {
		backendID := strings.Clone(c.Params("id")) // note: strings.Clone is required for multiple requests!
		log.Debug().Msgf("UI job submitted to install backend: %+v\n", backendID)

		id, err := uuid.NewUUID()
		if err != nil {
			return err
		}

		uid := id.String()

		opcache.Set(backendID, uid)

		op := services.GalleryOp[gallery.GalleryBackend]{
			ID:                 uid,
			GalleryElementName: backendID,
			Galleries:          appConfig.BackendGalleries,
		}
		go func() {
			galleryService.BackendGalleryChannel <- op
		}()

		return c.SendString(elements.StartBackendProgressBar(uid, "0", "Backend Installation"))
	})

	// Delete backend route
	app.Post("/browse/delete/backend/:id", func(c *fiber.Ctx) error {
		backendID := strings.Clone(c.Params("id")) // note: strings.Clone is required for multiple requests!
		log.Debug().Msgf("UI job submitted to delete backend: %+v\n", backendID)
		var backendName = backendID
		if strings.Contains(backendID, "@") {
			// TODO: this is ugly workaround - we should handle this consistently across the codebase
			backendName = strings.Split(backendID, "@")[1]
		}

		id, err := uuid.NewUUID()
		if err != nil {
			return err
		}

		uid := id.String()

		opcache.Set(backendName, uid)
		opcache.Set(backendID, uid)

		op := services.GalleryOp[gallery.GalleryBackend]{
			ID:                 uid,
			Delete:             true,
			GalleryElementName: backendName,
			Galleries:          appConfig.BackendGalleries,
		}
		go func() {
			galleryService.BackendGalleryChannel <- op
		}()

		return c.SendString(elements.StartBackendProgressBar(uid, "0", "Backend Deletion"))
	})

	// Display the job current progress status
	app.Get("/browse/backend/job/progress/:uid", func(c *fiber.Ctx) error {
		jobUID := strings.Clone(c.Params("uid")) // note: strings.Clone is required for multiple requests!

		status := galleryService.GetStatus(jobUID)
		if status == nil {
			return c.SendString(elements.ProgressBar("0"))
		}

		if status.Progress == 100 && status.Processed && status.Message == "completed" {
			c.Set("HX-Trigger", "done") // this triggers /browse/backend/job/:uid
			return c.SendString(elements.ProgressBar("100"))
		}
		if status.Error != nil {
			opcache.DeleteUUID(jobUID)
			return c.SendString(elements.ErrorProgress(status.Error.Error(), status.GalleryElementName))
		}

		return c.SendString(elements.ProgressBar(fmt.Sprint(status.Progress)))
	})

	// Job completion route
	app.Get("/browse/backend/job/:uid", func(c *fiber.Ctx) error {
		jobUID := strings.Clone(c.Params("uid")) // note: strings.Clone is required for multiple requests!

		status := galleryService.GetStatus(jobUID)

		backendID := status.GalleryElementName
		opcache.DeleteUUID(jobUID)
		if backendID == "" {
			log.Debug().Msgf("no processing backend found for job: %+v\n", jobUID)
		}

		log.Debug().Msgf("JOB finished: %+v\n", status)
		showDelete := true
		displayText := "Backend Installation completed"
		if status.Deletion {
			showDelete = false
			displayText = "Backend Deletion completed"
		}

		return c.SendString(elements.DoneBackendProgress(backendID, displayText, showDelete))
	})
}
