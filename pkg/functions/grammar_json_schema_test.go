package functions_test

import (
	"strings"

	"github.com/mudler/LocalAI/pkg/functions"
	. "github.com/mudler/LocalAI/pkg/functions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func createFunction(field1 string, field2 string, name string, properties map[string]interface{}) map[string]interface{} {
	property := map[string]interface{}{}
	property[field1] = FunctionName{Const: name}
	property[field2] = Argument{
		Type:       "object",
		Properties: properties,
	}
	return property
}

var testFunctions = []Item{
	{
		Type: "object",
		Properties: createFunction(
			"function",
			"arguments",
			"create_event",
			map[string]interface{}{
				"title": map[string]string{"type": "string"},
				"date":  map[string]string{"type": "string"},
				"time":  map[string]string{"type": "string"},
			},
		),
	},
	{
		Type: "object",
		Properties: createFunction(
			"function",
			"arguments",
			"search",
			map[string]interface{}{
				"query": map[string]string{"type": "string"},
			}),
	},
}

var testFunctionsName = []Item{
	{
		Type: "object",
		Properties: createFunction(
			"name",
			"arguments",
			"create_event",
			map[string]interface{}{
				"title": map[string]string{"type": "string"},
				"date":  map[string]string{"type": "string"},
				"time":  map[string]string{"type": "string"},
			},
		),
	},
	{
		Type: "object",
		Properties: createFunction(
			"name",
			"arguments",
			"search",
			map[string]interface{}{
				"query": map[string]string{"type": "string"},
			}),
	},
}

func rootResult(s string) string {
	return `root-0-name ::= "\"create_event\""
freestring ::= (
		[^"\\] |
		"\\" (["\\/bfnrt] | "u" [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F])
  )* space
root-0 ::= "{" space "\"arguments\"" space ":" space root-0-arguments "," space "\"name\"" space ":" space root-0-name "}" space
root-1-arguments ::= "{" space "\"query\"" space ":" space string "}" space
realvalue ::= root-0 | root-1
root ::= ` + s + `
space ::= " "?
root-0-arguments ::= "{" space "\"date\"" space ":" space string "," space "\"time\"" space ":" space string "," space "\"title\"" space ":" space string "}" space
root-1 ::= "{" space "\"arguments\"" space ":" space root-1-arguments "," space "\"name\"" space ":" space root-1-name "}" space
string ::= "\"" (
[^"\\] |
"\\" (["\\/bfnrt] | "u" [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F])
)* "\"" space
arr  ::=
"[\n"  (
	realvalue
(",\n"  realvalue)*
)? "]"
root-1-name ::= "\"search\""`
}

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
freestring ::= (
		[^"\\] |
		"\\" (["\\/bfnrt] | "u" [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F])
  )* space
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

	inputResult2 = `root-0-function ::= "\"create_event\""
freestring ::= (
		[^"\\] |
		"\\" (["\\/bfnrt] | "u" [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F])
  )* space
root-0 ::= "{" space "\"arguments\"" space ":" space root-0-arguments "," space "\"function\"" space ":" space root-0-function "}" space
root-1-arguments ::= "{" space "\"query\"" space ":" space string "}" space
realvalue ::= root-0 | root-1
root ::= arr | realvalue
space ::= " "?
root-0-arguments ::= "{" space "\"date\"" space ":" space string "," space "\"time\"" space ":" space string "," space "\"title\"" space ":" space string "}" space
root-1 ::= "{" space "\"arguments\"" space ":" space root-1-arguments "," space "\"function\"" space ":" space root-1-function "}" space
string ::= "\"" (
	[^"\\] |
	"\\" (["\\/bfnrt] | "u" [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F])
)* "\"" space
arr  ::=
  "[\n"  (
		realvalue
    (",\n"  realvalue)*
  )? "]"
root-1-function ::= "\"search\""`

	testInput2 = `
{
	"oneOf": [
		{
			"type": "object",
			"properties": {
				"name": {"const": "create_event"},
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
				"name": {"const": "search"},
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

	inputResult3 = `root-0-name ::= "\"create_event\""
freestring ::= (
		[^"\\] |
		"\\" (["\\/bfnrt] | "u" [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F])
  )* space
root-0 ::= "{" space "\"arguments\"" space ":" space root-0-arguments "," space "\"name\"" space ":" space root-0-name "}" space
root-1-arguments ::= "{" space "\"query\"" space ":" space string "}" space
root ::= root-0 | root-1
space ::= " "?
root-0-arguments ::= "{" space "\"date\"" space ":" space string "," space "\"time\"" space ":" space string "," space "\"title\"" space ":" space string "}" space
root-1 ::= "{" space "\"arguments\"" space ":" space root-1-arguments "," space "\"name\"" space ":" space root-1-name "}" space
string ::= "\"" (
[^"\\] |
"\\" (["\\/bfnrt] | "u" [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F])
)* "\"" space
root-1-name ::= "\"search\""`

	inputResult4 = `root-0-name ::= "\"create_event\""
freestring ::= (
		[^"\\] |
		"\\" (["\\/bfnrt] | "u" [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F])
  )* space
root-0 ::= "{" space "\"arguments\"" space ":" space root-0-arguments "," space "\"name\"" space ":" space root-0-name "}" space
root-1-arguments ::= "{" space "\"query\"" space ":" space string "}" space
realvalue ::= root-0 | root-1
root ::= arr | realvalue
space ::= " "?
root-0-arguments ::= "{" space "\"date\"" space ":" space string "," space "\"time\"" space ":" space string "," space "\"title\"" space ":" space string "}" space
root-1 ::= "{" space "\"arguments\"" space ":" space root-1-arguments "," space "\"name\"" space ":" space root-1-name "}" space
string ::= "\"" (
[^"\\] |
"\\" (["\\/bfnrt] | "u" [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F])
)* "\"" space
arr  ::=
"[\n"  (
	realvalue
(",\n"  realvalue)*
)? "]"
root-1-name ::= "\"search\""`
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
		It("generates a valid grammar from JSON schema", func() {
			grammar := NewJSONSchemaConverter("").GrammarFromBytes([]byte(testInput2))
			results := strings.Split(inputResult3, "\n")
			for _, r := range results {
				if r != "" {
					Expect(grammar).To(ContainSubstring(r))
				}
			}
			Expect(len(results)).To(Equal(len(strings.Split(grammar, "\n"))))
		})
		It("generates a valid grammar from JSON Objects", func() {

			structuredGrammar := JSONFunctionStructure{
				OneOf: testFunctions}

			grammar := structuredGrammar.Grammar()
			results := strings.Split(inputResult1, "\n")
			for _, r := range results {
				if r != "" {
					Expect(grammar).To(ContainSubstring(r))
				}
			}
			Expect(len(results)).To(Equal(len(strings.Split(grammar, "\n"))))
		})

		It("generates a valid grammar from JSON Objects for multiple function return", func() {
			structuredGrammar := JSONFunctionStructure{
				OneOf: testFunctions}

			grammar := structuredGrammar.Grammar(functions.EnableMaybeArray)
			results := strings.Split(
				strings.Join([]string{
					inputResult2,
					"mixedstring ::= freestring | freestring arr | freestring realvalue"}, "\n"),
				"\n")
			for _, r := range results {
				if r != "" {
					Expect(grammar).To(ContainSubstring(r))
				}
			}
			Expect(len(results)).To(Equal(len(strings.Split(grammar, "\n"))), grammar)
		})

		It("generates a valid grammar from JSON Objects for multiple function return", func() {
			structuredGrammar := JSONFunctionStructure{
				OneOf: testFunctionsName}

			grammar := structuredGrammar.Grammar(functions.EnableMaybeArray)
			results := strings.Split(
				strings.Join([]string{
					inputResult4,
					"mixedstring ::= freestring | freestring arr | freestring realvalue"}, "\n"),
				"\n")
			for _, r := range results {
				if r != "" {
					Expect(grammar).To(ContainSubstring(r))
				}
			}
			Expect(len(results)).To(Equal(len(strings.Split(grammar, "\n"))), grammar)
		})

		It("generates a valid grammar from JSON Objects for multiple function return with a suffix and array", func() {
			structuredGrammar := JSONFunctionStructure{
				OneOf: testFunctionsName}

			grammar := structuredGrammar.Grammar(
				functions.SetPrefix("suffix"),
				functions.EnableMaybeArray,
			)
			results := strings.Split(
				strings.Join([]string{
					rootResult(`"suffix" arr | realvalue`),
					"mixedstring ::= freestring | freestring arr | freestring realvalue"}, "\n"),
				"\n")
			for _, r := range results {
				if r != "" {
					Expect(grammar).To(ContainSubstring(r))
				}
			}
			Expect(len(results)).To(Equal(len(strings.Split(grammar, "\n"))), grammar)
		})
		It("generates a valid grammar from JSON Objects with a suffix", func() {
			structuredGrammar := JSONFunctionStructure{
				OneOf: testFunctionsName}

			grammar := structuredGrammar.Grammar(functions.SetPrefix("suffix"))
			results := strings.Split(
				strings.Join([]string{
					rootResult(`"suffix" realvalue`),
					"mixedstring ::= freestring | freestring realvalue"}, "\n"),
				"\n")
			for _, r := range results {
				if r != "" {
					Expect(grammar).To(ContainSubstring(r))
				}
			}
			Expect(len(results)).To(Equal(len(strings.Split(grammar, "\n"))), grammar)
		})
		It("generates a valid grammar from JSON Objects with a suffix and could return string", func() {
			structuredGrammar := JSONFunctionStructure{
				OneOf: testFunctionsName}

			grammar := structuredGrammar.Grammar(functions.SetPrefix("suffix"), functions.EnableMaybeString)
			results := strings.Split(
				strings.Join([]string{
					rootResult(`( "suffix" realvalue | mixedstring )`),
					"mixedstring ::= freestring | freestring realvalue"}, "\n"),
				"\n")
			for _, r := range results {
				if r != "" {
					Expect(grammar).To(ContainSubstring(r))
				}
			}
			Expect(len(results)).To(Equal(len(strings.Split(grammar, "\n"))), grammar)
		})
		It("generates a valid grammar from JSON Objects with a suffix that could return text or an array of tools", func() {
			structuredGrammar := JSONFunctionStructure{
				OneOf: testFunctionsName}

			grammar := structuredGrammar.Grammar(functions.SetPrefix("suffix"), functions.EnableMaybeString, functions.EnableMaybeArray)
			results := strings.Split(
				strings.Join([]string{
					rootResult(`( "suffix" (arr | realvalue) | mixedstring )`),
					"mixedstring ::= freestring | freestring arr | freestring realvalue"}, "\n"),
				"\n")

			for _, r := range results {
				if r != "" {
					Expect(grammar).To(ContainSubstring(r))
				}
			}
			Expect(len(results)).To(Equal(len(strings.Split(grammar, "\n"))), grammar)
		})

		It("generates a valid grammar from JSON Objects without a suffix that could return text or an array of tools or just string", func() {
			structuredGrammar := JSONFunctionStructure{
				OneOf: testFunctionsName}

			grammar := structuredGrammar.Grammar(functions.EnableMaybeString, functions.EnableMaybeArray)
			results := strings.Split(
				strings.Join([]string{
					rootResult(`mixedstring | arr | realvalue`),
					"mixedstring ::= freestring | freestring arr | freestring realvalue"}, "\n"),
				"\n")
			for _, r := range results {
				if r != "" {
					Expect(grammar).To(ContainSubstring(r))
				}
			}
			Expect(len(results)).To(Equal(len(strings.Split(grammar, "\n"))), grammar)
		})

		It("generates a valid grammar from JSON Objects without a suffix that could return text or an array of tools or just string. Disables mixedstring", func() {
			structuredGrammar := JSONFunctionStructure{
				OneOf: testFunctionsName}

			grammar := structuredGrammar.Grammar(functions.EnableMaybeString, functions.EnableMaybeArray, functions.NoMixedFreeString)
			results := strings.Split(
				strings.Join([]string{
					rootResult(`freestring | arr | realvalue`),
					"mixedstring ::= freestring | freestring arr | freestring realvalue"}, "\n"),
				"\n")
			for _, r := range results {
				if r != "" {
					Expect(grammar).To(ContainSubstring(r))
				}
			}
			Expect(len(results)).To(Equal(len(strings.Split(grammar, "\n"))), grammar)
		})

		It("generates parallel tools without newlines in JSON", func() {
			structuredGrammar := JSONFunctionStructure{
				OneOf: testFunctionsName}
			content := `arr  ::=
"["  (
realvalue
(","  realvalue)*
)? "]"`
			grammar := structuredGrammar.Grammar(functions.EnableMaybeString, functions.EnableMaybeArray, functions.DisableParallelNewLines)
			results := strings.Split(content, "\n")
			for _, r := range results {
				if r != "" {
					Expect(grammar).To(ContainSubstring(r))
				}
			}
		})
	})
})
