package routes

import (
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/p2p"
	"github.com/mudler/LocalAI/core/services"
	"github.com/rs/zerolog/log"
)

// RegisterUIAPIRoutes registers JSON API routes for the web UI
func RegisterUIAPIRoutes(app *fiber.App, cl *config.ModelConfigLoader, appConfig *config.ApplicationConfig, galleryService *services.GalleryService, opcache *services.OpCache) {

	// Operations API - Get all current operations (models + backends)
	app.Get("/api/operations", func(c *fiber.Ctx) error {
		processingData, taskTypes := opcache.GetStatus()

		operations := []fiber.Map{}
		for galleryID, jobID := range processingData {
			taskType := "installation"
			if tt, ok := taskTypes[galleryID]; ok {
				taskType = tt
			}

			status := galleryService.GetStatus(jobID)
			progress := 0
			isDeletion := false
			isQueued := false
			message := ""

			if status != nil {
				// Skip completed operations
				if status.Processed {
					continue
				}

				progress = int(status.Progress)
				isDeletion = status.Deletion
				message = status.Message
				if isDeletion {
					taskType = "deletion"
				}
			} else {
				// Job is queued but hasn't started
				isQueued = true
				message = "Operation queued"
			}

			// Determine if it's a model or backend
			isBackend := false
			backends, _ := gallery.AvailableBackends(appConfig.BackendGalleries, appConfig.SystemState)
			for _, b := range backends {
				backendID := fmt.Sprintf("%s@%s", b.Gallery.Name, b.Name)
				if backendID == galleryID || b.Name == galleryID {
					isBackend = true
					break
				}
			}

			// Extract display name (remove repo prefix if exists)
			displayName := galleryID
			if strings.Contains(galleryID, "@") {
				parts := strings.Split(galleryID, "@")
				if len(parts) > 1 {
					displayName = parts[1]
				}
			}

			operations = append(operations, fiber.Map{
				"id":         galleryID,
				"name":       displayName,
				"fullName":   galleryID,
				"jobID":      jobID,
				"progress":   progress,
				"taskType":   taskType,
				"isDeletion": isDeletion,
				"isBackend":  isBackend,
				"isQueued":   isQueued,
				"message":    message,
			})
		}

		// Sort operations by progress (ascending), then by ID for stable display order
		sort.Slice(operations, func(i, j int) bool {
			progressI := operations[i]["progress"].(int)
			progressJ := operations[j]["progress"].(int)

			// Primary sort by progress
			if progressI != progressJ {
				return progressI < progressJ
			}

			// Secondary sort by ID for stability when progress is the same
			return operations[i]["id"].(string) < operations[j]["id"].(string)
		})

		return c.JSON(fiber.Map{
			"operations": operations,
		})
	})

	// Model Gallery APIs
	app.Get("/api/models", func(c *fiber.Ctx) error {
		term := c.Query("term")
		page := c.Query("page", "1")
		items := c.Query("items", "21")

		models, err := gallery.AvailableGalleryModels(appConfig.Galleries, appConfig.SystemState)
		if err != nil {
			log.Error().Err(err).Msg("could not list models from galleries")
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": err.Error(),
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

		pageNum, err := strconv.Atoi(page)
		if err != nil || pageNum < 1 {
			pageNum = 1
		}

		itemsNum, err := strconv.Atoi(items)
		if err != nil || itemsNum < 1 {
			itemsNum = 21
		}

		totalPages := int(math.Ceil(float64(len(models)) / float64(itemsNum)))
		totalModels := len(models)

		if pageNum > 0 {
			models = models.Paginate(pageNum, itemsNum)
		}

		// Convert models to JSON-friendly format and deduplicate by ID
		modelsJSON := make([]fiber.Map, 0, len(models))
		seenIDs := make(map[string]bool)

		for _, m := range models {
			modelID := m.ID()

			// Skip duplicate IDs to prevent Alpine.js x-for errors
			if seenIDs[modelID] {
				log.Debug().Msgf("Skipping duplicate model ID: %s", modelID)
				continue
			}
			seenIDs[modelID] = true

			currentlyProcessing := opcache.Exists(modelID)
			jobID := ""
			isDeletionOp := false
			if currentlyProcessing {
				jobID = opcache.Get(modelID)
				status := galleryService.GetStatus(jobID)
				if status != nil && status.Deletion {
					isDeletionOp = true
				}
			}

			_, trustRemoteCodeExists := m.Overrides["trust_remote_code"]

			modelsJSON = append(modelsJSON, fiber.Map{
				"id":              modelID,
				"name":            m.Name,
				"description":     m.Description,
				"icon":            m.Icon,
				"license":         m.License,
				"urls":            m.URLs,
				"tags":            m.Tags,
				"gallery":         m.Gallery.Name,
				"installed":       m.Installed,
				"processing":      currentlyProcessing,
				"jobID":           jobID,
				"isDeletion":      isDeletionOp,
				"trustRemoteCode": trustRemoteCodeExists,
			})
		}

		prevPage := pageNum - 1
		nextPage := pageNum + 1
		if prevPage < 1 {
			prevPage = 1
		}
		if nextPage > totalPages {
			nextPage = totalPages
		}

		return c.JSON(fiber.Map{
			"models":           modelsJSON,
			"repositories":     appConfig.Galleries,
			"allTags":          tags,
			"processingModels": processingModelsData,
			"taskTypes":        taskTypes,
			"availableModels":  totalModels,
			"currentPage":      pageNum,
			"totalPages":       totalPages,
			"prevPage":         prevPage,
			"nextPage":         nextPage,
		})
	})

	app.Post("/api/models/install/:id", func(c *fiber.Ctx) error {
		galleryID := strings.Clone(c.Params("id"))
		// URL decode the gallery ID (e.g., "localai%40model" -> "localai@model")
		galleryID, err := url.QueryUnescape(galleryID)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "invalid model ID",
			})
		}
		log.Debug().Msgf("API job submitted to install: %+v\n", galleryID)

		id, err := uuid.NewUUID()
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": err.Error(),
			})
		}

		uid := id.String()
		opcache.Set(galleryID, uid)

		op := services.GalleryOp[gallery.GalleryModel]{
			ID:                 uid,
			GalleryElementName: galleryID,
			Galleries:          appConfig.Galleries,
			BackendGalleries:   appConfig.BackendGalleries,
		}
		go func() {
			galleryService.ModelGalleryChannel <- op
		}()

		return c.JSON(fiber.Map{
			"jobID":   uid,
			"message": "Installation started",
		})
	})

	app.Post("/api/models/delete/:id", func(c *fiber.Ctx) error {
		galleryID := strings.Clone(c.Params("id"))
		// URL decode the gallery ID
		galleryID, err := url.QueryUnescape(galleryID)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "invalid model ID",
			})
		}
		log.Debug().Msgf("API job submitted to delete: %+v\n", galleryID)

		var galleryName = galleryID
		if strings.Contains(galleryID, "@") {
			galleryName = strings.Split(galleryID, "@")[1]
		}

		id, err := uuid.NewUUID()
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": err.Error(),
			})
		}

		uid := id.String()

		opcache.Set(galleryID, uid)

		op := services.GalleryOp[gallery.GalleryModel]{
			ID:                 uid,
			Delete:             true,
			GalleryElementName: galleryName,
			Galleries:          appConfig.Galleries,
			BackendGalleries:   appConfig.BackendGalleries,
		}
		go func() {
			galleryService.ModelGalleryChannel <- op
			cl.RemoveModelConfig(galleryName)
		}()

		return c.JSON(fiber.Map{
			"jobID":   uid,
			"message": "Deletion started",
		})
	})

	app.Post("/api/models/config/:id", func(c *fiber.Ctx) error {
		galleryID := strings.Clone(c.Params("id"))
		// URL decode the gallery ID
		galleryID, err := url.QueryUnescape(galleryID)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "invalid model ID",
			})
		}
		log.Debug().Msgf("API job submitted to get config for: %+v\n", galleryID)

		models, err := gallery.AvailableGalleryModels(appConfig.Galleries, appConfig.SystemState)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": err.Error(),
			})
		}

		model := gallery.FindGalleryElement(models, galleryID)
		if model == nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "model not found",
			})
		}

		config, err := gallery.GetGalleryConfigFromURL[gallery.ModelConfig](model.URL, appConfig.SystemState.Model.ModelsPath)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": err.Error(),
			})
		}

		_, err = gallery.InstallModel(appConfig.SystemState, model.Name, &config, model.Overrides, nil, false)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": err.Error(),
			})
		}

		return c.JSON(fiber.Map{
			"message": "Configuration file saved",
		})
	})

	app.Get("/api/models/job/:uid", func(c *fiber.Ctx) error {
		jobUID := strings.Clone(c.Params("uid"))

		status := galleryService.GetStatus(jobUID)
		if status == nil {
			// Job is queued but hasn't started processing yet
			return c.JSON(fiber.Map{
				"progress":           0,
				"message":            "Operation queued",
				"galleryElementName": "",
				"processed":          false,
				"deletion":           false,
				"queued":             true,
			})
		}

		response := fiber.Map{
			"progress":           status.Progress,
			"message":            status.Message,
			"galleryElementName": status.GalleryElementName,
			"processed":          status.Processed,
			"deletion":           status.Deletion,
			"queued":             false,
		}

		if status.Error != nil {
			response["error"] = status.Error.Error()
		}

		if status.Progress == 100 && status.Processed && status.Message == "completed" {
			opcache.DeleteUUID(jobUID)
			response["completed"] = true
		}

		return c.JSON(response)
	})

	// Backend Gallery APIs
	app.Get("/api/backends", func(c *fiber.Ctx) error {
		term := c.Query("term")
		page := c.Query("page", "1")
		items := c.Query("items", "21")

		backends, err := gallery.AvailableBackends(appConfig.BackendGalleries, appConfig.SystemState)
		if err != nil {
			log.Error().Err(err).Msg("could not list backends from galleries")
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": err.Error(),
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

		pageNum, err := strconv.Atoi(page)
		if err != nil || pageNum < 1 {
			pageNum = 1
		}

		itemsNum, err := strconv.Atoi(items)
		if err != nil || itemsNum < 1 {
			itemsNum = 21
		}

		totalPages := int(math.Ceil(float64(len(backends)) / float64(itemsNum)))
		totalBackends := len(backends)

		if pageNum > 0 {
			backends = backends.Paginate(pageNum, itemsNum)
		}

		// Convert backends to JSON-friendly format and deduplicate by ID
		backendsJSON := make([]fiber.Map, 0, len(backends))
		seenBackendIDs := make(map[string]bool)

		for _, b := range backends {
			backendID := b.ID()

			// Skip duplicate IDs to prevent Alpine.js x-for errors
			if seenBackendIDs[backendID] {
				log.Debug().Msgf("Skipping duplicate backend ID: %s", backendID)
				continue
			}
			seenBackendIDs[backendID] = true

			currentlyProcessing := opcache.Exists(backendID)
			jobID := ""
			isDeletionOp := false
			if currentlyProcessing {
				jobID = opcache.Get(backendID)
				status := galleryService.GetStatus(jobID)
				if status != nil && status.Deletion {
					isDeletionOp = true
				}
			}

			backendsJSON = append(backendsJSON, fiber.Map{
				"id":          backendID,
				"name":        b.Name,
				"description": b.Description,
				"icon":        b.Icon,
				"license":     b.License,
				"urls":        b.URLs,
				"tags":        b.Tags,
				"gallery":     b.Gallery.Name,
				"installed":   b.Installed,
				"processing":  currentlyProcessing,
				"jobID":       jobID,
				"isDeletion":  isDeletionOp,
			})
		}

		prevPage := pageNum - 1
		nextPage := pageNum + 1
		if prevPage < 1 {
			prevPage = 1
		}
		if nextPage > totalPages {
			nextPage = totalPages
		}

		return c.JSON(fiber.Map{
			"backends":           backendsJSON,
			"repositories":       appConfig.BackendGalleries,
			"allTags":            tags,
			"processingBackends": processingBackendsData,
			"taskTypes":          taskTypes,
			"availableBackends":  totalBackends,
			"currentPage":        pageNum,
			"totalPages":         totalPages,
			"prevPage":           prevPage,
			"nextPage":           nextPage,
		})
	})

	app.Post("/api/backends/install/:id", func(c *fiber.Ctx) error {
		backendID := strings.Clone(c.Params("id"))
		// URL decode the backend ID
		backendID, err := url.QueryUnescape(backendID)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "invalid backend ID",
			})
		}
		log.Debug().Msgf("API job submitted to install backend: %+v\n", backendID)

		id, err := uuid.NewUUID()
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": err.Error(),
			})
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

		return c.JSON(fiber.Map{
			"jobID":   uid,
			"message": "Backend installation started",
		})
	})

	app.Post("/api/backends/delete/:id", func(c *fiber.Ctx) error {
		backendID := strings.Clone(c.Params("id"))
		// URL decode the backend ID
		backendID, err := url.QueryUnescape(backendID)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "invalid backend ID",
			})
		}
		log.Debug().Msgf("API job submitted to delete backend: %+v\n", backendID)

		var backendName = backendID
		if strings.Contains(backendID, "@") {
			backendName = strings.Split(backendID, "@")[1]
		}

		id, err := uuid.NewUUID()
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": err.Error(),
			})
		}

		uid := id.String()

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

		return c.JSON(fiber.Map{
			"jobID":   uid,
			"message": "Backend deletion started",
		})
	})

	app.Get("/api/backends/job/:uid", func(c *fiber.Ctx) error {
		jobUID := strings.Clone(c.Params("uid"))

		status := galleryService.GetStatus(jobUID)
		if status == nil {
			// Job is queued but hasn't started processing yet
			return c.JSON(fiber.Map{
				"progress":           0,
				"message":            "Operation queued",
				"galleryElementName": "",
				"processed":          false,
				"deletion":           false,
				"queued":             true,
			})
		}

		response := fiber.Map{
			"progress":           status.Progress,
			"message":            status.Message,
			"galleryElementName": status.GalleryElementName,
			"processed":          status.Processed,
			"deletion":           status.Deletion,
			"queued":             false,
		}

		if status.Error != nil {
			response["error"] = status.Error.Error()
		}

		if status.Progress == 100 && status.Processed && status.Message == "completed" {
			opcache.DeleteUUID(jobUID)
			response["completed"] = true
		}

		return c.JSON(response)
	})

	// P2P APIs
	app.Get("/api/p2p/workers", func(c *fiber.Ctx) error {
		nodes := p2p.GetAvailableNodes(p2p.NetworkID(appConfig.P2PNetworkID, p2p.WorkerID))

		nodesJSON := make([]fiber.Map, 0, len(nodes))
		for _, n := range nodes {
			nodesJSON = append(nodesJSON, fiber.Map{
				"name":          n.Name,
				"id":            n.ID,
				"tunnelAddress": n.TunnelAddress,
				"serviceID":     n.ServiceID,
				"lastSeen":      n.LastSeen,
				"isOnline":      n.IsOnline(),
			})
		}

		return c.JSON(fiber.Map{
			"nodes": nodesJSON,
		})
	})

	app.Get("/api/p2p/federation", func(c *fiber.Ctx) error {
		nodes := p2p.GetAvailableNodes(p2p.NetworkID(appConfig.P2PNetworkID, p2p.FederatedID))

		nodesJSON := make([]fiber.Map, 0, len(nodes))
		for _, n := range nodes {
			nodesJSON = append(nodesJSON, fiber.Map{
				"name":          n.Name,
				"id":            n.ID,
				"tunnelAddress": n.TunnelAddress,
				"serviceID":     n.ServiceID,
				"lastSeen":      n.LastSeen,
				"isOnline":      n.IsOnline(),
			})
		}

		return c.JSON(fiber.Map{
			"nodes": nodesJSON,
		})
	})

	app.Get("/api/p2p/stats", func(c *fiber.Ctx) error {
		workerNodes := p2p.GetAvailableNodes(p2p.NetworkID(appConfig.P2PNetworkID, p2p.WorkerID))
		federatedNodes := p2p.GetAvailableNodes(p2p.NetworkID(appConfig.P2PNetworkID, p2p.FederatedID))

		workersOnline := 0
		for _, n := range workerNodes {
			if n.IsOnline() {
				workersOnline++
			}
		}

		federatedOnline := 0
		for _, n := range federatedNodes {
			if n.IsOnline() {
				federatedOnline++
			}
		}

		return c.JSON(fiber.Map{
			"workers": fiber.Map{
				"online": workersOnline,
				"total":  len(workerNodes),
			},
			"federated": fiber.Map{
				"online": federatedOnline,
				"total":  len(federatedNodes),
			},
		})
	})
}
