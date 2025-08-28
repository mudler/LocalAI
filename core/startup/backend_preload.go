package startup

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/rs/zerolog/log"
)

func InstallExternalBackends(galleries []config.Gallery, systemState *system.SystemState, modelLoader *model.ModelLoader, downloadStatus func(string, string, string, float64), backend, name, alias string) error {
	uri := downloader.URI(backend)
	switch {
	case uri.LooksLikeDir():
		if name == "" { // infer it from the path
			name = filepath.Base(backend)
		}
		log.Info().Str("backend", backend).Str("name", name).Msg("Installing backend from path")
		if err := gallery.InstallBackend(systemState, modelLoader, &gallery.GalleryBackend{
			Metadata: gallery.Metadata{
				Name: name,
			},
			Alias: alias,
			URI:   backend,
		}, downloadStatus); err != nil {
			return fmt.Errorf("error installing backend %s: %w", backend, err)
		}
	case uri.LooksLikeOCI() && !uri.LooksLikeOCIFile():
		if name == "" {
			return fmt.Errorf("specifying a name is required for OCI images")
		}
		log.Info().Str("backend", backend).Str("name", name).Msg("Installing backend from OCI image")
		if err := gallery.InstallBackend(systemState, modelLoader, &gallery.GalleryBackend{
			Metadata: gallery.Metadata{
				Name: name,
			},
			Alias: alias,
			URI:   backend,
		}, downloadStatus); err != nil {
			return fmt.Errorf("error installing backend %s: %w", backend, err)
		}
	case uri.LooksLikeOCIFile():
		name, err := uri.FilenameFromUrl()
		if err != nil {
			return fmt.Errorf("failed to get filename from URL: %w", err)
		}
		// strip extension if any
		name = strings.TrimSuffix(name, filepath.Ext(name))

		log.Info().Str("backend", backend).Str("name", name).Msg("Installing backend from OCI image")
		if err := gallery.InstallBackend(systemState, modelLoader, &gallery.GalleryBackend{
			Metadata: gallery.Metadata{
				Name: name,
			},
			Alias: alias,
			URI:   backend,
		}, downloadStatus); err != nil {
			return fmt.Errorf("error installing backend %s: %w", backend, err)
		}
	default:
		if name != "" || alias != "" {
			return fmt.Errorf("specifying a name or alias is not supported for this backend")
		}
		err := gallery.InstallBackendFromGallery(galleries, systemState, modelLoader, backend, downloadStatus, true)
		if err != nil {
			return fmt.Errorf("error installing backend %s: %w", backend, err)
		}

	}

	return nil
}
