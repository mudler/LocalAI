// SPDX-License-Identifier: MIT
package gallery

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/xlog"
)

// ModelIndexSource says which file the host reads the models of the official
// gallery from.
type ModelIndexSource string

const (
	// ModelIndexGallery reads the model gallery index. It is the default.
	ModelIndexGallery ModelIndexSource = "gallery"
	// ModelIndexLibrary reads the model library, the same models in the shared
	// library entry shape.
	ModelIndexLibrary ModelIndexSource = "library"
)

const (
	// OfficialModelGalleryName is the name of the default gallery. The library
	// takes the place of this gallery only; a gallery the owner added keeps
	// its own index.
	OfficialModelGalleryName = "localai"
	// OfficialModelLibraryURL is the model library, rendered from the model
	// gallery by scripts/models/cmd/modelcatalog and kept in this repository.
	OfficialModelLibraryURL = "https://raw.githubusercontent.com/mudler/LocalAI/master/gallery/models/index.yaml"
)

// ParseModelIndexSource reads the value of --model-index-source. Empty means
// the gallery.
func ParseModelIndexSource(s string) (ModelIndexSource, error) {
	switch ModelIndexSource(strings.ToLower(strings.TrimSpace(s))) {
	case "", ModelIndexGallery:
		return ModelIndexGallery, nil
	case ModelIndexLibrary:
		return ModelIndexLibrary, nil
	}
	return "", fmt.Errorf("unknown model index source %q: use %q or %q", s, ModelIndexGallery, ModelIndexLibrary)
}

type modelIndexSetting struct {
	source ModelIndexSource
	url    string
}

var modelIndex atomic.Pointer[modelIndexSetting]

// SetModelIndex selects the file the host reads the official gallery's models
// from. It is set once at start. An empty url means OfficialModelLibraryURL.
func SetModelIndex(source ModelIndexSource, url string) {
	if url == "" {
		url = OfficialModelLibraryURL
	}
	modelIndex.Store(&modelIndexSetting{source: source, url: url})
}

// modelLibraryFor returns the library to read instead of the index of the
// gallery, or false when the gallery is read as it is.
func modelLibraryFor(g config.Gallery) (config.Gallery, bool) {
	s := modelIndex.Load()
	if s == nil || s.source != ModelIndexLibrary || g.Name != OfficialModelGalleryName {
		return config.Gallery{}, false
	}
	// The library has no mirrors and no artifact policy of its own: the
	// gallery's would describe another file.
	return config.Gallery{Name: g.Name, URL: s.url}, true
}

// getModelLibraryElements reads the models of a gallery from the model
// library. A library that cannot be fetched or read is an error: falling back
// to the gallery would hide the setting from the owner who chose it.
func getModelLibraryElements(g, lib config.Gallery, basePath string, requireIntegrity bool, isInstalled func(*GalleryModel) bool) ([]*GalleryModel, error) {
	cacheKey := galleryIndexCacheKey(lib)
	var body []byte
	if galleryCache.Exists(cacheKey) {
		if entry := galleryCache.Get(cacheKey); !entry.hasExpired() {
			body = entry.yamlEntry
		} else {
			galleryCache.Delete(cacheKey)
		}
	}
	if body == nil {
		fetched, servedBy, err := fetchGalleryIndex(context.Background(), lib, basePath, requireIntegrity)
		if err != nil {
			return nil, fmt.Errorf("failed to read the model library: %w", err)
		}
		if servedBy != lib.URL {
			xlog.Info("model library served by a fallback source", "gallery", g.Name, "source", servedBy)
		}
		body = fetched
	}
	res, err := DecodeModelIndex(body)
	if err != nil {
		return nil, fmt.Errorf("failed to read the model library: %w", err)
	}
	if len(res.Skipped) > 0 {
		xlog.Info("model library entries of another kind were skipped", "gallery", g.Name, "count", len(res.Skipped))
	}
	models, err := ModelsFromLibrary(res.Entries)
	if err != nil {
		return nil, fmt.Errorf("failed to read the model library: %w", err)
	}
	// Only a library that decodes is kept, so a bad file does not stay cached.
	galleryCache.Set(cacheKey, galleryCacheEntry{yamlEntry: body, lastUpdated: time.Now()})
	for _, m := range models {
		m.SetGallery(g)
		m.SetInstalled(isInstalled(m))
	}
	return models, nil
}
