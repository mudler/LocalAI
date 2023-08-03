package grammar_test

import (
	"strings"

	. "github.com/go-skynet/LocalAI/pkg/grammar"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	testInput1 = `
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

	inputResult1 = `root-0-function ::= "\"create_event\""
root-0 ::= "{" space "\"arguments\"" space ":" space root-0-arguments "," space "\"function\"" space ":" space root-0-function "}" space
root-1-arguments ::= "{" space "\"query\"" space ":" space string "}" space
root ::= root-0 | root-1
space ::= " "?
root-0-arguments ::= "{" space "\"date\"" space ":" space string "," space "\"time\"" space ":" space string "," space "\"title\"" space ":" space string "}" space
root-1 ::= "{" space "\"arguments\"" space ":" space root-1-arguments "," space "\"function\"" space ":" space root-1-function "}" space
string ::= "\"" (
	[^"\\] |
	"\\" (["\\/bfnrt] | "u" [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F])
)* "\"" space
root-1-function ::= "\"search\""`
)

var _ = Describe("JSON schema grammar tests", func() {
	Context("JSON", func() {
		It("generates a valid grammar from JSON schema", func() {
			grammar := NewJSONSchemaConverter("").GrammarFromBytes([]byte(testInput1))
			results := strings.Split(inputResult1, "\n")
			for _, r := range results {
				if r != "" {
					Expect(grammar).To(ContainSubstring(r))
				}
			}
			Expect(len(results)).To(Equal(len(strings.Split(grammar, "\n"))))
		})
		It("generates a valid grammar from JSON Objects", func() {

			structuredGrammar := JSONFunctionStructure{
				OneOf: []Item{
					{
						Type: "object",
						Properties: Properties{
							Function: FunctionName{
								Const: "create_event",
							},
							Arguments: Argument{ // this is OpenAI's parameter
								Type: "object",
								Properties: map[string]interface{}{
									"title": map[string]string{"type": "string"},
									"date":  map[string]string{"type": "string"},
									"time":  map[string]string{"type": "string"},
								},
							},
						},
					},
					{
						Type: "object",
						Properties: Properties{
							Function: FunctionName{
								Const: "search",
							},
							Arguments: Argument{
								Type: "object",
								Properties: map[string]interface{}{
									"query": map[string]string{"type": "string"},
								},
							},
						},
					},
				}}

			grammar := structuredGrammar.Grammar("")
			results := strings.Split(inputResult1, "\n")
			for _, r := range results {
				if r != "" {
					Expect(grammar).To(ContainSubstring(r))
				}
			}
			Expect(len(results)).To(Equal(len(strings.Split(grammar, "\n"))))
		})
	})
})
