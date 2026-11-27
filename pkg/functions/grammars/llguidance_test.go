package grammars_test

import (
	"encoding/json"
	"strings"

	. "github.com/mudler/LocalAI/pkg/functions/grammars"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// jsonRuleSchema returns the schema of the %json rule named rule.
func jsonRuleSchema(grammar, rule string) map[string]any {
	GinkgoHelper()
	for _, line := range strings.Split(grammar, "\n") {
		if body, ok := strings.CutPrefix(line, rule+": %json "); ok {
			var schema map[string]any
			Expect(json.Unmarshal([]byte(body), &schema)).To(Succeed())
			return schema
		}
	}
	Fail("no %json rule " + rule + " in:\n" + grammar)
	return nil
}

var _ = Describe("LLGuidanceGrammar", func() {
	toolSchema := func() map[string]any {
		return map[string]any{
			"oneOf": []any{
				map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{"const": "get_weather"},
						"arguments": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"city": map[string]any{"type": "string"},
								"unit": map[string]any{"enum": []any{"c", "f"}},
							},
						},
					},
				},
			},
		}
	}

	It("wraps the schema in one %json start rule", func() {
		g, err := LLGuidanceGrammar(toolSchema())
		Expect(err).ToNot(HaveOccurred())
		Expect(g).To(HavePrefix("%llguidance {}\nstart: %json {"))
		s := jsonRuleSchema(g, "start")
		Expect(s).To(HaveKey("x-guidance"))
		Expect(s["x-guidance"]).To(HaveKeyWithValue("whitespace_pattern", `\x20`))
		Expect(s["x-guidance"]).To(HaveKeyWithValue("coerce_one_of", true))
	})

	It("requires every property and allows no other, like the GBNF grammar", func() {
		g, err := LLGuidanceGrammar(toolSchema())
		Expect(err).ToNot(HaveOccurred())
		s := jsonRuleSchema(g, "start")
		call := s["oneOf"].([]any)[0].(map[string]any)
		Expect(call["required"]).To(Equal([]any{"arguments", "name"}))
		Expect(call["additionalProperties"]).To(Equal(false))
		args := call["properties"].(map[string]any)["arguments"].(map[string]any)
		Expect(args["required"]).To(Equal([]any{"city", "unit"}))
		Expect(args["additionalProperties"]).To(Equal(false))
	})

	It("keeps the required list and additionalProperties of the schema", func() {
		g, err := LLGuidanceGrammar(map[string]any{
			"type":                 "object",
			"properties":           map[string]any{"a": map[string]any{"type": "string"}, "b": map[string]any{"type": "integer"}},
			"required":             []any{"a"},
			"additionalProperties": true,
		})
		Expect(err).ToNot(HaveOccurred())
		s := jsonRuleSchema(g, "start")
		Expect(s["required"]).To(Equal([]any{"a"}))
		Expect(s["additionalProperties"]).To(Equal(true))
	})

	It("does not change the schema it was given", func() {
		in := toolSchema()
		_, err := LLGuidanceGrammar(in)
		Expect(err).ToNot(HaveOccurred())
		Expect(in).To(Equal(toolSchema()))
	})

	It("writes the properties in the property order, then alphabetically", func() {
		g, err := LLGuidanceGrammar(toolSchema(), SetPropOrder("name,arguments"))
		Expect(err).ToNot(HaveOccurred())
		Expect(strings.Index(g, `"name":`)).To(BeNumerically("<", strings.Index(g, `"arguments":`)))
		Expect(strings.Index(g, `"city":`)).To(BeNumerically("<", strings.Index(g, `"unit":`)))

		g, err = LLGuidanceGrammar(toolSchema())
		Expect(err).ToNot(HaveOccurred())
		Expect(strings.Index(g, `"arguments":`)).To(BeNumerically("<", strings.Index(g, `"name":`)))
	})

	It("does not escape HTML characters of the schema", func() {
		g, err := LLGuidanceGrammar(map[string]any{"const": "a<b>&c"})
		Expect(err).ToNot(HaveOccurred())
		Expect(g).To(ContainSubstring(`"a<b>&c"`))
	})

	It("builds the array of parallel calls and the prefix", func() {
		g, err := LLGuidanceGrammar(toolSchema(), EnableMaybeArray, SetPrefix("<tool>\n"))
		Expect(err).ToNot(HaveOccurred())
		Expect(g).To(ContainSubstring("start: \"<tool>\\n\" (arr | value)\n"))
		Expect(g).To(ContainSubstring("arr: \"[\\n\" (value (\",\\n\" value)*)? \"]\"\n"))
		jsonRuleSchema(g, "value")

		g, err = LLGuidanceGrammar(toolSchema(), EnableMaybeArray, DisableParallelNewLines)
		Expect(err).ToNot(HaveOccurred())
		Expect(g).To(ContainSubstring("start: arr | value\n"))
		Expect(g).To(ContainSubstring(`arr: "[" (value ("," value)*)? "]"`))
	})

	It("leaves mixed mode and the llama 3.1 syntax to GBNF", func() {
		_, err := LLGuidanceGrammar(toolSchema(), EnableMaybeString)
		Expect(err).To(MatchError(ErrLLGuidanceUnsupported))
		_, err = LLGuidanceGrammar(toolSchema(), WithSchemaType(LLama31Schema))
		Expect(err).To(MatchError(ErrLLGuidanceUnsupported))
	})
})
