package peg_test

import (
	"encoding/json"

	"github.com/mudler/LocalAI/pkg/functions/peg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("PEG Utils", func() {
	Context("peg.NormalizeQuotesToJSON", func() {
		It("converts basic single quotes to double quotes", func() {
			input := "{'key': 'value'}"
			expected := `{"key": "value"}`
			Expect(peg.NormalizeQuotesToJSON(input)).To(Equal(expected))
		})

		It("handles escaped single quotes", func() {
			input := `{'code': 'print(\'hello\')'}`
			expected := `{"code": "print('hello')"}`
			Expect(peg.NormalizeQuotesToJSON(input)).To(Equal(expected))
		})

		It("handles double quotes inside single-quoted strings", func() {
			input := `{'msg': 'He said "hi"'}`
			expected := `{"msg": "He said \"hi\""}`
			Expect(peg.NormalizeQuotesToJSON(input)).To(Equal(expected))
		})

		It("handles nested backslash escapes", func() {
			input := `{'path': 'C:\\Users\\test'}`
			expected := `{"path": "C:\\Users\\test"}`
			Expect(peg.NormalizeQuotesToJSON(input)).To(Equal(expected))
		})

		It("handles newline escapes", func() {
			input := `{'text': 'line1\nline2'}`
			expected := `{"text": "line1\nline2"}`
			Expect(peg.NormalizeQuotesToJSON(input)).To(Equal(expected))
		})

		It("handles mixed quotes", func() {
			input := `{"already_double": 'single_value'}`
			expected := `{"already_double": "single_value"}`
			Expect(peg.NormalizeQuotesToJSON(input)).To(Equal(expected))
		})

		It("handles embedded quotes complex case", func() {
			input := `{'filename': 'foo.cpp', 'oldString': 'def foo(arg = "14"):\n    return arg + "bar"\n', 'newString': 'def foo(arg = "15"):\n    pass\n'}`
			result := peg.NormalizeQuotesToJSON(input)

			var parsed map[string]string
			err := json.Unmarshal([]byte(result), &parsed)
			Expect(err).NotTo(HaveOccurred(), "result is not valid JSON: %s", result)

			Expect(parsed["filename"]).To(Equal("foo.cpp"))
			Expect(parsed["oldString"]).NotTo(BeEmpty())
		})
	})

	Context("peg.EscapeJSONStringInner", func() {
		It("leaves basic strings unchanged", func() {
			Expect(peg.EscapeJSONStringInner("hello")).To(Equal("hello"))
		})

		It("escapes double quotes", func() {
			Expect(peg.EscapeJSONStringInner(`hello "world"`)).To(Equal(`hello \"world\"`))
		})

		It("escapes backslash-n sequences", func() {
			Expect(peg.EscapeJSONStringInner(`line1\nline2`)).To(Equal(`line1\\nline2`))
		})
	})

	Context("StandardJSONTools OpenAI format", func() {
		It("parses OpenAI-style tool calls with call ID", func() {
			tools := []peg.ToolDef{
				{
					Name: "get_current_weather",
					Properties: map[string]peg.PropDef{
						"location": {Type: "string"},
					},
				},
			}

			parser := peg.BuildChatPegParser(func(p *peg.ChatBuilder) peg.ParserID {
				toolCall := p.StandardJSONTools(peg.StandardJSONToolsOpts{
					SectionStart:    "<tool_call>",
					SectionEnd:      "</tool_call>",
					Tools:           tools,
					CallIDKey:       "id",
					ParametersOrder: []string{"id", "name", "arguments"},
				})
				return p.Seq(
					p.Content(p.Until("<tool_call>")),
					p.Optional(p.Seq(p.Space(), toolCall)),
					p.End(),
				)
			})

			input := `Let me check the weather.<tool_call>{"id": "call_abc123", "name": "get_current_weather", "arguments": {"location": "NYC"}}</tool_call>`

			ctx := peg.NewParseContext(input, false)
			result := parser.Parse(ctx)

			Expect(result.Type).To(Equal(peg.Success))

			mapper := &peg.ChatPegMapper{}
			mapper.FromAST(&ctx.Ast, &result)
			msg := mapper.Result

			Expect(msg.ToolCalls).To(HaveLen(1))
			Expect(msg.ToolCalls[0].Name).To(Equal("get_current_weather"))
			Expect(msg.ToolCalls[0].ID).To(Equal("call_abc123"))
		})
	})

	Context("StandardJSONTools Cohere format", func() {
		It("parses Cohere-style tool calls with custom keys", func() {
			tools := []peg.ToolDef{
				{
					Name: "get_current_weather",
					Properties: map[string]peg.PropDef{
						"location": {Type: "string"},
						"unit":     {Type: "string"},
					},
				},
			}

			parser := peg.BuildChatPegParser(func(p *peg.ChatBuilder) peg.ParserID {
				toolCall := p.StandardJSONTools(peg.StandardJSONToolsOpts{
					SectionStart:    "<|START_ACTION|>[",
					SectionEnd:      "]<|END_ACTION|>",
					Tools:           tools,
					NameKey:         "tool_name",
					ArgsKey:         "parameters",
					GenCallIDKey:    "tool_call_id",
					ParametersOrder: []string{"tool_call_id", "tool_name", "parameters"},
				})
				return p.Seq(
					p.Content(p.Until("<|START_ACTION|>")),
					p.Optional(p.Seq(p.Space(), toolCall)),
					p.End(),
				)
			})

			input := `Let me search for that.<|START_ACTION|>[{"tool_call_id": 0, "tool_name": "get_current_weather", "parameters": {"location": "NYC", "unit": "celsius"}}]<|END_ACTION|>`

			ctx := peg.NewParseContext(input, false)
			result := parser.Parse(ctx)

			Expect(result.Type).To(Equal(peg.Success))

			mapper := &peg.ChatPegMapper{}
			mapper.FromAST(&ctx.Ast, &result)
			msg := mapper.Result

			Expect(msg.ToolCalls).To(HaveLen(1))
			Expect(msg.ToolCalls[0].Name).To(Equal("get_current_weather"))
			Expect(msg.ToolCalls[0].ID).To(Equal("0"))
		})
	})

	Context("StandardJSONTools function-as-key format", func() {
		It("parses function name as JSON key", func() {
			tools := []peg.ToolDef{
				{
					Name: "get_current_weather",
					Properties: map[string]peg.PropDef{
						"location": {Type: "string"},
						"unit":     {Type: "string"},
					},
				},
			}

			parser := peg.BuildChatPegParser(func(p *peg.ChatBuilder) peg.ParserID {
				toolCall := p.StandardJSONTools(peg.StandardJSONToolsOpts{
					SectionStart:  "<tool_calls>[",
					SectionEnd:    "]</tool_calls>",
					Tools:         tools,
					ArgsKey:       "args",
					FunctionIsKey: true,
					CallIDKey:     "id",
				})
				return p.Seq(
					p.Content(p.Until("<tool_calls>")),
					p.Optional(p.Seq(p.Space(), toolCall)),
					p.End(),
				)
			})

			input := `I'll call the weather function.<tool_calls>[{"get_current_weather": {"id": "call-0001", "args": {"location": "NYC", "unit": "celsius"}}}]</tool_calls>`

			ctx := peg.NewParseContext(input, false)
			result := parser.Parse(ctx)

			Expect(result.Type).To(Equal(peg.Success))

			mapper := &peg.ChatPegMapper{}
			mapper.FromAST(&ctx.Ast, &result)
			msg := mapper.Result

			Expect(msg.ToolCalls).To(HaveLen(1))
			Expect(msg.ToolCalls[0].Name).To(Equal("get_current_weather"))
			Expect(msg.ToolCalls[0].ID).To(Equal("call-0001"))
		})
	})

	Context("Tagged args with embedded quotes", func() {
		It("handles embedded double quotes in tagged parameters", func() {
			tools := []peg.ToolDef{
				{
					Name: "edit",
					Properties: map[string]peg.PropDef{
						"filename":  {Type: "string"},
						"oldString": {Type: "string"},
						"newString": {Type: "string"},
					},
				},
			}

			parser := peg.BuildChatPegParser(func(p *peg.ChatBuilder) peg.ParserID {
				toolCall := p.StandardConstructedTools(
					map[string]string{
						"tool_call_start_marker": "<seed:tool_call>",
						"tool_call_end_marker":   "</seed:tool_call>",
						"function_opener":        "<function=",
						"function_name_suffix":   ">",
						"function_closer":        "</function>",
						"parameter_key_prefix":   "<parameter=",
						"parameter_key_suffix":   ">",
						"parameter_closer":       "</parameter>",
					},
					tools,
					false,
					true,
				)
				return p.Seq(toolCall, p.Space(), p.End())
			})

			input := "<seed:tool_call>\n" +
				"<function=edit>\n" +
				"<parameter=filename>\nfoo.cpp\n</parameter>\n" +
				"<parameter=oldString>def foo(arg = \"14\"):\n    return arg + \"bar\"\n</parameter>\n" +
				"<parameter=newString>def foo(arg = \"15\"):\n    pass\n</parameter>\n" +
				"</function>\n" +
				"</seed:tool_call>"

			ctx := peg.NewParseContext(input, false)
			result := parser.Parse(ctx)

			Expect(result.Type).To(Equal(peg.Success))

			mapper := &peg.ChatPegMapper{}
			mapper.FromAST(&ctx.Ast, &result)
			msg := mapper.Result

			Expect(msg.ToolCalls).To(HaveLen(1))
			Expect(msg.ToolCalls[0].Name).To(Equal("edit"))

			var parsed map[string]any
			err := json.Unmarshal([]byte(msg.ToolCalls[0].Arguments), &parsed)
			Expect(err).NotTo(HaveOccurred(), "arguments not valid JSON: %s", msg.ToolCalls[0].Arguments)
			Expect(parsed["filename"]).NotTo(BeNil())
		})
	})
})
