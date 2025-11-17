package startup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
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
	"gopkg.in/yaml.v2"
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

	installBackend := func(modelPath string) error {
		// Then load the model file, and read the backend
		modelYAML, e := os.ReadFile(modelPath)
		if e != nil {
			log.Error().Err(e).Str("filepath", modelPath).Msg("error reading model definition")
			return e
		}

		var model config.ModelConfig
		if e := yaml.Unmarshal(modelYAML, &model); e != nil {
			log.Error().Err(e).Str("filepath", modelPath).Msg("error unmarshalling model definition")
			return e
		}

		if model.Backend == "" {
			log.Debug().Str("filepath", modelPath).Msg("no backend found in model definition")
			return nil
		}

		if err := gallery.InstallBackendFromGallery(ctx, backendGalleries, systemState, modelLoader, model.Backend, downloadStatus, false); err != nil {
			log.Error().Err(err).Str("backend", model.Backend).Msg("error installing backend")
			return err
		}

		return nil
	}

	for _, url := range models {
		// As a best effort, try to resolve the model from the remote library
		// if it's not resolved we try with the other method below
		if _, e := os.Stat(url); e == nil {
			log.Debug().Msgf("[startup] resolved local model: %s", url)
			// copy to modelPath
			md5Name := utils.MD5(url)

			modelYAML, e := os.ReadFile(url)
			if e != nil {
				log.Error().Err(e).Str("filepath", url).Msg("error reading model definition")
				err = errors.Join(err, e)
				continue
			}

			modelDefinitionFilePath := filepath.Join(systemState.Model.ModelsPath, md5Name) + YAML_EXTENSION
			if e := os.WriteFile(modelDefinitionFilePath, modelYAML, 0600); e != nil {
				log.Error().Err(err).Str("filepath", modelDefinitionFilePath).Msg("error loading model: %s")
				err = errors.Join(err, e)
			}

			// Check if we have the backend installed
			if autoloadBackendGalleries && path.Ext(modelDefinitionFilePath) == YAML_EXTENSION {
				if err := installBackend(modelDefinitionFilePath); err != nil {
					log.Error().Err(err).Str("filepath", modelDefinitionFilePath).Msg("error installing backend")
				}
			}
		} else {
			// Check if it's a model gallery, or print a warning
			e, found := installModel(ctx, galleries, backendGalleries, url, systemState, modelLoader, downloadStatus, enforceScan, autoloadBackendGalleries)
			if e != nil && found {
				log.Error().Err(err).Msgf("[startup] failed installing model '%s'", url)
				err = errors.Join(err, e)
			} else if !found {
				log.Warn().Msgf("[startup] failed resolving model '%s'", url)

				if galleryService == nil {
					err = errors.Join(err, fmt.Errorf("cannot start autoimporter, not sure how to handle this uri"))
					continue
				}

				// TODO: we should just use the discoverModelConfig here and default to this.
				modelConfig, discoverErr := importers.DiscoverModelConfig(url, json.RawMessage{})
				if discoverErr != nil {
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
					return status.Error
				}

				log.Info().Msgf("[startup] imported model '%s' from '%s'", modelConfig.Name, url)
			}
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
