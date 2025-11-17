package startup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/gallery/importers"
	"github.com/mudler/LocalAI/core/services"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/rs/zerolog/log"
)

const (
	YAML_EXTENSION = ".yaml"
)

// InstallModels will preload models from the given list of URLs and galleries
// It will download the model if it is not already present in the model path
// It will also try to resolve if the model is an embedded model YAML configuration
func InstallModels(ctx context.Context, galleryService *services.GalleryService, galleries, backendGalleries []config.Gallery, systemState *system.SystemState, modelLoader *model.ModelLoader, enforceScan, autoloadBackendGalleries bool, downloadStatus func(string, string, string, float64), models ...string) error {
	// create an error that groups all errors
	var err error
	for _, url := range models {
		// Check if it's a model gallery, or print a warning
		e, found := installModel(ctx, galleries, backendGalleries, url, systemState, modelLoader, downloadStatus, enforceScan, autoloadBackendGalleries)
		if e != nil && found {
			log.Error().Err(err).Msgf("[startup] failed installing model '%s'", url)
			err = errors.Join(err, e)
		} else if !found {
			log.Debug().Msgf("[startup] model not found in the gallery '%s'", url)

			if galleryService == nil {
				return fmt.Errorf("cannot start autoimporter, not sure how to handle this uri")
			}

			// TODO: we should just use the discoverModelConfig here and default to this.
			modelConfig, discoverErr := importers.DiscoverModelConfig(url, json.RawMessage{})
			if discoverErr != nil {
				log.Error().Err(discoverErr).Msgf("[startup] failed to discover model config '%s'", url)
				err = errors.Join(discoverErr, fmt.Errorf("failed to discover model config: %w", err))
				continue
			}

			uuid, uuidErr := uuid.NewUUID()
			if uuidErr != nil {
				err = errors.Join(uuidErr, fmt.Errorf("failed to generate UUID: %w", uuidErr))
				continue
			}

			galleryService.ModelGalleryChannel <- services.GalleryOp[gallery.GalleryModel, gallery.ModelConfig]{
				Req: gallery.GalleryModel{
					Overrides: map[string]interface{}{},
				},
				ID:                 uuid.String(),
				GalleryElementName: modelConfig.Name,
				GalleryElement:     &modelConfig,
				BackendGalleries:   backendGalleries,
			}

			var status *services.GalleryOpStatus
			// wait for op to finish
			for {
				status = galleryService.GetStatus(uuid.String())
				if status != nil && status.Processed {
					break
				}
				time.Sleep(1 * time.Second)
			}

			if status.Error != nil {
				log.Error().Err(status.Error).Msgf("[startup] failed to import model '%s' from '%s'", modelConfig.Name, url)
				return status.Error
			}

			log.Info().Msgf("[startup] imported model '%s' from '%s'", modelConfig.Name, url)
		}
	}
	return err
}

func installModel(ctx context.Context, galleries, backendGalleries []config.Gallery, modelName string, systemState *system.SystemState, modelLoader *model.ModelLoader, downloadStatus func(string, string, string, float64), enforceScan, autoloadBackendGalleries bool) (error, bool) {
	models, err := gallery.AvailableGalleryModels(galleries, systemState)
	if err != nil {
		return err, false
	}

	model := gallery.FindGalleryElement(models, modelName)
	if model == nil {
		return err, false
	}

	if downloadStatus == nil {
		downloadStatus = utils.DisplayDownloadFunction
	}

	log.Info().Str("model", modelName).Str("license", model.License).Msg("installing model")
	err = gallery.InstallModelFromGallery(ctx, galleries, backendGalleries, systemState, modelLoader, modelName, gallery.GalleryModel{}, downloadStatus, enforceScan, autoloadBackendGalleries)
	if err != nil {
		return err, true
	}

	return nil, true
}
