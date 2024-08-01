package cli

import (
	"encoding/json"
	"errors"
	"fmt"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/LocalAI/pkg/startup"
	"github.com/rs/zerolog/log"
	"github.com/schollz/progressbar/v3"
)

type ModelsCMDFlags struct {
	Galleries  string `env:"LOCALAI_GALLERIES,GALLERIES" help:"JSON list of galleries" group:"models" default:"${galleries}"`
	ModelsPath string `env:"LOCALAI_MODELS_PATH,MODELS_PATH" type:"path" default:"${basepath}/models" help:"Path containing models used for inferencing" group:"storage"`
}

type ModelsList struct {
	ModelsCMDFlags `embed:""`
}

type ModelsInstall struct {
	DisablePredownloadScan bool     `env:"LOCALAI_DISABLE_PREDOWNLOAD_SCAN" help:"If true, disables the best-effort security scanner before downloading any files." group:"hardening" default:"false"`
	ModelArgs              []string `arg:"" optional:"" name:"models" help:"Model configuration URLs to load"`

	ModelsCMDFlags `embed:""`
}

type ModelsCMD struct {
	List    ModelsList    `cmd:"" help:"List the models available in your galleries" default:"withargs"`
	Install ModelsInstall `cmd:"" help:"Install a model from the gallery"`
}

func (ml *ModelsList) Run(ctx *cliContext.Context) error {
	var galleries []config.Gallery
	if err := json.Unmarshal([]byte(ml.Galleries), &galleries); err != nil {
		log.Error().Err(err).Msg("unable to load galleries")
	}

	models, err := gallery.AvailableGalleryModels(galleries, ml.ModelsPath)
	if err != nil {
		return err
	}
	for _, model := range models {
		if model.Installed {
			fmt.Printf(" * %s@%s (installed)\n", model.Gallery.Name, model.Name)
		} else {
			fmt.Printf(" - %s@%s\n", model.Gallery.Name, model.Name)
		}
	}
	return nil
}

func (mi *ModelsInstall) Run(ctx *cliContext.Context) error {
	var galleries []config.Gallery
	if err := json.Unmarshal([]byte(mi.Galleries), &galleries); err != nil {
		log.Error().Err(err).Msg("unable to load galleries")
	}

	for _, modelName := range mi.ModelArgs {

		progressBar := progressbar.NewOptions(
			1000,
			progressbar.OptionSetDescription(fmt.Sprintf("downloading model %s", modelName)),
			progressbar.OptionShowBytes(false),
			progressbar.OptionClearOnFinish(),
		)
		progressCallback := func(fileName string, current string, total string, percentage float64) {
			v := int(percentage * 10)
			err := progressBar.Set(v)
			if err != nil {
				log.Error().Err(err).Str("filename", fileName).Int("value", v).Msg("error while updating progress bar")
			}
		}
		//startup.InstallModels()
		models, err := gallery.AvailableGalleryModels(galleries, mi.ModelsPath)
		if err != nil {
			return err
		}

		modelURI := downloader.URI(modelName)

		if !modelURI.LooksLikeOCI() {
			model := gallery.FindModel(models, modelName, mi.ModelsPath)
			if model == nil {
				log.Error().Str("model", modelName).Msg("model not found")
				return err
			}

			err = gallery.SafetyScanGalleryModel(model)
			if err != nil && !errors.Is(err, downloader.ErrNonHuggingFaceFile) {
				return err
			}

			log.Info().Str("model", modelName).Str("license", model.License).Msg("installing model")
		}

		err = startup.InstallModels(galleries, "", mi.ModelsPath, !mi.DisablePredownloadScan, progressCallback, modelName)
		if err != nil {
			return err
		}
	}
	return nil
}
