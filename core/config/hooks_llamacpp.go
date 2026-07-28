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

	// Default context size if not set, or if a context_size=-1 auto-max was
	// requested but the GGUF could not be parsed, so guessGGUFFromFile never ran
	// to resolve it. A negative value must never reach the backend.
	defer func() {
		if cfg.ContextSize == nil || *cfg.ContextSize < 0 {
			ctx := DefaultContextSize
			cfg.ContextSize = &ctx
		}
	}()

	// Startup parses every model's GGUF header to guess defaults. We only need
	// scalar metadata (architecture, head/ff counts, chat_template, token IDs,
	// MTP head) plus array *lengths* — never the array *contents*. Two options
	// keep this cheap, which matters when many models live on slow storage such
	// as a Docker volume (see https://github.com/mudler/LocalAI/issues/9790):
	//
	//   - SkipLargeMetadata: seek past large array-valued metadata (the tokenizer
	//     vocab: tokenizer.ggml.tokens/scores/merges, often >100k entries) instead
	//     of reading and allocating every element. Lengths stay populated.
	//   - UseMMap: read the header via a memory map so faulting in a few pages
	//     replaces hundreds of thousands of tiny read() syscalls (measured ~524k
	//     -> 8 for a 256k-token vocab), the dominant cost on slow filesystems.
	//
	// The mapping is released when ParseGGUFFile returns.
	f, err := gguf.ParseGGUFFile(guessPath, gguf.UseMMap(), gguf.SkipLargeMetadata())
	if err == nil {
		guessGGUFFromFile(cfg, f, 0)
	}
}
