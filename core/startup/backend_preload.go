package startup

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/rs/zerolog/log"
)

func InstallExternalBackends(galleries []config.Gallery, backendPath string, downloadStatus func(string, string, string, float64), backends ...string) error {
	var errs error
	systemState, err := system.GetSystemState()
	if err != nil {
		return fmt.Errorf("failed to get system state: %w", err)
	}
	for _, backend := range backends {
		uri := downloader.URI(backend)
		switch {
		case uri.LooksLikeDir():
			name := filepath.Base(backend)
			log.Info().Str("backend", backend).Str("name", name).Msg("Installing backend from path")
			if err := gallery.InstallBackend(backendPath, &gallery.GalleryBackend{
				Metadata: gallery.Metadata{
					Name: name,
				},
				URI: backend,
			}, downloadStatus); err != nil {
				errs = errors.Join(err, fmt.Errorf("error installing backend %s", backend))
			}
		case uri.LooksLikeOCI():
			name, err := uri.FilenameFromUrl()
			if err != nil {
				return fmt.Errorf("failed to get filename from URL: %w", err)
			}
			// strip extension if any
			name = strings.TrimSuffix(name, filepath.Ext(name))

			log.Info().Str("backend", backend).Str("name", name).Msg("Installing backend from OCI image")
			if err := gallery.InstallBackend(backendPath, &gallery.GalleryBackend{
				Metadata: gallery.Metadata{
					Name: name,
				},
				URI: backend,
			}, downloadStatus); err != nil {
				errs = errors.Join(err, fmt.Errorf("error installing backend %s", backend))
			}
		default:
			err := gallery.InstallBackendFromGallery(galleries, systemState, backend, backendPath, downloadStatus, true)
			if err != nil {
				errs = errors.Join(err, fmt.Errorf("error installing backend %s", backend))
			}
		}
	}
	return errs
}
