package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"

	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/rs/zerolog/log"
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
	} else {
		log.Warn().Msgf("installing backend %s", op.GalleryElementName)
		log.Debug().Msgf("backend galleries: %v", g.appConfig.BackendGalleries)
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
		log.Error().Err(err).Msgf("error installing backend %s", op.GalleryElementName)
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
