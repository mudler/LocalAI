package config

import (
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/mudler/xlog"

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
		xlog.Error("guessDefaultsFromFile(TotalAvailableVRAM)", "error", err)
	} else if vram > 0 {
		estimate, err := xsysinfo.EstimateGGUFVRAMUsage(f, vram)
		if err != nil {
			xlog.Error("guessDefaultsFromFile(EstimateGGUFVRAMUsage)", "error", err)
		} else {
			if estimate.IsFullOffload {
				xlog.Warn("guessDefaultsFromFile: full offload is recommended")
			}

			if estimate.EstimatedVRAM > vram {
				xlog.Warn("guessDefaultsFromFile: estimated VRAM usage is greater than available VRAM")
			}

			if cfg.NGPULayers == nil && estimate.EstimatedLayers > 0 {
				xlog.Debug("guessDefaultsFromFile: layers estimated", "layers", estimate.EstimatedLayers)
				cfg.NGPULayers = &estimate.EstimatedLayers
			}
		}
	}

	if cfg.NGPULayers == nil {
		// we assume we want to offload all layers
		defaultHigh := defaultNGPULayers
		cfg.NGPULayers = &defaultHigh
	}

	xlog.Debug("guessDefaultsFromFile: NGPULayers set", "NGPULayers", cfg.NGPULayers)

	// template estimations
	if cfg.HasTemplate() {
		// nothing to guess here
		xlog.Debug("guessDefaultsFromFile: template already set", "name", cfg.Name)
		return
	}

	xlog.Debug("Model file loaded", "file", cfg.ModelFileName(), "eosTokenID", f.Tokenizer().EOSTokenID, "bosTokenID", f.Tokenizer().BOSTokenID, "modelName", f.Metadata().Name, "architecture", f.Architecture().Architecture)

	// guess the name
	if cfg.Name == "" {
		cfg.Name = f.Metadata().Name
	}

	// Instruct to use template from llama.cpp
	cfg.TemplateConfig.UseTokenizerTemplate = true
	cfg.FunctionsConfig.GrammarConfig.NoGrammar = true
	cfg.Options = append(cfg.Options, "use_jinja:true")
	cfg.KnownUsecaseStrings = append(cfg.KnownUsecaseStrings, "FLAG_CHAT")
}
