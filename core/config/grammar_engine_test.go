package config_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/functions"
)

var _ = Describe("ModelConfig grammar engine", func() {
	structure := func() functions.JSONFunctionStructure {
		return functions.JSONFunctionStructure{OneOf: []functions.Item{{
			Type: "object",
			Properties: map[string]any{
				"name":      map[string]any{"const": "f"},
				"arguments": map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "string"}}},
			},
		}}}
	}

	It("reads grammar_engine from the options, auto when unset", func() {
		Expect((&config.ModelConfig{}).GrammarEngine()).To(Equal(config.GrammarEngineAuto))
		Expect((&config.ModelConfig{Options: []string{"use_jinja:true", "grammar_engine: GBNF"}}).GrammarEngine()).To(Equal(config.GrammarEngineGBNF))
		Expect((&config.ModelConfig{Options: []string{"grammar_engine:llg"}}).GrammarEngine()).To(Equal(config.GrammarEngineLLGuidance))
	})

	It("keeps the llguidance form of a schema grammar while it is the grammar in use", func() {
		c := &config.ModelConfig{}
		g, err := c.SchemaGrammar(structure())
		Expect(err).ToNot(HaveOccurred())
		Expect(c.LLGuidanceGrammar()).To(BeEmpty(), "Grammar is not set yet")
		c.Grammar = g
		Expect(c.LLGuidanceGrammar()).To(HavePrefix("%llguidance {}\n"))

		// A grammar the user wrote has no llguidance form.
		c.Grammar = `root ::= "yes"`
		Expect(c.LLGuidanceGrammar()).To(BeEmpty())
	})

	It("builds no llguidance form with grammar_engine gbnf or in mixed mode", func() {
		c := &config.ModelConfig{Options: []string{"grammar_engine:gbnf"}}
		g, err := c.SchemaGrammar(structure())
		Expect(err).ToNot(HaveOccurred())
		c.Grammar = g
		Expect(c.LLGuidanceGrammar()).To(BeEmpty())

		m := &config.ModelConfig{}
		m.FunctionsConfig.GrammarConfig.MixedMode = true
		g, err = m.SchemaGrammar(structure())
		Expect(err).ToNot(HaveOccurred())
		m.Grammar = g
		Expect(m.LLGuidanceGrammar()).To(BeEmpty())
	})

	It("does not leak a form into a copy of the config made before", func() {
		base := config.ModelConfig{}
		g, err := base.SchemaGrammar(structure())
		Expect(err).ToNot(HaveOccurred())
		copied := base
		other := functions.JSONFunctionStructure{AnyOf: []functions.Item{{Type: "object", Properties: map[string]any{"y": map[string]any{"type": "integer"}}}}}
		g2, err := copied.SchemaGrammar(other)
		Expect(err).ToNot(HaveOccurred())
		base.Grammar = g2
		Expect(base.LLGuidanceGrammar()).To(BeEmpty())
		base.Grammar = g
		Expect(base.LLGuidanceGrammar()).ToNot(BeEmpty())
	})
})
