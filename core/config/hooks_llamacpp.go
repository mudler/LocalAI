package config

import (
	"os"
	"path/filepath"

	gguf "github.com/gpustack/gguf-parser-go"
	"github.com/mudler/xlog"
)

func init() {
	// Register for both explicit llama-cpp and empty backend (auto-detect from GGUF file)
	RegisterBackendHook("llama-cpp", llamaCppDefaults)
	RegisterBackendHook("", llamaCppDefaults)
}

func llamaCppDefaults(cfg *ModelConfig, modelPath string) {
	if os.Getenv("LOCALAI_DISABLE_GUESSING") == "true" {
		xlog.Debug("llamaCppDefaults: guessing disabled")
		return
	}
	if modelPath == "" {
		return
	}

	guessPath := filepath.Join(modelPath, cfg.ModelFileName())

	defer func() {
		if r := recover(); r != nil {
			xlog.Error("llamaCppDefaults: panic while parsing gguf file")
		}
	}()

	// Default context size if not set, regardless of whether GGUF parsing succeeds
	defer func() {
		if cfg.ContextSize == nil {
			ctx := defaultContextSize
			cfg.ContextSize = &ctx
		}
	}()

	f, err := gguf.ParseGGUFFile(guessPath)
	if err == nil {
		guessGGUFFromFile(cfg, f, 0)
	}
}
