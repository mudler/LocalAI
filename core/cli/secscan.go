package cli

import (
	"encoding/json"

	cliContext "github.com/go-skynet/LocalAI/core/cli/context"
	"github.com/go-skynet/LocalAI/pkg/gallery"
	"github.com/rs/zerolog/log"
)

type SecScanCLI struct {
	ModelsPath string `env:"LOCALAI_MODELS_PATH,MODELS_PATH" type:"path" default:"${basepath}/models" help:"Path containing models used for inferencing" group:"storage"`
	Galleries  string `env:"LOCALAI_GALLERIES,GALLERIES" help:"JSON list of galleries" group:"models" default:"${galleries}"`
}

func (sscli *SecScanCLI) Run(ctx *cliContext.Context) error {
	log.Info().Msg("LocalAI Security Scanner - This is BEST EFFORT functionality! Currently limited to huggingface models!")
	var galleries []gallery.Gallery
	if err := json.Unmarshal([]byte(sscli.Galleries), &galleries); err != nil {
		log.Error().Err(err).Msg("unable to load galleries")
	}

	err := gallery.SafetyScanGalleryModels(galleries, sscli.ModelsPath)
	if err == nil {
		log.Info().Msg("No security warnings were detected for your installed models. Please note that this is a BEST EFFORT tool, and all issues may not be detected.")
	} else {
		log.Error().Err(err).Msg("! WARNING ! A known-vulnerable model is installed!")
	}
	return err
}
