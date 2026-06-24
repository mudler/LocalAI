package ollama

import (
	"regexp"
	"strings"

	"github.com/mudler/LocalAI/core/config"
)

// modelCapabilities maps a LocalAI ModelConfig to the Ollama capability strings
// (https://github.com/ollama/ollama/blob/main/docs/api.md#show-model-information).
//
// Ollama clients use these to decide which models are eligible for a given task
// (e.g. only allow embedding models in an "embedding model" picker). Returning
// an empty list makes clients assume "completion" everywhere, which is wrong
// for embedding/rerank/audio backends — see issue #9760.
func modelCapabilities(cfg *config.ModelConfig) []string {
	if cfg == nil {
		return nil
	}

	var caps []string

	if cfg.HasUsecases(config.FLAG_EMBEDDINGS) {
		caps = append(caps, "embedding")
	}

	chatCapable := cfg.HasUsecases(config.FLAG_CHAT) || cfg.HasUsecases(config.FLAG_COMPLETION)
	if chatCapable {
		caps = append(caps, "completion")
	}

	if chatCapable && hasVisionSupport(cfg) {
		caps = append(caps, "vision")
	}

	if chatCapable && hasToolSupport(cfg) {
		caps = append(caps, "tools")
	}

	if chatCapable && hasThinkingSupport(cfg) {
		caps = append(caps, "thinking")
	}

	if chatCapable && cfg.TemplateConfig.Completion != "" {
		caps = append(caps, "insert")
	}

	return caps
}

// hasVisionSupport reports whether the model can accept image inputs.
// The detection heuristic is the canonical config.ModelConfig.VisionSupported —
// kept as a thin wrapper here so the Ollama capability mapping reads cleanly.
func hasVisionSupport(cfg *config.ModelConfig) bool {
	return cfg.VisionSupported()
}

// hasToolSupport reports whether the model is wired up for tool / function
// calling. Delegates to the canonical config.ModelConfig.ToolSupported.
func hasToolSupport(cfg *config.ModelConfig) bool {
	return cfg.ToolSupported()
}

// hasThinkingSupport reports whether the model has reasoning / thinking enabled.
// Delegates to the canonical config.ModelConfig.ThinkingSupported.
func hasThinkingSupport(cfg *config.ModelConfig) bool {
	return cfg.ThinkingSupported()
}

// quantRegex matches GGUF-style quantization suffixes (Q4_K_M, Q8_0, IQ3_XS, F16, ...).
// Matches the convention used by GGUF tooling and what ggml-org/llama.cpp report.
var quantRegex = regexp.MustCompile(`(?i)(IQ\d+(?:_[A-Z0-9]+)*|Q\d+(?:_[A-Z0-9]+)*|F16|F32|BF16)`)

// paramSizeRegex matches a parameter-size token surrounded by separators
// (e.g. "-7B-", "_3b.", ".70B-"). Avoids matching the "7" inside "Qwen3".
var paramSizeRegex = regexp.MustCompile(`(?i)(?:^|[-_.])(\d+(?:\.\d+)?[BM])(?:[-_.]|$)`)

// extractQuantizationLevel pulls the quantization tag from the model filename.
// Returns the uppercased token (e.g. "Q4_K_M") or "" when not present.
func extractQuantizationLevel(modelFile string) string {
	if modelFile == "" {
		return ""
	}
	base := strings.TrimSuffix(modelFile, ".gguf")
	if m := quantRegex.FindString(base); m != "" {
		return strings.ToUpper(m)
	}
	return ""
}

// extractParameterSize pulls the parameter count from the model filename.
// Returns "" when no recognizable token is present.
func extractParameterSize(modelFile string) string {
	if modelFile == "" {
		return ""
	}
	base := strings.TrimSuffix(modelFile, ".gguf")
	if m := paramSizeRegex.FindStringSubmatch(base); len(m) > 1 {
		return strings.ToUpper(m[1])
	}
	return ""
}
