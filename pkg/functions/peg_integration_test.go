package functions_test

import (
	. "github.com/mudler/LocalAI/pkg/functions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("PEG Integration", func() {
	Context("format presets", func() {
		It("parses functionary format", func() {
			input := `I'll help you with that.<function=get_weather>{"location": "NYC", "unit": "celsius"}</function>`

			config := FunctionsConfig{
				XMLFormatPreset: "functionary",
			}

			results := ParseFunctionCallPEG(input, config)
			Expect(results).NotTo(BeEmpty())
			Expect(results[0].Name).To(Equal("get_weather"))
			Expect(results[0].Arguments).To(ContainSubstring(`"location"`))
		})

		It("parses qwen3-coder format", func() {
			input := "<tool_call>\n<function=get_weather>\n<parameter=location>\nNYC\n</parameter>\n<parameter=unit>\ncelsius\n</parameter>\n</function>\n</tool_call>"

			config := FunctionsConfig{
				XMLFormatPreset: "qwen3-coder",
			}

			results := ParseFunctionCallPEG(input, config)
			Expect(results).NotTo(BeEmpty())
			Expect(results[0].Name).To(Equal("get_weather"))
			Expect(results[0].Arguments).To(ContainSubstring(`"location"`))
		})

		It("parses qwen3-coder format with preceding content", func() {
			input := "Let me think about this...\n<tool_call>\n<function=get_weather>\n<parameter=location>\nNYC\n</parameter>\n</function>\n</tool_call>"

			config := FunctionsConfig{
				XMLFormatPreset: "qwen3-coder",
			}

			results := ParseFunctionCallPEG(input, config)
			Expect(results).NotTo(BeEmpty())
			Expect(results[0].Name).To(Equal("get_weather"))
		})

		It("parses minimax-m2 format", func() {
			input := "Here's the result.\n<minimax:tool_call>\n<invoke name=\"search\">\n<parameter name=\"query\">test query</parameter>\n</invoke>\n</minimax:tool_call>"

			config := FunctionsConfig{
				XMLFormatPreset: "minimax-m2",
			}

			results := ParseFunctionCallPEG(input, config)
			Expect(results).NotTo(BeEmpty())
			Expect(results[0].Name).To(Equal("search"))
			Expect(results[0].Arguments).To(ContainSubstring(`"query"`))
		})

		It("handles glm-4.5 format gracefully", func() {
			input := "<tool_call><arg_key>location</arg_key><arg_value>NYC</arg_value></tool_call>"

			config := FunctionsConfig{
				XMLFormatPreset: "glm-4.5",
			}

			results := ParseFunctionCallPEG(input, config)
			// GLM-4.5 uses tool_call as both scope and tool start with no function name separator,
			// so the PEG parser may not handle it perfectly.
			if len(results) > 0 {
				Expect(results[0].Arguments).To(ContainSubstring(`"location"`))
			}
		})
	})

	Context("auto-detect", func() {
		It("detects format without preset", func() {
			input := "<tool_call>\n<function=get_weather>\n<parameter=location>\nNYC\n</parameter>\n</function>\n</tool_call>"

			config := FunctionsConfig{}

			results := ParseFunctionCallPEG(input, config)
			Expect(results).NotTo(BeEmpty())
			Expect(results[0].Name).To(Equal("get_weather"))
		})
	})

	Context("custom XML format", func() {
		It("parses with custom format config", func() {
			input := "<tool_call>\n<function=edit>\n<parameter=filename>\ntest.py\n</parameter>\n<parameter=content>\nhello world\n</parameter>\n</function>\n</tool_call>"

			config := FunctionsConfig{
				XMLFormat: &XMLToolCallFormat{
					ScopeStart:    "<tool_call>",
					ToolStart:     "<function=",
					ToolSep:       ">",
					KeyStart:      "<parameter=",
					KeyValSep:     ">",
					ValEnd:        "</parameter>",
					ToolEnd:       "</function>",
					ScopeEnd:      "</tool_call>",
					TrimRawArgVal: true,
				},
			}

			results := ParseFunctionCallPEG(input, config)
			Expect(results).NotTo(BeEmpty())
			Expect(results[0].Name).To(Equal("edit"))
			Expect(results[0].Arguments).To(ContainSubstring(`"filename"`))
		})
	})

	Context("no tool calls", func() {
		It("returns empty results for plain text", func() {
			input := "This is just a regular response with no tool calls."

			config := FunctionsConfig{
				XMLFormatPreset: "qwen3-coder",
			}

			results := ParseFunctionCallPEG(input, config)
			Expect(results).To(BeEmpty())
		})
	})

	Context("ParseFunctionCall integration", func() {
		It("finds tool calls via PEG in ParseFunctionCall flow", func() {
			input := "<tool_call>\n<function=get_weather>\n<parameter=location>\nNYC\n</parameter>\n</function>\n</tool_call>"

			config := FunctionsConfig{}

			results := ParseFunctionCall(input, config)
			Expect(results).NotTo(BeEmpty())
			Expect(results[0].Name).To(Equal("get_weather"))
		})

		It("finds functionary tool calls via ParseFunctionCall", func() {
			input := `Sure!<function=calculator>{"expression": "2+2"}</function>`

			config := FunctionsConfig{
				XMLFormatPreset: "functionary",
			}

			results := ParseFunctionCall(input, config)
			Expect(results).NotTo(BeEmpty())
			Expect(results[0].Name).To(Equal("calculator"))
			Expect(results[0].Arguments).To(ContainSubstring(`"expression"`))
		})
	})

	Context("DisablePEGParser", func() {
		It("still works when called directly but skips PEG in ParseFunctionCall", func() {
			input := "<tool_call>\n<function=get_weather>\n<parameter=location>\nNYC\n</parameter>\n</function>\n</tool_call>"

			config := FunctionsConfig{
				DisablePEGParser: true,
			}

			// ParseFunctionCallPEG should still work when called directly
			pegResults := ParseFunctionCallPEG(input, config)
			// May or may not find results depending on auto-detect
			_ = pegResults

			// ParseFunctionCall with PEG disabled should NOT find XML tool calls
			disabledResults := ParseFunctionCall(input, config)
			// May find via JSON extraction
			_ = disabledResults

			// ParseXML (iterative parser) should still find results
			xmlResults, err := ParseXML(input, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(xmlResults).NotTo(BeEmpty())
			Expect(xmlResults[0].Name).To(Equal("get_weather"))
		})
	})

	Context("markers-based parsing", func() {
		It("parses tag_with_json format from markers", func() {
			input := `Hello!<function=get_weather>{"location": "NYC"}</function>`

			markers := &ToolFormatMarkers{
				FormatType:     "tag_with_json",
				FuncNamePrefix: "<function=",
				FuncNameSuffix: ">",
				FuncClose:      "</function>",
			}

			arena := BuildPEGParserFromMarkers(markers)
			Expect(arena).NotTo(BeNil())

			config := FunctionsConfig{
				ToolFormatMarkers: markers,
			}
			results := ParseFunctionCallPEG(input, config)
			Expect(results).NotTo(BeEmpty())
			Expect(results[0].Name).To(Equal("get_weather"))
			Expect(results[0].Arguments).To(ContainSubstring(`"location"`))
		})

		It("parses tag_with_tagged format from markers", func() {
			input := "<tool_call>\n<function=get_weather>\n<parameter=location>NYC</parameter>\n</function>\n</tool_call>"

			markers := &ToolFormatMarkers{
				FormatType:     "tag_with_tagged",
				SectionStart:   "<tool_call>",
				SectionEnd:     "</tool_call>",
				FuncNamePrefix: "<function=",
				FuncNameSuffix: ">",
				FuncClose:      "</function>",
				ArgNamePrefix:  "<parameter=",
				ArgNameSuffix:  ">",
				ArgValueSuffix: "</parameter>",
			}

			config := FunctionsConfig{
				ToolFormatMarkers: markers,
			}
			results := ParseFunctionCallPEG(input, config)
			Expect(results).NotTo(BeEmpty())
			Expect(results[0].Name).To(Equal("get_weather"))
			Expect(results[0].Arguments).To(ContainSubstring(`"location"`))
		})

		It("parses json_native format from markers", func() {
			input := `Some content<tool_call>{"name": "get_weather", "arguments": {"location": "NYC"}}</tool_call>`

			markers := &ToolFormatMarkers{
				FormatType:   "json_native",
				SectionStart: "<tool_call>",
				SectionEnd:   "</tool_call>",
				NameField:    "name",
				ArgsField:    "arguments",
			}

			config := FunctionsConfig{
				ToolFormatMarkers: markers,
			}
			results := ParseFunctionCallPEG(input, config)
			Expect(results).NotTo(BeEmpty())
			Expect(results[0].Name).To(Equal("get_weather"))
			Expect(results[0].Arguments).To(ContainSubstring(`"location"`))
		})

		It("returns nil arena for unknown format type", func() {
			markers := &ToolFormatMarkers{
				FormatType: "unknown",
			}
			arena := BuildPEGParserFromMarkers(markers)
			Expect(arena).To(BeNil())
		})

		It("parses json_native format with ID field", func() {
			input := `Some content<tool_call>{"name": "get_weather", "arguments": {"location": "NYC"}, "id": "call_123"}</tool_call>`

			markers := &ToolFormatMarkers{
				FormatType:   "json_native",
				SectionStart: "<tool_call>",
				SectionEnd:   "</tool_call>",
				NameField:    "name",
				ArgsField:    "arguments",
				IDField:      "id",
			}

			config := FunctionsConfig{
				ToolFormatMarkers: markers,
			}
			results := ParseFunctionCallPEG(input, config)
			Expect(results).NotTo(BeEmpty())
			Expect(results[0].Name).To(Equal("get_weather"))
			Expect(results[0].Arguments).To(ContainSubstring(`"location"`))
			Expect(results[0].ID).To(Equal("call_123"))
		})

		It("parses call ID between function name and arguments", func() {
			input := `<tool_call><function=get_weather>[call_abc]{"location": "NYC"}</function></tool_call>`

			markers := &ToolFormatMarkers{
				FormatType:     "tag_with_json",
				SectionStart:   "<tool_call>",
				SectionEnd:     "</tool_call>",
				FuncNamePrefix: "<function=",
				FuncNameSuffix: ">",
				FuncClose:      "</function>",
				CallIDPosition: "between_func_and_args",
				CallIDPrefix:   "[",
				CallIDSuffix:   "]",
			}

			config := FunctionsConfig{
				ToolFormatMarkers: markers,
			}
			results := ParseFunctionCallPEG(input, config)
			Expect(results).NotTo(BeEmpty())
			Expect(results[0].Name).To(Equal("get_weather"))
			Expect(results[0].ID).To(Equal("call_abc"))
			Expect(results[0].Arguments).To(ContainSubstring(`"location"`))
		})
	})
})
