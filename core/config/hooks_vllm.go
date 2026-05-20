package config

import (
	_ "embed"
	"encoding/json"
	"strings"

	"github.com/mudler/xlog"
)

//go:embed parser_defaults.json
var parserDefaultsJSON []byte

type parserDefaultsData struct {
	Families map[string]map[string]string `json:"families"`
	Patterns []string                     `json:"patterns"`
}

var parsersData *parserDefaultsData

func init() {
	parsersData = &parserDefaultsData{}
	if err := json.Unmarshal(parserDefaultsJSON, parsersData); err != nil {
		xlog.Warn("failed to parse parser_defaults.json", "error", err)
	}

	RegisterBackendHook("vllm", vllmDefaults)
	RegisterBackendHook("vllm-omni", vllmDefaults)
}

// MatchParserDefaults returns parser defaults for the best-matching model family.
// Returns nil if no family matches. Used both at load time (via hook) and at import time.
func MatchParserDefaults(modelID string) map[string]string {
	if parsersData == nil || len(parsersData.Patterns) == 0 {
		return nil
	}
	normalized := normalizeModelID(modelID)
	for _, pattern := range parsersData.Patterns {
		if strings.Contains(normalized, pattern) {
			if family, ok := parsersData.Families[pattern]; ok {
				return family
			}
		}
	}
	return nil
}

// productionEngineArgsDefaults are vLLM ≥ 0.6 features that production deployments
// almost always want. Applied at load time when the user hasn't set the key in
// engine_args. Anything user-supplied wins; we never silently override.
var productionEngineArgsDefaults = map[string]any{
	"enable_prefix_caching":  true,
	"enable_chunked_prefill": true,
}

func vllmDefaults(cfg *ModelConfig, modelPath string) {
	applyEngineArgDefaults(cfg)
	applyParserDefaults(cfg)
}

// applyEngineArgDefaults seeds production-friendly engine_args without overwriting
// anything the user already set.
func applyEngineArgDefaults(cfg *ModelConfig) {
	if cfg.EngineArgs == nil {
		cfg.EngineArgs = map[string]any{}
	}
	for k, v := range productionEngineArgsDefaults {
		if _, set := cfg.EngineArgs[k]; set {
			continue
		}
		cfg.EngineArgs[k] = v
	}
}

func applyParserDefaults(cfg *ModelConfig) {
	hasToolParser := false
	hasReasoningParser := false
	for _, opt := range cfg.Options {
		if strings.HasPrefix(opt, "tool_parser:") {
			hasToolParser = true
		}
		if strings.HasPrefix(opt, "reasoning_parser:") {
			hasReasoningParser = true
		}
	}
	if hasToolParser && hasReasoningParser {
		return
	}

	parsers := MatchParserDefaults(cfg.Model)
	if parsers == nil {
		parsers = MatchParserDefaults(cfg.Name)
	}
	if parsers == nil {
		return
	}

	if !hasToolParser {
		if tp, ok := parsers["tool_parser"]; ok {
			cfg.Options = append(cfg.Options, "tool_parser:"+tp)
			xlog.Debug("[parser_defaults] auto-set tool_parser", "parser", tp, "model", cfg.Model)
		}
	}
	if !hasReasoningParser {
		if rp, ok := parsers["reasoning_parser"]; ok {
			cfg.Options = append(cfg.Options, "reasoning_parser:"+rp)
			xlog.Debug("[parser_defaults] auto-set reasoning_parser", "parser", rp, "model", cfg.Model)
		}
	}
}
