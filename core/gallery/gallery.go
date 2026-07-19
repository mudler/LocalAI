package gallery

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lithammer/fuzzysearch/fuzzy"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/LocalAI/pkg/xsync"
	"github.com/mudler/xlog"

	"gopkg.in/yaml.v3"
)

// validateGalleryConfigURL guards the gallery config fetch against SSRF. A
// gallery config URL can be attacker-controlled (e.g. POST /models/apply with
// an empty id fetches it directly), so a plain http(s) URL must not be allowed
// to reach private, loopback, link-local or cloud-metadata addresses. Other
// schemes (huggingface://, github:, oci://, ollama://, file://) resolve to
// fixed public services or local files and are not a network-SSRF vector, so
// they are left untouched.
// See https://github.com/mudler/LocalAI/issues/10665
func validateGalleryConfigURL(rawURL string) error {
	lower := strings.ToLower(strings.TrimSpace(rawURL))
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return utils.ValidateExternalURL(rawURL)
	}
	return nil
}

func GetGalleryConfigFromURL[T any](url string, basePath string) (T, error) {
	var config T
	if err := validateGalleryConfigURL(url); err != nil {
		xlog.Error("refusing to fetch gallery config", "error", err, "url", url)
		return config, err
	}
	uri := downloader.URI(url)
	err := uri.ReadWithCallback(basePath, func(url string, d []byte) error {
		return yaml.Unmarshal(d, &config)
	})
	if err != nil {
		xlog.Error("failed to get gallery config for url", "error", err, "url", url)
		return config, err
	}
	return config, nil
}

func GetGalleryConfigFromURLWithContext[T any](ctx context.Context, url string, basePath string) (T, error) {
	var config T
	if err := validateGalleryConfigURL(url); err != nil {
		xlog.Error("refusing to fetch gallery config", "error", err, "url", url)
		return config, err
	}
	uri := downloader.URI(url)
	err := uri.ReadWithAuthorizationAndCallback(ctx, basePath, "", func(url string, d []byte) error {
		return yaml.Unmarshal(d, &config)
	})
	if err != nil {
		xlog.Error("failed to get gallery config for url", "error", err, "url", url)
		return config, err
	}
	return config, nil
}

func ReadConfigFile[T any](filePath string) (*T, error) {
	// Read the YAML file
	yamlFile, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read YAML file: %v", err)
	}

	// Unmarshal YAML data into a Config struct
	var config T
	err = yaml.Unmarshal(yamlFile, &config)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal YAML: %v", err)
	}

	return &config, nil
}

type GalleryElement interface {
	SetGallery(gallery config.Gallery)
	SetInstalled(installed bool)
	GetName() string
	GetDescription() string
	GetTags() []string
	GetInstalled() bool
	GetLicense() string
	GetGallery() config.Gallery
}

type GalleryElements[T GalleryElement] []T

func (gm GalleryElements[T]) Search(term string) GalleryElements[T] {
	var filteredModels GalleryElements[T]
	term = strings.ToLower(term)
	for _, m := range gm {
		if fuzzy.Match(term, strings.ToLower(m.GetName())) ||
			fuzzy.Match(term, strings.ToLower(m.GetGallery().Name)) ||
			strings.Contains(strings.ToLower(m.GetName()), term) ||
			strings.Contains(strings.ToLower(m.GetDescription()), term) ||
			strings.Contains(strings.ToLower(m.GetGallery().Name), term) ||
			strings.Contains(strings.ToLower(strings.Join(m.GetTags(), ",")), term) {
			filteredModels = append(filteredModels, m)
		}
	}

	return filteredModels
}

// FilterGalleryModelsByUsecase returns models whose known_usecases include all
// the bits set in usecase. For example, passing FLAG_CHAT matches any model
// with the chat usecase; passing FLAG_CHAT|FLAG_VISION matches only models
// that have both.
func FilterGalleryModelsByUsecase(models GalleryElements[*GalleryModel], usecase config.ModelConfigUsecase) GalleryElements[*GalleryModel] {
	var filtered GalleryElements[*GalleryModel]
	for _, m := range models {
		u := m.GetKnownUsecases()
		if u != nil && (*u&usecase) == usecase {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

// FilterGalleryModelsByMultimodal returns models whose known_usecases span two
// or more orthogonal modality groups (e.g. chat+vision, tts+transcript).
func FilterGalleryModelsByMultimodal(models GalleryElements[*GalleryModel]) GalleryElements[*GalleryModel] {
	var filtered GalleryElements[*GalleryModel]
	for _, m := range models {
		u := m.GetKnownUsecases()
		if u != nil && config.IsMultimodal(*u) {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

func (gm GalleryElements[T]) FilterByTag(tag string) GalleryElements[T] {
	var filtered GalleryElements[T]
	for _, m := range gm {
		for _, t := range m.GetTags() {
			if strings.EqualFold(t, tag) {
				filtered = append(filtered, m)
				break
			}
		}
	}
	return filtered
}

func (gm GalleryElements[T]) SortByName(sortOrder string) GalleryElements[T] {
	slices.SortFunc(gm, func(a, b T) int {
		r := strings.Compare(strings.ToLower(a.GetName()), strings.ToLower(b.GetName()))
		if sortOrder == "desc" {
			return -r
		}
		return r
	})
	return gm
}

func (gm GalleryElements[T]) SortByRepository(sortOrder string) GalleryElements[T] {
	slices.SortFunc(gm, func(a, b T) int {
		r := strings.Compare(strings.ToLower(a.GetGallery().Name), strings.ToLower(b.GetGallery().Name))
		if sortOrder == "desc" {
			return -r
		}
		return r
	})
	return gm
}

func (gm GalleryElements[T]) SortByLicense(sortOrder string) GalleryElements[T] {
	slices.SortFunc(gm, func(a, b T) int {
		licenseA := a.GetLicense()
		licenseB := b.GetLicense()
		var r int
		if licenseA == "" && licenseB != "" {
			r = 1
		} else if licenseA != "" && licenseB == "" {
			r = -1
		} else {
			r = strings.Compare(strings.ToLower(licenseA), strings.ToLower(licenseB))
		}
		if sortOrder == "desc" {
			return -r
		}
		return r
	})
	return gm
}

func (gm GalleryElements[T]) SortByInstalled(sortOrder string) GalleryElements[T] {
	slices.SortFunc(gm, func(a, b T) int {
		var r int
		// Sort by installed status: installed items first (true > false)
		if a.GetInstalled() != b.GetInstalled() {
			if a.GetInstalled() {
				r = -1
			} else {
				r = 1
			}
		} else {
			r = strings.Compare(strings.ToLower(a.GetName()), strings.ToLower(b.GetName()))
		}
		if sortOrder == "desc" {
			return -r
		}
		return r
	})
	return gm
}

func (gm GalleryElements[T]) FindByName(name string) T {
	for _, m := range gm {
		if strings.EqualFold(m.GetName(), name) {
			return m
		}
	}
	var zero T
	return zero
}

func (gm GalleryElements[T]) Paginate(pageNum int, itemsNum int) GalleryElements[T] {
	start := (pageNum - 1) * itemsNum
	end := start + itemsNum
	if start > len(gm) {
		start = len(gm)
	}
	if end > len(gm) {
		end = len(gm)
	}
	return gm[start:end]
}

func FindGalleryElement[T GalleryElement](models []T, name string) T {
	var model T
	name = strings.ReplaceAll(name, string(os.PathSeparator), "__")

	if !strings.Contains(name, "@") {
		for _, m := range models {
			if strings.EqualFold(strings.ToLower(m.GetName()), strings.ToLower(name)) {
				model = m
				break
			}
		}

	} else {
		for _, m := range models {
			if strings.EqualFold(strings.ToLower(name), strings.ToLower(fmt.Sprintf("%s@%s", m.GetGallery().Name, m.GetName()))) {
				model = m
				break
			}
		}
	}

	return model
}

// List available models
// Models galleries are a list of yaml files that are hosted on a remote server (for example github).
// Each yaml file contains a list of models that can be downloaded and optionally overrides to define a new model setting.
func AvailableGalleryModels(galleries []config.Gallery, systemState *system.SystemState) (GalleryElements[*GalleryModel], error) {
	var models []*GalleryModel

	// Get models from galleries
	for _, gallery := range galleries {
		galleryModels, err := getGalleryElements(gallery, systemState.Model.ModelsPath, func(model *GalleryModel) bool {
			if _, err := os.Stat(filepath.Join(systemState.Model.ModelsPath, fmt.Sprintf("%s.yaml", model.GetName()))); err == nil {
				return true
			}
			return false
		})
		if err != nil {
			return nil, err
		}

		// Resolve model URLs locally (for local galleries) and collect unique
		// URLs that need fetching for backend resolution.
		uniqueURLs := map[string]struct{}{}
		for _, m := range galleryModels {
			if m.URL != "" {
				m.URL = resolveModelURLLocally(m.URL, gallery.URL)
			}
			if m.Backend == "" && m.URL != "" {
				uniqueURLs[m.URL] = struct{}{}
			}
		}

		// Pre-warm cache with parallel fetches to avoid sequential HTTP
		// requests on cold start (~50 unique gallery config files).
		if len(uniqueURLs) > 0 {
			urls := make([]string, 0, len(uniqueURLs))
			for u := range uniqueURLs {
				urls = append(urls, u)
			}
			prefetchModelConfigs(urls, systemState.Model.ModelsPath)
		}

		// Resolve backends from warm cache.
		for _, m := range galleryModels {
			if m.Backend == "" {
				m.Backend = resolveBackend(m, systemState.Model.ModelsPath)
			}
		}

		models = append(models, galleryModels...)
	}

	return models, nil
}

var (
	availableModelsMu    sync.RWMutex
	availableModelsCache GalleryElements[*GalleryModel]
	refreshing           atomic.Bool
	galleryGeneration    atomic.Uint64
)

// GalleryGeneration returns a counter that increments each time the gallery
// model list is refreshed from upstream. VRAM estimation caches use this to
// invalidate entries when the gallery data changes.
func GalleryGeneration() uint64 { return galleryGeneration.Load() }

// AvailableGalleryModelsCached returns gallery models from an in-memory cache.
// Local-only fields (installed status) are refreshed on every call. A background
// goroutine is triggered to re-fetch the full model list (including network
// calls) so subsequent requests pick up changes without blocking the caller.
// The first call with an empty cache blocks until the initial load completes.
func AvailableGalleryModelsCached(galleries []config.Gallery, systemState *system.SystemState) (GalleryElements[*GalleryModel], error) {
	availableModelsMu.RLock()
	cached := availableModelsCache
	availableModelsMu.RUnlock()

	if cached != nil {
		// Refresh installed status under write lock to avoid races with
		// concurrent readers and the background refresh goroutine.
		availableModelsMu.Lock()
		for _, m := range cached {
			_, err := os.Stat(filepath.Join(systemState.Model.ModelsPath, fmt.Sprintf("%s.yaml", m.GetName())))
			m.SetInstalled(err == nil)
		}
		availableModelsMu.Unlock()
		// Trigger a background refresh if one is not already running.
		triggerGalleryRefresh(galleries, systemState)
		return cached, nil
	}

	// No cache yet — must do a blocking load.
	models, err := AvailableGalleryModels(galleries, systemState)
	if err != nil {
		return nil, err
	}

	availableModelsMu.Lock()
	availableModelsCache = models
	galleryGeneration.Add(1)
	availableModelsMu.Unlock()

	return models, nil
}

// triggerGalleryRefresh starts a background goroutine that refreshes the
// gallery model cache. Only one refresh runs at a time; concurrent calls
// are no-ops.
func triggerGalleryRefresh(galleries []config.Gallery, systemState *system.SystemState) {
	if !refreshing.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer refreshing.Store(false)
		models, err := AvailableGalleryModels(galleries, systemState)
		if err != nil {
			xlog.Error("background gallery refresh failed", "error", err)
			return
		}
		availableModelsMu.Lock()
		availableModelsCache = models
		galleryGeneration.Add(1)
		availableModelsMu.Unlock()
	}()
}

// List available backends
func AvailableBackends(galleries []config.Gallery, systemState *system.SystemState) (GalleryElements[*GalleryBackend], error) {
	return availableBackendsWithFilter(galleries, systemState, func(backend *GalleryBackend) bool {
		return backend.IsCompatibleWith(systemState)
	})
}

// AvailableBackendsUnfiltered returns all available backends without filtering by system capability.
func AvailableBackendsUnfiltered(galleries []config.Gallery, systemState *system.SystemState) (GalleryElements[*GalleryBackend], error) {
	return availableBackendsWithFilter(galleries, systemState, nil)
}

// AvailableBackendsForCapabilities lists backends runnable on the local system
// OR on any remote host reporting one of the supplied capabilities.
//
// In a distributed deployment the host serving this listing (the controller)
// is usually a GPU-less pod while the GPUs live on worker nodes. Filtering
// only against the controller hid every GPU-only meta backend from admins even
// though installing it by name on a worker worked fine, so compatibility is
// evaluated as a union over the cluster. An empty capabilities slice reproduces
// AvailableBackends exactly, keeping single-node behavior untouched.
func AvailableBackendsForCapabilities(galleries []config.Gallery, systemState *system.SystemState, capabilities []string) (GalleryElements[*GalleryBackend], error) {
	if len(capabilities) == 0 {
		return AvailableBackends(galleries, systemState)
	}

	// Each remote capability is evaluated through a state pinned to that exact
	// capability, so the controller's own detection (and any forced capability
	// on the controller image) cannot leak into the worker's verdict. Backend
	// paths still come from the controller's state because that is where the
	// gallery metadata is read from.
	nodeStates := make([]*system.SystemState, 0, len(capabilities))
	for _, capability := range capabilities {
		nodeStates = append(nodeStates, system.NewCapabilityState(capability,
			system.WithBackendPath(systemState.Backend.BackendsPath)))
	}

	return availableBackendsWithFilter(galleries, systemState, func(backend *GalleryBackend) bool {
		if backend.IsCompatibleWith(systemState) {
			return true
		}
		for _, nodeState := range nodeStates {
			if backend.IsCompatibleWith(nodeState) {
				return true
			}
		}
		return false
	})
}

// availableBackendsWithFilter lists available backends, keeping only those
// accepted by compatible. A nil compatible keeps everything.
func availableBackendsWithFilter(galleries []config.Gallery, systemState *system.SystemState, compatible func(*GalleryBackend) bool) (GalleryElements[*GalleryBackend], error) {
	var backends []*GalleryBackend

	systemBackends, err := ListSystemBackends(systemState)
	if err != nil {
		return nil, err
	}

	// Get backends from galleries
	for _, gallery := range galleries {
		galleryBackends, err := getGalleryElements(gallery, systemState.Backend.BackendsPath, func(backend *GalleryBackend) bool {
			return systemBackends.Exists(backend.GetName())
		})
		if err != nil {
			return nil, err
		}

		if compatible == nil {
			backends = append(backends, galleryBackends...)
			continue
		}

		for _, backend := range galleryBackends {
			if compatible(backend) {
				backends = append(backends, backend)
			}
		}
	}

	return backends, nil
}

func findGalleryURLFromReferenceURL(url string, basePath string) (string, error) {
	var refFile string
	uri := downloader.URI(url)
	err := uri.ReadWithCallback(basePath, func(url string, d []byte) error {
		refFile = string(d)
		if len(refFile) == 0 {
			return fmt.Errorf("invalid reference file at url %s: %s", url, d)
		}
		cutPoint := strings.LastIndex(url, "/")
		refFile = url[:cutPoint+1] + refFile
		return nil
	})
	return refFile, err
}

type galleryCacheEntry struct {
	yamlEntry   []byte
	lastUpdated time.Time
}

func (entry galleryCacheEntry) hasExpired() bool {
	return entry.lastUpdated.Before(time.Now().Add(-1 * time.Hour))
}

var galleryCache = xsync.NewSyncedMap[string, galleryCacheEntry]()

func getGalleryElements[T GalleryElement](gallery config.Gallery, basePath string, isInstalledCallback func(T) bool) ([]T, error) {
	var models []T = []T{}

	if strings.HasSuffix(gallery.URL, ".ref") {
		var err error
		gallery.URL, err = findGalleryURLFromReferenceURL(gallery.URL, basePath)
		if err != nil {
			return models, err
		}
	}

	cacheKey := fmt.Sprintf("%s-%s", gallery.Name, gallery.URL)
	if galleryCache.Exists(cacheKey) {
		entry := galleryCache.Get(cacheKey)
		// refresh if last updated is more than 1 hour ago
		if !entry.hasExpired() {
			err := yaml.Unmarshal(entry.yamlEntry, &models)
			if err != nil {
				return models, err
			}
		} else {
			galleryCache.Delete(cacheKey)
		}
	}

	uri := downloader.URI(gallery.URL)

	if len(models) == 0 {
		err := uri.ReadWithCallback(basePath, func(url string, d []byte) error {
			galleryCache.Set(cacheKey, galleryCacheEntry{
				yamlEntry:   d,
				lastUpdated: time.Now(),
			})
			return yaml.Unmarshal(d, &models)
		})
		if err != nil {
			if yamlErr, ok := err.(*yaml.TypeError); ok {
				xlog.Debug("YAML errors", "errors", strings.Join(yamlErr.Errors, "\n"), "models", models)
			}
			return models, fmt.Errorf("failed to read gallery elements: %w", err)
		}
	}

	// Add gallery to models
	for _, model := range models {
		model.SetGallery(gallery)
		model.SetInstalled(isInstalledCallback(model))
	}
	return models, nil
}
