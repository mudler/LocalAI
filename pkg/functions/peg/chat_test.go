package peg_test

import (
	"github.com/mudler/LocalAI/pkg/functions/peg"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func createTools() []peg.ToolDef {
	return []peg.ToolDef{
		{
			Name: "get_current_weather",
			Properties: map[string]peg.PropDef{
				"location": {Type: "string"},
				"unit":     {Type: "string"},
			},
		},
		{
			Name: "get_forecast",
			Properties: map[string]peg.PropDef{
				"location": {Type: "string"},
				"unit":     {Type: "string"},
				"days":     {Type: "integer"},
			},
		},
		{
			Name: "search_knowledge_base",
			Properties: map[string]peg.PropDef{
				"query":       {Type: "string"},
				"max_results": {Type: "integer"},
				"category":    {Type: "string"},
			},
		},
	}
}

func simpleTokenize(input string) []string {
	var result []string
	var current string

	for _, c := range input {
		switch c {
		case ' ', '\n', '\t', '{', '}', ',', '[', '"', ']', '.', '<', '>', '=', '/':
			if current != "" {
				result = append(result, current)
				current = ""
			}
		}
		current += string(c)
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

var _ = Describe("Chat PEG Parser", func() {
	Context("ExampleNative", func() {
		type testCase struct {
			name            string
			tools           []peg.ToolDef
			reasoningFormat string
			parallelCalls   bool
			forcedOpen      bool
			forceToolCalls  bool
			input           string
			expectReasoning string
			expectContent   string
			expectToolCalls []peg.ToolCall
		}

		buildParser := func(tc testCase) *peg.Arena {
			return peg.BuildChatPegParser(func(p *peg.ChatBuilder) peg.ParserID {
				reasoningInContent := tc.reasoningFormat == "none"

				var reasoning peg.ParserID
				if tc.forcedOpen {
					reasoning = p.Seq(
						p.Reasoning(p.Until("</think>")),
						p.Literal("</think>"),
						p.Space(),
					)
				} else {
					reasoning = p.Optional(p.Seq(
						p.Literal("<think>"),
						p.Reasoning(p.Until("</think>")),
						p.Literal("</think>"),
						p.Space(),
					))
				}

				if len(tc.tools) > 0 {
					toolCall := p.StandardJSONTools(peg.StandardJSONToolsOpts{
						SectionStart:   "<tool_call>[",
						SectionEnd:     "]</tool_call>",
						Tools:          tc.tools,
						ParallelCalls:  tc.parallelCalls,
						ForceToolCalls: tc.forceToolCalls,
					})

					var parts []peg.ParserID
					if reasoningInContent {
						parts = append(parts, p.Eps())
					} else {
						parts = append(parts, reasoning)
					}
					parts = append(parts,
						p.Content(p.Until("<tool_call>")),
						p.Optional(p.Seq(p.Space(), toolCall)),
						p.Space(),
						p.End(),
					)
					return p.Seq(parts...)
				}

				var parts []peg.ParserID
				if reasoningInContent {
					parts = append(parts, p.Eps())
				} else {
					parts = append(parts, reasoning)
				}
				parts = append(parts, p.Content(p.Rest()), p.End())
				return p.Seq(parts...)
			})
		}

		DescribeTable("native format cases",
			func(tc testCase) {
				parser := buildParser(tc)
				ctx := peg.NewParseContext(tc.input, false)
				result := parser.Parse(ctx)

				Expect(result.Type).To(Equal(peg.Success))

				mapper := &peg.ChatPegMapper{}
				mapper.FromAST(&ctx.Ast, &result)
				msg := mapper.Result

				Expect(msg.Content).To(Equal(tc.expectContent))
				Expect(msg.ReasoningContent).To(Equal(tc.expectReasoning))
				Expect(msg.ToolCalls).To(HaveLen(len(tc.expectToolCalls)))

				for i := 0; i < len(tc.expectToolCalls) && i < len(msg.ToolCalls); i++ {
					Expect(msg.ToolCalls[i].Name).To(Equal(tc.expectToolCalls[i].Name))
					Expect(msg.ToolCalls[i].Arguments).To(Equal(tc.expectToolCalls[i].Arguments))
				}
			},
			Entry("content with thinking", testCase{
				reasoningFormat: "auto",
				input:           "<think>The user said hello, I must say hello back</think>\nHello",
				expectReasoning: "The user said hello, I must say hello back",
				expectContent:   "Hello",
			}),
			Entry("content without thinking", testCase{
				reasoningFormat: "auto",
				input:           "Hello",
				expectContent:   "Hello",
			}),
			Entry("content with reasoning_format = none", testCase{
				reasoningFormat: "none",
				forcedOpen:      true,
				input:           "<think>The user said hello, I must say hello back</think>\nHello",
				expectContent:   "<think>The user said hello, I must say hello back</think>\nHello",
			}),
			Entry("content with forced_open", testCase{
				reasoningFormat: "auto",
				forcedOpen:      true,
				input:           "The user said hello, I must say hello back</think>\nHello",
				expectReasoning: "The user said hello, I must say hello back",
				expectContent:   "Hello",
			}),
			Entry("content with forced_open and reasoning_format = none", testCase{
				reasoningFormat: "none",
				forcedOpen:      true,
				input:           "The user said hello, I must say hello back</think>\nHello",
				expectContent:   "The user said hello, I must say hello back</think>\nHello",
			}),
			Entry("single tool call", testCase{
				tools:           createTools(),
				reasoningFormat: "auto",
				forcedOpen:      true,
				input: "I must get the weather in New York</think>\n" +
					"<tool_call>[" +
					`{"name": "get_current_weather", "arguments": {"location": "New York City, NY", "unit": "fahrenheit"}}` +
					"]</tool_call>",
				expectReasoning: "I must get the weather in New York",
				expectToolCalls: []peg.ToolCall{
					{
						Name:      "get_current_weather",
						Arguments: `{"location": "New York City, NY", "unit": "fahrenheit"}`,
					},
				},
			}),
			Entry("parallel tool calls", testCase{
				tools:           createTools(),
				reasoningFormat: "auto",
				parallelCalls:   true,
				forcedOpen:      true,
				input: "I must get the weather in New York and San Francisco and a 3 day forecast of each.</think>\nLet me search that for you." +
					"<tool_call>[" +
					`{"name": "get_current_weather", "arguments": {"location": "New York City, NY", "unit": "fahrenheit"}}` +
					", " +
					`{"name": "get_current_weather", "arguments": {"location": "San Francisco, CA", "unit": "fahrenheit"}}` +
					", " +
					`{"name": "get_forecast", "arguments": {"location": "New York City, NY", "unit": "fahrenheit", "days": 3}}` +
					", " +
					`{"name": "get_forecast", "arguments": {"location": "San Francisco, CA", "unit": "fahrenheit", "days": 3}}` +
					"]</tool_call>",
				expectReasoning: "I must get the weather in New York and San Francisco and a 3 day forecast of each.",
				expectContent:   "Let me search that for you.",
				expectToolCalls: []peg.ToolCall{
					{Name: "get_current_weather", Arguments: `{"location": "New York City, NY", "unit": "fahrenheit"}`},
					{Name: "get_current_weather", Arguments: `{"location": "San Francisco, CA", "unit": "fahrenheit"}`},
					{Name: "get_forecast", Arguments: `{"location": "New York City, NY", "unit": "fahrenheit", "days": 3}`},
					{Name: "get_forecast", Arguments: `{"location": "San Francisco, CA", "unit": "fahrenheit", "days": 3}`},
				},
			}),
			Entry("JSON schema response format", testCase{
				tools:           createTools(),
				reasoningFormat: "auto",
				forcedOpen:      true,
				forceToolCalls:  false,
				input: "Thinking about the answer</think>\n" +
					`<tool_call>[{"name": "get_current_weather", "arguments": {"location": "NYC", "unit": "celsius"}}]</tool_call>`,
				expectReasoning: "Thinking about the answer",
				expectToolCalls: []peg.ToolCall{
					{Name: "get_current_weather", Arguments: `{"location": "NYC", "unit": "celsius"}`},
				},
			}),
		)
	})

	Context("ExampleQwen3Coder", func() {
		It("parses tool calls with tagged parameters", func() {
			tools := createTools()
			parser := peg.BuildChatPegParser(func(p *peg.ChatBuilder) peg.ParserID {
				content := p.Rule("content", p.Content(p.Until("<tool_call>")))

				var toolParsers []peg.ParserID
				for _, tool := range tools {
					var argChoices []peg.ParserID
					for propName, prop := range tool.Properties {
						var argValueParser peg.ParserID
						if prop.Type == "string" {
							argValueParser = p.ToolArgStringValue(p.UntilOneOf("</parameter>\n<parameter=", "</parameter>\n</function>"))
						} else {
							argValueParser = p.ToolArgJSONValue(p.JSON())
						}

						arg := p.ToolArg(p.Seq(
							p.ToolArgOpen(p.Literal("<parameter="+propName+">")),
							argValueParser,
							p.ToolArgClose(p.Seq(
								p.Literal("</parameter>\n"),
								p.Peek(p.Choice(p.Literal("<parameter="), p.Literal("</function>"))),
							)),
						))
						argChoices = append(argChoices, arg)
					}

					argChoice := p.Choice(argChoices...)
					args := p.ZeroOrMore(argChoice)

					toolParser := p.Rule("tool-"+tool.Name, p.Seq(
						p.ToolOpen(p.Seq(
							p.Literal("<function="),
							p.ToolName(p.Literal(tool.Name)),
							p.Literal(">\n"),
						)),
						args,
						p.ToolClose(p.Literal("</function>")),
					))
					toolParsers = append(toolParsers, toolParser)
				}

				toolCall := p.TriggerRule("tool-call", p.Seq(
					p.Literal("<tool_call>"), p.Space(),
					p.Choice(toolParsers...), p.Space(),
					p.Literal("</tool_call>"),
				))

				return p.Seq(content, p.ZeroOrMore(p.Seq(p.Space(), toolCall)), p.End())
			})

			input := "Let me search the knowledge base for cat pictures." +
				"<tool_call>\n" +
				"<function=search_knowledge_base>\n" +
				"<parameter=query>cat pictures</parameter>\n" +
				"<parameter=category>general</parameter>\n" +
				"</function>\n" +
				"</tool_call>"

			ctx := peg.NewParseContext(input, false)
			result := parser.Parse(ctx)

			Expect(result.Type).To(Equal(peg.Success))

			mapper := &peg.ChatPegMapper{}
			mapper.FromAST(&ctx.Ast, &result)
			msg := mapper.Result

			Expect(msg.Content).To(Equal("Let me search the knowledge base for cat pictures."))
			Expect(msg.ToolCalls).To(HaveLen(1))
			Expect(msg.ToolCalls[0].Name).To(Equal("search_knowledge_base"))
			Expect(msg.ToolCalls[0].Arguments).NotTo(BeEmpty())
		})
	})

	Context("ExampleQwen3NonCoder", func() {
		It("parses JSON tool calls", func() {
			tools := createTools()
			parser := peg.BuildChatPegParser(func(p *peg.ChatBuilder) peg.ParserID {
				toolCall := p.StandardJSONTools(peg.StandardJSONToolsOpts{
					SectionStart:  "<tool_call>",
					SectionEnd:    "</tool_call>",
					Tools:         tools,
					ParallelCalls: true,
				})
				return p.Seq(
					p.Content(p.Until("<tool_call>")),
					p.Optional(p.Seq(p.Space(), toolCall)),
					p.End(),
				)
			})

			input := "I need to get the weather.\n" +
				"<tool_call>" +
				`{"name": "get_current_weather", "arguments": {"location": "New York City, NY", "unit": "fahrenheit"}}` +
				"</tool_call>"

			ctx := peg.NewParseContext(input, false)
			result := parser.Parse(ctx)

			Expect(result.Type).To(Equal(peg.Success))

			mapper := &peg.ChatPegMapper{}
			mapper.FromAST(&ctx.Ast, &result)
			msg := mapper.Result

			Expect(msg.Content).To(Equal("I need to get the weather.\n"))
			Expect(msg.ReasoningContent).To(BeEmpty())
			Expect(msg.ToolCalls).To(HaveLen(1))
			Expect(msg.ToolCalls[0].Name).To(Equal("get_current_weather"))
			Expect(msg.ToolCalls[0].Arguments).To(Equal(`{"location": "New York City, NY", "unit": "fahrenheit"}`))
		})
	})

	Context("Command7", func() {
		var parser *peg.Arena

		BeforeEach(func() {
			parser = peg.BuildChatPegParser(func(p *peg.ChatBuilder) peg.ParserID {
				thinking := p.ReasoningBlock(p.Seq(
					p.Literal("<|START_THINKING|>"), p.Space(),
					p.Reasoning(p.Until("<|END_THINKING|>")), p.Space(),
					p.Literal("<|END_THINKING|>"),
				))

				response := p.Seq(
					p.Literal("<|START_RESPONSE|>"), p.Space(),
					p.Content(p.Until("<|END_RESPONSE|>")), p.Space(),
					p.Literal("<|END_RESPONSE|>"),
				)

				toolCallID := p.Atomic(p.Seq(
					p.Literal("\"tool_call_id\""), p.Space(), p.Literal(":"), p.Space(),
					p.Literal("\""), p.ToolID(p.JSONString()), p.Literal("\""),
				))
				toolCallName := p.Atomic(p.Seq(
					p.Literal("\"tool_name\""), p.Space(), p.Literal(":"), p.Space(),
					p.Literal("\""), p.ToolName(p.JSONString()), p.Literal("\""),
				))
				toolCallArgs := p.Seq(
					p.Literal("\"parameters\""), p.Space(), p.Literal(":"), p.Space(),
					p.ToolArgs(p.JSON()),
				)

				toolCallFields := p.Rule("tool-call-fields", p.Choice(toolCallID, toolCallName, toolCallArgs))
				toolCall := p.Rule("tool-call-single", p.Tool(p.Seq(
					p.ToolOpen(p.Literal("{")), p.Space(),
					toolCallFields,
					p.ZeroOrMore(p.Seq(p.Literal(","), p.Space(), toolCallFields)),
					p.Space(), p.ToolClose(p.Literal("}")),
				)))

				toolCalls := p.Rule("tool-calls", p.Seq(
					p.Literal("<|START_ACTION|>"), p.Space(),
					p.Literal("["), p.Space(),
					toolCall,
					p.ZeroOrMore(p.Seq(p.Literal(","), p.Space(), toolCall)),
					p.Space(), p.Literal("]"), p.Space(),
					p.Literal("<|END_ACTION|>"),
				))

				return p.Seq(
					p.Optional(p.Seq(thinking, p.Space())),
					p.Choice(toolCalls, response),
					p.End(),
				)
			})
		})

		It("parses tool call with reasoning", func() {
			input := "<|START_THINKING|>I need to plan a trip to Japan.\n<|END_THINKING|>" +
				"<|START_ACTION|>[" +
				`{"tool_call_id": "call_0", "tool_name": "plan_trip", "parameters": {"destination": "Japan", "duration": 14}}` +
				"]<|END_ACTION|>"

			ctx := peg.NewParseContext(input, false)
			result := parser.Parse(ctx)

			Expect(result.Type).To(Equal(peg.Success))

			mapper := &peg.ChatPegMapper{}
			mapper.FromAST(&ctx.Ast, &result)
			msg := mapper.Result

			Expect(msg.ReasoningContent).To(Equal("I need to plan a trip to Japan.\n"))
			Expect(msg.ToolCalls).To(HaveLen(1))
			Expect(msg.ToolCalls[0].Name).To(Equal("plan_trip"))
			Expect(msg.ToolCalls[0].ID).To(Equal("call_0"))
		})

		It("parses content-only response", func() {
			input := "<|START_RESPONSE|>Hello, how can I help you?<|END_RESPONSE|>"

			ctx := peg.NewParseContext(input, false)
			result := parser.Parse(ctx)

			Expect(result.Type).To(Equal(peg.Success))

			mapper := &peg.ChatPegMapper{}
			mapper.FromAST(&ctx.Ast, &result)
			msg := mapper.Result

			Expect(msg.Content).To(Equal("Hello, how can I help you?"))
			Expect(msg.ToolCalls).To(BeEmpty())
		})
	})

	Context("PrefixToolNames", func() {
		var parser *peg.Arena

		BeforeEach(func() {
			tools := []peg.ToolDef{
				{Name: "special_function", Properties: map[string]peg.PropDef{"arg1": {Type: "string"}}},
				{Name: "special_function_with_opt", Properties: map[string]peg.PropDef{"arg1": {Type: "string"}}},
			}

			parser = peg.BuildChatPegParser(func(p *peg.ChatBuilder) peg.ParserID {
				toolCall := p.StandardConstructedTools(
					map[string]string{},
					tools,
					true,
					false,
				)
				return p.Seq(
					p.Content(p.Until("<tool_call>")),
					p.Optional(p.Seq(p.Space(), toolCall)),
					p.End(),
				)
			})
		})

		It("parses long tool name", func() {
			input := "Let me call the function.<tool_call><function=special_function_with_opt><param=arg1>42</param></function></tool_call>"

			ctx := peg.NewParseContext(input, false)
			result := parser.Parse(ctx)

			Expect(result.Type).To(Equal(peg.Success))

			mapper := &peg.ChatPegMapper{}
			mapper.FromAST(&ctx.Ast, &result)
			Expect(mapper.Result.ToolCalls).To(HaveLen(1))
			Expect(mapper.Result.ToolCalls[0].Name).To(Equal("special_function_with_opt"))
		})

		It("parses short tool name", func() {
			input := "Let me call the function.<tool_call><function=special_function><param=arg1>42</param></function></tool_call>"

			ctx := peg.NewParseContext(input, false)
			result := parser.Parse(ctx)

			Expect(result.Type).To(Equal(peg.Success))

			mapper := &peg.ChatPegMapper{}
			mapper.FromAST(&ctx.Ast, &result)
			Expect(mapper.Result.ToolCalls).To(HaveLen(1))
			Expect(mapper.Result.ToolCalls[0].Name).To(Equal("special_function"))
		})

		It("never prematurely matches during incremental parsing", func() {
			input := "Let me call the function." +
				"<tool_call>" +
				"<function=special_function_with_opt>" +
				"<param=arg1>42</param>" +
				"</function>" +
				"</tool_call>"

			tokens := simpleTokenize(input)
			var accumulated string

			for i, tok := range tokens {
				accumulated += tok
				isPartial := i < len(tokens)-1

				ctx := peg.NewParseContext(accumulated, isPartial)
				result := parser.Parse(ctx)

				Expect(result.Type).NotTo(Equal(peg.Fail), "parse failed at token %d, input: %s", i, accumulated)

				mapper := &peg.ChatPegMapper{}
				mapper.FromAST(&ctx.Ast, &result)

				for _, tc := range mapper.Result.ToolCalls {
					Expect(tc.Name).NotTo(Equal("special_function"),
						"premature tool name match at token %d, input: %s", i, accumulated)
				}
			}

			ctx := peg.NewParseContext(input, false)
			result := parser.Parse(ctx)
			Expect(result.Type).To(Equal(peg.Success))
			mapper := &peg.ChatPegMapper{}
			mapper.FromAST(&ctx.Ast, &result)
			Expect(mapper.Result.ToolCalls).To(HaveLen(1))
			Expect(mapper.Result.ToolCalls[0].Name).To(Equal("special_function_with_opt"))
		})
	})

	Context("IncrementalParsing", func() {
		It("handles qwen3 coder format incrementally", func() {
			tools := createTools()
			parser := peg.BuildChatPegParser(func(p *peg.ChatBuilder) peg.ParserID {
				content := p.Rule("content", p.Content(p.Until("<tool_call>")))

				var toolParsers []peg.ParserID
				for _, tool := range tools {
					var argChoices []peg.ParserID
					for propName, prop := range tool.Properties {
						var argValueParser peg.ParserID
						if prop.Type == "string" {
							argValueParser = p.ToolArgStringValue(p.UntilOneOf("</parameter>\n<parameter=", "</parameter>\n</function>"))
						} else {
							argValueParser = p.ToolArgJSONValue(p.JSON())
						}
						arg := p.ToolArg(p.Seq(
							p.ToolArgOpen(p.Literal("<parameter="+propName+">")),
							argValueParser,
							p.ToolArgClose(p.Seq(
								p.Literal("</parameter>\n"),
								p.Peek(p.Choice(p.Literal("<parameter="), p.Literal("</function>"))),
							)),
						))
						argChoices = append(argChoices, arg)
					}
					argChoice := p.Choice(argChoices...)
					args := p.ZeroOrMore(argChoice)
					toolParser := p.Rule("tool-"+tool.Name, p.Seq(
						p.ToolOpen(p.Seq(p.Literal("<function="), p.ToolName(p.Literal(tool.Name)), p.Literal(">\n"))),
						args,
						p.ToolClose(p.Literal("</function>")),
					))
					toolParsers = append(toolParsers, toolParser)
				}
				toolCall := p.TriggerRule("tool-call", p.Seq(
					p.Literal("<tool_call>"), p.Space(),
					p.Choice(toolParsers...), p.Space(),
					p.Literal("</tool_call>"),
				))
				return p.Seq(content, p.ZeroOrMore(p.Seq(p.Space(), toolCall)), p.End())
			})

			input := "Let me search the knowledge base for cat pictures." +
				"<tool_call>\n" +
				"<function=search_knowledge_base>\n" +
				"<parameter=query>cat pictures</parameter>\n" +
				"<parameter=category>general</parameter>\n" +
				"</function>\n" +
				"</tool_call>"

			tokens := simpleTokenize(input)
			var accumulated string
			var prevToolCalls int

			for i, tok := range tokens {
				accumulated += tok
				isPartial := i < len(tokens)-1

				ctx := peg.NewParseContext(accumulated, isPartial)
				result := parser.Parse(ctx)

				Expect(result.Type).NotTo(Equal(peg.Fail), "parse failed at token %d, input: %s", i, accumulated)

				mapper := &peg.ChatPegMapper{}
				mapper.FromAST(&ctx.Ast, &result)

				Expect(len(mapper.Result.ToolCalls)).To(BeNumerically(">=", prevToolCalls),
					"tool call count decreased at token %d", i)
				prevToolCalls = len(mapper.Result.ToolCalls)
			}
		})

		It("handles qwen3 non-coder format incrementally", func() {
			tools := createTools()
			parser := peg.BuildChatPegParser(func(p *peg.ChatBuilder) peg.ParserID {
				toolCall := p.StandardJSONTools(peg.StandardJSONToolsOpts{
					SectionStart:  "<tool_call>",
					SectionEnd:    "</tool_call>",
					Tools:         tools,
					ParallelCalls: true,
				})
				return p.Seq(
					p.Content(p.Until("<tool_call>")),
					p.Optional(p.Seq(p.Space(), toolCall)),
					p.End(),
				)
			})

			input := "I need to get the weather.\n" +
				"<tool_call>" +
				`{"name": "get_current_weather", "arguments": {"location": "New York City, NY", "unit": "fahrenheit"}}` +
				"</tool_call>"

			tokens := simpleTokenize(input)
			var accumulated string

			for i, tok := range tokens {
				accumulated += tok
				isPartial := i < len(tokens)-1

				ctx := peg.NewParseContext(accumulated, isPartial)
				result := parser.Parse(ctx)

				Expect(result.Type).NotTo(Equal(peg.Fail), "parse failed at token %d, input: %s", i, accumulated)
			}

			ctx := peg.NewParseContext(input, false)
			result := parser.Parse(ctx)
			Expect(result.Type).To(Equal(peg.Success))

			mapper := &peg.ChatPegMapper{}
			mapper.FromAST(&ctx.Ast, &result)
			Expect(mapper.Result.ToolCalls).To(HaveLen(1))
			Expect(mapper.Result.ToolCalls[0].Name).To(Equal("get_current_weather"))
		})
	})

	Context("Command7 complex input", func() {
		It("parses complex reasoning and tool calls", func() {
			parser := peg.BuildChatPegParser(func(p *peg.ChatBuilder) peg.ParserID {
				thinking := p.ReasoningBlock(p.Seq(
					p.Literal("<|START_THINKING|>"), p.Space(),
					p.Reasoning(p.Until("<|END_THINKING|>")), p.Space(),
					p.Literal("<|END_THINKING|>"),
				))

				toolCallID := p.Atomic(p.Seq(
					p.Literal("\"tool_call_id\""), p.Space(), p.Literal(":"), p.Space(),
					p.Literal("\""), p.ToolID(p.JSONString()), p.Literal("\""),
				))
				toolCallName := p.Atomic(p.Seq(
					p.Literal("\"tool_name\""), p.Space(), p.Literal(":"), p.Space(),
					p.Literal("\""), p.ToolName(p.JSONString()), p.Literal("\""),
				))
				toolCallArgs := p.Seq(
					p.Literal("\"parameters\""), p.Space(), p.Literal(":"), p.Space(),
					p.ToolArgs(p.JSON()),
				)

				toolCallFields := p.Rule("tool-call-fields", p.Choice(toolCallID, toolCallName, toolCallArgs))
				toolCall := p.Rule("tool-call-single", p.Tool(p.Seq(
					p.ToolOpen(p.Literal("{")), p.Space(),
					toolCallFields,
					p.ZeroOrMore(p.Seq(p.Literal(","), p.Space(), toolCallFields)),
					p.Space(), p.ToolClose(p.Literal("}")),
				)))

				toolCalls := p.Rule("tool-calls", p.Seq(
					p.Literal("<|START_ACTION|>"), p.Space(),
					p.Literal("["), p.Space(),
					toolCall,
					p.ZeroOrMore(p.Seq(p.Literal(","), p.Space(), toolCall)),
					p.Space(), p.Literal("]"), p.Space(),
					p.Literal("<|END_ACTION|>"),
				))

				return p.Seq(
					p.Optional(p.Seq(thinking, p.Space())),
					toolCalls,
					p.End(),
				)
			})

			reasoning := "To plan an effective trip to Japan that includes both historical sites and modern attractions within a " +
				"budget of $4000 for a two-week stay, we need to:\n\n" +
				"1. Identify key historical sites and modern attractions in Japan.\n" +
				"2. Find affordable accommodation options that provide a balance between comfort and cost.\n" +
				"3. Determine the best modes of transportation for getting around Japan.\n" +
				"4. Create a day-by-day itinerary that ensures the user gets to see a variety of attractions without " +
				"overspending.\n" +
				"5. Provide a detailed cost breakdown that includes accommodation, transportation, meals, and entry fees " +
				"to attractions."

			input := "<|START_THINKING|>" + reasoning + "<|END_THINKING|>" +
				`<|START_ACTION|>[{"tool_call_id": "call_0", "tool_name": "plan_trip", "parameters": {"destination": "Japan", "duration": 14, "budget": 4000, "interests": ["historical sites", "modern attractions"], "accommodation_preferences": "affordable", "transportation_preferences": "efficient", "meal_preferences": "local cuisine"}}]<|END_ACTION|>`

			ctx := peg.NewParseContext(input, false)
			result := parser.Parse(ctx)

			Expect(result.Type).To(Equal(peg.Success))

			mapper := &peg.ChatPegMapper{}
			mapper.FromAST(&ctx.Ast, &result)
			msg := mapper.Result

			Expect(msg.ReasoningContent).To(Equal(reasoning))
			Expect(msg.ToolCalls).To(HaveLen(1))
			Expect(msg.ToolCalls[0].Name).To(Equal("plan_trip"))
			Expect(msg.ToolCalls[0].ID).To(Equal("call_0"))
			Expect(msg.ToolCalls[0].Arguments).To(ContainSubstring(`"interests"`))
			Expect(msg.ToolCalls[0].Arguments).To(ContainSubstring(`"historical sites"`))
		})
	})

	Context("ForceToolCalls", func() {
		var parser *peg.Arena

		BeforeEach(func() {
			tools := createTools()
			parser = peg.BuildChatPegParser(func(p *peg.ChatBuilder) peg.ParserID {
				toolCall := p.StandardJSONTools(peg.StandardJSONToolsOpts{
					SectionStart:   "<tool_call>[",
					SectionEnd:     "]</tool_call>",
					Tools:          tools,
					ForceToolCalls: true,
				})
				return p.Seq(
					p.Content(p.Until("<tool_call>")),
					p.Space(),
					toolCall,
					p.Space(),
					p.End(),
				)
			})
		})

		It("succeeds with tool call present", func() {
			input := "Let me check." +
				`<tool_call>[{"name": "get_current_weather", "arguments": {"location": "NYC", "unit": "celsius"}}]</tool_call>`

			ctx := peg.NewParseContext(input, false)
			result := parser.Parse(ctx)

			Expect(result.Type).To(Equal(peg.Success))

			mapper := &peg.ChatPegMapper{}
			mapper.FromAST(&ctx.Ast, &result)
			Expect(mapper.Result.ToolCalls).To(HaveLen(1))
			Expect(mapper.Result.ToolCalls[0].Name).To(Equal("get_current_weather"))
		})

		It("fails without tool call", func() {
			input := "Just a response without any tool calls."

			ctx := peg.NewParseContext(input, false)
			result := parser.Parse(ctx)

			Expect(result.Type).To(Equal(peg.Fail))
		})
	})

	Context("NestedKeysJSONTools", func() {
		It("parses nested function.name and function.arguments keys", func() {
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
					SectionStart: "<tool_call>",
					SectionEnd:   "</tool_call>",
					Tools:        tools,
					NameKey:      "function.name",
					ArgsKey:      "function.arguments",
					CallIDKey:    "id",
				})
				return p.Seq(
					p.Content(p.Until("<tool_call>")),
					p.Optional(p.Seq(p.Space(), toolCall)),
					p.End(),
				)
			})

			input := `Let me check.<tool_call>{"id": "call_123", "function": {"name": "get_current_weather", "arguments": {"location": "NYC"}}}</tool_call>`

			ctx := peg.NewParseContext(input, false)
			result := parser.Parse(ctx)

			Expect(result.Type).To(Equal(peg.Success))

			mapper := &peg.ChatPegMapper{}
			mapper.FromAST(&ctx.Ast, &result)
			msg := mapper.Result

			Expect(msg.ToolCalls).To(HaveLen(1))
			Expect(msg.ToolCalls[0].Name).To(Equal("get_current_weather"))
			Expect(msg.ToolCalls[0].ID).To(Equal("call_123"))
		})
	})

	Context("Command7 incremental", func() {
		It("handles incremental parsing without regressions", func() {
			parser := peg.BuildChatPegParser(func(p *peg.ChatBuilder) peg.ParserID {
				thinking := p.ReasoningBlock(p.Seq(
					p.Literal("<|START_THINKING|>"), p.Space(),
					p.Reasoning(p.Until("<|END_THINKING|>")), p.Space(),
					p.Literal("<|END_THINKING|>"),
				))

				toolCallID := p.Atomic(p.Seq(
					p.Literal("\"tool_call_id\""), p.Space(), p.Literal(":"), p.Space(),
					p.Literal("\""), p.ToolID(p.JSONString()), p.Literal("\""),
				))
				toolCallName := p.Atomic(p.Seq(
					p.Literal("\"tool_name\""), p.Space(), p.Literal(":"), p.Space(),
					p.Literal("\""), p.ToolName(p.JSONString()), p.Literal("\""),
				))
				toolCallArgs := p.Seq(
					p.Literal("\"parameters\""), p.Space(), p.Literal(":"), p.Space(),
					p.ToolArgs(p.JSON()),
				)

				toolCallFields := p.Rule("tool-call-fields", p.Choice(toolCallID, toolCallName, toolCallArgs))
				toolCall := p.Rule("tool-call-single", p.Tool(p.Seq(
					p.ToolOpen(p.Literal("{")), p.Space(),
					toolCallFields,
					p.ZeroOrMore(p.Seq(p.Literal(","), p.Space(), toolCallFields)),
					p.Space(), p.ToolClose(p.Literal("}")),
				)))

				toolCalls := p.Rule("tool-calls", p.Seq(
					p.Literal("<|START_ACTION|>"), p.Space(),
					p.Literal("["), p.Space(),
					toolCall,
					p.ZeroOrMore(p.Seq(p.Literal(","), p.Space(), toolCall)),
					p.Space(), p.Literal("]"), p.Space(),
					p.Literal("<|END_ACTION|>"),
				))

				return p.Seq(
					p.Optional(p.Seq(thinking, p.Space())),
					toolCalls,
					p.End(),
				)
			})

			reasoning := "To plan an effective trip to Japan that includes both historical sites and modern attractions within a " +
				"budget of $4000 for a two-week stay, we need to:\n\n" +
				"1. Identify key historical sites and modern attractions in Japan.\n" +
				"2. Find affordable accommodation options.\n" +
				"3. Determine the best modes of transportation.\n" +
				"4. Create a day-by-day itinerary.\n" +
				"5. Provide a detailed cost breakdown."

			input := "<|START_THINKING|>" + reasoning + "<|END_THINKING|>" +
				`<|START_ACTION|>[{"tool_call_id": "call_0", "tool_name": "plan_trip", "parameters": {"destination": "Japan", "duration": 14, "budget": 4000, "interests": ["historical sites", "modern attractions"]}}]<|END_ACTION|>`

			tokens := simpleTokenize(input)
			var accumulated string
			var prevToolCalls int

			for i, tok := range tokens {
				accumulated += tok
				isPartial := i < len(tokens)-1

				ctx := peg.NewParseContext(accumulated, isPartial)
				result := parser.Parse(ctx)

				Expect(result.Type).NotTo(Equal(peg.Fail), "parse failed at token %d, accumulated length=%d", i, len(accumulated))

				mapper := &peg.ChatPegMapper{}
				mapper.FromAST(&ctx.Ast, &result)

				Expect(len(mapper.Result.ToolCalls)).To(BeNumerically(">=", prevToolCalls),
					"tool call count decreased at token %d", i)
				prevToolCalls = len(mapper.Result.ToolCalls)
			}

			ctx := peg.NewParseContext(input, false)
			result := parser.Parse(ctx)
			Expect(result.Type).To(Equal(peg.Success))

			mapper := &peg.ChatPegMapper{}
			mapper.FromAST(&ctx.Ast, &result)
			msg := mapper.Result

			Expect(msg.ReasoningContent).To(Equal(reasoning))
			Expect(msg.ToolCalls).To(HaveLen(1))
			Expect(msg.ToolCalls[0].Name).To(Equal("plan_trip"))
			Expect(msg.ToolCalls[0].ID).To(Equal("call_0"))
		})
	})
})
