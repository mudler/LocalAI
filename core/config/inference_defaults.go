package config

//go:generate go run ./gen_inference_defaults/

import (
	_ "embed"
	"encoding/json"
	"strings"

	"github.com/mudler/xlog"
)

//go:embed inference_defaults.json
var inferenceDefaultsJSON []byte

// inferenceDefaults holds the parsed inference defaults data
type inferenceDefaults struct {
	Families map[string]map[string]float64 `json:"families"`
	Patterns []string                      `json:"patterns"`
}

var defaultsData *inferenceDefaults

func init() {
	defaultsData = &inferenceDefaults{}
	if err := json.Unmarshal(inferenceDefaultsJSON, defaultsData); err != nil {
		xlog.Warn("failed to parse inference_defaults.json", "error", err)
	}
}

// normalizeModelID lowercases, strips org prefix (before /), and removes .gguf extension
func normalizeModelID(modelID string) string {
	modelID = strings.ToLower(modelID)

	// Strip org prefix (e.g., "unsloth/Qwen3.5-9B-GGUF" -> "qwen3.5-9b-gguf")
	if idx := strings.LastIndex(modelID, "/"); idx >= 0 {
		modelID = modelID[idx+1:]
	}

	// Strip .gguf extension
	modelID = strings.TrimSuffix(modelID, ".gguf")

	// Replace underscores with hyphens for matching
	modelID = strings.ReplaceAll(modelID, "_", "-")

	return modelID
}

// MatchModelFamily returns the inference defaults for the best-matching model family.
// Patterns are checked in order (longest-match-first as defined in the JSON).
// Returns nil if no family matches.
func MatchModelFamily(modelID string) map[string]float64 {
	if defaultsData == nil || len(defaultsData.Patterns) == 0 {
		return nil
	}

	normalized := normalizeModelID(modelID)

	for _, pattern := range defaultsData.Patterns {
		if strings.Contains(normalized, pattern) {
			if family, ok := defaultsData.Families[pattern]; ok {
				return family
			}
		}
	}

	return nil
}

// ApplyInferenceDefaults sets recommended inference parameters on cfg based on modelID.
// Only fills in parameters that are not already set (nil pointers or zero values).
func ApplyInferenceDefaults(cfg *ModelConfig, modelID string) {
	family := MatchModelFamily(modelID)
	if family == nil {
		return
	}

	xlog.Debug("[inference_defaults] applying defaults for model", "modelID", modelID, "family", family)

	if cfg.Temperature == nil {
		if v, ok := family["temperature"]; ok {
			cfg.Temperature = &v
		}
	}

	if cfg.TopP == nil {
		if v, ok := family["top_p"]; ok {
			cfg.TopP = &v
		}
	}

	if cfg.TopK == nil {
		if v, ok := family["top_k"]; ok {
			intV := int(v)
			cfg.TopK = &intV
		}
	}

	if cfg.MinP == nil {
		if v, ok := family["min_p"]; ok {
			cfg.MinP = &v
		}
	}

	if cfg.RepeatPenalty == 0 {
		if v, ok := family["repeat_penalty"]; ok {
			cfg.RepeatPenalty = v
		}
	}

	if cfg.PresencePenalty == 0 {
		if v, ok := family["presence_penalty"]; ok {
			cfg.PresencePenalty = v
		}
	}
}
