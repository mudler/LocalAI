package cli

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/rs/zerolog/log"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/downloader"
	gguf "github.com/thxcode/gguf-parser-go"
)

type UtilCMD struct {
	GGUFInfo         GGUFInfoCMD         `cmd:"" name:"gguf-info" help:"Get information about a GGUF file"`
	HFScan           HFScanCMD           `cmd:"" name:"hf-scan" help:"Checks installed models for known security issues. WARNING: this is a best-effort feature and may not catch everything!"`
	UsecaseHeuristic UsecaseHeuristicCMD `cmd:"" name:"usecase-heuristic" help:"Checks a specific model config and prints what usecase LocalAI will offer for it."`
}

type GGUFInfoCMD struct {
	Args   []string `arg:"" optional:"" name:"args" help:"Arguments to pass to the utility command"`
	Header bool     `optional:"" default:"false" name:"header" help:"Show header information"`
}

type HFScanCMD struct {
	ModelsPath string   `env:"LOCALAI_MODELS_PATH,MODELS_PATH" type:"path" default:"${basepath}/models" help:"Path containing models used for inferencing" group:"storage"`
	Galleries  string   `env:"LOCALAI_GALLERIES,GALLERIES" help:"JSON list of galleries" group:"models" default:"${galleries}"`
	ToScan     []string `arg:""`
}

type UsecaseHeuristicCMD struct {
	ConfigName string `name:"The config file to check"`
	ModelsPath string `env:"LOCALAI_MODELS_PATH,MODELS_PATH" type:"path" default:"${basepath}/models" help:"Path containing models used for inferencing" group:"storage"`
}

func (u *GGUFInfoCMD) Run(ctx *cliContext.Context) error {
	if u.Args == nil || len(u.Args) == 0 {
		return fmt.Errorf("no GGUF file provided")
	}
	// We try to guess only if we don't have a template defined already
	f, err := gguf.ParseGGUFFile(u.Args[0])
	if err != nil {
		// Only valid for gguf files
		log.Error().Msgf("guessDefaultsFromFile: %s", "not a GGUF file")
		return err
	}

	log.Info().
		Any("eosTokenID", f.Tokenizer().EOSTokenID).
		Any("bosTokenID", f.Tokenizer().BOSTokenID).
		Any("modelName", f.Model().Name).
		Any("architecture", f.Architecture().Architecture).Msgf("GGUF file loaded: %s", u.Args[0])

	log.Info().Any("tokenizer", fmt.Sprintf("%+v", f.Tokenizer())).Msg("Tokenizer")
	log.Info().Any("architecture", fmt.Sprintf("%+v", f.Architecture())).Msg("Architecture")

	v, exists := f.Header.MetadataKV.Get("tokenizer.chat_template")
	if exists {
		log.Info().Msgf("chat_template: %s", v.ValueString())
	}

	if u.Header {
		for _, metadata := range f.Header.MetadataKV {
			log.Info().Msgf("%s: %+v", metadata.Key, metadata.Value)
		}
		//	log.Info().Any("header", fmt.Sprintf("%+v", f.Header)).Msg("Header")
	}

	return nil
}

func (hfscmd *HFScanCMD) Run(ctx *cliContext.Context) error {
	log.Info().Msg("LocalAI Security Scanner - This is BEST EFFORT functionality! Currently limited to huggingface models!")
	if len(hfscmd.ToScan) == 0 {
		log.Info().Msg("Checking all installed models against galleries")
		var galleries []config.Gallery
		if err := json.Unmarshal([]byte(hfscmd.Galleries), &galleries); err != nil {
			log.Error().Err(err).Msg("unable to load galleries")
		}

		err := gallery.SafetyScanGalleryModels(galleries, hfscmd.ModelsPath)
		if err == nil {
			log.Info().Msg("No security warnings were detected for your installed models. Please note that this is a BEST EFFORT tool, and all issues may not be detected.")
		} else {
			log.Error().Err(err).Msg("! WARNING ! A known-vulnerable model is installed!")
		}
		return err
	} else {
		var errs error = nil
		for _, uri := range hfscmd.ToScan {
			log.Info().Str("uri", uri).Msg("scanning specific uri")
			scanResults, err := downloader.HuggingFaceScan(downloader.URI(uri))
			if err != nil && errors.Is(err, downloader.ErrUnsafeFilesFound) {
				log.Error().Err(err).Strs("clamAV", scanResults.ClamAVInfectedFiles).Strs("pickles", scanResults.DangerousPickles).Msg("! WARNING ! A known-vulnerable model is included in this repo!")
				errs = errors.Join(errs, err)
			}
		}
		if errs != nil {
			return errs
		}
		log.Info().Msg("No security warnings were detected for your installed models. Please note that this is a BEST EFFORT tool, and all issues may not be detected.")
		return nil
	}
}

func (uhcmd *UsecaseHeuristicCMD) Run(ctx *cliContext.Context) error {
	if len(uhcmd.ConfigName) == 0 {
		log.Error().Msg("ConfigName is a required parameter")
		return fmt.Errorf("config name is a required parameter")
	}
	if len(uhcmd.ModelsPath) == 0 {
		log.Error().Msg("ModelsPath is a required parameter")
		return fmt.Errorf("model path is a required parameter")
	}
	bcl := config.NewBackendConfigLoader(uhcmd.ModelsPath)
	err := bcl.LoadBackendConfig(uhcmd.ConfigName)
	if err != nil {
		log.Error().Err(err).Str("ConfigName", uhcmd.ConfigName).Msg("error while loading backend")
		return err
	}
	bc, exists := bcl.GetBackendConfig(uhcmd.ConfigName)
	if !exists {
		log.Error().Str("ConfigName", uhcmd.ConfigName).Msg("ConfigName not found")
	}
	for name, uc := range config.GetAllBackendConfigUsecases() {
		if bc.HasUsecases(uc) {
			log.Info().Str("Usecase", name)
		}
	}
	log.Info().Msg("---")
	return nil
}
