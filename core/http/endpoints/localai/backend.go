package localai

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/gallery/importers"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
)

// knownPrefOnlyBackends lists backends that have no dedicated importer
// and no importer-hosted drop-in entry, but users may still pick them
// via the preference-only path. Edit this slice to add new pref-only
// backends that should appear in the import form dropdown.
var knownPrefOnlyBackends = []schema.KnownBackend{
	// Text LLM
	{Name: "sglang", Modality: "text", AutoDetect: false, Description: "SGLang runtime (preference-only)"},
	{Name: "tinygrad", Modality: "text", AutoDetect: false, Description: "tinygrad runtime (preference-only)"},
	{Name: "trl", Modality: "text", AutoDetect: false, Description: "Transformers Reinforcement Learning (preference-only)"},
	{Name: "mlx-vlm", Modality: "text", AutoDetect: false, Description: "MLX vision-language models (preference-only)"},
	// ASR
	{Name: "whisperx", Modality: "asr", AutoDetect: false, Description: "WhisperX transcription (preference-only)"},
	// TTS
	{Name: "kokoros", Modality: "tts", AutoDetect: false, Description: "Kokoros TTS (preference-only)"},
	{Name: "qwen-tts", Modality: "tts", AutoDetect: false, Description: "Qwen TTS (preference-only)"},
	{Name: "qwen3-tts-cpp", Modality: "tts", AutoDetect: false, Description: "Qwen3 TTS C++ (preference-only)"},
	{Name: "faster-qwen3-tts", Modality: "tts", AutoDetect: false, Description: "Faster Qwen3 TTS (preference-only)"},
	// Detection
	{Name: "sam3-cpp", Modality: "detection", AutoDetect: false, Description: "SAM3 C++ object detection (preference-only)"},
	// Audio transform (audio-in / audio-out, optional reference signal)
	{Name: "localvqe", Modality: "audio-transform", AutoDetect: false, Description: "LocalVQE C++ joint AEC + noise suppression + dereverberation (preference-only)"},
}

// UpgradeInfoProvider is an interface for querying cached backend upgrade information.
type UpgradeInfoProvider interface {
	GetAvailableUpgrades() map[string]gallery.UpgradeInfo
	TriggerCheck()
}

type BackendEndpointService struct {
	galleries         []config.Gallery
	backendPath       string
	backendSystemPath string
	backendApplier    *galleryop.GalleryService
	upgradeChecker    UpgradeInfoProvider
}

type GalleryBackend struct {
	ID string `json:"id"`
}

func CreateBackendEndpointService(galleries []config.Gallery, systemState *system.SystemState, backendApplier *galleryop.GalleryService, upgradeChecker UpgradeInfoProvider) BackendEndpointService {
	return BackendEndpointService{
		galleries:         galleries,
		backendPath:       systemState.Backend.BackendsPath,
		backendSystemPath: systemState.Backend.BackendsSystemPath,
		backendApplier:    backendApplier,
		upgradeChecker:    upgradeChecker,
	}
}

// GetOpStatusEndpoint returns the job status
// @Summary Returns the job status
// @Tags backends
// @Success 200 {object} galleryop.OpStatus "Response"
// @Router /backends/jobs/{uuid} [get]
func (mgs *BackendEndpointService) GetOpStatusEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		status := mgs.backendApplier.GetStatus(c.Param("uuid"))
		if status == nil {
			return fmt.Errorf("could not find any status for ID")
		}
		return c.JSON(200, status)
	}
}

// GetAllStatusEndpoint returns all the jobs status progress
// @Summary Returns all the jobs status progress
// @Tags backends
// @Success 200 {object} map[string]galleryop.OpStatus "Response"
// @Router /backends/jobs [get]
func (mgs *BackendEndpointService) GetAllStatusEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.JSON(200, mgs.backendApplier.GetAllStatus())
	}
}

// ApplyBackendEndpoint installs a new backend to a LocalAI instance
// @Summary Install backends to LocalAI.
// @Tags backends
// @Param request body GalleryBackend true "query params"
// @Success 200 {object} schema.BackendResponse "Response"
// @Router /backends/apply [post]
func (mgs *BackendEndpointService) ApplyBackendEndpoint(systemState *system.SystemState) echo.HandlerFunc {
	return func(c echo.Context) error {
		input := new(GalleryBackend)
		// Get input data from the request body
		if err := c.Bind(input); err != nil {
			return err
		}

		// In distributed mode, refuse to fan out a hardware-specific build to
		// every node — a CPU build landing on a GPU cluster is almost always
		// wrong, and the silent footgun is exactly what this guard exists for.
		// Auto-resolving (meta) backends are fine because each node picks its
		// own variant. Tooling can recover by hitting
		// POST /api/nodes/{id}/backends/install per target node.
		if mgs.backendApplier.BackendManager().IsDistributed() && input.ID != "" {
			if guard := concreteFanOutGuard(c, mgs.galleries, systemState, input.ID); guard != nil {
				return guard
			}
		}

		uuid, err := uuid.NewUUID()
		if err != nil {
			return err
		}
		mgs.backendApplier.BackendGalleryChannel <- galleryop.ManagementOp[gallery.GalleryBackend, any]{
			ID:                 uuid.String(),
			GalleryElementName: input.ID,
			Galleries:          mgs.galleries,
		}

		return c.JSON(200, schema.BackendResponse{ID: uuid.String(), StatusURL: fmt.Sprintf("%sbackends/jobs/%s", middleware.BaseURL(c), uuid.String())})
	}
}

// concreteFanOutGuard returns a 409 response if the requested backend is a
// hardware-specific build (not auto-resolving / meta) and we are in
// distributed mode. It looks up the backend in the configured galleries; if
// the lookup itself fails (gallery unreachable, name not found), the guard
// stays out of the way and lets the install enqueue normally — a missing
// name will surface from the worker as a clearer error than the guard could
// produce here. The response body deliberately speaks human, with `code` and
// `meta_alternative` as the programmatic contract for tooling.
func concreteFanOutGuard(c echo.Context, galleries []config.Gallery, systemState *system.SystemState, backendID string) error {
	// Use the unfiltered listing because in distributed mode the frontend's
	// hardware is irrelevant — the install targets workers, not us — and the
	// filtered list would hide variants that don't match the frontend host
	// (e.g. a CUDA build on a CPU-only frontend), preventing the guard from
	// firing for exactly the cases it's meant to protect against.
	available, err := gallery.AvailableBackendsUnfiltered(galleries, systemState)
	if err != nil {
		return nil
	}
	requested := available.FindByName(backendID)
	if requested == nil || requested.IsMeta() {
		return nil
	}

	// Try to find an auto-resolving (meta) backend that has this concrete
	// variant in its CapabilitiesMap, so we can suggest it as a one-shot
	// alternative. Optional — empty string is fine if no parent exists.
	metaAlternative := ""
	for _, b := range available {
		if !b.IsMeta() {
			continue
		}
		for _, concrete := range b.CapabilitiesMap {
			if concrete == backendID {
				metaAlternative = b.Name
				break
			}
		}
		if metaAlternative != "" {
			break
		}
	}

	msg := fmt.Sprintf(
		"Backend %q is a hardware-specific build and won't run correctly on every node in this cluster. In distributed mode, install it on specific nodes:\n\n  POST /api/nodes/{node_id}/backends/install\n  {\"backend\": %q}",
		backendID, backendID,
	)
	if metaAlternative != "" {
		msg += fmt.Sprintf(
			"\n\nTo install across all nodes, use the auto-resolving backend %q — each node picks its own variant based on its hardware.",
			metaAlternative,
		)
	}

	return c.JSON(409, map[string]any{
		"error":            msg,
		"code":             "concrete_backend_requires_target",
		"meta_alternative": metaAlternative,
	})
}

// DeleteBackendEndpoint lets delete backends from a LocalAI instance
// @Summary delete backends from LocalAI.
// @Tags backends
// @Param name	path string	true	"Backend name"
// @Success 200 {object} schema.BackendResponse "Response"
// @Router /backends/delete/{name} [post]
func (mgs *BackendEndpointService) DeleteBackendEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		backendName := c.Param("name")

		mgs.backendApplier.BackendGalleryChannel <- galleryop.ManagementOp[gallery.GalleryBackend, any]{
			Delete:             true,
			GalleryElementName: backendName,
			Galleries:          mgs.galleries,
		}

		uuid, err := uuid.NewUUID()
		if err != nil {
			return err
		}

		return c.JSON(200, schema.BackendResponse{ID: uuid.String(), StatusURL: fmt.Sprintf("%sbackends/jobs/%s", middleware.BaseURL(c), uuid.String())})
	}
}

// ListBackendsEndpoint list the available backends configured in LocalAI
// @Summary List all Backends
// @Tags backends
// @Success 200 {object} []gallery.GalleryBackend "Response"
// @Router /backends [get]
func (mgs *BackendEndpointService) ListBackendsEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		backends, err := mgs.backendApplier.ListBackends()
		if err != nil {
			return err
		}
		return c.JSON(200, backends.GetAll())
	}
}

// ListModelGalleriesEndpoint list the available galleries configured in LocalAI
// @Summary List all Galleries
// @Tags backends
// @Success 200 {object} []config.Gallery "Response"
// @Router /backends/galleries [get]
// NOTE: This is different (and much simpler!) than above! This JUST lists the model galleries that have been loaded, not their contents!
func (mgs *BackendEndpointService) ListBackendGalleriesEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		xlog.Debug("Listing backend galleries", "galleries", mgs.galleries)
		dat, err := json.Marshal(mgs.galleries)
		if err != nil {
			return err
		}
		return c.Blob(200, "application/json", dat)
	}
}

// GetUpgradesEndpoint returns the cached backend upgrade information
// @Summary Get available backend upgrades
// @Tags backends
// @Success 200 {object} map[string]gallery.UpgradeInfo "Response"
// @Router /backends/upgrades [get]
func (mgs *BackendEndpointService) GetUpgradesEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		if mgs.upgradeChecker == nil {
			return c.JSON(200, map[string]gallery.UpgradeInfo{})
		}
		return c.JSON(200, mgs.upgradeChecker.GetAvailableUpgrades())
	}
}

// CheckUpgradesEndpoint forces an immediate upgrade check
// @Summary Force backend upgrade check
// @Tags backends
// @Success 200 {object} map[string]gallery.UpgradeInfo "Response"
// @Router /backends/upgrades/check [post]
func (mgs *BackendEndpointService) CheckUpgradesEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		if mgs.upgradeChecker == nil {
			return c.JSON(200, map[string]gallery.UpgradeInfo{})
		}
		mgs.upgradeChecker.TriggerCheck()
		// Return current cached results (the triggered check runs async)
		return c.JSON(200, mgs.upgradeChecker.GetAvailableUpgrades())
	}
}

// UpgradeBackendEndpoint triggers an upgrade for a specific backend
// @Summary Upgrade a backend
// @Tags backends
// @Param name path string true "Backend name"
// @Success 200 {object} schema.BackendResponse "Response"
// @Router /backends/upgrade/{name} [post]
func (mgs *BackendEndpointService) UpgradeBackendEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		backendName := c.Param("name")

		uuid, err := uuid.NewUUID()
		if err != nil {
			return err
		}

		mgs.backendApplier.BackendGalleryChannel <- galleryop.ManagementOp[gallery.GalleryBackend, any]{
			ID:                 uuid.String(),
			GalleryElementName: backendName,
			Galleries:          mgs.galleries,
			Upgrade:            true,
		}

		return c.JSON(200, schema.BackendResponse{ID: uuid.String(), StatusURL: fmt.Sprintf("%sbackends/jobs/%s", middleware.BaseURL(c), uuid.String())})
	}
}

// ListAvailableBackendsEndpoint list the available backends in the galleries configured in LocalAI
// @Summary List all available Backends
// @Tags backends
// @Success 200 {object} []gallery.GalleryBackend "Response"
// @Router /backends/available [get]
func (mgs *BackendEndpointService) ListAvailableBackendsEndpoint(systemState *system.SystemState) echo.HandlerFunc {
	return func(c echo.Context) error {
		backends, err := gallery.AvailableBackends(mgs.galleries, systemState)
		if err != nil {
			return err
		}
		return c.JSON(200, backends)
	}
}

// ListKnownBackendsEndpoint returns every backend the import system is
// aware of, regardless of install state or host compatibility. This is
// the source of truth for the import form dropdown — users may pick a
// backend that is not yet installed so LocalAI can auto-install it.
// @Summary List all known Backends (importer registry + curated pref-only + installed-on-disk)
// @Tags backends
// @Success 200 {object} []schema.KnownBackend "Response"
// @Router /backends/known [get]
func (mgs *BackendEndpointService) ListKnownBackendsEndpoint(systemState *system.SystemState) echo.HandlerFunc {
	return func(c echo.Context) error {
		// byName dedupes entries while preserving "importer wins over
		// pref-only" priority. Insertion order: importers → drop-ins →
		// pref-only → installed-on-disk.
		byName := make(map[string]schema.KnownBackend)

		for _, imp := range importers.Registry() {
			byName[imp.Name()] = schema.KnownBackend{
				Name:       imp.Name(),
				Modality:   imp.Modality(),
				AutoDetect: imp.AutoDetects(),
			}

			if host, ok := imp.(importers.AdditionalBackendsProvider); ok {
				for _, extra := range host.AdditionalBackends() {
					if _, exists := byName[extra.Name]; exists {
						continue
					}
					byName[extra.Name] = schema.KnownBackend{
						Name:        extra.Name,
						Modality:    extra.Modality,
						AutoDetect:  false,
						Description: extra.Description,
					}
				}
			}
		}

		for _, pref := range knownPrefOnlyBackends {
			if _, exists := byName[pref.Name]; exists {
				continue
			}
			byName[pref.Name] = pref
		}

		// Surface backends installed on this host and flag them as such.
		// Importer/pref-only entries that are also on disk get Installed=true
		// while keeping their metadata. System-only backends join the map
		// with empty Modality (we can't classify them) and AutoDetect=false
		// because they require an explicit preference.
		if systemState != nil {
			installed, err := gallery.ListSystemBackends(systemState)
			if err != nil {
				xlog.Debug("ListKnownBackendsEndpoint: failed to list installed backends", "error", err)
			} else {
				for name := range installed {
					if entry, exists := byName[name]; exists {
						entry.Installed = true
						byName[name] = entry
						continue
					}
					byName[name] = schema.KnownBackend{
						Name:       name,
						Modality:   "",
						AutoDetect: false,
						Installed:  true,
					}
				}
			}
		}

		out := make([]schema.KnownBackend, 0, len(byName))
		for _, b := range byName {
			out = append(out, b)
		}
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].Modality != out[j].Modality {
				return out[i].Modality < out[j].Modality
			}
			return out[i].Name < out[j].Name
		})

		return c.JSON(200, out)
	}
}
