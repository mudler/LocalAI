package functions_test

import (
	. "github.com/mudler/LocalAI/pkg/functions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LocalAI function parse tests", func() {
	var functionConfig FunctionsConfig

	BeforeEach(func() {
		// Default configuration setup
		functionConfig = FunctionsConfig{}
	})

	Context("when using grammars and single result expected", func() {
		It("should parse the function name and arguments correctly", func() {
			input := `{"function": "add", "arguments": {"x": 5, "y": 3}}`

			results := ParseFunctionCall(input, functionConfig)
			Expect(results).To(HaveLen(1))
			Expect(results[0].Name).To(Equal("add"))
			Expect(results[0].Arguments).To(Equal(`{"x":5,"y":3}`))
		})
	})

	Context("when not using grammars and regex is needed", func() {
		It("should extract function name and arguments from the regex", func() {
			input := `add({"x":5,"y":3})`
			functionConfig.ResponseRegex = []string{`(?P<function>\w+)\s*\((?P<arguments>.*)\)`}

			results := ParseFunctionCall(input, functionConfig)
			Expect(results).To(HaveLen(1))
			Expect(results[0].Name).To(Equal("add"))
			Expect(results[0].Arguments).To(Equal(`{"x":5,"y":3}`))
		})
	})

	Context("when having invalid input", func() {
		It("returns no results when there is no input", func() {
			input := ""
			results := ParseFunctionCall(input, functionConfig)
			Expect(results).To(HaveLen(0))
		})
		It("returns no results when is invalid", func() {
			input := "invalid input"

			results := ParseFunctionCall(input, functionConfig)
			Expect(results).To(HaveLen(0))
		})
	})

	Context("when parallel calls are enabled", func() {
		It("should handle multiple function calls", func() {
			input := `[{"function": "add", "arguments": {"x": 5, "y": 3}}, {"function": "subtract", "arguments": {"x": 10, "y": 7}}]`

			results := ParseFunctionCall(input, functionConfig)
			Expect(results).To(HaveLen(2))
			Expect(results[0].Name).To(Equal("add"))
			Expect(results[0].Arguments).To(Equal(`{"x":5,"y":3}`))
			Expect(results[1].Name).To(Equal("subtract"))
			Expect(results[1].Arguments).To(Equal(`{"x":10,"y":7}`))
		})
	})

	Context("without grammars and without regex", func() {
		It("should parse the function name and arguments correctly with the name key", func() {
			input := `{"name": "add", "arguments": {"x": 5, "y": 3}}`
			functionConfig.FunctionName = true

			results := ParseFunctionCall(input, functionConfig)
			Expect(results).To(HaveLen(1))
			Expect(results[0].Name).To(Equal("add"))
			Expect(results[0].Arguments).To(Equal(`{"x":5,"y":3}`))
		})

		It("should parse the function name and arguments correctly with the function key", func() {
			input := `{"function": "add", "arguments": {"x": 5, "y": 3}}`

			results := ParseFunctionCall(input, functionConfig)
			Expect(results).To(HaveLen(1))
			Expect(results[0].Name).To(Equal("add"))
			Expect(results[0].Arguments).To(Equal(`{"x":5,"y":3}`))
		})

		It("should parse the result by matching the JSONRegexMatch", func() {
			input := `
<tool_call>
{"function": "add", "arguments": {"x": 5, "y": 3}}
</tool_call>`

			functionConfig.JSONRegexMatch = []string{`(?s)<tool_call>(.*?)</tool_call>`}

			results := ParseFunctionCall(input, functionConfig)
			Expect(results).To(HaveLen(1))
			Expect(results[0].Name).To(Equal("add"))
			Expect(results[0].Arguments).To(Equal(`{"x":5,"y":3}`))
		})

		It("should parse the result by matching the JSONRegexMatch", func() {
			input := `
{"function": "add", "arguments": {"x": 5, "y": 3}}
</tool_call>`

			functionConfig.JSONRegexMatch = []string{`(?s)(.*?)</tool_call>`}

			results := ParseFunctionCall(input, functionConfig)
			Expect(results).To(HaveLen(1))
			Expect(results[0].Name).To(Equal("add"))
			Expect(results[0].Arguments).To(Equal(`{"x":5,"y":3}`))
		})
	})

	Context("when using ReplaceResults to clean up input", func() {
		It("should replace text before and after JSON blob", func() {
			input := `
Some text before the JSON
{"function": "add", "arguments": {"x": 5, "y": 3}}
Some text after the JSON
`

			functionConfig.ReplaceFunctionResults = []ReplaceResult{
				{Key: `(?s)^[^{\[]*`, Value: ""},
				{Key: `(?s)[^}\]]*$`, Value: ""},
			}

			results := ParseFunctionCall(input, functionConfig)
			Expect(results).To(HaveLen(1))
			Expect(results[0].Name).To(Equal("add"))
			Expect(results[0].Arguments).To(Equal(`{"x":5,"y":3}`))
		})

		It("should replace text before and after array JSON blob", func() {
			input := `
Some text before the JSON
[{"function": "add", "arguments": {"x": 5, "y": 3}}, {"function": "subtract", "arguments": {"x": 10, "y": 7}}]
Some text after the JSON
`
			functionConfig.ReplaceFunctionResults = []ReplaceResult{
				{Key: `(?s)^[^{\[]*`, Value: ""},
				{Key: `(?s)[^}\]]*$`, Value: ""},
			}

			results := ParseFunctionCall(input, functionConfig)
			Expect(results).To(HaveLen(2))
			Expect(results[0].Name).To(Equal("add"))
			Expect(results[0].Arguments).To(Equal(`{"x":5,"y":3}`))
			Expect(results[1].Name).To(Equal("subtract"))
			Expect(results[1].Arguments).To(Equal(`{"x":10,"y":7}`))
		})

		It("should convert single-quoted key-value pairs to double-quoted and escape double quotes within values", func() {
			input := `
Some text before the JSON
{'function': '"add"', 'arguments': {'x': 5, 'z': '"v"', 'y': 'v"value"'}}
Some text after the JSON
`
			functionConfig.JSONRegexMatch = []string{`(?s)<tool_call>(.*?)</tool_call>`}

			// Regex to match non-JSON characters before the JSON structure
			//reBefore := regexp.MustCompile(`(?s)^.*?(?=\{|\[)`)
			// Regex to match non-JSON characters after the JSON structure
			//reAfter := regexp.MustCompile(`(?s)(?<=\}|\]).*$`)

			functionConfig.ReplaceFunctionResults = []ReplaceResult{
				{Key: `(?s)^[^{\[]*`, Value: ""},
				{Key: `(?s)[^}\]]*$`, Value: ""},
				// Regex pattern to match single quotes around keys and values
				// Step 1: Replace single quotes around keys and values with double quotes
				{Key: `'([^']*?)'`, Value: `_DQUOTE_${1}_DQUOTE_`},
				// Step 2: Replace double quotes inside values with placeholders
				{Key: `\\"`, Value: `__TEMP_QUOTE__`},
				{Key: `"`, Value: `\"`},
				{Key: `\'`, Value: `'`},
				{Key: `_DQUOTE_`, Value: `"`},
				{Key: `__TEMP_QUOTE__`, Value: `"`},
			}

			results := ParseFunctionCall(input, functionConfig)
			Expect(results).To(HaveLen(1))
			Expect(results[0].Name).To(Equal("\"add\""))
			Expect(results[0].Arguments).To(Equal(`{"x":5,"y":"v\"value\"","z":"\"v\""}`))
		})

		It("should convert single-quoted key-value pairs to double-quoted and escape double quotes within values", func() {
			input := `
Some text before the JSON
<tool_call>{'function': '"add"', 'arguments': {'x': 5, 'z': '"v"', 'y': 'v"value"'}}</tool_call>
Some text after the JSON
`
			functionConfig.JSONRegexMatch = []string{`(?s)<tool_call>(.*?)</tool_call>`}

			// Regex to match non-JSON characters before the JSON structure
			//reBefore := regexp.MustCompile(`(?s)^.*?(?=\{|\[)`)
			// Regex to match non-JSON characters after the JSON structure
			//reAfter := regexp.MustCompile(`(?s)(?<=\}|\]).*$`)

			functionConfig.ReplaceFunctionResults = []ReplaceResult{
				{Key: `(?s)^[^{\[]*`, Value: ""},
				{Key: `(?s)[^}\]]*$`, Value: ""},
				// Regex pattern to match single quotes around keys and values
				// Step 1: Replace single quotes around keys and values with double quotes
				{Key: `'([^']*?)'`, Value: `_DQUOTE_${1}_DQUOTE_`},
				// Step 2: Replace double quotes inside values with placeholders
				{Key: `\\"`, Value: `__TEMP_QUOTE__`},
				{Key: `"`, Value: `\"`},
				{Key: `\'`, Value: `'`},
				{Key: `_DQUOTE_`, Value: `"`},
				{Key: `__TEMP_QUOTE__`, Value: `"`},
			}

			results := ParseFunctionCall(input, functionConfig)
			Expect(results).To(HaveLen(1))
			Expect(results[0].Name).To(Equal("\"add\""))
			Expect(results[0].Arguments).To(Equal(`{"x":5,"y":"v\"value\"","z":"\"v\""}`))
		})

		It("should detect multiple functions call where the JSONRegexMatch is repeated", func() {
			input := `
Some text before the JSON
<tool_call>{"function": "add", "arguments": {"x": 5, "y": 3}}</tool_call>
<tool_call>{"function": "subtract", "arguments": {"x": 10, "y": 7}}</tool_call>
Some text after the JSON
`
			functionConfig.JSONRegexMatch = []string{`(?s)<tool_call>(.*?)</tool_call>`}

			results := ParseFunctionCall(input, functionConfig)
			Expect(results).To(HaveLen(2))
			Expect(results[0].Name).To(Equal("add"))
			Expect(results[0].Arguments).To(Equal(`{"x":5,"y":3}`))
			Expect(results[1].Name).To(Equal("subtract"))
			Expect(results[1].Arguments).To(Equal(`{"x":10,"y":7}`))
		})
	})
	Context("ParseTextContent", func() {
		It("Can extract notes from the LLM result", func() {
			input := `
		Some text before the JSON
<sketchpad>
roses are red
</sketchpad>
		<tool_call>{"function": "subtract", "arguments": {"x": 10, "y": 7}}</tool_call>
		Some text after the JSON
		`
			functionConfig.CaptureLLMResult = []string{`(?s)<sketchpad>(.*?)</sketchpad>`}
			results := ParseTextContent(input, functionConfig)
			Expect(results).To(Equal("roses are red"))
		})

		It("Defaults to empty if doesn't catch any", func() {
			input := `
		Some text before the JSON
		<tool_call>{"function": "subtract", "arguments": {"x": 10, "y": 7}}</tool_call>
		Some text after the JSON
		`
			functionConfig.CaptureLLMResult = []string{`(?s)<sketchpad>(.*?)</sketchpad>`}
			results := ParseTextContent(input, functionConfig)
			Expect(results).To(Equal(""))
		})
	})
})
