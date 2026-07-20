package routes

import "os"

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
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
	multimodalFilterKey     = "multimodal"
	voiceCloningCapability  = "voice_cloning"
)

// usecaseFilters maps UI filter keys to ModelConfigUsecase flags for
// capability-based gallery filtering.
var usecaseFilters = map[string]config.ModelConfigUsecase{
	config.UsecaseChat:                config.FLAG_CHAT,
	config.UsecaseImage:               config.FLAG_IMAGE,
	config.UsecaseVideo:               config.FLAG_VIDEO,
	config.UsecaseVision:              config.FLAG_VISION,
	config.UsecaseTTS:                 config.FLAG_TTS,
	config.UsecaseTranscript:          config.FLAG_TRANSCRIPT,
	config.UsecaseSoundGeneration:     config.FLAG_SOUND_GENERATION,
	config.UsecaseEmbeddings:          config.FLAG_EMBEDDINGS,
	config.UsecaseRerank:              config.FLAG_RERANK,
	config.UsecaseDetection:           config.FLAG_DETECTION,
	config.UsecaseVAD:                 config.FLAG_VAD,
	config.UsecaseAudioTransform:      config.FLAG_AUDIO_TRANSFORM,
	config.UsecaseDiarization:         config.FLAG_DIARIZATION,
	config.UsecaseSoundClassification: config.FLAG_SOUND_CLASSIFICATION,
	config.UsecaseRealtimeAudio:       config.FLAG_REALTIME_AUDIO,
	config.UsecaseTokenClassify:       config.FLAG_TOKEN_CLASSIFY,
}

// extractHFRepo tries to find a HuggingFace repo ID from model overrides or URLs.
func extractHFRepo(overrides map[string]any, urls []string) string {
	if overrides != nil {
		if params, ok := overrides["parameters"].(map[string]any); ok {
			if modelRef, ok := params["model"].(string); ok {
				if repoID, ok := vram.ExtractHFRepoID(modelRef); ok {
					return repoID
				}
			}
		}
	}
	for _, u := range urls {
		if repoID, ok := vram.ExtractHFRepoID(u); ok {
			return repoID
		}
	}
	return ""
}

// buildEstimateInput creates a vram.ModelEstimateInput from gallery model metadata.
func buildEstimateInput(m *gallery.GalleryModel) vram.ModelEstimateInput {
	var input vram.ModelEstimateInput
	input.Size = m.Size
	if hfRepoID := extractHFRepo(m.Overrides, m.URLs); hfRepoID != "" {
		input.HFRepo = hfRepoID
	}
	for _, f := range m.AdditionalFiles {
		if vram.IsWeightFile(f.URI) {
			input.Files = append(input.Files, vram.FileInput{URI: f.URI, Size: 0})
		}
	}
	return input
}

// parseContextSizes parses a comma-separated list of context sizes from a query param.
// Returns a default of [8192] if the param is empty or unparseable.
func parseContextSizes(raw string) []uint32 {
	if raw == "" {
		return []uint32{8192}
	}
	var sizes []uint32
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if v, err := strconv.ParseUint(s, 10, 32); err == nil && v > 0 {
			sizes = append(sizes, uint32(v))
		}
	}
	if len(sizes) == 0 {
		return []uint32{8192}
	}
	return sizes
}

// getDirectorySize calculates the total size of files in a directory
// metaParentOf returns the name of the auto-resolving (meta) backend that
// declares `name` as one of its hardware-specific variants in its
// CapabilitiesMap, or "" if there is no such parent. The install picker uses
// this to render hints like "CPU build of llama-cpp" without re-walking the
// whole gallery on the client side.
func metaParentOf(name string, backends gallery.GalleryElements[*gallery.GalleryBackend]) string {
	for _, b := range backends {
		if !b.IsMeta() {
			continue
		}
		for _, concreteName := range b.CapabilitiesMap {
			if concreteName == name {
				return b.Name
			}
		}
	}
	return ""
}

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

		operations := []map[string]any{}
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
			phase := ""
			currentBytes := int64(0)
			totalBytes := int64(0)

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
				phase = status.Phase
				currentBytes = status.CurrentBytes
				totalBytes = status.TotalBytes
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

			// Node-scoped backend ops (from /api/nodes/:id/backends/install)
			// carry the nodeID inside the opcache key as "node:<nodeID>:<backend>".
			// Pull it back out so the operations panel can label which node the
			// install is targeting, and so the display name is just the backend
			// slug instead of the full prefixed key.
			scopedNodeID := ""
			if nodeID, backend, ok := galleryop.ParseNodeScopedKey(galleryID); ok {
				scopedNodeID = nodeID
				galleryID = backend
			}

			// Extract display name (remove repo prefix if exists)
			displayName := galleryID
			if strings.Contains(galleryID, "@") {
				parts := strings.Split(galleryID, "@")
				if len(parts) > 1 {
					displayName = parts[1]
				}
			}

			opData := map[string]any{
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
			// Only attach nodeID when this op was node-scoped: an empty string
			// would mislead the UI into rendering a node attribution that never
			// existed in the first place.
			if scopedNodeID != "" {
				opData["nodeID"] = scopedNodeID
			}
			if phase != "" {
				opData["phase"] = phase
			}
			if currentBytes > 0 {
				opData["currentBytes"] = currentBytes
			}
			if totalBytes > 0 {
				opData["totalBytes"] = totalBytes
			}
			if status != nil && status.Error != nil {
				opData["error"] = status.Error.Error()
			}
			// Expose the per-node breakdown when the Phase 4 progress sink
			// has populated OpStatus.Nodes (distributed backend installs).
			// We sort by node_name for stable UI rendering across polls;
			// the underlying slice is order-dependent on UpdateNodeProgress
			// arrival order, which the UI must not depend on. Single-node
			// ops and model installs leave Nodes empty so this block emits
			// no key, preserving the legacy payload shape.
			if status != nil && len(status.Nodes) > 0 {
				nodes := make([]map[string]any, 0, len(status.Nodes))
				for _, n := range status.Nodes {
					entry := map[string]any{
						"node_id":    n.NodeID,
						"node_name":  n.NodeName,
						"status":     n.Status,
						"percentage": n.Percentage,
					}
					if n.FileName != "" {
						entry["file_name"] = n.FileName
					}
					if n.Current != "" {
						entry["current"] = n.Current
					}
					if n.Total != "" {
						entry["total"] = n.Total
					}
					if n.Phase != "" {
						entry["phase"] = n.Phase
					}
					if n.Error != "" {
						entry["error"] = n.Error
					}
					nodes = append(nodes, entry)
				}
				sort.SliceStable(nodes, func(i, j int) bool {
					return fmt.Sprintf("%v", nodes[i]["node_name"]) < fmt.Sprintf("%v", nodes[j]["node_name"])
				})
				opData["nodes"] = nodes
			}
			operations = append(operations, opData)
		}

		// Append active file staging operations (distributed mode only)
		if d := applicationInstance.Distributed(); d != nil && d.Router != nil {
			for modelID, status := range d.Router.StagingTracker().GetAll() {
				operations = append(operations, map[string]any{
					"id":          "staging:" + modelID,
					"name":        modelID,
					"fullName":    modelID,
					"jobID":       "staging:" + modelID,
					"progress":    int(status.Progress),
					"taskType":    "staging",
					"isDeletion":  false,
					"isBackend":   false,
					"isQueued":    false,
					"isCancelled": false,
					"cancellable": false,
					"message":     status.Message,
					"nodeName":    status.NodeName,
				})
			}
		}

		// Sort operations by progress (ascending), then by ID for stable display order
		slices.SortFunc(operations, func(a, b map[string]any) int {
			progressA := a["progress"].(int)
			progressB := b["progress"].(int)

			// Primary sort by progress
			if progressA != progressB {
				return cmp.Compare(progressA, progressB)
			}

			// Secondary sort by ID for stability when progress is the same
			return cmp.Compare(a["id"].(string), b["id"].(string))
		})

		return c.JSON(200, map[string]any{
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
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error": err.Error(),
			})
		}

		// Clean up opcache for cancelled operation
		opcache.DeleteUUID(jobID)

		return c.JSON(200, map[string]any{
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

		return c.JSON(200, map[string]any{
			"success": true,
			"message": "Operation dismissed",
		})
	}, adminMiddleware)

	// Model Gallery APIs (admin only)
	app.GET("/api/models", func(c echo.Context) error {
		// Trimmed once, here, so "is the user searching?" has a single answer
		// for both the search itself and the variant collapse below.
		// Whitespace is not a lookup: an untrimmed blank term used to narrow
		// the listing to whatever happened to contain a space.
		term := strings.TrimSpace(c.QueryParam("term"))
		tag := c.QueryParam("tag")
		page := c.QueryParam("page")
		if page == "" {
			page = "1"
		}
		items := c.QueryParam("items")
		if items == "" {
			items = "9"
		}

		models, err := gallery.AvailableGalleryModelsCached(appConfig.Galleries, appConfig.SystemState)
		if err != nil {
			xlog.Error("could not list models from galleries", "error", err)
			return c.JSON(http.StatusInternalServerError, map[string]any{
				"error": err.Error(),
			})
		}
		// The filters below rebind models, so keep the unfiltered gallery for
		// the questions that are about the gallery as a whole rather than about
		// the current result set, such as which entries another entry already
		// offers as a variant.
		allModels := models

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

		// Filter by usecase tags (comma-separated for multi-select).
		if tag != "" {
			var combinedFlag config.ModelConfigUsecase
			hasMultimodal := false
			var plainTags []string
			for _, t := range strings.Split(tag, ",") {
				t = strings.TrimSpace(t)
				if t == multimodalFilterKey {
					hasMultimodal = true
				} else if flag, ok := usecaseFilters[t]; ok {
					combinedFlag |= flag
				} else if t != "" {
					plainTags = append(plainTags, t)
				}
			}
			if hasMultimodal {
				models = gallery.FilterGalleryModelsByMultimodal(models)
			}
			if combinedFlag != config.FLAG_ANY {
				models = gallery.FilterGalleryModelsByUsecase(models, combinedFlag)
			}
			for _, pt := range plainTags {
				models = gallery.GalleryElements[*gallery.GalleryModel](models).FilterByTag(pt)
			}
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

		// Collapse the listing to one row per model: drop the individual builds
		// a parent entry already offers as variants, and keep everything else.
		// What survives is every entry installable in its own right, with
		// nothing shown twice, rather than one row per quantization.
		//
		// Off by default so the response with the parameter absent is exactly
		// what it was. Adoption is a handful of entries, so this hides little
		// today, but it is the view the gallery is heading towards and it costs
		// nothing until someone asks for it.
		//
		// The referenced set is computed over the whole gallery rather than
		// over what the other filters left, so an entry is hidden because a
		// parent offers it, never because of which tag or backend the user
		// picked while browsing.
		//
		// A search term is the exception: collapsing serves browsing, but a user
		// who types a name is looking up a specific entry and must get it even
		// when a parent offers it as a variant. Otherwise the only answer the
		// gallery can give for a build it does hold is "no models found", which
		// reads as "that model does not exist". Tag and backend do not bypass
		// the collapse because they refine a listing the user is still reading
		// rather than name something they already know exists.
		//
		// term is already trimmed, so a stray space in the search box does not
		// count as a search and cannot silently expand the listing.
		//
		// Server-side because the listing paginates at 9 items; filtering the
		// current page in the client would leave the page count describing the
		// unfiltered set and hand the user empty pages.
		if term == "" && c.QueryParam("collapse_variants") == "true" {
			referenced := gallery.VariantReferencedIDs(allModels)
			filtered := make(gallery.GalleryElements[*gallery.GalleryModel], 0, len(models))
			for _, m := range models {
				if _, hidden := referenced[m.ID()]; hidden {
					continue
				}
				filtered = append(filtered, m)
			}
			models = filtered
		}

		// Capability filters are derived from the effective gallery model
		// configuration. In particular, voice cloning is variant-sensitive, so
		// filtering by the TTS usecase or backend name alone would advertise
		// incompatible CustomVoice/VoiceDesign models.
		capabilityFilter := strings.ToLower(strings.TrimSpace(c.QueryParam("capability")))
		switch capabilityFilter {
		case "":
		case voiceCloningCapability:
			filtered := make(gallery.GalleryElements[*gallery.GalleryModel], 0, len(models))
			for _, m := range models {
				if m.VoiceCloningCapability(appConfig.SystemState.Model.ModelsPath) != nil {
					filtered = append(filtered, m)
				}
			}
			models = filtered
		default:
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error": fmt.Sprintf("unsupported capability filter %q", capabilityFilter),
			})
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
		modelsJSON := make([]map[string]any, 0, len(models))
		seenIDs := make(map[string]bool)

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

			obj := map[string]any{
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
				"voice_cloning":   m.VoiceCloningCapability(appConfig.SystemState.Model.ModelsPath),
			}

			// Only the cheap declaration flag, never the description itself.
			// Describing a variant probes every referenced entry's weight files
			// over the network, so doing it here would cost one page load
			// (entries x variants) serial round trips; a client that wants the
			// description asks /api/models/variants/:id for one entry at a
			// time, exactly as it already does for VRAM estimates.
			//
			// The flag is omitted rather than sent as false so an entry that
			// declares nothing stays byte-for-byte what it was before variants
			// existed, and so a client never asks about it.
			if m.HasVariants() {
				obj["has_variants"] = true
			}

			modelsJSON = append(modelsJSON, obj)
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
		modelsWithoutConfig, _ := galleryop.ListModels(cl, ml, config.NoFilterFn, galleryop.LOOSE_ONLY)
		installedModelsCount := len(modelConfigs) + len(modelsWithoutConfig)

		ramInfo, _ := xsysinfo.GetSystemRAMInfo()

		return c.JSON(200, map[string]any{
			"models":           modelsJSON,
			"repositories":     appConfig.Galleries,
			"allTags":          tags,
			"allBackends":      backendNames,
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

		type loadedOn struct {
			NodeID     string `json:"node_id"`
			NodeName   string `json:"node_name"`
			State      string `json:"state"`
			NodeStatus string `json:"node_status"`
		}
		type modelCapability struct {
			ID           string                         `json:"id"`
			Capabilities []string                       `json:"capabilities"`
			Backend      string                         `json:"backend"`
			Disabled     bool                           `json:"disabled"`
			Pinned       bool                           `json:"pinned"`
			VoiceCloning *config.VoiceCloningCapability `json:"voice_cloning,omitempty"`
			// LoadedOn is populated only when the node registry is active
			// (distributed mode). Lets the UI show "loaded on worker-1" without
			// the operator having to expand every node manually. An empty slice
			// with nil reports "no loaded replicas" vs. nil reports "not in
			// cluster mode" — the frontend treats both as "no distribution info".
			LoadedOn []loadedOn `json:"loaded_on,omitempty"`
			// Source="registry-only" marks models adopted from the cluster that
			// have no local config yet (ghosts that the reconciler discovered).
			Source string `json:"source,omitempty"`
		}

		// Join with the node registry when we have one (distributed mode). A
		// single registry fetch + map join beats per-model queries for the
		// 100-model case.
		var loadedByModel map[string][]loadedOn
		if ds := applicationInstance.Distributed(); ds != nil && ds.Registry != nil {
			nodeModels, err := ds.Registry.ListAllLoadedModels(c.Request().Context())
			if err == nil {
				allNodes, _ := ds.Registry.List(c.Request().Context())
				nameByID := make(map[string]string, len(allNodes))
				statusByID := make(map[string]string, len(allNodes))
				for _, n := range allNodes {
					nameByID[n.ID] = n.Name
					statusByID[n.ID] = n.Status
				}
				loadedByModel = make(map[string][]loadedOn)
				for _, nm := range nodeModels {
					loadedByModel[nm.ModelName] = append(loadedByModel[nm.ModelName], loadedOn{
						NodeID:     nm.NodeID,
						NodeName:   nameByID[nm.NodeID],
						State:      nm.State,
						NodeStatus: statusByID[nm.NodeID],
					})
				}
			}
		}

		result := make([]modelCapability, 0, len(modelConfigs)+len(modelsWithoutConfig))
		seen := make(map[string]bool, len(modelConfigs)+len(modelsWithoutConfig))
		for _, cfg := range modelConfigs {
			seen[cfg.Name] = true
			result = append(result, modelCapability{
				ID:           cfg.Name,
				Capabilities: cfg.KnownUsecaseStrings,
				Backend:      cfg.Backend,
				Disabled:     cfg.IsDisabled(),
				Pinned:       cfg.IsPinned(),
				VoiceCloning: config.VoiceCloningForModel(&cfg),
				LoadedOn:     loadedByModel[cfg.Name],
			})
		}
		for _, name := range modelsWithoutConfig {
			seen[name] = true
			result = append(result, modelCapability{
				ID:           name,
				Capabilities: []string{},
				LoadedOn:     loadedByModel[name],
			})
		}
		// Emit entries for cluster models that have no local config — these
		// are the actual ghosts. Without this the operator would have no way
		// to see a model the cluster is running if its config file wasn't
		// synced to this frontend's filesystem.
		for name, loc := range loadedByModel {
			if seen[name] {
				continue
			}
			result = append(result, modelCapability{
				ID:           name,
				Capabilities: []string{},
				LoadedOn:     loc,
				Source:       "registry-only",
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

	// Returns a mapping of backend names to the usecase filter keys they support.
	// Used by the gallery frontend to grey out usecase filter buttons when a
	// backend is selected.
	app.GET("/api/backends/usecases", func(c echo.Context) error {
		result := make(map[string][]string, len(config.BackendCapabilities))
		for name, cap := range config.BackendCapabilities {
			var keys []string
			for _, uc := range cap.PossibleUsecases {
				if _, ok := usecaseFilters[uc]; ok {
					keys = append(keys, uc)
				}
			}
			slices.Sort(keys)
			result[name] = keys
		}

		return c.JSON(200, result)
	}, adminMiddleware)

	// Returns VRAM/size estimates for a single gallery model at multiple
	// context sizes. The frontend calls this per-model so the gallery page
	// can load instantly and fill in estimates asynchronously.
	// Query params:
	//   contexts - comma-separated context sizes (default: 8192)
	app.GET("/api/models/estimate/:id", func(c echo.Context) error {
		modelID, err := url.QueryUnescape(c.Param("id"))
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid model ID"})
		}

		contextSizes := parseContextSizes(c.QueryParam("contexts"))

		// Look up the model from the gallery to build the estimate input.
		models, err := gallery.AvailableGalleryModelsCached(appConfig.Galleries, appConfig.SystemState)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
		}

		model := gallery.FindGalleryElement(models, modelID)
		if model == nil {
			return c.JSON(http.StatusNotFound, map[string]any{"error": "model not found"})
		}

		input := buildEstimateInput(model)
		if len(input.Files) == 0 && input.HFRepo == "" && input.Size == "" {
			return c.JSON(200, vram.MultiContextEstimate{})
		}

		ctx, cancel := context.WithTimeout(c.Request().Context(), 10*time.Second)
		defer cancel()
		result, err := vram.EstimateModelMultiContext(ctx, input, contextSizes)
		if err != nil {
			xlog.Debug("model estimate failed", "model", modelID, "error", err)
			return c.JSON(200, vram.MultiContextEstimate{})
		}

		return c.JSON(200, result)
	}, adminMiddleware)

	// Returns the selectable builds of a single gallery model and which one
	// auto-selection would install on this host. Companion to estimate/:id and
	// for the same reason: describing variants probes each referenced entry's
	// weight files over the network, so the gallery listing must stay free of
	// it and the frontend fills pickers in per-entry, on demand.
	//
	// An entry that declares no variants is not an error; it answers with an
	// empty description, so a client that asks anyway gets a well-formed reply
	// rather than a 404 it has to special-case.
	app.GET("/api/models/variants/:id", func(c echo.Context) error {
		modelID, err := url.QueryUnescape(c.Param("id"))
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{"error": "invalid model ID"})
		}

		models, err := gallery.AvailableGalleryModelsCached(appConfig.Galleries, appConfig.SystemState)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
		}

		model := gallery.FindGalleryElement(models, modelID)
		if model == nil {
			return c.JSON(http.StatusNotFound, map[string]any{"error": "model not found"})
		}

		// An allocated slice rather than the zero value, so "nothing
		// selectable" serializes as [] and a client can iterate the reply
		// without first distinguishing it from null.
		empty := gallery.EntryVariants{Variants: []gallery.VariantView{}}

		// The full, unpaginated list: a variant references another gallery
		// entry by name and that entry need not be anywhere near this one.
		env := gallery.HostResolveEnv(c.Request().Context(), appConfig.SystemState)
		view, err := gallery.DescribeVariants(models, model, env)
		if err != nil {
			// A malformed variant list must not break the picker; the entry
			// just reports nothing selectable and installs as-is.
			xlog.Debug("could not describe model variants", "model", modelID, "error", err)
			return c.JSON(200, empty)
		}
		if view == nil {
			return c.JSON(200, empty)
		}

		return c.JSON(200, view)
	}, adminMiddleware)

	app.POST("/api/models/install/:id", func(c echo.Context) error {
		galleryID := c.Param("id")
		// URL decode the gallery ID (e.g., "localai%40model" -> "localai@model")
		galleryID, err := url.QueryUnescape(galleryID)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error": "invalid model ID",
			})
		}
		// Optional: one of the variants the listing reported for this entry.
		// Absent means auto-select, which is what the listing's auto_variant
		// already told the client would happen.
		variant := c.QueryParam("variant")
		xlog.Debug("API job submitted to install", "galleryID", galleryID, "variant", variant)

		id, err := uuid.NewUUID()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]any{
				"error": err.Error(),
			})
		}

		uid := id.String()
		opcache.Set(galleryID, uid)

		ctx, cancelFunc := context.WithCancel(context.Background())
		op := galleryop.ManagementOp[gallery.GalleryModel, gallery.ModelConfig]{
			ID:                 uid,
			GalleryElementName: galleryID,
			Variant:            variant,
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

		return c.JSON(200, map[string]any{
			"jobID":   uid,
			"message": "Installation started",
		})
	}, adminMiddleware)

	app.POST("/api/models/delete/:id", func(c echo.Context) error {
		galleryID := c.Param("id")
		// URL decode the gallery ID
		galleryID, err := url.QueryUnescape(galleryID)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{
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
			return c.JSON(http.StatusInternalServerError, map[string]any{
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

		return c.JSON(200, map[string]any{
			"jobID":   uid,
			"message": "Deletion started",
		})
	}, adminMiddleware)

	app.POST("/api/models/config/:id", func(c echo.Context) error {
		galleryID := c.Param("id")
		// URL decode the gallery ID
		galleryID, err := url.QueryUnescape(galleryID)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error": "invalid model ID",
			})
		}
		xlog.Debug("API job submitted to get config", "galleryID", galleryID)

		models, err := gallery.AvailableGalleryModelsCached(appConfig.Galleries, appConfig.SystemState)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]any{
				"error": err.Error(),
			})
		}

		model := gallery.FindGalleryElement(models, galleryID)
		if model == nil {
			return c.JSON(http.StatusNotFound, map[string]any{
				"error": "model not found",
			})
		}

		config, err := gallery.GetGalleryConfigFromURL[gallery.ModelConfig](model.URL, appConfig.SystemState.Model.ModelsPath)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]any{
				"error": err.Error(),
			})
		}

		_, err = gallery.InstallModel(context.Background(), appConfig.SystemState, model.Name, &config, model.Overrides, nil, false,
			gallery.WithArtifactMaterializer(appConfig.ModelArtifactMaterializer))
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]any{
				"error": err.Error(),
			})
		}

		return c.JSON(200, map[string]any{
			"message": "Configuration file saved",
		})
	}, adminMiddleware)

	// Get installed model config as JSON (used by frontend for MCP detection, etc.)
	app.GET("/api/models/config-json/:name", func(c echo.Context) error {
		modelName := c.Param("name")
		if modelName == "" {
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error": "model name is required",
			})
		}

		modelConfig, exists := cl.GetModelConfig(modelName)
		if !exists {
			return c.JSON(http.StatusNotFound, map[string]any{
				"error": "model configuration not found",
			})
		}

		return c.JSON(http.StatusOK, modelConfig)
	}, adminMiddleware)

	// Config metadata API - returns field metadata for all ~170 config fields
	app.GET("/api/models/config-metadata", localai.ConfigMetadataEndpoint(), adminMiddleware)

	// Autocomplete providers for config fields (dynamic values only)
	app.GET("/api/models/config-metadata/autocomplete/:provider", localai.AutocompleteEndpoint(cl, ml, appConfig), adminMiddleware)

	// PATCH config endpoint - partial update using nested JSON merge
	app.PATCH("/api/models/config-json/:name", localai.PatchConfigEndpoint(cl, ml, galleryService, appConfig), adminMiddleware)

	// VRAM estimation endpoint
	app.POST("/api/models/vram-estimate", localai.VRAMEstimateEndpoint(cl, appConfig), adminMiddleware)

	// Get installed model YAML config for the React model editor
	app.GET("/api/models/edit/:name", func(c echo.Context) error {
		modelName := c.Param("name")
		if decoded, err := url.PathUnescape(modelName); err == nil {
			modelName = decoded
		}
		if modelName == "" {
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error": "model name is required",
			})
		}

		modelConfig, exists := cl.GetModelConfig(modelName)
		if !exists {
			return c.JSON(http.StatusNotFound, map[string]any{
				"error": "model configuration not found",
			})
		}

		modelConfigFile := modelConfig.GetModelConfigFile()
		if modelConfigFile == "" {
			return c.JSON(http.StatusNotFound, map[string]any{
				"error": "model configuration file not found",
			})
		}

		configData, err := os.ReadFile(modelConfigFile)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]any{
				"error": "failed to read configuration file: " + err.Error(),
			})
		}

		return c.JSON(http.StatusOK, map[string]any{
			"config": string(configData),
			"name":   modelName,
		})
	}, adminMiddleware)

	app.GET("/api/models/job/:uid", func(c echo.Context) error {
		jobUID := c.Param("uid")

		status := galleryService.GetStatus(jobUID)
		if status == nil {
			// Job is queued but hasn't started processing yet
			return c.JSON(200, map[string]any{
				"progress":           0,
				"message":            "Operation queued",
				"galleryElementName": "",
				"processed":          false,
				"deletion":           false,
				"queued":             true,
			})
		}

		response := map[string]any{
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

		backends, err := gallery.AvailableBackendsUnfiltered(appConfig.BackendGalleries, appConfig.SystemState)
		if err != nil {
			xlog.Error("could not list backends from galleries", "error", err)
			return c.JSON(http.StatusInternalServerError, map[string]any{
				"error": err.Error(),
			})
		}

		// Collect concrete backend names that are referenced by any meta backend's
		// CapabilitiesMap. These are the per-capability variants the UI hides by
		// default behind "Show all" (the meta backend is the preferred entry).
		aliasedByMeta := make(map[string]bool)
		for _, b := range backends {
			if !b.IsMeta() {
				continue
			}
			for _, concreteName := range b.CapabilitiesMap {
				aliasedByMeta[concreteName] = true
			}
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

		// Get dev suffix from SystemState for development backend detection
		devSuffix := ""
		if appConfig.SystemState != nil {
			devSuffix = appConfig.SystemState.BackendDevSuffix
		}

		// Convert backends to JSON-friendly format and deduplicate by ID
		backendsJSON := make([]map[string]any, 0, len(backends))
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

			// Per-node distribution + parent meta lookup for the install picker.
			// `nodes` populates the Nodes column on the gallery; `metaBackendFor`
			// lets the picker name the parent (e.g. "CPU build of llama-cpp")
			// without re-walking the whole gallery on the client.
			var perNode []gallery.NodeBackendRef
			if installedBackends != nil {
				if sb, ok := installedBackends.Get(b.Name); ok {
					perNode = sb.Nodes
				}
			}

			backendsJSON = append(backendsJSON, map[string]any{
				"id":             backendID,
				"name":           b.Name,
				"description":    b.Description,
				"icon":           b.Icon,
				"license":        b.License,
				"urls":           b.URLs,
				"tags":           b.Tags,
				"gallery":        b.Gallery.Name,
				"installed":      b.Installed,
				"version":        b.Version,
				"processing":     currentlyProcessing,
				"jobID":          jobID,
				"isDeletion":     isDeletionOp,
				"isMeta":         b.IsMeta(),
				"isAlias":        aliasedByMeta[b.Name],
				"isDevelopment":  b.IsDevelopment(devSuffix),
				"capabilities":   b.CapabilitiesMap,
				"metaBackendFor": metaParentOf(b.Name, backends),
				"nodes":          perNode,
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

		return c.JSON(200, map[string]any{
			"backends":                  backendsJSON,
			"repositories":              appConfig.BackendGalleries,
			"allTags":                   tags,
			"processingBackends":        processingBackendsData,
			"taskTypes":                 taskTypes,
			"availableBackends":         totalBackends,
			"installedBackends":         installedBackendsCount,
			"currentPage":               pageNum,
			"totalPages":                totalPages,
			"prevPage":                  prevPage,
			"nextPage":                  nextPage,
			"systemCapability":          detectedCapability,
			"preferDevelopmentBackends": appConfig.PreferDevelopmentBackends,
		})
	}, adminMiddleware)

	app.POST("/api/backends/install/:id", func(c echo.Context) error {
		backendID := c.Param("id")
		// URL decode the backend ID
		backendID, err := url.QueryUnescape(backendID)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error": "invalid backend ID",
			})
		}
		xlog.Debug("API job submitted to install backend", "backendID", backendID)

		id, err := uuid.NewUUID()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]any{
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
			// The React UI's "Reinstall backend" action reuses this route, so
			// the op must force even when the backend is already installed.
			Force: true,
		}
		// Store cancellation function immediately so queued operations can be cancelled
		galleryService.StoreCancellation(uid, cancelFunc)
		go func() {
			galleryService.BackendGalleryChannel <- op
		}()

		return c.JSON(200, map[string]any{
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
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error": "invalid request body",
			})
		}

		// Validate required fields
		if req.URI == "" {
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error": "uri is required",
			})
		}

		xlog.Debug("API job submitted to install external backend", "uri", req.URI, "name", req.Name, "alias", req.Alias)

		id, err := uuid.NewUUID()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]any{
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

		return c.JSON(200, map[string]any{
			"jobID":   uid,
			"message": "External backend installation started",
		})
	}, adminMiddleware)

	app.POST("/api/backends/delete/:id", func(c echo.Context) error {
		backendID := c.Param("id")
		// URL decode the backend ID
		backendID, err := url.QueryUnescape(backendID)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{
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
			return c.JSON(http.StatusInternalServerError, map[string]any{
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

		return c.JSON(200, map[string]any{
			"jobID":   uid,
			"message": "Backend deletion started",
		})
	}, adminMiddleware)

	app.GET("/api/backends/job/:uid", func(c echo.Context) error {
		jobUID := c.Param("uid")

		status := galleryService.GetStatus(jobUID)
		if status == nil {
			// Job is queued but hasn't started processing yet
			return c.JSON(200, map[string]any{
				"progress":           0,
				"message":            "Operation queued",
				"galleryElementName": "",
				"processed":          false,
				"deletion":           false,
				"queued":             true,
			})
		}

		response := map[string]any{
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
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error": "invalid backend name",
			})
		}
		xlog.Debug("API request to delete system backend", "backendName", backendName)

		// Use the gallery service's backend manager, which in distributed mode
		// fans out deletion to worker nodes via NATS.
		if err := galleryService.DeleteBackend(backendName); err != nil {
			xlog.Error("Failed to delete backend", "error", err, "backendName", backendName)
			return c.JSON(http.StatusInternalServerError, map[string]any{
				"error": err.Error(),
			})
		}

		return c.JSON(200, map[string]any{
			"success": true,
			"message": "Backend deleted successfully",
		})
	}, adminMiddleware)

	// Backend upgrade APIs
	app.GET("/api/backends/upgrades", func(c echo.Context) error {
		if applicationInstance == nil || applicationInstance.UpgradeChecker() == nil {
			return c.JSON(200, map[string]any{})
		}
		return c.JSON(200, applicationInstance.UpgradeChecker().GetAvailableUpgrades())
	}, adminMiddleware)

	app.POST("/api/backends/upgrades/check", func(c echo.Context) error {
		if applicationInstance == nil || applicationInstance.UpgradeChecker() == nil {
			return c.JSON(200, map[string]any{})
		}
		applicationInstance.UpgradeChecker().TriggerCheck()
		return c.JSON(200, applicationInstance.UpgradeChecker().GetAvailableUpgrades())
	}, adminMiddleware)

	app.POST("/api/backends/upgrade/:name", func(c echo.Context) error {
		backendName := c.Param("name")
		backendName, err := url.QueryUnescape(backendName)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{
				"error": "invalid backend name",
			})
		}

		id, err := uuid.NewUUID()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
		}

		uid := id.String()

		// Register in opcache so the operation shows up in /api/operations
		// and the Backends UI can reflect progress on the affected row.
		opcache.SetBackend(backendName, uid)

		ctx, cancelFunc := context.WithCancel(context.Background())
		op := galleryop.ManagementOp[gallery.GalleryBackend, any]{
			ID:                 uid,
			GalleryElementName: backendName,
			Galleries:          appConfig.BackendGalleries,
			Upgrade:            true,
			Context:            ctx,
			CancelFunc:         cancelFunc,
		}
		// Store cancellation function immediately so queued operations can be cancelled
		galleryService.StoreCancellation(uid, cancelFunc)
		// Non-blocking send — BackendGalleryChannel is unbuffered and a direct
		// send would hang the HTTP handler whenever the worker is busy.
		go func() {
			galleryService.BackendGalleryChannel <- op
		}()

		return c.JSON(200, map[string]any{
			"jobID":     uid,
			"uuid":      uid,
			"statusUrl": fmt.Sprintf("/api/backends/job/%s", uid),
			"message":   "Backend upgrade started",
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

		nodesJSON := make([]map[string]any, 0, len(nodes))
		for _, n := range nodes {
			nodesJSON = append(nodesJSON, map[string]any{
				"name":          n.Name,
				"id":            n.ID,
				"tunnelAddress": n.TunnelAddress,
				"serviceID":     n.ServiceID,
				"lastSeen":      n.LastSeen,
				"isOnline":      n.IsOnline(),
			})
		}

		return c.JSON(200, map[string]any{
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

		response := map[string]any{
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

	// Branding / whitelabeling. The read endpoint and the asset server are
	// public so the login screen can render the configured logo and instance
	// name before authentication. Mutations are admin-only. See app.go where
	// "/api/branding" and "/branding/" are added to PathWithoutAuth.
	app.GET("/api/branding", localai.GetBrandingEndpoint(appConfig))
	app.GET("/branding/asset/:kind", localai.ServeBrandingAssetEndpoint(appConfig))
	app.POST("/api/branding/asset/:kind", localai.UploadBrandingAssetEndpoint(appConfig), adminMiddleware)
	app.DELETE("/api/branding/asset/:kind", localai.DeleteBrandingAssetEndpoint(appConfig), adminMiddleware)

}
