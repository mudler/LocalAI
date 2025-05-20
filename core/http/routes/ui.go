package routes

import (
	"fmt"
	"html/template"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/elements"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/utils"
	"github.com/mudler/LocalAI/core/p2p"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/xsync"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/microcosm-cc/bluemonday"
	"github.com/rs/zerolog/log"
)

type modelOpCache struct {
	status *xsync.SyncedMap[string, string]
}

func NewModelOpCache() *modelOpCache {
	return &modelOpCache{
		status: xsync.NewSyncedMap[string, string](),
	}
}

func (m *modelOpCache) Set(key string, value string) {
	m.status.Set(key, value)
}

func (m *modelOpCache) Get(key string) string {
	return m.status.Get(key)
}

func (m *modelOpCache) DeleteUUID(uuid string) {
	for _, k := range m.status.Keys() {
		if m.status.Get(k) == uuid {
			m.status.Delete(k)
		}
	}
}

func (m *modelOpCache) Map() map[string]string {
	return m.status.Map()
}

func (m *modelOpCache) Exists(key string) bool {
	return m.status.Exists(key)
}

func RegisterUIRoutes(app *fiber.App,
	cl *config.BackendConfigLoader,
	ml *model.ModelLoader,
	appConfig *config.ApplicationConfig,
	galleryService *services.GalleryService) {

	// keeps the state of models that are being installed from the UI
	var processingModels = NewModelOpCache()

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

	app.Get("/", localai.WelcomeEndpoint(appConfig, cl, ml, modelStatus))

	if p2p.IsP2PEnabled() {
		app.Get("/p2p", func(c *fiber.Ctx) error {
			summary := fiber.Map{
				"Title":   "LocalAI - P2P dashboard",
				"BaseURL": utils.BaseURL(c),
				"Version": internal.PrintableVersion(),
				//"Nodes":          p2p.GetAvailableNodes(""),
				//"FederatedNodes": p2p.GetAvailableNodes(p2p.FederatedID),
				"IsP2PEnabled": p2p.IsP2PEnabled(),
				"P2PToken":     appConfig.P2PToken,
				"NetworkID":    appConfig.P2PNetworkID,
			}

			// Render index
			return c.Render("views/p2p", summary)
		})

		/* show nodes live! */
		app.Get("/p2p/ui/workers", func(c *fiber.Ctx) error {
			return c.SendString(elements.P2PNodeBoxes(p2p.GetAvailableNodes(p2p.NetworkID(appConfig.P2PNetworkID, p2p.WorkerID))))
		})
		app.Get("/p2p/ui/workers-federation", func(c *fiber.Ctx) error {
			return c.SendString(elements.P2PNodeBoxes(p2p.GetAvailableNodes(p2p.NetworkID(appConfig.P2PNetworkID, p2p.FederatedID))))
		})

		app.Get("/p2p/ui/workers-stats", func(c *fiber.Ctx) error {
			return c.SendString(elements.P2PNodeStats(p2p.GetAvailableNodes(p2p.NetworkID(appConfig.P2PNetworkID, p2p.WorkerID))))
		})
		app.Get("/p2p/ui/workers-federation-stats", func(c *fiber.Ctx) error {
			return c.SendString(elements.P2PNodeStats(p2p.GetAvailableNodes(p2p.NetworkID(appConfig.P2PNetworkID, p2p.FederatedID))))
		})
	}

	if !appConfig.DisableGalleryEndpoint {

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
				models = gallery.GalleryModels(models).Search(term)
			}

			// Get model statuses
			processingModelsData, taskTypes := modelStatus()

			summary := fiber.Map{
				"Title":            "LocalAI - Models",
				"BaseURL":          utils.BaseURL(c),
				"Version":          internal.PrintableVersion(),
				"Models":           template.HTML(elements.ListModels(models, processingModels, galleryService)),
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
				summary["Models"] = template.HTML(elements.ListModels(models, processingModels, galleryService))
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

			return c.SendString(elements.ListModels(models, processingModels, galleryService))
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
				Galleries:        appConfig.Galleries,
			}
			go func() {
				galleryService.C <- op
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
				galleryService.C <- op
				cl.RemoveBackendConfig(galleryName)
			}()

			return c.SendString(elements.StartProgressBar(uid, "0", "Deletion"))
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

			if status.Progress == 100 {
				c.Set("HX-Trigger", "done") // this triggers /browse/job/:uid (which is when the job is done)
				return c.SendString(elements.ProgressBar("100"))
			}
			if status.Error != nil {
				// TODO: instead of deleting the job, we should keep it in the cache and make it dismissable by the user
				processingModels.DeleteUUID(jobUID)
				return c.SendString(elements.ErrorProgress(status.Error.Error(), status.GalleryModelName))
			}

			return c.SendString(elements.ProgressBar(fmt.Sprint(status.Progress)))
		})

		// this route is hit when the job is done, and we display the
		// final state (for now just displays "Installation completed")
		app.Get("/browse/job/:uid", func(c *fiber.Ctx) error {
			jobUID := strings.Clone(c.Params("uid")) // note: strings.Clone is required for multiple requests!

			status := galleryService.GetStatus(jobUID)

			galleryID := ""
			processingModels.DeleteUUID(jobUID)
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
	}

	app.Get("/talk/", func(c *fiber.Ctx) error {
		backendConfigs, _ := services.ListModels(cl, ml, config.NoFilterFn, services.SKIP_IF_CONFIGURED)

		if len(backendConfigs) == 0 {
			// If no model is available redirect to the index which suggests how to install models
			return c.Redirect(utils.BaseURL(c))
		}

		summary := fiber.Map{
			"Title":        "LocalAI - Talk",
			"BaseURL":      utils.BaseURL(c),
			"ModelsConfig": backendConfigs,
			"Model":        backendConfigs[0],
			"IsP2PEnabled": p2p.IsP2PEnabled(),
			"Version":      internal.PrintableVersion(),
		}

		// Render index
		return c.Render("views/talk", summary)
	})

	app.Get("/chat/", func(c *fiber.Ctx) error {
		backendConfigs := cl.GetAllBackendConfigs()
		modelsWithoutConfig, _ := services.ListModels(cl, ml, config.NoFilterFn, services.LOOSE_ONLY)

		if len(backendConfigs)+len(modelsWithoutConfig) == 0 {
			// If no model is available redirect to the index which suggests how to install models
			return c.Redirect(utils.BaseURL(c))
		}
		modelThatCanBeUsed := ""
		galleryConfigs := map[string]*gallery.Config{}

		for _, m := range backendConfigs {
			cfg, err := gallery.GetLocalModelConfiguration(ml.ModelPath, m.Name)
			if err != nil {
				continue
			}
			galleryConfigs[m.Name] = cfg
		}

		title := "LocalAI - Chat"

		for _, b := range backendConfigs {
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
			"ModelsConfig":        backendConfigs,
			"Model":               modelThatCanBeUsed,
			"Version":             internal.PrintableVersion(),
			"IsP2PEnabled":        p2p.IsP2PEnabled(),
		}

		// Render index
		return c.Render("views/chat", summary)
	})

	// Show the Chat page
	app.Get("/chat/:model", func(c *fiber.Ctx) error {
		backendConfigs := cl.GetAllBackendConfigs()
		modelsWithoutConfig, _ := services.ListModels(cl, ml, config.NoFilterFn, services.LOOSE_ONLY)

		galleryConfigs := map[string]*gallery.Config{}

		for _, m := range backendConfigs {
			cfg, err := gallery.GetLocalModelConfiguration(ml.ModelPath, m.Name)
			if err != nil {
				continue
			}
			galleryConfigs[m.Name] = cfg
		}

		summary := fiber.Map{
			"Title":               "LocalAI - Chat with " + c.Params("model"),
			"BaseURL":             utils.BaseURL(c),
			"ModelsConfig":        backendConfigs,
			"GalleryConfig":       galleryConfigs,
			"ModelsWithoutConfig": modelsWithoutConfig,
			"Model":               c.Params("model"),
			"Version":             internal.PrintableVersion(),
			"IsP2PEnabled":        p2p.IsP2PEnabled(),
		}

		// Render index
		return c.Render("views/chat", summary)
	})

	app.Get("/text2image/:model", func(c *fiber.Ctx) error {
		backendConfigs := cl.GetAllBackendConfigs()
		modelsWithoutConfig, _ := services.ListModels(cl, ml, config.NoFilterFn, services.LOOSE_ONLY)

		summary := fiber.Map{
			"Title":               "LocalAI - Generate images with " + c.Params("model"),
			"BaseURL":             utils.BaseURL(c),
			"ModelsConfig":        backendConfigs,
			"ModelsWithoutConfig": modelsWithoutConfig,
			"Model":               c.Params("model"),
			"Version":             internal.PrintableVersion(),
			"IsP2PEnabled":        p2p.IsP2PEnabled(),
		}

		// Render index
		return c.Render("views/text2image", summary)
	})

	app.Get("/text2image/", func(c *fiber.Ctx) error {
		backendConfigs := cl.GetAllBackendConfigs()
		modelsWithoutConfig, _ := services.ListModels(cl, ml, config.NoFilterFn, services.LOOSE_ONLY)

		if len(backendConfigs)+len(modelsWithoutConfig) == 0 {
			// If no model is available redirect to the index which suggests how to install models
			return c.Redirect(utils.BaseURL(c))
		}

		modelThatCanBeUsed := ""
		title := "LocalAI - Generate images"

		for _, b := range backendConfigs {
			if b.HasUsecases(config.FLAG_IMAGE) {
				modelThatCanBeUsed = b.Name
				title = "LocalAI - Generate images with " + modelThatCanBeUsed
				break
			}
		}

		summary := fiber.Map{
			"Title":               title,
			"BaseURL":             utils.BaseURL(c),
			"ModelsConfig":        backendConfigs,
			"ModelsWithoutConfig": modelsWithoutConfig,
			"Model":               modelThatCanBeUsed,
			"Version":             internal.PrintableVersion(),
			"IsP2PEnabled":        p2p.IsP2PEnabled(),
		}

		// Render index
		return c.Render("views/text2image", summary)
	})

	app.Get("/tts/:model", func(c *fiber.Ctx) error {
		backendConfigs := cl.GetAllBackendConfigs()
		modelsWithoutConfig, _ := services.ListModels(cl, ml, config.NoFilterFn, services.LOOSE_ONLY)

		summary := fiber.Map{
			"Title":               "LocalAI - Generate images with " + c.Params("model"),
			"BaseURL":             utils.BaseURL(c),
			"ModelsConfig":        backendConfigs,
			"ModelsWithoutConfig": modelsWithoutConfig,
			"Model":               c.Params("model"),
			"Version":             internal.PrintableVersion(),
			"IsP2PEnabled":        p2p.IsP2PEnabled(),
		}

		// Render index
		return c.Render("views/tts", summary)
	})

	app.Get("/tts/", func(c *fiber.Ctx) error {
		backendConfigs := cl.GetAllBackendConfigs()
		modelsWithoutConfig, _ := services.ListModels(cl, ml, config.NoFilterFn, services.LOOSE_ONLY)

		if len(backendConfigs)+len(modelsWithoutConfig) == 0 {
			// If no model is available redirect to the index which suggests how to install models
			return c.Redirect(utils.BaseURL(c))
		}

		modelThatCanBeUsed := ""
		title := "LocalAI - Generate audio"

		for _, b := range backendConfigs {
			if b.HasUsecases(config.FLAG_TTS) {
				modelThatCanBeUsed = b.Name
				title = "LocalAI - Generate audio with " + modelThatCanBeUsed
				break
			}
		}
		summary := fiber.Map{
			"Title":               title,
			"BaseURL":             utils.BaseURL(c),
			"ModelsConfig":        backendConfigs,
			"ModelsWithoutConfig": modelsWithoutConfig,
			"Model":               modelThatCanBeUsed,
			"IsP2PEnabled":        p2p.IsP2PEnabled(),
			"Version":             internal.PrintableVersion(),
		}

		// Render index
		return c.Render("views/tts", summary)
	})
}
