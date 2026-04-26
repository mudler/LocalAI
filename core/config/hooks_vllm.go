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

func vllmDefaults(cfg *ModelConfig, modelPath string) {
	// Check if user already set tool_parser or reasoning_parser in Options
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

	// Try matching against Model field, then Name
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
