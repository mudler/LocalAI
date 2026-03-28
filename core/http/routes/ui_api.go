package routes

import "os"

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"path"
	"cmp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/p2p"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/vram"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/mudler/xlog"
)

const (
	nameSortFieldName       = "name"
	repositorySortFieldName = "repository"
	licenseSortFieldName    = "license"
	statusSortFieldName     = "status"
	ascSortOrder            = "asc"
)

// getDirectorySize calculates the total size of files in a directory
func getDirectorySize(path string) (int64, error) {
	var totalSize int64
	entries, err := os.ReadDir(path)
	if err != nil {
		return 0, err
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if !info.IsDir() {
			totalSize += info.Size()
		}
	}
	return totalSize, nil
}

// RegisterUIAPIRoutes registers JSON API routes for the web UI
func RegisterUIAPIRoutes(app *echo.Echo, cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig, galleryService *galleryop.GalleryService, opcache *galleryop.OpCache, applicationInstance *application.Application, adminMiddleware echo.MiddlewareFunc) {

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
				// Skip successfully completed operations
				if status.Processed && !status.Cancelled && status.Error == nil {
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
			// First check if it was explicitly marked as a backend operation
			isBackend := opcache.IsBackendOp(galleryID)
			// If not explicitly marked, check if it matches a known backend from the gallery
			if !isBackend {
				backends, _ := gallery.AvailableBackends(appConfig.BackendGalleries, appConfig.SystemState)
				for _, b := range backends {
					backendID := fmt.Sprintf("%s@%s", b.Gallery.Name, b.Name)
					if backendID == galleryID || b.Name == galleryID {
						isBackend = true
						break
					}
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

			opData := map[string]interface{}{
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
			}
			if status != nil && status.Error != nil {
				opData["error"] = status.Error.Error()
			}
			operations = append(operations, opData)
		}

		// Sort operations by progress (ascending), then by ID for stable display order
		slices.SortFunc(operations, func(a, b map[string]interface{}) int {
			progressA := a["progress"].(int)
			progressB := b["progress"].(int)

			// Primary sort by progress
			if progressA != progressB {
				return cmp.Compare(progressA, progressB)
			}

			// Secondary sort by ID for stability when progress is the same
			return cmp.Compare(a["id"].(string), b["id"].(string))
		})

		return c.JSON(200, map[string]interface{}{
			"operations": operations,
		})
	}, adminMiddleware)

	// Cancel operation endpoint (admin only)
	app.POST("/api/operations/:jobID/cancel", func(c echo.Context) error {
		jobID := c.Param("jobID")
		xlog.Debug("API request to cancel operation", "jobID", jobID)

		err := galleryService.CancelOperation(jobID)
		if err != nil {
			xlog.Error("Failed to cancel operation", "error", err, "jobID", jobID)
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
	}, adminMiddleware)

	// Dismiss a failed operation (acknowledge the error and remove it from the list)
	app.POST("/api/operations/:jobID/dismiss", func(c echo.Context) error {
		jobID := c.Param("jobID")
		xlog.Debug("API request to dismiss operation", "jobID", jobID)

		// Remove the operation from the opcache so it no longer appears
		opcache.DeleteUUID(jobID)

		return c.JSON(200, map[string]interface{}{
			"success": true,
			"message": "Operation dismissed",
		})
	}, adminMiddleware)

	// Model Gallery APIs (admin only)
	app.GET("/api/models", func(c echo.Context) error {
		term := c.QueryParam("term")
		tag := c.QueryParam("tag")
		page := c.QueryParam("page")
		if page == "" {
			page = "1"
		}
		items := c.QueryParam("items")
		if items == "" {
			items = "9"
		}

		models, err := gallery.AvailableGalleryModels(appConfig.Galleries, appConfig.SystemState)
		if err != nil {
			xlog.Error("could not list models from galleries", "error", err)
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
		slices.Sort(tags)

		// Get all available backends (before filtering so dropdown always shows all)
		allBackendsMap := map[string]struct{}{}
		for _, m := range models {
			if b := m.Backend; b != "" {
				allBackendsMap[b] = struct{}{}
			}
		}
		backendNames := make([]string, 0, len(allBackendsMap))
		for b := range allBackendsMap {
			backendNames = append(backendNames, b)
		}
		slices.Sort(backendNames)

		if tag != "" {
			models = gallery.GalleryElements[*gallery.GalleryModel](models).FilterByTag(tag)
		}
		if term != "" {
			models = gallery.GalleryElements[*gallery.GalleryModel](models).Search(term)
		}

		// Filter by backend if requested
		backendFilter := c.QueryParam("backend")
		if backendFilter != "" {
			var filtered gallery.GalleryElements[*gallery.GalleryModel]
			for _, m := range models {
				if m.Backend == backendFilter {
					filtered = append(filtered, m)
				}
			}
			models = filtered
		}

		// Get model statuses
		processingModelsData, taskTypes := opcache.GetStatus()

		// Apply sorting if requested
		sortBy := c.QueryParam("sort")
		sortOrder := c.QueryParam("order")
		if sortOrder == "" {
			sortOrder = ascSortOrder
		}

		switch sortBy {
		case nameSortFieldName:
			models = gallery.GalleryElements[*gallery.GalleryModel](models).SortByName(sortOrder)
		case repositorySortFieldName:
			models = gallery.GalleryElements[*gallery.GalleryModel](models).SortByRepository(sortOrder)
		case licenseSortFieldName:
			models = gallery.GalleryElements[*gallery.GalleryModel](models).SortByLicense(sortOrder)
		case statusSortFieldName:
			models = gallery.GalleryElements[*gallery.GalleryModel](models).SortByInstalled(sortOrder)
		}

		pageNum, err := strconv.Atoi(page)
		if err != nil || pageNum < 1 {
			pageNum = 1
		}

		itemsNum, err := strconv.Atoi(items)
		if err != nil || itemsNum < 1 {
			itemsNum = 9
		}

		totalPages := int(math.Ceil(float64(len(models)) / float64(itemsNum)))
		totalModels := len(models)

		if pageNum > 0 {
			models = models.Paginate(pageNum, itemsNum)
		}

		// Convert models to JSON-friendly format and deduplicate by ID
		modelsJSON := make([]map[string]interface{}, 0, len(models))
		seenIDs := make(map[string]bool)

		weightExts := map[string]bool{".gguf": true, ".safetensors": true, ".bin": true, ".pt": true}
		extractHFRepo := func(overrides map[string]interface{}, urls []string) string {
			// Try overrides.parameters.model first
			if overrides != nil {
				if params, ok := overrides["parameters"].(map[string]interface{}); ok {
					if modelRef, ok := params["model"].(string); ok {
						if repoID, ok := vram.ExtractHFRepoID(modelRef); ok {
							return repoID
						}
					}
				}
			}
			// Fall back to the first HuggingFace URL in the metadata urls list
			for _, u := range urls {
				if repoID, ok := vram.ExtractHFRepoID(u); ok {
					return repoID
				}
			}
			return ""
		}
		hasWeightFiles := func(files []gallery.File) bool {
			for _, f := range files {
				ext := strings.ToLower(path.Ext(path.Base(f.URI)))
				if weightExts[ext] {
					return true
				}
			}
			return false
		}

		const hfEstimateTimeout = 10 * time.Second
		const estimateConcurrency = 3
		sem := make(chan struct{}, estimateConcurrency)
		var wg sync.WaitGroup

		for _, m := range models {
			modelID := m.ID()

			// Skip duplicate IDs to prevent Alpine.js x-for errors
			if seenIDs[modelID] {
				xlog.Debug("Skipping duplicate model ID", "modelID", modelID)
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

			obj := map[string]interface{}{
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
				"backend":         m.Backend,
			}

			// Build EstimateModel input from available metadata
			var estimateInput vram.ModelEstimateInput
			estimateInput.Options = vram.EstimateOptions{ContextLength: 8192}
			estimateInput.Size = m.Size
			if hfRepoID := extractHFRepo(m.Overrides, m.URLs); hfRepoID != "" {
				estimateInput.HFRepo = hfRepoID
			}

			if hasWeightFiles(m.AdditionalFiles) {
				files := make([]gallery.File, len(m.AdditionalFiles))
				copy(files, m.AdditionalFiles)
				for _, f := range files {
					ext := strings.ToLower(path.Ext(path.Base(f.URI)))
					if weightExts[ext] {
						estimateInput.Files = append(estimateInput.Files, vram.FileInput{URI: f.URI, Size: 0})
					}
				}
			}

			// Run estimation (async for file-based and HF repo, sync for size string only)
			needsAsync := len(estimateInput.Files) > 0 || estimateInput.HFRepo != ""
			if needsAsync {
				input := estimateInput
				wg.Go(func() {
					sem <- struct{}{}
					defer func() { <-sem }()
					ctx, cancel := context.WithTimeout(context.Background(), hfEstimateTimeout)
					defer cancel()
					result, err := vram.EstimateModel(ctx, input)
					if err == nil {
						if result.SizeBytes > 0 {
							obj["estimated_size_bytes"] = result.SizeBytes
							obj["estimated_size_display"] = result.SizeDisplay
						}
						if result.VRAMBytes > 0 {
							obj["estimated_vram_bytes"] = result.VRAMBytes
							obj["estimated_vram_display"] = result.VRAMDisplay
						}
					}
				})
			} else if estimateInput.Size != "" {
				result, _ := vram.EstimateModel(context.Background(), estimateInput)
				if result.SizeBytes > 0 {
					obj["estimated_size_bytes"] = result.SizeBytes
					obj["estimated_size_display"] = result.SizeDisplay
				}
				if result.VRAMBytes > 0 {
					obj["estimated_vram_bytes"] = result.VRAMBytes
					obj["estimated_vram_display"] = result.VRAMDisplay
				}
			}

			modelsJSON = append(modelsJSON, obj)
		}

		wg.Wait()

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
		modelsWithoutConfig, _ := galleryop.ListModels(cl, ml, config.NoFilterFn, galleryop.LOOSE_ONLY)
		installedModelsCount := len(modelConfigs) + len(modelsWithoutConfig)

		ramInfo, _ := xsysinfo.GetSystemRAMInfo()

		return c.JSON(200, map[string]interface{}{
			"models":           modelsJSON,
			"repositories":     appConfig.Galleries,
			"allTags":          tags,
			"allBackends":     backendNames,
			"processingModels": processingModelsData,
			"taskTypes":        taskTypes,
			"availableModels":  totalModels,
			"installedModels":  installedModelsCount,
			"ramTotal":         ramInfo.Total,
			"ramUsed":          ramInfo.Used,
			"ramUsagePercent":  ramInfo.UsagePercent,
			"currentPage":      pageNum,
			"totalPages":       totalPages,
			"prevPage":         prevPage,
			"nextPage":         nextPage,
		})
	}, adminMiddleware)

	// Returns installed models with their capability flags for UI filtering
	app.GET("/api/models/capabilities", func(c echo.Context) error {
		modelConfigs := cl.GetAllModelsConfigs()
		modelsWithoutConfig, _ := galleryop.ListModels(cl, ml, config.NoFilterFn, galleryop.LOOSE_ONLY)

		type modelCapability struct {
			ID           string   `json:"id"`
			Capabilities []string `json:"capabilities"`
			Backend      string   `json:"backend"`
		}

		result := make([]modelCapability, 0, len(modelConfigs)+len(modelsWithoutConfig))
		for _, cfg := range modelConfigs {
			result = append(result, modelCapability{
				ID:           cfg.Name,
				Capabilities: cfg.KnownUsecaseStrings,
				Backend:      cfg.Backend,
			})
		}
		for _, name := range modelsWithoutConfig {
			result = append(result, modelCapability{
				ID:           name,
				Capabilities: []string{},
			})
		}

		// Filter by user's model allowlist if auth is enabled
		if authDB := applicationInstance.AuthDB(); authDB != nil {
			if user := auth.GetUser(c); user != nil && user.Role != auth.RoleAdmin {
				perm, err := auth.GetCachedUserPermissions(c, authDB, user.ID)
				if err == nil && perm.AllowedModels.Enabled {
					allowed := map[string]bool{}
					for _, m := range perm.AllowedModels.Models {
						allowed[m] = true
					}
					filtered := make([]modelCapability, 0, len(result))
					for _, mc := range result {
						if allowed[mc.ID] {
							filtered = append(filtered, mc)
						}
					}
					result = filtered
				}
			}
		}

		return c.JSON(200, map[string]any{
			"data": result,
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
		xlog.Debug("API job submitted to install", "galleryID", galleryID)

		id, err := uuid.NewUUID()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]interface{}{
				"error": err.Error(),
			})
		}

		uid := id.String()
		opcache.Set(galleryID, uid)

		ctx, cancelFunc := context.WithCancel(context.Background())
		op := galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
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
	}, adminMiddleware)

	app.POST("/api/models/delete/:id", func(c echo.Context) error {
		galleryID := c.Param("id")
		// URL decode the gallery ID
		galleryID, err := url.QueryUnescape(galleryID)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]interface{}{
				"error": "invalid model ID",
			})
		}
		xlog.Debug("API job submitted to delete", "galleryID", galleryID)

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
		op := galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
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
	}, adminMiddleware)

	app.POST("/api/models/config/:id", func(c echo.Context) error {
		galleryID := c.Param("id")
		// URL decode the gallery ID
		galleryID, err := url.QueryUnescape(galleryID)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]interface{}{
				"error": "invalid model ID",
			})
		}
		xlog.Debug("API job submitted to get config", "galleryID", galleryID)

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
	}, adminMiddleware)

	// Get installed model config as JSON (used by frontend for MCP detection, etc.)
	app.GET("/api/models/config-json/:name", func(c echo.Context) error {
		modelName := c.Param("name")
		if modelName == "" {
			return c.JSON(http.StatusBadRequest, map[string]interface{}{
				"error": "model name is required",
			})
		}

		modelConfig, exists := cl.GetModelConfig(modelName)
		if !exists {
			return c.JSON(http.StatusNotFound, map[string]interface{}{
				"error": "model configuration not found",
			})
		}

		return c.JSON(http.StatusOK, modelConfig)
	}, adminMiddleware)

	// Get installed model YAML config for the React model editor
	app.GET("/api/models/edit/:name", func(c echo.Context) error {
		modelName := c.Param("name")
		if decoded, err := url.PathUnescape(modelName); err == nil {
			modelName = decoded
		}
		if modelName == "" {
			return c.JSON(http.StatusBadRequest, map[string]interface{}{
				"error": "model name is required",
			})
		}

		modelConfig, exists := cl.GetModelConfig(modelName)
		if !exists {
			return c.JSON(http.StatusNotFound, map[string]interface{}{
				"error": "model configuration not found",
			})
		}

		modelConfigFile := modelConfig.GetModelConfigFile()
		if modelConfigFile == "" {
			return c.JSON(http.StatusNotFound, map[string]interface{}{
				"error": "model configuration file not found",
			})
		}

		configData, err := os.ReadFile(modelConfigFile)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]interface{}{
				"error": "failed to read configuration file: " + err.Error(),
			})
		}

		return c.JSON(http.StatusOK, map[string]interface{}{
			"config": string(configData),
			"name":   modelName,
		})
	}, adminMiddleware)

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
	}, adminMiddleware)

	// Backend Gallery APIs
	app.GET("/api/backends", func(c echo.Context) error {
		term := c.QueryParam("term")
		tag := c.QueryParam("tag")
		page := c.QueryParam("page")
		if page == "" {
			page = "1"
		}
		items := c.QueryParam("items")
		if items == "" {
			items = "9"
		}

		backends, err := gallery.AvailableBackends(appConfig.BackendGalleries, appConfig.SystemState)
		if err != nil {
			xlog.Error("could not list backends from galleries", "error", err)
			return c.JSON(http.StatusInternalServerError, map[string]interface{}{
				"error": err.Error(),
			})
		}

		// Use the BackendManager's list to determine installed status.
		// In standalone mode this checks the local filesystem; in distributed
		// mode it aggregates from all healthy worker nodes.
		installedBackends, listErr := galleryService.ListBackends()
		if listErr == nil {
			for i, b := range backends {
				if installedBackends.Exists(b.GetName()) {
					backends[i].Installed = true
				}
			}
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
		slices.Sort(tags)

		if tag != "" {
			backends = gallery.GalleryElements[*gallery.GalleryBackend](backends).FilterByTag(tag)
		}
		if term != "" {
			backends = gallery.GalleryElements[*gallery.GalleryBackend](backends).Search(term)
		}

		// Get backend statuses
		processingBackendsData, taskTypes := opcache.GetStatus()

		// Apply sorting if requested
		sortBy := c.QueryParam("sort")
		sortOrder := c.QueryParam("order")
		if sortOrder == "" {
			sortOrder = ascSortOrder
		}

		switch sortBy {
		case nameSortFieldName:
			backends = gallery.GalleryElements[*gallery.GalleryBackend](backends).SortByName(sortOrder)
		case repositorySortFieldName:
			backends = gallery.GalleryElements[*gallery.GalleryBackend](backends).SortByRepository(sortOrder)
		case licenseSortFieldName:
			backends = gallery.GalleryElements[*gallery.GalleryBackend](backends).SortByLicense(sortOrder)
		case statusSortFieldName:
			backends = gallery.GalleryElements[*gallery.GalleryBackend](backends).SortByInstalled(sortOrder)
		}

		pageNum, err := strconv.Atoi(page)
		if err != nil || pageNum < 1 {
			pageNum = 1
		}

		itemsNum, err := strconv.Atoi(items)
		if err != nil || itemsNum < 1 {
			itemsNum = 9
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
				xlog.Debug("Skipping duplicate backend ID", "backendID", backendID)
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

		// Calculate installed backends count (reuse the already-fetched data)
		installedBackendsCount := 0
		if listErr == nil {
			installedBackendsCount = len(installedBackends)
		} else {
			// Fallback to local listing if manager listing failed
			if localBackends, localErr := gallery.ListSystemBackends(appConfig.SystemState); localErr == nil {
				installedBackendsCount = len(localBackends)
			}
		}

		// Get the detected system capability
		detectedCapability := ""
		if appConfig.SystemState != nil {
			detectedCapability = appConfig.SystemState.DetectedCapability()
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
			"systemCapability":   detectedCapability,
		})
	}, adminMiddleware)

	app.POST("/api/backends/install/:id", func(c echo.Context) error {
		backendID := c.Param("id")
		// URL decode the backend ID
		backendID, err := url.QueryUnescape(backendID)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]interface{}{
				"error": "invalid backend ID",
			})
		}
		xlog.Debug("API job submitted to install backend", "backendID", backendID)

		id, err := uuid.NewUUID()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]interface{}{
				"error": err.Error(),
			})
		}

		uid := id.String()
		opcache.SetBackend(backendID, uid)

		ctx, cancelFunc := context.WithCancel(context.Background())
		op := galleryop.ManagementOp[gallery.GalleryBackend, any]{
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
	}, adminMiddleware)

	// Install backend from external source (OCI image, URL, or path)
	app.POST("/api/backends/install-external", func(c echo.Context) error {
		// Request body structure
		type ExternalBackendRequest struct {
			URI   string `json:"uri"`
			Name  string `json:"name"`
			Alias string `json:"alias"`
		}

		var req ExternalBackendRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]interface{}{
				"error": "invalid request body",
			})
		}

		// Validate required fields
		if req.URI == "" {
			return c.JSON(http.StatusBadRequest, map[string]interface{}{
				"error": "uri is required",
			})
		}

		xlog.Debug("API job submitted to install external backend", "uri", req.URI, "name", req.Name, "alias", req.Alias)

		id, err := uuid.NewUUID()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]interface{}{
				"error": err.Error(),
			})
		}

		uid := id.String()

		// Use URI as the key for opcache, or name if provided
		cacheKey := req.URI
		if req.Name != "" {
			cacheKey = req.Name
		}
		opcache.SetBackend(cacheKey, uid)

		ctx, cancelFunc := context.WithCancel(context.Background())
		op := galleryop.ManagementOp[gallery.GalleryBackend, any]{
			ID:                 uid,
			GalleryElementName: req.Name, // May be empty, will be derived during installation
			Galleries:          appConfig.BackendGalleries,
			Context:            ctx,
			CancelFunc:         cancelFunc,
			ExternalURI:        req.URI,
			ExternalName:       req.Name,
			ExternalAlias:      req.Alias,
		}
		// Store cancellation function immediately so queued operations can be cancelled
		galleryService.StoreCancellation(uid, cancelFunc)
		go func() {
			galleryService.BackendGalleryChannel <- op
		}()

		return c.JSON(200, map[string]interface{}{
			"jobID":   uid,
			"message": "External backend installation started",
		})
	}, adminMiddleware)

	app.POST("/api/backends/delete/:id", func(c echo.Context) error {
		backendID := c.Param("id")
		// URL decode the backend ID
		backendID, err := url.QueryUnescape(backendID)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]interface{}{
				"error": "invalid backend ID",
			})
		}
		xlog.Debug("API job submitted to delete backend", "backendID", backendID)

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

		opcache.SetBackend(backendID, uid)

		ctx, cancelFunc := context.WithCancel(context.Background())
		op := galleryop.ManagementOp[gallery.GalleryBackend, any]{
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
	}, adminMiddleware)

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
	}, adminMiddleware)

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
		xlog.Debug("API request to delete system backend", "backendName", backendName)

		// Use the gallery service's backend manager, which in distributed mode
		// fans out deletion to worker nodes via NATS.
		if err := galleryService.DeleteBackend(backendName); err != nil {
			xlog.Error("Failed to delete backend", "error", err, "backendName", backendName)
			return c.JSON(http.StatusInternalServerError, map[string]interface{}{
				"error": err.Error(),
			})
		}

		return c.JSON(200, map[string]interface{}{
			"success": true,
			"message": "Backend deleted successfully",
		})
	}, adminMiddleware)

	// P2P APIs
	app.GET("/api/p2p/workers", func(c echo.Context) error {
		llamaNodes := p2p.GetAvailableNodes(p2p.NetworkID(appConfig.P2PNetworkID, p2p.LlamaCPPWorkerID))
		mlxNodes := p2p.GetAvailableNodes(p2p.NetworkID(appConfig.P2PNetworkID, p2p.MLXWorkerID))

		llamaJSON := make([]map[string]any, 0, len(llamaNodes))
		for _, n := range llamaNodes {
			llamaJSON = append(llamaJSON, map[string]any{
				"name":          n.Name,
				"id":            n.ID,
				"tunnelAddress": n.TunnelAddress,
				"serviceID":     n.ServiceID,
				"lastSeen":      n.LastSeen,
				"isOnline":      n.IsOnline(),
			})
		}

		mlxJSON := make([]map[string]any, 0, len(mlxNodes))
		for _, n := range mlxNodes {
			mlxJSON = append(mlxJSON, map[string]any{
				"name":          n.Name,
				"id":            n.ID,
				"tunnelAddress": n.TunnelAddress,
				"serviceID":     n.ServiceID,
				"lastSeen":      n.LastSeen,
				"isOnline":      n.IsOnline(),
			})
		}

		return c.JSON(200, map[string]any{
			"llama_cpp": map[string]any{
				"nodes": llamaJSON,
			},
			"mlx": map[string]any{
				"nodes": mlxJSON,
			},
			// Keep backward-compatible "nodes" key with llama.cpp workers
			"nodes": llamaJSON,
		})
	}, adminMiddleware)

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
	}, adminMiddleware)

	app.GET("/api/p2p/stats", func(c echo.Context) error {
		llamaCPPNodes := p2p.GetAvailableNodes(p2p.NetworkID(appConfig.P2PNetworkID, p2p.LlamaCPPWorkerID))
		federatedNodes := p2p.GetAvailableNodes(p2p.NetworkID(appConfig.P2PNetworkID, p2p.FederatedID))
		mlxWorkerNodes := p2p.GetAvailableNodes(p2p.NetworkID(appConfig.P2PNetworkID, p2p.MLXWorkerID))

		llamaCPPOnline := 0
		for _, n := range llamaCPPNodes {
			if n.IsOnline() {
				llamaCPPOnline++
			}
		}

		federatedOnline := 0
		for _, n := range federatedNodes {
			if n.IsOnline() {
				federatedOnline++
			}
		}

		mlxWorkersOnline := 0
		for _, n := range mlxWorkerNodes {
			if n.IsOnline() {
				mlxWorkersOnline++
			}
		}

		return c.JSON(200, map[string]any{
			"llama_cpp_workers": map[string]any{
				"online": llamaCPPOnline,
				"total":  len(llamaCPPNodes),
			},
			"federated": map[string]any{
				"online": federatedOnline,
				"total":  len(federatedNodes),
			},
			"mlx_workers": map[string]any{
				"online": mlxWorkersOnline,
				"total":  len(mlxWorkerNodes),
			},
		})
	}, adminMiddleware)

	// Resources API endpoint - unified memory info (GPU if available, otherwise RAM)
	app.GET("/api/resources", func(c echo.Context) error {
		resourceInfo := xsysinfo.GetResourceInfo()

		// Format watchdog interval
		watchdogInterval := "2s" // default
		if appConfig.WatchDogInterval > 0 {
			watchdogInterval = appConfig.WatchDogInterval.String()
		}

		storageSize, _ := getDirectorySize(appConfig.SystemState.Model.ModelsPath)

		response := map[string]interface{}{
			"type":                resourceInfo.Type, // "gpu" or "ram"
			"available":           resourceInfo.Available,
			"gpus":                resourceInfo.GPUs,
			"ram":                 resourceInfo.RAM,
			"aggregate":           resourceInfo.Aggregate,
			"storage_size":        storageSize,
			"reclaimer_enabled":   appConfig.MemoryReclaimerEnabled,
			"reclaimer_threshold": appConfig.MemoryReclaimerThreshold,
			"watchdog_interval":   watchdogInterval,
		}

		return c.JSON(200, response)
	}, adminMiddleware)

	if !appConfig.DisableRuntimeSettings {
		// Settings API
		app.GET("/api/settings", localai.GetSettingsEndpoint(applicationInstance), adminMiddleware)
		app.POST("/api/settings", localai.UpdateSettingsEndpoint(applicationInstance), adminMiddleware)
	}

	// Logs API (admin only)
	app.GET("/api/traces", func(c echo.Context) error {
		if !appConfig.EnableTracing {
			return c.JSON(503, map[string]any{
				"error": "Tracing disabled",
			})
		}
		traces := middleware.GetTraces()
		return c.JSON(200, map[string]interface{}{
			"traces": traces,
		})
	}, adminMiddleware)

	app.POST("/api/traces/clear", func(c echo.Context) error {
		middleware.ClearTraces()
		return c.JSON(200, map[string]interface{}{
			"message": "Traces cleared",
		})
	}, adminMiddleware)
}
