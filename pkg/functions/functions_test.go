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
	Context("SanitizeTools()", func() {
		It("returns empty slice when input is empty", func() {
			tools := Tools{}
			sanitized := SanitizeTools(tools)
			Expect(len(sanitized)).To(Equal(0))
		})

		It("converts null values in parameters.properties to empty objects", func() {
			tools := Tools{
				{
					Type: "function",
					Function: Function{
						Name:        "test_function",
						Description: "A test function",
						Parameters: map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"valid_param": map[string]interface{}{
									"type": "string",
								},
								"null_param": nil,
								"another_valid": map[string]interface{}{
									"type": "integer",
								},
							},
						},
					},
				},
			}

			sanitized := SanitizeTools(tools)
			Expect(len(sanitized)).To(Equal(1))
			Expect(sanitized[0].Function.Name).To(Equal("test_function"))

			properties := sanitized[0].Function.Parameters["properties"].(map[string]interface{})
			Expect(properties["valid_param"]).NotTo(BeNil())
			Expect(properties["null_param"]).NotTo(BeNil())
			Expect(properties["null_param"]).To(Equal(map[string]interface{}{}))
			Expect(properties["another_valid"]).NotTo(BeNil())
		})

		It("preserves valid parameter structures unchanged", func() {
			tools := Tools{
				{
					Type: "function",
					Function: Function{
						Name:        "valid_function",
						Description: "A function with valid parameters",
						Parameters: map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"param1": map[string]interface{}{
									"type":        "string",
									"description": "First parameter",
								},
								"param2": map[string]interface{}{
									"type": "integer",
								},
							},
						},
					},
				},
			}

			sanitized := SanitizeTools(tools)
			Expect(len(sanitized)).To(Equal(1))
			Expect(sanitized[0].Function.Name).To(Equal("valid_function"))

			properties := sanitized[0].Function.Parameters["properties"].(map[string]interface{})
			Expect(properties["param1"].(map[string]interface{})["type"]).To(Equal("string"))
			Expect(properties["param1"].(map[string]interface{})["description"]).To(Equal("First parameter"))
			Expect(properties["param2"].(map[string]interface{})["type"]).To(Equal("integer"))
		})

		It("handles tools without parameters field", func() {
			tools := Tools{
				{
					Type: "function",
					Function: Function{
						Name:        "no_params_function",
						Description: "A function without parameters",
					},
				},
			}

			sanitized := SanitizeTools(tools)
			Expect(len(sanitized)).To(Equal(1))
			Expect(sanitized[0].Function.Name).To(Equal("no_params_function"))
			Expect(sanitized[0].Function.Parameters).To(BeNil())
		})

		It("handles tools without properties field", func() {
			tools := Tools{
				{
					Type: "function",
					Function: Function{
						Name:        "no_properties_function",
						Description: "A function without properties",
						Parameters: map[string]interface{}{
							"type": "object",
						},
					},
				},
			}

			sanitized := SanitizeTools(tools)
			Expect(len(sanitized)).To(Equal(1))
			Expect(sanitized[0].Function.Name).To(Equal("no_properties_function"))
			Expect(sanitized[0].Function.Parameters["type"]).To(Equal("object"))
		})

		It("handles multiple tools with mixed valid and null values", func() {
			tools := Tools{
				{
					Type: "function",
					Function: Function{
						Name: "function_with_nulls",
						Parameters: map[string]interface{}{
							"properties": map[string]interface{}{
								"valid": map[string]interface{}{
									"type": "string",
								},
								"null1": nil,
								"null2": nil,
							},
						},
					},
				},
				{
					Type: "function",
					Function: Function{
						Name: "function_all_valid",
						Parameters: map[string]interface{}{
							"properties": map[string]interface{}{
								"param1": map[string]interface{}{
									"type": "string",
								},
								"param2": map[string]interface{}{
									"type": "integer",
								},
							},
						},
					},
				},
				{
					Type: "function",
					Function: Function{
						Name: "function_no_params",
					},
				},
			}

			sanitized := SanitizeTools(tools)
			Expect(len(sanitized)).To(Equal(3))

			// First tool should have nulls converted to empty objects
			props1 := sanitized[0].Function.Parameters["properties"].(map[string]interface{})
			Expect(props1["valid"]).NotTo(BeNil())
			Expect(props1["null1"]).To(Equal(map[string]interface{}{}))
			Expect(props1["null2"]).To(Equal(map[string]interface{}{}))

			// Second tool should remain unchanged
			props2 := sanitized[1].Function.Parameters["properties"].(map[string]interface{})
			Expect(props2["param1"].(map[string]interface{})["type"]).To(Equal("string"))
			Expect(props2["param2"].(map[string]interface{})["type"]).To(Equal("integer"))

			// Third tool should remain unchanged
			Expect(sanitized[2].Function.Parameters).To(BeNil())
		})

		It("does not modify the original tools slice", func() {
			tools := Tools{
				{
					Type: "function",
					Function: Function{
						Name: "test_function",
						Parameters: map[string]interface{}{
							"properties": map[string]interface{}{
								"null_param": nil,
							},
						},
					},
				},
			}

			originalProperties := tools[0].Function.Parameters["properties"].(map[string]interface{})
			originalNullValue := originalProperties["null_param"]

			sanitized := SanitizeTools(tools)

			// Original should still have nil
			Expect(originalNullValue).To(BeNil())

			// Sanitized should have empty object
			sanitizedProperties := sanitized[0].Function.Parameters["properties"].(map[string]interface{})
			Expect(sanitizedProperties["null_param"]).To(Equal(map[string]interface{}{}))
		})
	})
})
