package config

import (
	"os"
	"path/filepath"

	gguf "github.com/gpustack/gguf-parser-go"
	"github.com/rs/zerolog/log"
)

func guessDefaultsFromFile(cfg *ModelConfig, modelPath string, defaultCtx int) {
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

	defer func() {
		if r := recover(); r != nil {
			log.Error().Msgf("guessDefaultsFromFile: %s", "panic while parsing gguf file")
		}
	}()

	defer func() {
		if cfg.ContextSize == nil {
			if defaultCtx == 0 {
				defaultCtx = defaultContextSize
			}
			cfg.ContextSize = &defaultCtx
		}
	}()

	// try to parse the gguf file
	f, err := gguf.ParseGGUFFile(guessPath)
	if err == nil {
		guessGGUFFromFile(cfg, f, defaultCtx)
		return
	}
}
