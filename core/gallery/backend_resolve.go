package gallery

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/xsync"
	"github.com/mudler/xlog"
	"gopkg.in/yaml.v3"
)

// modelConfigCacheEntry holds a cached parsed config_file map from a URL-referenced model config.
type modelConfigCacheEntry struct {
	configMap   map[string]any
	lastUpdated time.Time
}

func (e modelConfigCacheEntry) hasExpired() bool {
	return e.lastUpdated.Before(time.Now().Add(-1 * time.Hour))
}

// modelConfigCache caches parsed model config maps keyed by URL.
var modelConfigCache = xsync.NewSyncedMap[string, modelConfigCacheEntry]()

// resolveBackend determines the backend for a GalleryModel by checking (in priority order):
// 1. Overrides["backend"] — highest priority, same as install-time merge
// 2. Inline ConfigFile["backend"] — for models with inline config maps
// 3. URL-referenced config file — fetched, parsed, and cached
//
// The model's URL should already be resolved (local override applied) before calling this.
func resolveBackend(m *GalleryModel, basePath string) string {
	// 1. Overrides take priority (matches install-time mergo.WithOverride behavior)
	if b, ok := m.Overrides["backend"].(string); ok && b != "" {
		return b
	}

	// 2. Inline config_file map
	if b, ok := m.ConfigFile["backend"].(string); ok && b != "" {
		return b
	}

	// 3. Fetch and parse the URL-referenced config
	if m.URL != "" {
		configMap := fetchModelConfigMap(m.URL, basePath)
		if b, ok := configMap["backend"].(string); ok && b != "" {
			return b
		}
	}

	return ""
}

// fetchModelConfigMap fetches a model config URL, parses the config_file YAML string
// inside it, and returns the result as a map. Results are cached for 1 hour.
// Local file:// URLs skip the cache so edits are picked up immediately.
func fetchModelConfigMap(modelURL, basePath string) map[string]any {
	// Check cache (skip for file:// URLs so local edits are picked up immediately)
	isLocal := strings.HasPrefix(modelURL, downloader.LocalPrefix)
	if !isLocal && modelConfigCache.Exists(modelURL) {
		entry := modelConfigCache.Get(modelURL)
		if !entry.hasExpired() {
			return entry.configMap
		}
		modelConfigCache.Delete(modelURL)
	}

	// Reuse existing gallery config fetcher
	modelConfig, err := GetGalleryConfigFromURL[ModelConfig](modelURL, basePath)
	if err != nil {
		xlog.Debug("Failed to fetch model config for backend resolution", "url", modelURL, "error", err)
		// Cache the failure for remote URLs to avoid repeated fetch attempts
		if !isLocal {
			modelConfigCache.Set(modelURL, modelConfigCacheEntry{
				configMap:   map[string]any{},
				lastUpdated: time.Now(),
			})
		}
		return map[string]any{}
	}

	// Parse the config_file YAML string into a map
	configMap := make(map[string]any)
	if modelConfig.ConfigFile != "" {
		if err := yaml.Unmarshal([]byte(modelConfig.ConfigFile), &configMap); err != nil {
			xlog.Debug("Failed to parse config_file for backend resolution", "url", modelURL, "error", err)
		}
	}

	// Cache for remote URLs
	if !isLocal {
		modelConfigCache.Set(modelURL, modelConfigCacheEntry{
			configMap:   configMap,
			lastUpdated: time.Now(),
		})
	}

	return configMap
}

// prefetchModelConfigs fetches model config URLs in parallel to warm the cache.
// This avoids sequential HTTP requests on cold start (~50 unique gallery files).
func prefetchModelConfigs(urls []string, basePath string) {
	const maxConcurrency = 10
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	for _, url := range urls {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			fetchModelConfigMap(url, basePath)
		})
	}
	wg.Wait()
}

// resolveModelURLLocally attempts to resolve a github: model URL to a local file://
// path when the gallery itself was loaded from a local path. This supports development
// workflows where new model files are added locally before being pushed to GitHub.
//
// For example, if the gallery was loaded from file:///path/to/gallery/index.yaml
// and a model references github:mudler/LocalAI/gallery/foo.yaml@master, this will
// check if /path/to/gallery/foo.yaml exists locally and return file:///path/to/gallery/foo.yaml.
//
// This is applied to model.URL in AvailableGalleryModels so that both listing (backend
// resolution) and installation use the same resolved URL.
func resolveModelURLLocally(modelURL, galleryURL string) string {
	galleryDir := localGalleryDir(galleryURL)
	if galleryDir == "" {
		return modelURL
	}

	// Only handle github: URLs
	if !strings.HasPrefix(modelURL, downloader.GithubURI) && !strings.HasPrefix(modelURL, downloader.GithubURI2) {
		return modelURL
	}

	// Extract the filename from the github URL
	// Format: github:org/repo/path/to/file.yaml@branch
	raw := strings.TrimPrefix(modelURL, downloader.GithubURI2)
	raw = strings.TrimPrefix(raw, downloader.GithubURI)
	// Remove @branch suffix
	if idx := strings.LastIndex(raw, "@"); idx >= 0 {
		raw = raw[:idx]
	}
	filename := filepath.Base(raw)

	localPath := filepath.Join(galleryDir, filename)
	if _, err := os.Stat(localPath); err == nil {
		return downloader.LocalPrefix + localPath
	}

	return modelURL
}

// localGalleryDir returns the directory of a gallery URL if it's local, or "" if remote.
func localGalleryDir(galleryURL string) string {
	if strings.HasPrefix(galleryURL, downloader.LocalPrefix) {
		return filepath.Dir(strings.TrimPrefix(galleryURL, downloader.LocalPrefix))
	}
	// Plain path (no scheme) that exists on disk
	if !strings.Contains(galleryURL, "://") && !strings.HasPrefix(galleryURL, downloader.GithubURI) {
		if info, err := os.Stat(galleryURL); err == nil && !info.IsDir() {
			return filepath.Dir(galleryURL)
		}
	}
	return ""
}
