package grammar_test

import (
	. "github.com/go-skynet/LocalAI/pkg/grammar"
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

			js := functions.ToJSONStructure()
			Expect(len(js.OneOf)).To(Equal(2))
			Expect(js.OneOf[0].Properties.Function.Const).To(Equal("create_event"))
			Expect(js.OneOf[0].Properties.Arguments.Properties["event_name"].(map[string]interface{})["type"]).To(Equal("string"))
			Expect(js.OneOf[0].Properties.Arguments.Properties["event_date"].(map[string]interface{})["type"]).To(Equal("string"))
			Expect(js.OneOf[1].Properties.Function.Const).To(Equal("search"))
			Expect(js.OneOf[1].Properties.Arguments.Properties["query"].(map[string]interface{})["type"]).To(Equal("string"))
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
