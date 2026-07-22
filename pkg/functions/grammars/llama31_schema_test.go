package grammars_test

import (
	"strings"

	. "github.com/mudler/LocalAI/pkg/functions/grammars"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	testllama31Input1 = `
	{
		"oneOf": [
			{
				"type": "object",
				"properties": {
					"function": {"const": "create_event"},
					"arguments": {
						"type": "object",
						"properties": {
							"title": {"type": "string"},
							"date": {"type": "string"},
							"time": {"type": "string"}
						}
					}
				}
			},
			{
				"type": "object",
				"properties": {
					"function": {"const": "search"},
					"arguments": {
						"type": "object",
						"properties": {
							"query": {"type": "string"}
						}
					}
				}
			}
		]
	}`
	// <function=example_function_name>{{"example_name": "example_value"}}</function>
	testllama31inputResult1 = `root-0-function ::= "create_event"
freestring ::= (
		[^"\\] |
		"\\" (["\\/bfnrt] | "u" [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F])
  )* space
root-0 ::= "<function=" root-0-function ">{" root-0-arguments "}</function>"
root-1-arguments ::= "{" space "\"query\"" space ":" space string "}" space
root ::= root-0 | root-1
space ::= " "?
root-0-arguments ::= "{" space "\"date\"" space ":" space string "," space "\"time\"" space ":" space string "," space "\"title\"" space ":" space string "}" space
root-1 ::= "<function=" root-1-function ">{" root-1-arguments "}</function>"
string ::= "\"" (
	[^"\\] |
	"\\" (["\\/bfnrt] | "u" [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F])
)* "\"" space
root-1-function ::= "search"`
)

var _ = Describe("JSON schema grammar tests", func() {
	Context("JSON", func() {
		It("generates a valid grammar from JSON schema", func() {
			grammar, err := NewLLama31SchemaConverter("function").GrammarFromBytes([]byte(testllama31Input1))
			Expect(err).ToNot(HaveOccurred())
			results := strings.Split(testllama31inputResult1, "\n")
			for _, r := range results {
				if r != "" {
					Expect(grammar).To(ContainSubstring(r))
				}
			}
			Expect(len(results)).To(Equal(len(strings.Split(grammar, "\n"))))
		})
	})
})

var _ = Describe("LLama31 schema grammar cyclic $ref and depth handling", func() {
	// LLama31SchemaConverter.visit is another production entry point named in
	// the crash report (#11020). A cyclic $ref or a deeply nested acyclic schema
	// must fail the request rather than recurse until the stack is exhausted.
	It("returns an error for a directly self-referential $ref", func() {
		const schema = `{
			"$defs": {"A": {"$ref": "#/$defs/A"}},
			"oneOf": [{"type": "object", "properties": {"x": {"$ref": "#/$defs/A"}}}]
		}`
		_, err := NewLLama31SchemaConverter("").GrammarFromBytes([]byte(schema))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("cyclic $ref"))
	})

	It("returns an error for an indirect $ref cycle (A -> B -> A)", func() {
		const schema = `{
			"$defs": {
				"A": {"type": "object", "properties": {"b": {"$ref": "#/$defs/B"}}},
				"B": {"type": "object", "properties": {"a": {"$ref": "#/$defs/A"}}}
			},
			"$ref": "#/$defs/A"
		}`
		_, err := NewLLama31SchemaConverter("").GrammarFromBytes([]byte(schema))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("cyclic $ref"))
	})

	It("returns an error for a schema nested deeper than the depth limit", func() {
		const layers = 400
		schema := `{"type": "string"}`
		for i := 0; i < layers; i++ {
			schema = `{"type": "array", "items": ` + schema + `}`
		}
		_, err := NewLLama31SchemaConverter("").GrammarFromBytes([]byte(schema))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("maximum depth"))
	})
})
