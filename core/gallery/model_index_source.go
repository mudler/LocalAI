// SPDX-License-Identifier: MIT
package gallery

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/xlog"
)

const (
	// OfficialModelGalleryName is the name of the default model gallery.
	OfficialModelGalleryName = "localai"
	// OfficialModelLibraryURL is the model library, the source of the models,
	// kept in this repository. It is a default model source. The gallery
	// index is generated from it by scripts/models/cmd/modelcatalog.
	OfficialModelLibraryURL = catalog.OfficialModelLibraryURL
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
	body, err := libraryBody(g, lib, basePath, requireIntegrity, "model")
	if err != nil {
		return nil, err
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
	keepLibraryBody(lib, body)
	for _, m := range models {
		m.SetGallery(g)
		m.SetInstalled(isInstalled(m))
	}
	return models, nil
}

// libraryBody returns the text of a library: the cached copy while it is fresh,
// else a fetch that follows the mirrors, the cooldown and the last known good
// copy of any gallery. what names the kind in the error.
func libraryBody(g, lib config.Gallery, basePath string, requireIntegrity bool, what string) ([]byte, error) {
	cacheKey := galleryIndexCacheKey(lib)
	if galleryCache.Exists(cacheKey) {
		if entry := galleryCache.Get(cacheKey); !entry.hasExpired() {
			return entry.yamlEntry, nil
		}
		galleryCache.Delete(cacheKey)
	}
	fetched, servedBy, err := fetchGalleryIndex(context.Background(), lib, basePath, requireIntegrity)
	if err != nil {
		return nil, fmt.Errorf("failed to read the %s library: %w", what, err)
	}
	if servedBy != lib.URL {
		xlog.Info(what+" library served by a fallback source", "gallery", g.Name, "source", servedBy)
	}
	return fetched, nil
}

// keepLibraryBody caches the text of a library that has been read.
func keepLibraryBody(lib config.Gallery, body []byte) {
	galleryCache.Set(galleryIndexCacheKey(lib), galleryCacheEntry{yamlEntry: body, lastUpdated: time.Now()})
}
