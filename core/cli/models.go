package cli

import (
	"encoding/json"
	"fmt"

	cliContext "github.com/go-skynet/LocalAI/core/cli/context"

	"github.com/go-skynet/LocalAI/pkg/gallery"
	"github.com/rs/zerolog/log"
	"github.com/schollz/progressbar/v3"
)

type ModelsCMDFlags struct {
	Galleries  string `env:"LOCALAI_GALLERIES,GALLERIES" help:"JSON list of galleries" group:"models"`
	ModelsPath string `env:"LOCALAI_MODELS_PATH,MODELS_PATH" type:"path" default:"${basepath}/models" help:"Path containing models used for inferencing" group:"storage"`
}

type ModelsList struct {
	ModelsCMDFlags `embed:""`
}

type ModelsInstall struct {
	ModelArgs []string `arg:"" optional:"" name:"models" help:"Model configuration URLs to load"`

	ModelsCMDFlags `embed:""`
}

type ModelsCMD struct {
	List    ModelsList    `cmd:"" help:"List the models available in your galleries" default:"withargs"`
	Install ModelsInstall `cmd:"" help:"Install a model from the gallery"`
}

func (ml *ModelsList) Run(ctx *cliContext.Context) error {
	var galleries []gallery.Gallery
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
	modelName := mi.ModelArgs[0]

	var galleries []gallery.Gallery
	if err := json.Unmarshal([]byte(mi.Galleries), &galleries); err != nil {
		log.Error().Err(err).Msg("unable to load galleries")
	}

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
	err := gallery.InstallModelFromGallery(galleries, modelName, mi.ModelsPath, gallery.GalleryModel{}, progressCallback)
	if err != nil {
		return err
	}
	return nil
}
