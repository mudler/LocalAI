package services

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"

	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/xlog"
)

func (g *GalleryService) backendHandler(op *GalleryOp[gallery.GalleryBackend, any], systemState *system.SystemState) error {
	utils.ResetDownloadTimers()

	// Check if already cancelled
	if op.Context != nil {
		select {
		case <-op.Context.Done():
			g.UpdateStatus(op.ID, &GalleryOpStatus{
				Cancelled:          true,
				Processed:          true,
				Message:            "cancelled",
				GalleryElementName: op.GalleryElementName,
			})
			return op.Context.Err()
		default:
		}
	}

	g.UpdateStatus(op.ID, &GalleryOpStatus{Message: fmt.Sprintf("processing backend: %s", op.GalleryElementName), Progress: 0, Cancellable: true})

	// displayDownload displays the download progress
	progressCallback := func(fileName string, current string, total string, percentage float64) {
		// Check for cancellation during progress updates
		if op.Context != nil {
			select {
			case <-op.Context.Done():
				return
			default:
			}
		}
		g.UpdateStatus(op.ID, &GalleryOpStatus{Message: fmt.Sprintf(processingMessage, fileName, total, current), FileName: fileName, Progress: percentage, TotalFileSize: total, DownloadedFileSize: current, Cancellable: true})
		utils.DisplayDownloadFunction(fileName, current, total, percentage)
	}

	ctx := op.Context
	if ctx == nil {
		ctx = context.Background()
	}

	var err error
	if op.Delete {
		err = gallery.DeleteBackendFromSystem(g.appConfig.SystemState, op.GalleryElementName)
		g.modelLoader.DeleteExternalBackend(op.GalleryElementName)
	} else if op.ExternalURI != "" {
		// External backend installation (OCI image, URL, or path)
		xlog.Info("Installing external backend", "uri", op.ExternalURI, "name", op.ExternalName, "alias", op.ExternalAlias)
		err = InstallExternalBackend(ctx, g.appConfig.BackendGalleries, systemState, g.modelLoader, progressCallback, op.ExternalURI, op.ExternalName, op.ExternalAlias)
		// Update GalleryElementName for status tracking if a name was derived
		if op.ExternalName != "" {
			op.GalleryElementName = op.ExternalName
		}
	} else {
		// Standard gallery installation
		xlog.Warn("installing backend", "backend", op.GalleryElementName)
		xlog.Debug("backend galleries", "galleries", g.appConfig.BackendGalleries)
		err = gallery.InstallBackendFromGallery(ctx, g.appConfig.BackendGalleries, systemState, g.modelLoader, op.GalleryElementName, progressCallback, true)
	}
	if err != nil {
		// Check if error is due to cancellation
		if op.Context != nil && errors.Is(err, op.Context.Err()) {
			g.UpdateStatus(op.ID, &GalleryOpStatus{
				Cancelled:          true,
				Processed:          true,
				Message:            "cancelled",
				GalleryElementName: op.GalleryElementName,
			})
			return err
		}
		xlog.Error("error installing backend", "error", err, "backend", op.GalleryElementName)
		if !op.Delete {
			// If we didn't install the backend, we need to make sure we don't have a leftover directory
			gallery.DeleteBackendFromSystem(systemState, op.GalleryElementName)
		}
		return err
	}

	g.UpdateStatus(op.ID,
		&GalleryOpStatus{
			Deletion:           op.Delete,
			Processed:          true,
			GalleryElementName: op.GalleryElementName,
			Message:            "completed",
			Progress:           100,
			Cancellable:        false})
	return nil
}

// InstallExternalBackend installs a backend from an external source (OCI image, URL, or path).
// This method contains the logic to detect the input type and call the appropriate installation function.
// It can be used by both CLI and Web UI for installing backends from external sources.
func InstallExternalBackend(ctx context.Context, galleries []config.Gallery, systemState *system.SystemState, modelLoader *model.ModelLoader, downloadStatus func(string, string, string, float64), backend, name, alias string) error {
	uri := downloader.URI(backend)
	switch {
	case uri.LooksLikeDir():
		if name == "" { // infer it from the path
			name = filepath.Base(backend)
		}
		xlog.Info("Installing backend from path", "backend", backend, "name", name)
		if err := gallery.InstallBackend(ctx, systemState, modelLoader, &gallery.GalleryBackend{
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
		xlog.Info("Installing backend from OCI image", "backend", backend, "name", name)
		if err := gallery.InstallBackend(ctx, systemState, modelLoader, &gallery.GalleryBackend{
			Metadata: gallery.Metadata{
				Name: name,
			},
			Alias: alias,
			URI:   backend,
		}, downloadStatus); err != nil {
			return fmt.Errorf("error installing backend %s: %w", backend, err)
		}
	case uri.LooksLikeOCIFile():
		derivedName, err := uri.FilenameFromUrl()
		if err != nil {
			return fmt.Errorf("failed to get filename from URL: %w", err)
		}
		// strip extension if any
		derivedName = strings.TrimSuffix(derivedName, filepath.Ext(derivedName))
		// Use provided name if available, otherwise use derived name
		if name == "" {
			name = derivedName
		}

		xlog.Info("Installing backend from OCI image", "backend", backend, "name", name)
		if err := gallery.InstallBackend(ctx, systemState, modelLoader, &gallery.GalleryBackend{
			Metadata: gallery.Metadata{
				Name: name,
			},
			Alias: alias,
			URI:   backend,
		}, downloadStatus); err != nil {
			return fmt.Errorf("error installing backend %s: %w", backend, err)
		}
	default:
		// Treat as gallery backend name
		if name != "" || alias != "" {
			return fmt.Errorf("specifying a name or alias is not supported for gallery backends")
		}
		err := gallery.InstallBackendFromGallery(ctx, galleries, systemState, modelLoader, backend, downloadStatus, true)
		if err != nil {
			return fmt.Errorf("error installing backend %s: %w", backend, err)
		}
	}

	return nil
}
