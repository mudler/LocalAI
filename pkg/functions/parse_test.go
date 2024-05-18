package functions_test

import (
	. "github.com/go-skynet/LocalAI/pkg/functions"
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
			functionConfig.ResponseRegex = `(?P<function>\w+)\s*\((?P<arguments>.*)\)`

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

		It("Should parse the result by matching the JSONRegexMatch", func() {
			input := `
<tool_call>
{"function": "add", "arguments": {"x": 5, "y": 3}}
</tool_call>`

			functionConfig.JSONRegexMatch = `(?s)<tool_call>(.*?)</tool_call>`

			results := ParseFunctionCall(input, functionConfig)
			Expect(results).To(HaveLen(1))
			Expect(results[0].Name).To(Equal("add"))
			Expect(results[0].Arguments).To(Equal(`{"x":5,"y":3}`))
		})

		It("Should parse the result by matching the JSONRegexMatch", func() {
			input := `
{"function": "add", "arguments": {"x": 5, "y": 3}}
</tool_call>`

			functionConfig.JSONRegexMatch = `(?s)(.*?)</tool_call>`

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
			functionConfig.ReplaceResults = map[string]string{
				`(?s)^[^\\{]*(?=\\{)`: "",
				`(?s)\\}[^{}]+$`:      "",
			}

			results := ParseFunctionCall(input, functionConfig)
			Expect(results).To(HaveLen(1))
			Expect(results[0].Name).To(Equal("add"))
			Expect(results[0].Arguments).To(Equal(`{"x":5,"y":3}`))
		})

	Context("when using ReplaceResults to clean up input from JSON array", func() {
		It("should replace text before and after array JSON array", func() {
			input := `
Some text before the JSON
[{"function": "add", "arguments": {"x": 5, "y": 3}}, {"function": "subtract", "arguments": {"x": 10, "y": 7}}]
Some text after the JSON
`
			functionConfig.FunctionName = true
			functionConfig.ReplaceResults = map[string]string{
				`(?s)^[^\\[]{*(?=\\[)`: "",
				`(?s)\\][^]]+$`:         "",
			}

			results := ParseFunctionCall(input, functionConfig)
			Expect(results).To(HaveLen(2))
			Expect(results[0].Name).To(Equal("add"))
			Expect(results[0].Arguments).To(Equal(`{"x":5,"y":3}`))
			Expect(results[1].Name).To(Equal("subtract"))
			Expect(results[1].Arguments).To(Equal(`{"x":10,"y":7}`))
		})
	})
	})
})
