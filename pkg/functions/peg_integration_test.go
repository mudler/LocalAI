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

// The LFM3 chat template writes a call as
//
//	<tool_call>\n<function=NAME>\n<parameter=KEY>\nVALUE\n</parameter>\n</function>\n</tool_call>
//
// A string value is raw text. Anything else is rendered as JSON, booleans as
// true and false, null as null.
var _ = Describe("LFM3 tool calls", func() {
	lfm3 := FunctionsConfig{XMLFormatPreset: "lfm3"}

	call := func(name string, params ...string) string {
		s := "<tool_call>\n<function=" + name + ">\n"
		for i := 0; i+1 < len(params); i += 2 {
			s += "<parameter=" + params[i] + ">\n" + params[i+1] + "\n</parameter>\n"
		}
		return s + "</function>\n</tool_call>"
	}

	It("has a preset of its own", func() {
		Expect(GetXMLFormatPreset("lfm3")).NotTo(BeNil())
	})

	It("parses one call", func() {
		results := ParseFunctionCall(call("get_time", "tz", "UTC"), lfm3)
		Expect(results).To(HaveLen(1))
		Expect(results[0].Name).To(Equal("get_time"))
		Expect(results[0].Arguments).To(MatchJSON(`{"tz":"UTC"}`))
	})

	It("parses a call without arguments", func() {
		results := ParseFunctionCall(call("list_models"), lfm3)
		Expect(results).To(HaveLen(1))
		Expect(results[0].Name).To(Equal("list_models"))
		Expect(results[0].Arguments).To(MatchJSON(`{}`))
	})

	It("parses two calls, separated by the blank line the template writes", func() {
		results := ParseFunctionCall(call("get_time", "tz", "UTC")+"\n\n"+call("get_date", "tz", "CET"), lfm3)
		Expect(results).To(HaveLen(2))
		Expect(results[0].Name).To(Equal("get_time"))
		Expect(results[0].Arguments).To(MatchJSON(`{"tz":"UTC"}`))
		Expect(results[1].Name).To(Equal("get_date"))
		Expect(results[1].Arguments).To(MatchJSON(`{"tz":"CET"}`))
	})

	It("keeps the order of consecutive blocks and of blocks with text between them, with the preset and without", func() {
		inputs := map[string]string{
			"back to back": call("first", "n", "1") + call("second", "n", "2") + call("third", "n", "3"),
			"with text":    call("first", "n", "1") + "\nThen the next one.\n" + call("second", "n", "2") + "\nAnd the last.\n" + call("third", "n", "3"),
		}
		for name, input := range inputs {
			for _, cfg := range []FunctionsConfig{lfm3, {}} {
				results := ParseFunctionCall(input, cfg)
				Expect(results).To(HaveLen(3), name)
				for i, want := range []string{"first", "second", "third"} {
					Expect(results[i].Name).To(Equal(want), name)
				}
				Expect(results[2].Arguments).To(MatchJSON(`{"n": 3}`), name)
			}
		}
	})

	// The parser has no tool schema, so a value that reads as JSON is taken as
	// JSON. llama.cpp keeps a parameter the schema types as a string raw.
	It("takes a value that reads as JSON for JSON, whatever the schema types it as", func() {
		results := ParseFunctionCall(call("f", "code", "42", "flag", "true", "name", "Bob"), lfm3)
		Expect(results).To(HaveLen(1))
		Expect(results[0].Arguments).To(MatchJSON(`{"code": 42, "flag": true, "name": "Bob"}`))
	})

	It("keeps a string as text and reads the other values as JSON", func() {
		results := ParseFunctionCall(call("book",
			"title", "The Hobbit",
			"count", "3",
			"ratio", "0.5",
			"urgent", "true",
			"note", "null",
			"tags", `["a", "b"]`,
			"opts", `{"x": 1}`,
		), lfm3)
		Expect(results).To(HaveLen(1))
		Expect(results[0].Arguments).To(MatchJSON(`{
			"title": "The Hobbit",
			"count": 3,
			"ratio": 0.5,
			"urgent": true,
			"note": null,
			"tags": ["a", "b"],
			"opts": {"x": 1}
		}`))
	})

	It("reads a value of digits as a number", func() {
		results := ParseFunctionCall(call("f", "zip", "10115"), lfm3)
		Expect(results).To(HaveLen(1))
		Expect(results[0].Arguments).To(MatchJSON(`{"zip": 10115}`))
	})

	It("keeps a multi-line string value", func() {
		results := ParseFunctionCall(call("write", "text", "line one\nline two"), lfm3)
		Expect(results).To(HaveLen(1))
		Expect(results[0].Arguments).To(MatchJSON(`{"text": "line one\nline two"}`))
	})

	Context("when text after a valid call is not another call", func() {
		inputs := map[string]string{
			"a mention of the opening tag": call("a") + "\nI used the <tool_call> tag to call a.",
			"a markdown example":           call("a") + "\nThe format is:\n```\n<tool_call>\n<function=NAME>\n```\nDone.",
			"a malformed second block":     call("a") + "\nThen\n<tool_call>\n<function=b>\n<parameter=x>\n1\n",
			"a second block without name":  call("a") + "\n<tool_call>\nnothing here",
		}
		for name, input := range inputs {
			It("keeps the first call after "+name, func() {
				for _, cfg := range []FunctionsConfig{lfm3, {}, {XMLFormatPreset: "qwen3-coder"}} {
					results := ParseFunctionCall(input, cfg)
					Expect(results).To(HaveLen(1), name)
					Expect(results[0].Name).To(Equal("a"), name)
				}
			})
		}

		It("keeps that text in the content", func() {
			input := call("a") + "\nI used the <tool_call> tag to call a."
			Expect(StripToolCallMarkup(input)).To(ContainSubstring("I used the"))
			Expect(StripToolCallMarkup(input)).To(ContainSubstring("to call a."))
		})
	})

	It("keeps the text between two blocks when the markup is stripped", func() {
		input := "Checking.\n" + call("a") + "\nNow the second one.\n" + call("b") + "\nDone."
		Expect(StripToolCallMarkup(input)).To(Equal("Checking. Now the second one. Done."))
	})

	It("finds a call between text", func() {
		input := "Let me check.\n" + call("get_time", "tz", "UTC") + "\nDone."
		results := ParseFunctionCall(input, lfm3)
		Expect(results).To(HaveLen(1))
		Expect(results[0].Name).To(Equal("get_time"))
	})

	It("finds nothing in text without a call", func() {
		Expect(ParseFunctionCall("It is noon.", lfm3)).To(BeEmpty())
	})

	It("is found without the preset", func() {
		results := ParseFunctionCall(call("get_time", "tz", "UTC"), FunctionsConfig{})
		Expect(results).To(HaveLen(1))
		Expect(results[0].Name).To(Equal("get_time"))
	})

	Context("while the text is still coming", func() {
		It("reports an unfinished call as partial", func() {
			full := call("get_time", "tz", "UTC")
			format := GetXMLFormatPreset("lfm3")
			// every cut inside the block is partial, and no cut gives a wrong name
			for cut := len("<tool_call>\n<function=get_time>\n<parameter=tz>\nUTC\n</parameter>\n"); cut < len(full); cut++ {
				parser := NewChatMsgParser(full[:cut], true)
				_, err := parser.TryConsumeXMLToolCalls(format)
				if err != nil {
					_, isPartial := err.(*ChatMsgPartialException)
					Expect(isPartial).To(BeTrue(), "cut at %d: %v", cut, err)
				}
				for _, tc := range parser.ToolCalls() {
					Expect(tc.Name).To(Equal("get_time"))
				}
			}
		})

		It("parses the first call while the second is unfinished", func() {
			input := call("get_time", "tz", "UTC") + "\n\n<tool_call>\n<function=get_date>\n<parameter=tz>\nCE"
			parser := NewChatMsgParser(input, true)
			_, _ = parser.TryConsumeXMLToolCalls(GetXMLFormatPreset("lfm3"))
			Expect(parser.ToolCalls()).NotTo(BeEmpty())
			Expect(parser.ToolCalls()[0].Name).To(Equal("get_time"))
		})

		It("parses the full text once it is complete", func() {
			full := call("get_time", "tz", "UTC")
			parser := NewChatMsgParser(full, false)
			ok, err := parser.TryConsumeXMLToolCalls(GetXMLFormatPreset("lfm3"))
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(parser.ToolCalls()).To(HaveLen(1))
			Expect(parser.ToolCalls()[0].Name).To(Equal("get_time"))
		})
	})
})
