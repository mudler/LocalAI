package functions_test

import (
	"encoding/json"

	. "github.com/mudler/LocalAI/pkg/functions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("LocalAI grammar functions", func() {
	Describe("ToJSONStructure()", func() {
		It("converts a list of functions to a JSON structure that can be parsed to a grammar", func() {
			var functions Functions = []Function{
				{
					Name: "create_event",
					Parameters: map[string]any{
						"properties": map[string]any{
							"event_name": map[string]any{
								"type": "string",
							},
							"event_date": map[string]any{
								"type": "string",
							},
						},
					},
				},
				{
					Name: "search",
					Parameters: map[string]any{
						"properties": map[string]any{
							"query": map[string]any{
								"type": "string",
							},
						},
					},
				},
			}

			js := functions.ToJSONStructure("function", "arguments")
			Expect(len(js.OneOf)).To(Equal(2))
			fnName := js.OneOf[0].Properties["function"].(FunctionName)
			fnArgs := js.OneOf[0].Properties["arguments"].(Argument)
			Expect(fnName.Const).To(Equal("create_event"))
			Expect(fnArgs.Properties["event_name"].(map[string]any)["type"]).To(Equal("string"))
			Expect(fnArgs.Properties["event_date"].(map[string]any)["type"]).To(Equal("string"))

			fnName = js.OneOf[1].Properties["function"].(FunctionName)
			fnArgs = js.OneOf[1].Properties["arguments"].(Argument)
			Expect(fnName.Const).To(Equal("search"))
			Expect(fnArgs.Properties["query"].(map[string]any)["type"]).To(Equal("string"))

			// Test with custom keys
			jsN := functions.ToJSONStructure("name", "arguments")
			Expect(len(jsN.OneOf)).To(Equal(2))

			fnName = jsN.OneOf[0].Properties["name"].(FunctionName)
			fnArgs = jsN.OneOf[0].Properties["arguments"].(Argument)

			Expect(fnName.Const).To(Equal("create_event"))
			Expect(fnArgs.Properties["event_name"].(map[string]any)["type"]).To(Equal("string"))
			Expect(fnArgs.Properties["event_date"].(map[string]any)["type"]).To(Equal("string"))

			fnName = jsN.OneOf[1].Properties["name"].(FunctionName)
			fnArgs = jsN.OneOf[1].Properties["arguments"].(Argument)

			Expect(fnName.Const).To(Equal("search"))
			Expect(fnArgs.Properties["query"].(map[string]any)["type"]).To(Equal("string"))
		})

		It("keeps the name and the arguments in separate properties when both keys are customized", func() {
			var functions Functions = []Function{
				{
					Name: "get_weather",
					Parameters: map[string]any{
						"properties": map[string]any{
							"city": map[string]any{
								"type": "string",
							},
						},
					},
				},
			}

			config := FunctionsConfig{
				FunctionNameKey:      "function",
				FunctionArgumentsKey: "parameters",
			}
			js := config.ToJSONStructure(functions)
			Expect(js.OneOf[0].Properties).To(HaveLen(2))

			fnName := js.OneOf[0].Properties["function"].(FunctionName)
			fnArgs := js.OneOf[0].Properties["parameters"].(Argument)
			Expect(fnName.Const).To(Equal("get_weather"))
			Expect(fnArgs.Properties["city"].(map[string]any)["type"]).To(Equal("string"))
		})
	})
	Context("Select()", func() {
		It("selects one of the functions and returns a list containing only the selected one", func() {
			var functions Functions = []Function{
				{
					Name: "create_event",
				},
				{
					Name: "search",
				},
			}

			functions = functions.Select("create_event")
			Expect(len(functions)).To(Equal(1))
			Expect(functions[0].Name).To(Equal("create_event"))
		})
	})
})

var _ = Describe("Tool JSON", func() {
	It("omits strict when it is false so upstream providers do not see a disabled strict mode", func() {
		tool := Tool{Type: "function", Function: Function{Name: "lookup", Parameters: map[string]any{"type": "object"}}}
		data, err := json.Marshal(tool)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(data)).ToNot(ContainSubstring("strict"))
	})

	It("keeps strict when it is true", func() {
		tool := Tool{Type: "function", Function: Function{Name: "lookup", Parameters: map[string]any{"type": "object"}, Strict: true}}
		data, err := json.Marshal(tool)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(data)).To(ContainSubstring(`"strict":true`))
	})
})

var _ = Describe("JSONBNF", func() {
	It("excludes unescaped control characters in the string rule", func() {
		Expect(JSONBNF).To(ContainSubstring(`[^"\\\x7F\x00-\x1F]`))
		Expect(JSONBNF).ToNot(ContainSubstring(`[^"\\]`))
		Expect(JSONBNF).To(ContainSubstring(`"\\" (["\\/bfnrt] | "u" [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F] [0-9a-fA-F])`))
	})
})
