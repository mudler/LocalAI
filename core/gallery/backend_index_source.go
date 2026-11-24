// SPDX-License-Identifier: MIT
package gallery

import (
	"fmt"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/xlog"
)

const (
	// OfficialBackendLibraryURL is the backend library, the source of the
	// backends, kept in this repository. It is a default backend source. The
	// gallery index is generated from it by scripts/backends/cmd/backendcatalog.
	OfficialBackendLibraryURL = catalog.OfficialBackendLibraryURL
)

// backendLibraryFor returns the library to read for a gallery, or false when
// the gallery is read as a gallery index. A gallery is a library when its entry
// says so, which is how the backend library, shipped or added with
// `local-ai library add`, is handed to the reader.
func backendLibraryFor(g config.Gallery) (config.Gallery, bool) {
	if !g.Library {
		return config.Gallery{}, false
	}
	// The library has no mirrors of its own: the gallery's would describe
	// another file. Its images are installed the way the gallery installs them,
	// by the tag and the mirrors the entry names.
	return config.Gallery{Name: g.Name, URL: g.URL}, true
}

// getBackendLibraryElements reads the backends of a gallery from the backend
// library. A library that cannot be fetched or read is an error: falling back
// to the gallery would hide the setting from the owner who chose it.
func getBackendLibraryElements(g, lib config.Gallery, basePath string, requireIntegrity bool, isInstalled func(*GalleryBackend) bool) ([]*GalleryBackend, error) {
	body, err := libraryBody(g, lib, basePath, requireIntegrity, "backend")
	if err != nil {
		return nil, err
	}
	res, err := catalog.ReadIndex(body, catalog.KindBackend)
	if err != nil {
		return nil, fmt.Errorf("failed to read the backend library: %w", err)
	}
	if len(res.Skipped) > 0 {
		xlog.Info("backend library entries of another kind were skipped", "gallery", g.Name, "count", len(res.Skipped))
	}
	backends, err := BackendsFromLibrary(res.Releases)
	if err != nil {
		return nil, fmt.Errorf("failed to read the backend library: %w", err)
	}
	// Only a library that decodes is kept, so a bad file does not stay cached.
	keepLibraryBody(lib, body)
	for _, b := range backends {
		b.SetGallery(g)
		b.SetInstalled(isInstalled(b))
	}
	return backends, nil
}
