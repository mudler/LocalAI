package importers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/xlog"
)

// ImportLocalPath scans a local directory for exported model files and produces
// a config.ModelConfig with the correct backend, model path, and options.
// Paths in the returned config are relative to modelsPath when possible so that
// the YAML config remains portable.
//
// Detection order:
//  1. GGUF files (*.gguf) — uses llama-cpp backend
//  2. whisper.cpp ggml-*.bin — uses whisper backend
//  3. silero_vad*.onnx — uses silero-vad backend
//  4. piper .onnx + .onnx.json pair — uses piper backend
//  5. LoRA adapter (adapter_config.json) — uses transformers backend with lora_adapter
//  6. Merged model (*.safetensors or pytorch_model*.bin + config.json) — uses transformers backend
func ImportLocalPath(dirPath, name string) (*config.ModelConfig, error) {
	// Make paths relative to the models directory (parent of dirPath)
	// so config YAML stays portable.
	modelsDir := filepath.Dir(dirPath)
	relPath := func(absPath string) string {
		if rel, err := filepath.Rel(modelsDir, absPath); err == nil {
			return rel
		}
		return absPath
	}

	// 1. GGUF: check dirPath and dirPath_gguf/ (Unsloth convention)
	ggufFile := findGGUF(dirPath)
	if ggufFile == "" {
		ggufSubdir := dirPath + "_gguf"
		ggufFile = findGGUF(ggufSubdir)
	}
	if ggufFile != "" {
		xlog.Info("ImportLocalPath: detected GGUF model", "path", ggufFile)
		cfg := &config.ModelConfig{
			Name:                name,
			Backend:             "llama-cpp",
			KnownUsecaseStrings: []string{"chat"},
			Options:             []string{"use_jinja:true"},
		}
		cfg.Model = relPath(ggufFile)
		cfg.TemplateConfig.UseTokenizerTemplate = true
		cfg.Description = buildDescription(dirPath, "GGUF")
		return cfg, nil
	}

	// 2. whisper.cpp ggml-*.bin models
	if ggmlFile := findFileByPrefixSuffix(dirPath, "ggml-", ".bin"); ggmlFile != "" {
		xlog.Info("ImportLocalPath: detected whisper.cpp GGML model", "path", ggmlFile)
		cfg := &config.ModelConfig{
			Name:                name,
			Backend:             "whisper",
			KnownUsecaseStrings: []string{"transcript"},
		}
		cfg.Model = relPath(ggmlFile)
		cfg.Description = buildDescription(dirPath, "Whisper GGML")
		return cfg, nil
	}

	// 3/4. Single .onnx file in dir — silero-vad or piper depending on signals.
	if onnxFile := findSingleONNX(dirPath); onnxFile != "" {
		base := filepath.Base(onnxFile)
		lowerBase := strings.ToLower(base)
		switch {
		case strings.HasPrefix(lowerBase, "silero"):
			xlog.Info("ImportLocalPath: detected Silero VAD model", "path", onnxFile)
			cfg := &config.ModelConfig{
				Name:    name,
				Backend: "silero-vad",
			}
			cfg.Model = relPath(onnxFile)
			cfg.Description = buildDescription(dirPath, "Silero VAD")
			return cfg, nil
		case fileExists(onnxFile + ".json"):
			xlog.Info("ImportLocalPath: detected Piper voice", "path", onnxFile)
			cfg := &config.ModelConfig{
				Name:    name,
				Backend: "piper",
			}
			cfg.Model = relPath(onnxFile)
			cfg.Description = buildDescription(dirPath, "Piper voice")
			return cfg, nil
		}
		// Lone .onnx without piper config and without silero prefix: fall
		// through — no reliable backend to assign.
	}

	// 5. LoRA adapter: look for adapter_config.json

	adapterConfigPath := filepath.Join(dirPath, "adapter_config.json")
	if fileExists(adapterConfigPath) {
		xlog.Info("ImportLocalPath: detected LoRA adapter", "path", dirPath)
		baseModel := readBaseModel(dirPath)
		cfg := &config.ModelConfig{
			Name:                name,
			Backend:             "transformers",
			KnownUsecaseStrings: []string{"chat"},
		}
		cfg.Model = baseModel
		cfg.TemplateConfig.UseTokenizerTemplate = true
		cfg.LLMConfig.LoraAdapter = relPath(dirPath)
		cfg.Description = buildDescription(dirPath, "LoRA adapter")
		return cfg, nil
	}

	// Also check for adapter_model.safetensors or adapter_model.bin without adapter_config.json
	if fileExists(filepath.Join(dirPath, "adapter_model.safetensors")) || fileExists(filepath.Join(dirPath, "adapter_model.bin")) {
		xlog.Info("ImportLocalPath: detected LoRA adapter (by model files)", "path", dirPath)
		baseModel := readBaseModel(dirPath)
		cfg := &config.ModelConfig{
			Name:                name,
			Backend:             "transformers",
			KnownUsecaseStrings: []string{"chat"},
		}
		cfg.Model = baseModel
		cfg.TemplateConfig.UseTokenizerTemplate = true
		cfg.LLMConfig.LoraAdapter = relPath(dirPath)
		cfg.Description = buildDescription(dirPath, "LoRA adapter")
		return cfg, nil
	}

	// 3. Merged model: *.safetensors or pytorch_model*.bin + config.json
	if fileExists(filepath.Join(dirPath, "config.json")) && (hasFileWithSuffix(dirPath, ".safetensors") || hasFileWithPrefix(dirPath, "pytorch_model")) {
		xlog.Info("ImportLocalPath: detected merged model", "path", dirPath)
		cfg := &config.ModelConfig{
			Name:                name,
			Backend:             "transformers",
			KnownUsecaseStrings: []string{"chat"},
		}
		cfg.Model = relPath(dirPath)
		cfg.TemplateConfig.UseTokenizerTemplate = true
		cfg.Description = buildDescription(dirPath, "merged model")
		return cfg, nil
	}

	return nil, fmt.Errorf("could not detect model format in directory %s", dirPath)
}

// findGGUF returns the path to the first .gguf file found in dir, or "".
func findGGUF(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".gguf") {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// findFileByPrefixSuffix returns the path to the first file in dir matching
// both prefix (case-sensitive) and suffix (case-insensitive), or "".
func findFileByPrefixSuffix(dir, prefix, suffix string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	lowerSuffix := strings.ToLower(suffix)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(strings.ToLower(name), lowerSuffix) {
			return filepath.Join(dir, name)
		}
	}
	return ""
}

// findSingleONNX returns the path to the first .onnx file found in dir, or "".
// Subdirectories are ignored — callers expect a flat layout.
func findSingleONNX(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".onnx") {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// readBaseModel reads the base model name from adapter_config.json or export_metadata.json.
func readBaseModel(dirPath string) string {
	// Try adapter_config.json → base_model_name_or_path (TRL writes this)
	if data, err := os.ReadFile(filepath.Join(dirPath, "adapter_config.json")); err == nil {
		var ac map[string]any
		if json.Unmarshal(data, &ac) == nil {
			if bm, ok := ac["base_model_name_or_path"].(string); ok && bm != "" {
				return bm
			}
		}
	}

	// Try export_metadata.json → base_model (Unsloth writes this)
	if data, err := os.ReadFile(filepath.Join(dirPath, "export_metadata.json")); err == nil {
		var meta map[string]any
		if json.Unmarshal(data, &meta) == nil {
			if bm, ok := meta["base_model"].(string); ok && bm != "" {
				return bm
			}
		}
	}

	return ""
}

// buildDescription creates a human-readable description using available metadata.
func buildDescription(dirPath, formatLabel string) string {
	base := ""

	// Try adapter_config.json
	if data, err := os.ReadFile(filepath.Join(dirPath, "adapter_config.json")); err == nil {
		var ac map[string]any
		if json.Unmarshal(data, &ac) == nil {
			if bm, ok := ac["base_model_name_or_path"].(string); ok && bm != "" {
				base = bm
			}
		}
	}

	// Try export_metadata.json
	if base == "" {
		if data, err := os.ReadFile(filepath.Join(dirPath, "export_metadata.json")); err == nil {
			var meta map[string]any
			if json.Unmarshal(data, &meta) == nil {
				if bm, ok := meta["base_model"].(string); ok && bm != "" {
					base = bm
				}
			}
		}
	}

	if base != "" {
		return fmt.Sprintf("Fine-tuned from %s (%s)", base, formatLabel)
	}
	return fmt.Sprintf("Fine-tuned model (%s)", formatLabel)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func hasFileWithSuffix(dir, suffix string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), suffix) {
			return true
		}
	}
	return false
}

func hasFileWithPrefix(dir, prefix string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			return true
		}
	}
	return false
}
