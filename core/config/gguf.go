package config

import (
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/rs/zerolog/log"

	gguf "github.com/gpustack/gguf-parser-go"
)

const (
	defaultContextSize = 1024
	defaultNGPULayers  = 99999999
)

func guessGGUFFromFile(cfg *ModelConfig, f *gguf.GGUFFile, defaultCtx int) {

	if defaultCtx == 0 && cfg.ContextSize == nil {
		ctxSize := f.EstimateLLaMACppRun().ContextSize
		if ctxSize > 0 {
			cSize := int(ctxSize)
			cfg.ContextSize = &cSize
		} else {
			defaultCtx = defaultContextSize
			cfg.ContextSize = &defaultCtx
		}
	}

	// GPU options
	if cfg.Options == nil {
		if xsysinfo.HasGPU("nvidia") || xsysinfo.HasGPU("amd") {
			cfg.Options = []string{"gpu"}
		}
	}

	// vram estimation
	vram, err := xsysinfo.TotalAvailableVRAM()
	if err != nil {
		log.Error().Msgf("guessDefaultsFromFile(TotalAvailableVRAM): %s", err)
	} else if vram > 0 {
		estimate, err := xsysinfo.EstimateGGUFVRAMUsage(f, vram)
		if err != nil {
			log.Error().Msgf("guessDefaultsFromFile(EstimateGGUFVRAMUsage): %s", err)
		} else {
			if estimate.IsFullOffload {
				log.Warn().Msgf("guessDefaultsFromFile: %s", "full offload is recommended")
			}

			if estimate.EstimatedVRAM > vram {
				log.Warn().Msgf("guessDefaultsFromFile: %s", "estimated VRAM usage is greater than available VRAM")
			}

			if cfg.NGPULayers == nil && estimate.EstimatedLayers > 0 {
				log.Debug().Msgf("guessDefaultsFromFile: %d layers estimated", estimate.EstimatedLayers)
				cfg.NGPULayers = &estimate.EstimatedLayers
			}
		}
	}

	if cfg.NGPULayers == nil {
		// we assume we want to offload all layers
		defaultHigh := defaultNGPULayers
		cfg.NGPULayers = &defaultHigh
	}

	log.Debug().Any("NGPULayers", cfg.NGPULayers).Msgf("guessDefaultsFromFile: %s", "NGPULayers set")

	// template estimations
	if cfg.HasTemplate() {
		// nothing to guess here
		log.Debug().Any("name", cfg.Name).Msgf("guessDefaultsFromFile: %s", "template already set")
		return
	}

	log.Debug().
		Any("eosTokenID", f.Tokenizer().EOSTokenID).
		Any("bosTokenID", f.Tokenizer().BOSTokenID).
		Any("modelName", f.Metadata().Name).
		Any("architecture", f.Architecture().Architecture).Msgf("Model file loaded: %s", cfg.ModelFileName())

	// guess the name
	if cfg.Name == "" {
		cfg.Name = f.Metadata().Name
	}

	// Instruct to use template from llama.cpp
	cfg.TemplateConfig.UseTokenizerTemplate = true
	//cfg.FunctionsConfig.GrammarConfig.NoGrammar = true
}
