// SPDX-License-Identifier: MIT
package gallery

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/xlog"
)

const (
	// OfficialModelGalleryName is the name of the default model gallery.
	OfficialModelGalleryName = "localai"
	// OfficialModelLibraryURL is the model library, rendered from the model
	// gallery by scripts/models/cmd/modelcatalog and kept in this repository.
	// Add it as a model source with `local-ai library add`.
	OfficialModelLibraryURL = "https://raw.githubusercontent.com/mudler/LocalAI/master/gallery/models/index.yaml"
)

// modelLibraryFor returns the library to read for a gallery, or false when the
// gallery is read as a gallery index. A gallery is a library when its entry
// says so, which is how a model source added with `local-ai library add` is
// handed to the reader.
func modelLibraryFor(g config.Gallery) (config.Gallery, bool) {
	if !g.Library {
		return config.Gallery{}, false
	}
	// The library has no mirrors and no artifact policy of its own: the
	// gallery's would describe another file.
	return config.Gallery{Name: g.Name, URL: g.URL}, true
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
