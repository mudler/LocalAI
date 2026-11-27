package config

import (
	"errors"
	"maps"
	"strings"

	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/functions/grammars"
	"github.com/mudler/xlog"
)

// The values of the grammar_engine model option, read by the llama.cpp
// backend. See docs/content/features/constrained_grammars.md.
const (
	GrammarEngineAuto       = "auto"
	GrammarEngineGBNF       = "gbnf"
	GrammarEngineLLGuidance = "llguidance"
)

// GrammarEngine returns the grammar_engine option of the model, auto when it
// is not set. An unknown value is returned as is: the backend ignores it.
func (c *ModelConfig) GrammarEngine() string {
	for _, o := range c.Options {
		k, v, ok := strings.Cut(o, ":")
		if ok && strings.TrimSpace(k) == "grammar_engine" {
			v = strings.ToLower(strings.TrimSpace(v))
			if v == "llg" {
				return GrammarEngineLLGuidance
			}
			return v
		}
	}
	return GrammarEngineAuto
}

// SchemaGrammar returns the GBNF grammar of a JSON structure that LocalAI
// builds (a response_format schema or the tool calls), with the grammar
// options of the model. Unless the grammar engine is gbnf, it also keeps the
// llguidance form of that grammar, which LLGuidanceGrammar returns while the
// grammar is in use.
func (c *ModelConfig) SchemaGrammar(js functions.JSONFunctionStructure) (string, error) {
	opts := c.FunctionsConfig.GrammarOptions()
	g, err := js.Grammar(opts...)
	if err != nil {
		return "", err
	}
	if c.GrammarEngine() == GrammarEngineGBNF {
		return g, nil
	}
	llg, err := js.LLGuidanceGrammar(opts...)
	if err != nil {
		if !errors.Is(err, grammars.ErrLLGuidanceUnsupported) {
			xlog.Debug("no llguidance form for the grammar, using GBNF", "error", err)
		}
		return g, nil
	}
	// Copy, never write in place: other copies of this config share the map.
	next := make(map[string]string, len(c.llguidanceGrammars)+1)
	maps.Copy(next, c.llguidanceGrammars)
	next[g] = llg
	c.llguidanceGrammars = next
	return g, nil
}

// LLGuidanceGrammar returns the llguidance form of Grammar, or "" when
// Grammar was not built by SchemaGrammar (a grammar the user wrote).
func (c *ModelConfig) LLGuidanceGrammar() string {
	if c.Grammar == "" {
		return ""
	}
	return c.llguidanceGrammars[c.Grammar]
}
