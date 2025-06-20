package startup

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/system"
)

func InstallExternalBackends(galleries []config.Gallery, backendPath string, downloadStatus func(string, string, string, float64), backends ...string) error {
	var errs error
	systemState, err := system.GetSystemState()
	if err != nil {
		return fmt.Errorf("failed to get system state: %w", err)
	}
	for _, backend := range backends {
		switch {
		case strings.HasPrefix(backend, "oci://"):
			backend = strings.TrimPrefix(backend, "oci://")

			if err := gallery.InstallBackend(backendPath, &gallery.GalleryBackend{
				URI: backend,
			}, downloadStatus); err != nil {
				errs = errors.Join(err, fmt.Errorf("error installing backend %s", backend))
			}
		default:
			err := gallery.InstallBackendFromGallery(galleries, systemState, backend, backendPath, downloadStatus)
			if err != nil {
				errs = errors.Join(err, fmt.Errorf("error installing backend %s", backend))
			}
		}
	}
	return errs
}
