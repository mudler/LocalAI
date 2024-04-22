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
		functionConfig = FunctionsConfig{
			ParallelCalls: false,
			NoGrammar:     false,
			ResponseRegex: `(?P<function>\w+)\s*\((?P<arguments>.*)\)`,
		}
	})

	Context("when using grammars and single result expected", func() {
		It("should parse the function name and arguments correctly", func() {
			input := `{"function": "add", "arguments": {"x": 5, "y": 3}}`
			functionConfig.ParallelCalls = false
			functionConfig.NoGrammar = false

			results := ParseFunctionCall(input, functionConfig)
			Expect(results).To(HaveLen(1))
			Expect(results[0].Name).To(Equal("add"))
			Expect(results[0].Arguments).To(Equal(`{"x":5,"y":3}`))
		})
	})

	Context("when not using grammars and regex is needed", func() {
		It("should extract function name and arguments from the regex", func() {
			input := `add({"x":5,"y":3})`
			functionConfig.NoGrammar = true

			results := ParseFunctionCall(input, functionConfig)
			Expect(results).To(HaveLen(1))
			Expect(results[0].Name).To(Equal("add"))
			Expect(results[0].Arguments).To(Equal(`{"x":5,"y":3}`))
		})
	})

	Context("when having invalid input", func() {
		It("returns no results when there is no input", func() {
			input := ""
			functionConfig.NoGrammar = true

			results := ParseFunctionCall(input, functionConfig)
			Expect(results).To(HaveLen(0))

			functionConfig.NoGrammar = false

			results = ParseFunctionCall(input, functionConfig)
			Expect(results).To(HaveLen(0))
		})
		It("returns no results when is invalid", func() {
			input := "invalid input"
			functionConfig.NoGrammar = true

			results := ParseFunctionCall(input, functionConfig)
			Expect(results).To(HaveLen(0))
			functionConfig.NoGrammar = false

			results = ParseFunctionCall(input, functionConfig)
			Expect(results).To(HaveLen(0))
		})
	})
	Context("when parallel calls are enabled", func() {
		It("should handle multiple function calls", func() {
			input := `[{"function": "add", "arguments": {"x": 5, "y": 3}}, {"function": "subtract", "arguments": {"x": 10, "y": 7}}]`
			functionConfig.ParallelCalls = true
			functionConfig.NoGrammar = false

			results := ParseFunctionCall(input, functionConfig)
			Expect(results).To(HaveLen(2))
			Expect(results[0].Name).To(Equal("add"))
			Expect(results[0].Arguments).To(Equal(`{"x":5,"y":3}`))
			Expect(results[1].Name).To(Equal("subtract"))
			Expect(results[1].Arguments).To(Equal(`{"x":10,"y":7}`))
		})
	})
})
