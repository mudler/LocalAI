package config

import (
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/rs/zerolog/log"
	gguf "github.com/thxcode/gguf-parser-go"
)

func guessDefaultsFromFile(cfg *BackendConfig, modelPath string, defaultCtx int) {
	if os.Getenv("LOCALAI_DISABLE_GUESSING") == "true" {
		log.Debug().Msgf("guessDefaultsFromFile: %s", "guessing disabled with LOCALAI_DISABLE_GUESSING")
		return
	}

	if modelPath == "" {
		log.Debug().Msgf("guessDefaultsFromFile: %s", "modelPath is empty")
		return
	}

	// We try to guess only if we don't have a template defined already
	guessPath := filepath.Join(modelPath, cfg.ModelFileName())

	// try to parse the gguf file
	f, err := gguf.ParseGGUFFile(guessPath)
	if err == nil {
		guessGGUFFromFile(cfg, f, defaultCtx)
		return
	}

	if cfg.ContextSize == nil {
		if defaultCtx == 0 {
			defaultCtx = defaultContextSize
		}
		cfg.ContextSize = &defaultCtx
	}

	if cfg.Options == nil {
		if xsysinfo.HasGPU("nvidia") || xsysinfo.HasGPU("amd") {
			cfg.Options = []string{"gpu"}
		}
	}
}
