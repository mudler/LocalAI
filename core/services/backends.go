package services

import (
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"

	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/rs/zerolog/log"
)

func (g *GalleryService) backendHandler(op *GalleryOp[gallery.GalleryBackend], systemState *system.SystemState) error {
	utils.ResetDownloadTimers()
	g.UpdateStatus(op.ID, &GalleryOpStatus{Message: "processing", Progress: 0})

	// displayDownload displays the download progress
	progressCallback := func(fileName string, current string, total string, percentage float64) {
		g.UpdateStatus(op.ID, &GalleryOpStatus{Message: "processing", FileName: fileName, Progress: percentage, TotalFileSize: total, DownloadedFileSize: current})
		utils.DisplayDownloadFunction(fileName, current, total, percentage)
	}

	var err error
	if op.Delete {
		err = gallery.DeleteBackendFromSystem(g.appConfig.SystemState, op.GalleryElementName)
		g.modelLoader.DeleteExternalBackend(op.GalleryElementName)
	} else {
		log.Warn().Msgf("installing backend %s", op.GalleryElementName)
		log.Debug().Msgf("backend galleries: %v", g.appConfig.BackendGalleries)
		err = gallery.InstallBackendFromGallery(g.appConfig.BackendGalleries, systemState, g.modelLoader, op.GalleryElementName, progressCallback, true)
	}
	if err != nil {
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
			Progress:           100})
	return nil
}
