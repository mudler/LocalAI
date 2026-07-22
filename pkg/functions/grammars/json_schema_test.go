package grammars_test

import (
	"strings"

	. "github.com/mudler/LocalAI/pkg/functions"
	. "github.com/mudler/LocalAI/pkg/functions/grammars"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var testFunctions = []Item{
	{
		Type: "object",
		Properties: createFunction(
			"function",
			"arguments",
			"create_event",
			map[string]any{
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
			map[string]any{
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
			map[string]any{
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
			map[string]any{
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
			grammar, err := NewJSONSchemaConverter("").GrammarFromBytes([]byte(testInput1))
			Expect(err).To(BeNil())
			results := strings.Split(inputResult1, "\n")
			for _, r := range results {
				if r != "" {
					Expect(grammar).To(ContainSubstring(r))
				}
			}
			Expect(len(results)).To(Equal(len(strings.Split(grammar, "\n"))))
		})
		It("generates a valid grammar from JSON schema", func() {
			grammar, err := NewJSONSchemaConverter("").GrammarFromBytes([]byte(testInput2))
			Expect(err).To(BeNil())
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

			grammar, err := structuredGrammar.Grammar()
			Expect(err).To(BeNil())
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

			grammar, err := structuredGrammar.Grammar(EnableMaybeArray)
			Expect(err).To(BeNil())
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

			grammar, err := structuredGrammar.Grammar(EnableMaybeArray)
			Expect(err).To(BeNil())
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

			grammar, err := structuredGrammar.Grammar(
				SetPrefix("suffix"),
				EnableMaybeArray,
			)
			Expect(err).To(BeNil())
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

			grammar, err := structuredGrammar.Grammar(SetPrefix("suffix"))
			Expect(err).To(BeNil())
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

			grammar, err := structuredGrammar.Grammar(SetPrefix("suffix"), EnableMaybeString)
			Expect(err).To(BeNil())
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

			grammar, err := structuredGrammar.Grammar(SetPrefix("suffix"), EnableMaybeString, EnableMaybeArray)
			Expect(err).To(BeNil())
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

			grammar, err := structuredGrammar.Grammar(EnableMaybeString, EnableMaybeArray)
			Expect(err).To(BeNil())
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

			grammar, err := structuredGrammar.Grammar(EnableMaybeString, EnableMaybeArray, NoMixedFreeString)
			Expect(err).To(BeNil())
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
			grammar, err := structuredGrammar.Grammar(EnableMaybeString, EnableMaybeArray, DisableParallelNewLines)
			Expect(err).To(BeNil())
			results := strings.Split(content, "\n")
			for _, r := range results {
				if r != "" {
					Expect(grammar).To(ContainSubstring(r))
				}
			}
		})

		It("handles empty object schema without properties", func() {
			// Test case for the bug fix: schema with empty properties map
			emptyObjectSchema := `{
				"type": "object",
				"properties": {}
			}`

			grammar, err := NewJSONSchemaConverter("").GrammarFromBytes([]byte(emptyObjectSchema))
			Expect(err).To(BeNil())
			Expect(grammar).To(ContainSubstring(`root ::= "{" space "}" space`))
		})

		It("handles object schema without properties field", func() {
			// Test case for object schema without properties field at all
			objectWithoutProperties := `{
				"type": "object"
			}`

			grammar, err := NewJSONSchemaConverter("").GrammarFromBytes([]byte(objectWithoutProperties))
			Expect(err).To(BeNil())
			Expect(grammar).To(ContainSubstring(`root ::= "{" space "}" space`))
		})

		It("handles schema with properties but no type field", func() {
			// Test case for the exact scenario causing the panic: schema with properties but no type
			schemaWithPropertiesNoType := `{
				"properties": {}
			}`

			grammar, err := NewJSONSchemaConverter("").GrammarFromBytes([]byte(schemaWithPropertiesNoType))
			Expect(err).To(BeNil())
			Expect(grammar).To(ContainSubstring(`root ::= "{" space "}" space`))
		})

		It("handles multi-type array definitions like [string, null]", func() {
			// Type defined as an array should not panic
			multiTypeSchema := `{
				"type": "object",
				"properties": {
					"street": {
						"description": "The given street name where the company resides.",
						"type": ["string", "null"]
					},
					"city": {
						"description": "The given city where the company resides.",
						"type": ["string", "null"]
					}
				}
			}`

			grammar, err := NewJSONSchemaConverter("").GrammarFromBytes([]byte(multiTypeSchema))
			Expect(err).To(BeNil())
			// The grammar should contain rules for both string and null types
			Expect(grammar).To(ContainSubstring("string"))
			Expect(grammar).To(ContainSubstring("null"))
			// Should not panic and should generate valid grammar
			Expect(grammar).ToNot(BeEmpty())
		})

		It("handles complex nested schema with multi-type arrays (issue #5572)", func() {
			complexSchema := `{
				"type": "object",
				"properties": {
					"companylist": {
						"type": "array",
						"items": {
							"type": "object",
							"properties": {
								"companyname": {
									"description": "The given name of the company.",
									"type": "string"
								},
								"street": {
									"description": "The given street name where the company resides.",
									"type": ["string", "null"]
								},
								"city": {
									"description": "The given city where the company resides.",
									"type": ["string", "null"]
								}
							},
							"additionalProperties": false,
							"required": ["companyname", "street", "city"]
						}
					},
					"filter": {
						"description": "The type we should filter the list of companies by.",
						"type": "string"
					}
				},
				"required": ["companylist", "filter"],
				"additionalProperties": false
			}`

			grammar, err := NewJSONSchemaConverter("").GrammarFromBytes([]byte(complexSchema))
			Expect(err).To(BeNil())
			// The grammar should be generated without panic
			Expect(grammar).ToNot(BeEmpty())
			// Should contain object and array structures
			Expect(grammar).To(ContainSubstring("{"))
			Expect(grammar).To(ContainSubstring("["))
		})
	})
})

var _ = Describe("JSON schema property ordering (issue #10052)", func() {
	// A function-call shaped schema. The grammar must honor the configured
	// properties_order. Before the fix, the sort guard `aOrder != 0 && bOrder != 0`
	// treated the first listed key (index 0) as "unset" and fell back to
	// alphabetical order, so "arguments" was emitted before "name" even when
	// properties_order put name first.
	const schema = `{
		"type": "object",
		"properties": {
			"name": {"type": "string"},
			"arguments": {"type": "object", "properties": {"cmd": {"type": "string"}}}
		}
	}`

	// keyIndex finds the position of an object-key literal (escaped as \"key\"
	// in GBNF), which only appears where the key is emitted in the rule — not
	// in derived rule names like root-name.
	keyIndex := func(grammar, key string) int {
		return strings.Index(grammar, `\"`+key+`\"`)
	}

	It("honors properties_order with name listed first (index 0)", func() {
		grammar, err := NewJSONSchemaConverter("name,arguments").GrammarFromBytes([]byte(schema))
		Expect(err).To(BeNil())
		ni := keyIndex(grammar, "name")
		ai := keyIndex(grammar, "arguments")
		Expect(ni).To(BeNumerically(">=", 0))
		Expect(ai).To(BeNumerically(">=", 0))
		Expect(ni).To(BeNumerically("<", ai),
			"properties_order lists name first, so the grammar must emit \"name\" before \"arguments\"")
	})

	It("keeps alphabetical order when properties_order is empty", func() {
		grammar, err := NewJSONSchemaConverter("").GrammarFromBytes([]byte(schema))
		Expect(err).To(BeNil())
		// No explicit order: keys fall back to alphabetical, so "arguments"
		// precedes "name". This is the documented default and must not change.
		Expect(keyIndex(grammar, "arguments")).To(BeNumerically("<", keyIndex(grammar, "name")))
	})

	It("sorts keys present in properties_order ahead of unlisted keys", func() {
		const schemaWithExtra = `{
			"type": "object",
			"properties": {
				"name": {"type": "string"},
				"arguments": {"type": "object", "properties": {"cmd": {"type": "string"}}},
				"aaa_unlisted": {"type": "string"}
			}
		}`
		// "aaa_unlisted" is alphabetically first but not in the order list, so
		// it must still come after the listed name/arguments keys.
		grammar, err := NewJSONSchemaConverter("name,arguments").GrammarFromBytes([]byte(schemaWithExtra))
		Expect(err).To(BeNil())
		Expect(keyIndex(grammar, "name")).To(BeNumerically("<", keyIndex(grammar, "arguments")))
		Expect(keyIndex(grammar, "arguments")).To(BeNumerically("<", keyIndex(grammar, "aaa_unlisted")))
	})
})

var _ = Describe("JSON schema grammar cyclic $ref handling", func() {
	// A client-supplied grammar_json_functions schema with a cyclic $ref used to
	// recurse until the goroutine stack overflowed, crashing the whole process
	// instead of failing the single request. The converter must now return an
	// error for such schemas rather than recursing forever.
	It("returns an error for a directly self-referential $ref", func() {
		const schema = `{
			"$defs": {"A": {"$ref": "#/$defs/A"}},
			"oneOf": [{"type": "object", "properties": {"x": {"$ref": "#/$defs/A"}}}]
		}`
		_, err := NewJSONSchemaConverter("").GrammarFromBytes([]byte(schema))
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
		_, err := NewJSONSchemaConverter("").GrammarFromBytes([]byte(schema))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("cyclic $ref"))
	})

	It("still resolves a non-cyclic $ref reused by sibling properties", func() {
		const schema = `{
			"$defs": {"Leaf": {"type": "string"}},
			"type": "object",
			"properties": {
				"x": {"$ref": "#/$defs/Leaf"},
				"y": {"$ref": "#/$defs/Leaf"}
			}
		}`
		grammar, err := NewJSONSchemaConverter("").GrammarFromBytes([]byte(schema))
		Expect(err).To(BeNil())
		Expect(grammar).ToNot(BeEmpty())
	})
})
