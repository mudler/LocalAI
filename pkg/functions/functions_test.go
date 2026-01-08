package functions_test

import (
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
					Parameters: map[string]interface{}{
						"properties": map[string]interface{}{
							"event_name": map[string]interface{}{
								"type": "string",
							},
							"event_date": map[string]interface{}{
								"type": "string",
							},
						},
					},
				},
				{
					Name: "search",
					Parameters: map[string]interface{}{
						"properties": map[string]interface{}{
							"query": map[string]interface{}{
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
			Expect(fnArgs.Properties["event_name"].(map[string]interface{})["type"]).To(Equal("string"))
			Expect(fnArgs.Properties["event_date"].(map[string]interface{})["type"]).To(Equal("string"))

			fnName = js.OneOf[1].Properties["function"].(FunctionName)
			fnArgs = js.OneOf[1].Properties["arguments"].(Argument)
			Expect(fnName.Const).To(Equal("search"))
			Expect(fnArgs.Properties["query"].(map[string]interface{})["type"]).To(Equal("string"))

			// Test with custom keys
			jsN := functions.ToJSONStructure("name", "arguments")
			Expect(len(jsN.OneOf)).To(Equal(2))

			fnName = jsN.OneOf[0].Properties["name"].(FunctionName)
			fnArgs = jsN.OneOf[0].Properties["arguments"].(Argument)

			Expect(fnName.Const).To(Equal("create_event"))
			Expect(fnArgs.Properties["event_name"].(map[string]interface{})["type"]).To(Equal("string"))
			Expect(fnArgs.Properties["event_date"].(map[string]interface{})["type"]).To(Equal("string"))

			fnName = jsN.OneOf[1].Properties["name"].(FunctionName)
			fnArgs = jsN.OneOf[1].Properties["arguments"].(Argument)

			Expect(fnName.Const).To(Equal("search"))
			Expect(fnArgs.Properties["query"].(map[string]interface{})["type"]).To(Equal("string"))
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
