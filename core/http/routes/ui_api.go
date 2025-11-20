package routes

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/p2p"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/rs/zerolog/log"
)

// RegisterUIAPIRoutes registers JSON API routes for the web UI
func RegisterUIAPIRoutes(app *echo.Echo, cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig, galleryService *services.GalleryService, opcache *services.OpCache, applicationInstance *application.Application) {

	// Operations API - Get all current operations (models + backends)
	app.GET("/api/operations", func(c echo.Context) error {
		processingData, taskTypes := opcache.GetStatus()

		operations := []map[string]interface{}{}
		for galleryID, jobID := range processingData {
			taskType := "installation"
			if tt, ok := taskTypes[galleryID]; ok {
				taskType = tt
			}

			status := galleryService.GetStatus(jobID)
			progress := 0
			isDeletion := false
			isQueued := false
			isCancelled := false
			isCancellable := false
			message := ""

			if status != nil {
				// Skip completed operations (unless cancelled and not yet cleaned up)
				if status.Processed && !status.Cancelled {
					continue
				}
				// Skip cancelled operations that are processed (they're done, no need to show)
				if status.Processed && status.Cancelled {
					continue
				}

				progress = int(status.Progress)
				isDeletion = status.Deletion
				isCancelled = status.Cancelled
				isCancellable = status.Cancellable
				message = status.Message
				if isDeletion {
					taskType = "deletion"
				}
				if isCancelled {
					taskType = "cancelled"
				}
			} else {
				// Job is queued but hasn't started
				isQueued = true
				isCancellable = true
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

			operations = append(operations, map[string]interface{}{
				"id":          galleryID,
				"name":        displayName,
				"fullName":    galleryID,
				"jobID":       jobID,
				"progress":    progress,
				"taskType":    taskType,
				"isDeletion":  isDeletion,
				"isBackend":   isBackend,
				"isQueued":    isQueued,
				"isCancelled": isCancelled,
				"cancellable": isCancellable,
				"message":     message,
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

		return c.JSON(200, map[string]interface{}{
			"operations": operations,
		})
	})

	// Cancel operation endpoint
	app.POST("/api/operations/:jobID/cancel", func(c echo.Context) error {
		jobID := c.Param("jobID")
		log.Debug().Msgf("API request to cancel operation: %s", jobID)

		err := galleryService.CancelOperation(jobID)
		if err != nil {
			log.Error().Err(err).Msgf("Failed to cancel operation: %s", jobID)
			return c.JSON(http.StatusBadRequest, map[string]interface{}{
				"error": err.Error(),
			})
		}

		// Clean up opcache for cancelled operation
		opcache.DeleteUUID(jobID)

		return c.JSON(200, map[string]interface{}{
			"success": true,
			"message": "Operation cancelled",
		})
	})

	// Model Gallery APIs
	app.GET("/api/models", func(c echo.Context) error {
		term := c.QueryParam("term")
		page := c.QueryParam("page")
		if page == "" {
			page = "1"
		}
		items := c.QueryParam("items")
		if items == "" {
			items = "21"
		}

		models, err := gallery.AvailableGalleryModels(appConfig.Galleries, appConfig.SystemState)
		if err != nil {
			log.Error().Err(err).Msg("could not list models from galleries")
			return c.JSON(http.StatusInternalServerError, map[string]interface{}{
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
		modelsJSON := make([]map[string]interface{}, 0, len(models))
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

			modelsJSON = append(modelsJSON, map[string]interface{}{
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
				"additionalFiles": m.AdditionalFiles,
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

		// Calculate installed models count (models with configs + models without configs)
		modelConfigs := cl.GetAllModelsConfigs()
		modelsWithoutConfig, _ := services.ListModels(cl, ml, config.NoFilterFn, services.LOOSE_ONLY)
		installedModelsCount := len(modelConfigs) + len(modelsWithoutConfig)

		return c.JSON(200, map[string]interface{}{
			"models":           modelsJSON,
			"repositories":     appConfig.Galleries,
			"allTags":          tags,
			"processingModels": processingModelsData,
			"taskTypes":        taskTypes,
			"availableModels":  totalModels,
			"installedModels":  installedModelsCount,
			"currentPage":      pageNum,
			"totalPages":       totalPages,
			"prevPage":         prevPage,
			"nextPage":         nextPage,
		})
	})

	app.POST("/api/models/install/:id", func(c echo.Context) error {
		galleryID := c.Param("id")
		// URL decode the gallery ID (e.g., "localai%40model" -> "localai@model")
		galleryID, err := url.QueryUnescape(galleryID)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]interface{}{
				"error": "invalid model ID",
			})
		}
		log.Debug().Msgf("API job submitted to install: %+v\n", galleryID)

		id, err := uuid.NewUUID()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]interface{}{
				"error": err.Error(),
			})
		}

		uid := id.String()
		opcache.Set(galleryID, uid)

		ctx, cancelFunc := context.WithCancel(context.Background())
		op := services.GalleryOp[gallery.GalleryModel, gallery.ModelConfig]{
			ID:                 uid,
			GalleryElementName: galleryID,
			Galleries:          appConfig.Galleries,
			BackendGalleries:   appConfig.BackendGalleries,
			Context:            ctx,
			CancelFunc:         cancelFunc,
		}
		// Store cancellation function immediately so queued operations can be cancelled
		galleryService.StoreCancellation(uid, cancelFunc)
		go func() {
			galleryService.ModelGalleryChannel <- op
		}()

		return c.JSON(200, map[string]interface{}{
			"jobID":   uid,
			"message": "Installation started",
		})
	})

	app.POST("/api/models/delete/:id", func(c echo.Context) error {
		galleryID := c.Param("id")
		// URL decode the gallery ID
		galleryID, err := url.QueryUnescape(galleryID)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]interface{}{
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
			return c.JSON(http.StatusInternalServerError, map[string]interface{}{
				"error": err.Error(),
			})
		}

		uid := id.String()

		opcache.Set(galleryID, uid)

		ctx, cancelFunc := context.WithCancel(context.Background())
		op := services.GalleryOp[gallery.GalleryModel, gallery.ModelConfig]{
			ID:                 uid,
			Delete:             true,
			GalleryElementName: galleryName,
			Galleries:          appConfig.Galleries,
			BackendGalleries:   appConfig.BackendGalleries,
			Context:            ctx,
			CancelFunc:         cancelFunc,
		}
		// Store cancellation function immediately so queued operations can be cancelled
		galleryService.StoreCancellation(uid, cancelFunc)
		go func() {
			galleryService.ModelGalleryChannel <- op
			cl.RemoveModelConfig(galleryName)
		}()

		return c.JSON(200, map[string]interface{}{
			"jobID":   uid,
			"message": "Deletion started",
		})
	})

	app.POST("/api/models/config/:id", func(c echo.Context) error {
		galleryID := c.Param("id")
		// URL decode the gallery ID
		galleryID, err := url.QueryUnescape(galleryID)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]interface{}{
				"error": "invalid model ID",
			})
		}
		log.Debug().Msgf("API job submitted to get config for: %+v\n", galleryID)

		models, err := gallery.AvailableGalleryModels(appConfig.Galleries, appConfig.SystemState)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]interface{}{
				"error": err.Error(),
			})
		}

		model := gallery.FindGalleryElement(models, galleryID)
		if model == nil {
			return c.JSON(http.StatusNotFound, map[string]interface{}{
				"error": "model not found",
			})
		}

		config, err := gallery.GetGalleryConfigFromURL[gallery.ModelConfig](model.URL, appConfig.SystemState.Model.ModelsPath)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]interface{}{
				"error": err.Error(),
			})
		}

		_, err = gallery.InstallModel(context.Background(), appConfig.SystemState, model.Name, &config, model.Overrides, nil, false)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]interface{}{
				"error": err.Error(),
			})
		}

		return c.JSON(200, map[string]interface{}{
			"message": "Configuration file saved",
		})
	})

	app.GET("/api/models/job/:uid", func(c echo.Context) error {
		jobUID := c.Param("uid")

		status := galleryService.GetStatus(jobUID)
		if status == nil {
			// Job is queued but hasn't started processing yet
			return c.JSON(200, map[string]interface{}{
				"progress":           0,
				"message":            "Operation queued",
				"galleryElementName": "",
				"processed":          false,
				"deletion":           false,
				"queued":             true,
			})
		}

		response := map[string]interface{}{
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

		return c.JSON(200, response)
	})

	// Backend Gallery APIs
	app.GET("/api/backends", func(c echo.Context) error {
		term := c.QueryParam("term")
		page := c.QueryParam("page")
		if page == "" {
			page = "1"
		}
		items := c.QueryParam("items")
		if items == "" {
			items = "21"
		}

		backends, err := gallery.AvailableBackends(appConfig.BackendGalleries, appConfig.SystemState)
		if err != nil {
			log.Error().Err(err).Msg("could not list backends from galleries")
			return c.JSON(http.StatusInternalServerError, map[string]interface{}{
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
		backendsJSON := make([]map[string]interface{}, 0, len(backends))
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

			backendsJSON = append(backendsJSON, map[string]interface{}{
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

		// Calculate installed backends count
		installedBackends, err := gallery.ListSystemBackends(appConfig.SystemState)
		installedBackendsCount := 0
		if err == nil {
			installedBackendsCount = len(installedBackends)
		}

		return c.JSON(200, map[string]interface{}{
			"backends":           backendsJSON,
			"repositories":       appConfig.BackendGalleries,
			"allTags":            tags,
			"processingBackends": processingBackendsData,
			"taskTypes":          taskTypes,
			"availableBackends":  totalBackends,
			"installedBackends":  installedBackendsCount,
			"currentPage":        pageNum,
			"totalPages":         totalPages,
			"prevPage":           prevPage,
			"nextPage":           nextPage,
		})
	})

	app.POST("/api/backends/install/:id", func(c echo.Context) error {
		backendID := c.Param("id")
		// URL decode the backend ID
		backendID, err := url.QueryUnescape(backendID)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]interface{}{
				"error": "invalid backend ID",
			})
		}
		log.Debug().Msgf("API job submitted to install backend: %+v\n", backendID)

		id, err := uuid.NewUUID()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]interface{}{
				"error": err.Error(),
			})
		}

		uid := id.String()
		opcache.Set(backendID, uid)

		ctx, cancelFunc := context.WithCancel(context.Background())
		op := services.GalleryOp[gallery.GalleryBackend, any]{
			ID:                 uid,
			GalleryElementName: backendID,
			Galleries:          appConfig.BackendGalleries,
			Context:            ctx,
			CancelFunc:         cancelFunc,
		}
		// Store cancellation function immediately so queued operations can be cancelled
		galleryService.StoreCancellation(uid, cancelFunc)
		go func() {
			galleryService.BackendGalleryChannel <- op
		}()

		return c.JSON(200, map[string]interface{}{
			"jobID":   uid,
			"message": "Backend installation started",
		})
	})

	app.POST("/api/backends/delete/:id", func(c echo.Context) error {
		backendID := c.Param("id")
		// URL decode the backend ID
		backendID, err := url.QueryUnescape(backendID)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]interface{}{
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
			return c.JSON(http.StatusInternalServerError, map[string]interface{}{
				"error": err.Error(),
			})
		}

		uid := id.String()

		opcache.Set(backendID, uid)

		ctx, cancelFunc := context.WithCancel(context.Background())
		op := services.GalleryOp[gallery.GalleryBackend, any]{
			ID:                 uid,
			Delete:             true,
			GalleryElementName: backendName,
			Galleries:          appConfig.BackendGalleries,
			Context:            ctx,
			CancelFunc:         cancelFunc,
		}
		// Store cancellation function immediately so queued operations can be cancelled
		galleryService.StoreCancellation(uid, cancelFunc)
		go func() {
			galleryService.BackendGalleryChannel <- op
		}()

		return c.JSON(200, map[string]interface{}{
			"jobID":   uid,
			"message": "Backend deletion started",
		})
	})

	app.GET("/api/backends/job/:uid", func(c echo.Context) error {
		jobUID := c.Param("uid")

		status := galleryService.GetStatus(jobUID)
		if status == nil {
			// Job is queued but hasn't started processing yet
			return c.JSON(200, map[string]interface{}{
				"progress":           0,
				"message":            "Operation queued",
				"galleryElementName": "",
				"processed":          false,
				"deletion":           false,
				"queued":             true,
			})
		}

		response := map[string]interface{}{
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

		return c.JSON(200, response)
	})

	// System Backend Deletion API (for installed backends on index page)
	app.POST("/api/backends/system/delete/:name", func(c echo.Context) error {
		backendName := c.Param("name")
		// URL decode the backend name
		backendName, err := url.QueryUnescape(backendName)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]interface{}{
				"error": "invalid backend name",
			})
		}
		log.Debug().Msgf("API request to delete system backend: %+v\n", backendName)

		// Use the gallery package to delete the backend
		if err := gallery.DeleteBackendFromSystem(appConfig.SystemState, backendName); err != nil {
			log.Error().Err(err).Msgf("Failed to delete backend: %s", backendName)
			return c.JSON(http.StatusInternalServerError, map[string]interface{}{
				"error": err.Error(),
			})
		}

		return c.JSON(200, map[string]interface{}{
			"success": true,
			"message": "Backend deleted successfully",
		})
	})

	// P2P APIs
	app.GET("/api/p2p/workers", func(c echo.Context) error {
		nodes := p2p.GetAvailableNodes(p2p.NetworkID(appConfig.P2PNetworkID, p2p.WorkerID))

		nodesJSON := make([]map[string]interface{}, 0, len(nodes))
		for _, n := range nodes {
			nodesJSON = append(nodesJSON, map[string]interface{}{
				"name":          n.Name,
				"id":            n.ID,
				"tunnelAddress": n.TunnelAddress,
				"serviceID":     n.ServiceID,
				"lastSeen":      n.LastSeen,
				"isOnline":      n.IsOnline(),
			})
		}

		return c.JSON(200, map[string]interface{}{
			"nodes": nodesJSON,
		})
	})

	app.GET("/api/p2p/federation", func(c echo.Context) error {
		nodes := p2p.GetAvailableNodes(p2p.NetworkID(appConfig.P2PNetworkID, p2p.FederatedID))

		nodesJSON := make([]map[string]interface{}, 0, len(nodes))
		for _, n := range nodes {
			nodesJSON = append(nodesJSON, map[string]interface{}{
				"name":          n.Name,
				"id":            n.ID,
				"tunnelAddress": n.TunnelAddress,
				"serviceID":     n.ServiceID,
				"lastSeen":      n.LastSeen,
				"isOnline":      n.IsOnline(),
			})
		}

		return c.JSON(200, map[string]interface{}{
			"nodes": nodesJSON,
		})
	})

	app.GET("/api/p2p/stats", func(c echo.Context) error {
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

		return c.JSON(200, map[string]interface{}{
			"workers": map[string]interface{}{
				"online": workersOnline,
				"total":  len(workerNodes),
			},
			"federated": map[string]interface{}{
				"online": federatedOnline,
				"total":  len(federatedNodes),
			},
		})
	})

	if !appConfig.DisableRuntimeSettings {
		// Settings API
		app.GET("/api/settings", localai.GetSettingsEndpoint(applicationInstance))
		app.POST("/api/settings", localai.UpdateSettingsEndpoint(applicationInstance))
	}
}
