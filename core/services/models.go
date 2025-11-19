package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v2"
)

const (
	processingMessage = "processing file: %s. Total: %s. Current: %s"
)

func (g *GalleryService) modelHandler(op *GalleryOp[gallery.GalleryModel, gallery.ModelConfig], cl *config.ModelConfigLoader, systemState *system.SystemState) error {
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

	g.UpdateStatus(op.ID, &GalleryOpStatus{Message: fmt.Sprintf("processing model: %s", op.GalleryElementName), Progress: 0, Cancellable: true})

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

	err := processModelOperation(op, systemState, g.modelLoader, g.appConfig.EnforcePredownloadScans, g.appConfig.AutoloadBackendGalleries, progressCallback)
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
		return err
	}

	// Check for cancellation before final steps
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

	// Reload models
	err = cl.LoadModelConfigsFromPath(systemState.Model.ModelsPath, g.appConfig.ToConfigLoaderOptions()...)
	if err != nil {
		return err
	}

	err = cl.Preload(systemState.Model.ModelsPath)
	if err != nil {
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

func installModelFromRemoteConfig(ctx context.Context, systemState *system.SystemState, modelLoader *model.ModelLoader, req gallery.GalleryModel, downloadStatus func(string, string, string, float64), enforceScan, automaticallyInstallBackend bool, backendGalleries []config.Gallery) error {
	config, err := gallery.GetGalleryConfigFromURLWithContext[gallery.ModelConfig](ctx, req.URL, systemState.Model.ModelsPath)
	if err != nil {
		return err
	}

	config.Files = append(config.Files, req.AdditionalFiles...)

	installedModel, err := gallery.InstallModel(ctx, systemState, req.Name, &config, req.Overrides, downloadStatus, enforceScan)
	if err != nil {
		return err
	}

	if automaticallyInstallBackend && installedModel.Backend != "" {
		if err := gallery.InstallBackendFromGallery(ctx, backendGalleries, systemState, modelLoader, installedModel.Backend, downloadStatus, false); err != nil {
			return err
		}
	}

	return nil
}

type galleryModel struct {
	gallery.GalleryModel `yaml:",inline"` // https://github.com/go-yaml/yaml/issues/63
	ID                   string           `json:"id"`
}

func processRequests(systemState *system.SystemState, modelLoader *model.ModelLoader, enforceScan, automaticallyInstallBackend bool, galleries []config.Gallery, backendGalleries []config.Gallery, requests []galleryModel) error {
	ctx := context.Background()
	var err error
	for _, r := range requests {
		utils.ResetDownloadTimers()
		if r.ID == "" {
			err = installModelFromRemoteConfig(ctx, systemState, modelLoader, r.GalleryModel, utils.DisplayDownloadFunction, enforceScan, automaticallyInstallBackend, backendGalleries)

		} else {
			err = gallery.InstallModelFromGallery(
				ctx, galleries, backendGalleries, systemState, modelLoader, r.ID, r.GalleryModel, utils.DisplayDownloadFunction, enforceScan, automaticallyInstallBackend)
		}
	}
	return err
}

func ApplyGalleryFromFile(systemState *system.SystemState, modelLoader *model.ModelLoader, enforceScan, automaticallyInstallBackend bool, galleries []config.Gallery, backendGalleries []config.Gallery, s string) error {
	dat, err := os.ReadFile(s)
	if err != nil {
		return err
	}
	var requests []galleryModel

	if err := yaml.Unmarshal(dat, &requests); err != nil {
		return err
	}

	return processRequests(systemState, modelLoader, enforceScan, automaticallyInstallBackend, galleries, backendGalleries, requests)
}

func ApplyGalleryFromString(systemState *system.SystemState, modelLoader *model.ModelLoader, enforceScan, automaticallyInstallBackend bool, galleries []config.Gallery, backendGalleries []config.Gallery, s string) error {
	var requests []galleryModel
	err := json.Unmarshal([]byte(s), &requests)
	if err != nil {
		return err
	}

	return processRequests(systemState, modelLoader, enforceScan, automaticallyInstallBackend, galleries, backendGalleries, requests)
}

// processModelOperation handles the installation or deletion of a model
func processModelOperation(
	op *GalleryOp[gallery.GalleryModel, gallery.ModelConfig],
	systemState *system.SystemState,
	modelLoader *model.ModelLoader,
	enforcePredownloadScans bool,
	automaticallyInstallBackend bool,
	progressCallback func(string, string, string, float64),
) error {
	ctx := op.Context
	if ctx == nil {
		ctx = context.Background()
	}

	// Check for cancellation before starting
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	switch {
	case op.Delete:
		return gallery.DeleteModelFromSystem(systemState, op.GalleryElementName)
	case op.GalleryElement != nil:
		installedModel, err := gallery.InstallModel(
			ctx, systemState, op.GalleryElement.Name,
			op.GalleryElement,
			op.Req.Overrides,
			progressCallback, enforcePredownloadScans)
		if err != nil {
			return err
		}
		if automaticallyInstallBackend && installedModel.Backend != "" {
			log.Debug().Msgf("Installing backend %q", installedModel.Backend)
			if err := gallery.InstallBackendFromGallery(ctx, op.BackendGalleries, systemState, modelLoader, installedModel.Backend, progressCallback, false); err != nil {
				return err
			}
		}
		return nil
	case op.GalleryElementName != "":
		return gallery.InstallModelFromGallery(ctx, op.Galleries, op.BackendGalleries, systemState, modelLoader, op.GalleryElementName, op.Req, progressCallback, enforcePredownloadScans, automaticallyInstallBackend)
	default:
		return installModelFromRemoteConfig(ctx, systemState, modelLoader, op.Req, progressCallback, enforcePredownloadScans, automaticallyInstallBackend, op.BackendGalleries)
	}
}
