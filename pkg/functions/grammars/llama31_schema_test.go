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
