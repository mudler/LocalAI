package main

// Renderer specs for RenderGemma4 against the canonical gemma4 chat template
// (see the normative template comment in gemma4_renderer.go).
//
// Fixture provenance:
//   - "single user message" and "enable_thinking" are the EXACT expected
//     decodes from transformers tests/models/diffusion_gemma/
//     test_modeling_diffusion_gemma.py (test_diffusion_gemma_chat_template
//     and ..._with_thinking) with ONE difference: the transformers fixtures
//     start with "<bos>" because apply_chat_template tokenizes the rendered
//     text with add_bos. Our prompt goes through dllm_capi_generate, whose
//     run_generate already tokenizes with prepend_bos = vocab.add_bos
//     (dllm.cpp src/capi.cpp:230-231, true for gemma4), so the renderer must
//     NOT emit a literal <bos> (it would double) and every expected string
//     here drops that leading token.
//   - All other expected strings were produced by rendering the verbatim
//     GGUF template with jinja2 3.1.2 (bos_token="<bos>") and dropping the
//     leading "<bos>" for the same reason.

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// Two-function tools array used by the tool fixtures (OpenAI wire shape, as
// LocalAI passes it through PredictOptions.Tools).
const testToolsJSON = `[{"type":"function","function":{"name":"get_weather","description":"Get the current weather in a location.","parameters":{"type":"object","properties":{"location":{"type":"string","description":"The city name."},"unit":{"type":"string","enum":["celsius","fahrenheit"]}},"required":["location"]}}},{"type":"function","function":{"name":"get_time","description":"Get the current time in a timezone.","parameters":{"type":"object","properties":{"timezone":{"type":"string","description":"IANA timezone name."}},"required":["timezone"]}}}]`

// The <|tool>...<tool|> block the template renders for testToolsJSON inside
// the system turn (jinja2-verified).
const testToolsBlock = `<|tool>declaration:get_weather{description:<|"|>Get the current weather in a location.<|"|>,parameters:{properties:{location:{description:<|"|>The city name.<|"|>,type:<|"|>STRING<|"|>},unit:{enum:[<|"|>celsius<|"|>,<|"|>fahrenheit<|"|>],type:<|"|>STRING<|"|>}},required:[<|"|>location<|"|>],type:<|"|>OBJECT<|"|>}}<tool|><|tool>declaration:get_time{description:<|"|>Get the current time in a timezone.<|"|>,parameters:{properties:{timezone:{description:<|"|>IANA timezone name.<|"|>,type:<|"|>STRING<|"|>}},required:[<|"|>timezone<|"|>],type:<|"|>OBJECT<|"|>}}<tool|>`

// A single tool exercising the deep format_parameters branches: array items
// (string-typed and nested-array), nullable, enum+nullable, nested object
// properties/required, and a response declaration.
const complexToolsJSON = `[{"type":"function","function":{"name":"complex_tool","description":"A complex tool.","parameters":{"type":"object","properties":{"tags":{"type":"array","description":"Tags.","items":{"type":"string"}},"matrix":{"type":"array","items":{"type":"array","items":{"type":"number"}}},"opts":{"type":"object","description":"Options.","properties":{"depth":{"type":"integer","nullable":true}},"required":["depth"]},"mode":{"type":"string","enum":["a","b"],"nullable":true}},"required":["tags","opts"]},"response":{"description":"The result.","type":"object"}}}]`

// jinja2-verified render of complexToolsJSON. Notable template quirks pinned
// here: nested array items go through format_argument with ESCAPED keys and
// an un-uppercased type (<|"|>type<|"|>:<|"|>number<|"|>), while direct item
// types are uppercased; properties dictsort case-insensitively.
const complexToolsBlock = `<|tool>declaration:complex_tool{description:<|"|>A complex tool.<|"|>,parameters:{properties:{matrix:{items:{items:{<|"|>type<|"|>:<|"|>number<|"|>},type:<|"|>ARRAY<|"|>},type:<|"|>ARRAY<|"|>},mode:{enum:[<|"|>a<|"|>,<|"|>b<|"|>],nullable:true,type:<|"|>STRING<|"|>},opts:{description:<|"|>Options.<|"|>,properties:{depth:{nullable:true,type:<|"|>INTEGER<|"|>}},required:[<|"|>depth<|"|>],type:<|"|>OBJECT<|"|>},tags:{description:<|"|>Tags.<|"|>,items:{type:<|"|>STRING<|"|>},type:<|"|>ARRAY<|"|>}},required:[<|"|>tags<|"|>,<|"|>opts<|"|>],type:<|"|>OBJECT<|"|>},response:{description:<|"|>The result.<|"|>,type:<|"|>OBJECT<|"|>}}<tool|>`

type renderGemma4Case struct {
	msgs               []*pb.Message
	toolsJSON          string
	enableThinking     bool
	noGenerationPrompt bool // inverted so the zero value is the common case
	expected           string
}

var _ = Describe("RenderGemma4", func() {
	DescribeTable("renders the canonical gemma4 prompt",
		func(c renderGemma4Case) {
			out, err := RenderGemma4(c.msgs, c.toolsJSON, c.enableThinking, !c.noGenerationPrompt)
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(Equal(c.expected))
			// The C-ABI generate prepends BOS itself: a literal <bos>
			// anywhere in the rendered prompt would double-encode it.
			Expect(out).ToNot(ContainSubstring("<bos>"))
		},

		// transformers fixture (test_diffusion_gemma_chat_template), sans <bos>:
		// default thinking pre-opens an EMPTY thought channel in the
		// generation prompt.
		Entry("single user message, default (no thinking)", renderGemma4Case{
			msgs: []*pb.Message{
				{Role: "user", Content: "Write a long essay about Portugal."},
			},
			expected: "<|turn>user\nWrite a long essay about Portugal.<turn|>\n<|turn>model\n<|channel>thought\n<channel|>",
		}),

		// transformers fixture (test_diffusion_gemma_chat_template_with_thinking),
		// sans <bos>: a system turn carrying <|think|> and NO auto-opened
		// thought channel.
		Entry("enable_thinking=true", renderGemma4Case{
			msgs: []*pb.Message{
				{Role: "user", Content: "Write a long essay about Portugal."},
			},
			enableThinking: true,
			expected:       "<|turn>system\n<|think|>\n<turn|>\n<|turn>user\nWrite a long essay about Portugal.<turn|>\n<|turn>model\n",
		}),

		Entry("multi-turn user/assistant/user", renderGemma4Case{
			msgs: []*pb.Message{
				{Role: "user", Content: "Hello, who are you?"},
				{Role: "assistant", Content: "I am Gemma, a helpful assistant."},
				{Role: "user", Content: "Tell me a joke."},
			},
			expected: "<|turn>user\nHello, who are you?<turn|>\n<|turn>model\nI am Gemma, a helpful assistant.<turn|>\n<|turn>user\nTell me a joke.<turn|>\n<|turn>model\n<|channel>thought\n<channel|>",
		}),

		// tpl L178-L195: a leading system message is folded into the system
		// turn (trimmed) and consumed from the loop.
		Entry("system message folds into the system turn", renderGemma4Case{
			msgs: []*pb.Message{
				{Role: "system", Content: "You are a pirate."},
				{Role: "user", Content: "Hello!"},
			},
			expected: "<|turn>system\nYou are a pirate.<turn|>\n<|turn>user\nHello!<turn|>\n<|turn>model\n<|channel>thought\n<channel|>",
		}),

		// tpl L182-L185: <|think|> goes at the very top of the SAME system
		// turn, before the system prompt text.
		Entry("system message with enable_thinking shares the turn", renderGemma4Case{
			msgs: []*pb.Message{
				{Role: "system", Content: "You are a pirate."},
				{Role: "user", Content: "Hello!"},
			},
			enableThinking: true,
			expected:       "<|turn>system\n<|think|>\nYou are a pirate.<turn|>\n<|turn>user\nHello!<turn|>\n<|turn>model\n",
		}),

		// tpl L196-L203: tool declarations render in the system turn, one
		// <|tool>declaration:...<tool|> block per tool, no separators.
		Entry("tools array (two functions)", renderGemma4Case{
			msgs: []*pb.Message{
				{Role: "user", Content: "What is the weather in Tokyo?"},
			},
			toolsJSON: testToolsJSON,
			expected:  "<|turn>system\n" + testToolsBlock + "<turn|>\n<|turn>user\nWhat is the weather in Tokyo?<turn|>\n<|turn>model\n<|channel>thought\n<channel|>",
		}),

		// format_parameters deep branches (tpl L1-L85) + response declaration
		// (tpl L106-L116).
		Entry("complex tool schema (array items, nullable, nested object, response)", renderGemma4Case{
			msgs: []*pb.Message{
				{Role: "user", Content: "go"},
			},
			toolsJSON: complexToolsJSON,
			expected:  "<|turn>system\n" + complexToolsBlock + "<turn|>\n<|turn>user\ngo<turn|>\n<|turn>model\n<|channel>thought\n<channel|>",
		}),

		// tpl L243-L313: assistant tool_calls render as
		// <|tool_call>call:name{args}<tool_call|>; the following role=tool
		// message renders inline as <|tool_response>response:name{value:..}
		// <tool_response|>; the model turn stays OPEN (no <turn|>, no new
		// generation prompt) so the model continues after the response.
		Entry("assistant tool_calls + role=tool result", renderGemma4Case{
			msgs: []*pb.Message{
				{Role: "user", Content: "What is the weather in Tokyo?"},
				{Role: "assistant", Content: "", ToolCalls: `[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"Tokyo\",\"unit\":\"celsius\"}"}}]`},
				{Role: "tool", ToolCallId: "call_1", Content: "Sunny, 22 degrees celsius."},
			},
			toolsJSON: testToolsJSON,
			expected:  "<|turn>system\n" + testToolsBlock + "<turn|>\n<|turn>user\nWhat is the weather in Tokyo?<turn|>\n<|turn>model\n" + `<|tool_call>call:get_weather{location:<|"|>Tokyo<|"|>,unit:<|"|>celsius<|"|>}<tool_call|><|tool_response>response:get_weather{value:<|"|>Sunny, 22 degrees celsius.<|"|>}<tool_response|>`,
		}),

		// tpl L348-L349: a tool_calls turn with no rendered responses ends
		// on an OPEN <|tool_response> marker for the runtime to fill, and
		// add_generation_prompt adds nothing (tpl L357).
		Entry("assistant tool_calls without a result leaves <|tool_response> open", renderGemma4Case{
			msgs: []*pb.Message{
				{Role: "user", Content: "What is the weather in Tokyo?"},
				{Role: "assistant", Content: "", ToolCalls: `[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"Tokyo\",\"unit\":\"celsius\"}"}}]`},
			},
			toolsJSON: testToolsJSON,
			expected:  "<|turn>system\n" + testToolsBlock + "<turn|>\n<|turn>user\nWhat is the weather in Tokyo?<turn|>\n<|turn>model\n" + `<|tool_call>call:get_weather{location:<|"|>Tokyo<|"|>,unit:<|"|>celsius<|"|>}<tool_call|><|tool_response>`,
		}),

		// tpl L237-L241: reasoning_content renders as a thought channel only
		// on a tool-calling turn after the last user message.
		Entry("reasoning_content with tool_calls renders the thought channel", renderGemma4Case{
			msgs: []*pb.Message{
				{Role: "user", Content: "weather?"},
				{Role: "assistant", Content: "", ReasoningContent: "I should call the tool", ToolCalls: `[{"index":0,"id":"c1","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"Tokyo\"}"}}]`},
				{Role: "tool", ToolCallId: "c1", Content: "Sunny"},
			},
			expected: "<|turn>user\nweather?<turn|>\n<|turn>model\n<|channel>thought\nI should call the tool\n<channel|>" + `<|tool_call>call:get_weather{location:<|"|>Tokyo<|"|>}<tool_call|><|tool_response>response:get_weather{value:<|"|>Sunny<|"|>}<tool_response|>`,
		}),

		// tpl L220-L235: the assistant answer following its own tool round
		// continues the SAME model turn (no second <|turn>model).
		Entry("tool round then final assistant answer then user", renderGemma4Case{
			msgs: []*pb.Message{
				{Role: "user", Content: "weather?"},
				{Role: "assistant", Content: "", ToolCalls: `[{"index":0,"id":"c1","type":"function","function":{"name":"get_weather","arguments":"{\"location\":\"Tokyo\"}"}}]`},
				{Role: "tool", ToolCallId: "c1", Content: "Sunny"},
				{Role: "assistant", Content: "It is sunny."},
				{Role: "user", Content: "thanks"},
			},
			expected: "<|turn>user\nweather?<turn|>\n<|turn>model\n" + `<|tool_call>call:get_weather{location:<|"|>Tokyo<|"|>}<tool_call|><|tool_response>response:get_weather{value:<|"|>Sunny<|"|>}<tool_response|>` + "It is sunny.<turn|>\n<|turn>user\nthanks<turn|>\n<|turn>model\n<|channel>thought\n<channel|>",
		}),

		// format_argument (tpl L118-L147): numbers keep their JSON literal,
		// booleans lower-case, nested maps have unquoted dictsorted keys,
		// arrays bracketed; top-level args are dictsorted case-insensitively.
		Entry("tool_call argument types (number/bool/nested/array)", renderGemma4Case{
			msgs: []*pb.Message{
				{Role: "user", Content: "go"},
				{Role: "assistant", Content: "", ToolCalls: `[{"index":0,"id":"c1","type":"function","function":{"name":"f","arguments":"{\"count\":42,\"ratio\":3.5,\"flag\":true,\"off\":false,\"nested\":{\"x\":\"y\",\"n\":7},\"list\":[\"a\",1,true]}"}}]`},
			},
			expected: "<|turn>user\ngo<turn|>\n<|turn>model\n" + `<|tool_call>call:f{count:42,flag:true,list:[<|"|>a<|"|>,1,true],nested:{n:7,x:<|"|>y<|"|>},off:false,ratio:3.5}<tool_call|><|tool_response>`,
		}),

		// jinja dictsort is case-insensitive: alpha sorts before Beta.
		Entry("tool_call argument dictsort is case-insensitive", renderGemma4Case{
			msgs: []*pb.Message{
				{Role: "user", Content: "go"},
				{Role: "assistant", Content: "", ToolCalls: `[{"index":0,"id":"c1","type":"function","function":{"name":"f","arguments":"{\"Beta\":1,\"alpha\":2}"}}]`},
			},
			expected: "<|turn>user\ngo<turn|>\n<|turn>model\n<|tool_call>call:f{alpha:2,Beta:1}<tool_call|><|tool_response>",
		}),

		// jinja renders Python None as "None" (round-trips through vLLM's
		// parser, which lowers "none" back to null).
		Entry("tool_call null argument renders as None", renderGemma4Case{
			msgs: []*pb.Message{
				{Role: "user", Content: "go"},
				{Role: "assistant", Content: "", ToolCalls: `[{"index":0,"id":"c1","type":"function","function":{"name":"f","arguments":"{\"maybe\":null}"}}]`},
			},
			expected: "<|turn>user\ngo<turn|>\n<|turn>model\n<|tool_call>call:f{maybe:None}<tool_call|><|tool_response>",
		}),

		Entry("tool_call empty arguments render empty braces", renderGemma4Case{
			msgs: []*pb.Message{
				{Role: "user", Content: "go"},
				{Role: "assistant", Content: "", ToolCalls: `[{"index":0,"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]`},
			},
			expected: "<|turn>user\ngo<turn|>\n<|turn>model\n<|tool_call>call:f{}<tool_call|><|tool_response>",
		}),

		// tpl L253-L254: a non-object arguments string renders verbatim.
		Entry("tool_call non-object string arguments render verbatim", renderGemma4Case{
			msgs: []*pb.Message{
				{Role: "user", Content: "go"},
				{Role: "assistant", Content: "", ToolCalls: `[{"index":0,"id":"c1","type":"function","function":{"name":"f","arguments":"just text"}}]`},
			},
			expected: "<|turn>user\ngo<turn|>\n<|turn>model\n<|tool_call>call:f{just text}<tool_call|><|tool_response>",
		}),

		// tpl L278-L285: unmatched tool_call_id falls back to the tool
		// message's own name.
		Entry("tool result name falls back when tool_call_id does not match", renderGemma4Case{
			msgs: []*pb.Message{
				{Role: "user", Content: "go"},
				{Role: "assistant", Content: "", ToolCalls: `[{"index":0,"id":"c1","type":"function","function":{"name":"f","arguments":"{}"}}]`},
				{Role: "tool", ToolCallId: "OTHER", Name: "named_tool", Content: "out"},
			},
			expected: "<|turn>user\ngo<turn|>\n<|turn>model\n" + `<|tool_call>call:f{}<tool_call|><|tool_response>response:named_tool{value:<|"|>out<|"|>}<tool_response|>`,
		}),

		// strip_thinking (tpl L148-L158): historical assistant content loses
		// its <|channel>...<channel|> spans.
		Entry("assistant content thinking channels are stripped", renderGemma4Case{
			msgs: []*pb.Message{
				{Role: "user", Content: "hi"},
				{Role: "assistant", Content: "<|channel>thought\nsecret\n<channel|>visible answer"},
				{Role: "user", Content: "more"},
			},
			expected: "<|turn>user\nhi<turn|>\n<|turn>model\nvisible answer<turn|>\n<|turn>user\nmore<turn|>\n<|turn>model\n<|channel>thought\n<channel|>",
		}),

		// tpl L220-L235: consecutive assistant messages suppress the second
		// <|turn>model (continuation), but each still closes with <turn|>.
		Entry("consecutive assistant messages continue the model turn", renderGemma4Case{
			msgs: []*pb.Message{
				{Role: "user", Content: "hi"},
				{Role: "assistant", Content: "part one"},
				{Role: "assistant", Content: "part two"},
				{Role: "user", Content: "ok"},
			},
			expected: "<|turn>user\nhi<turn|>\n<|turn>model\npart one<turn|>\npart two<turn|>\n<|turn>user\nok<turn|>\n<|turn>model\n<|channel>thought\n<channel|>",
		}),

		Entry("add_generation_prompt=false renders no model turn", renderGemma4Case{
			msgs: []*pb.Message{
				{Role: "user", Content: "hi"},
			},
			noGenerationPrompt: true,
			expected:           "<|turn>user\nhi<turn|>\n",
		}),
	)

	Describe("error handling", func() {
		It("fails loud on an unknown role", func() {
			_, err := RenderGemma4([]*pb.Message{
				{Role: "narrator", Content: "Meanwhile..."},
			}, "", false, true)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(`unknown role "narrator"`))
		})

		It("fails on invalid tools JSON", func() {
			_, err := RenderGemma4([]*pb.Message{
				{Role: "user", Content: "hi"},
			}, "{not json", false, true)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("tools JSON"))
		})

		It("fails on invalid tool_calls JSON", func() {
			_, err := RenderGemma4([]*pb.Message{
				{Role: "user", Content: "hi"},
				{Role: "assistant", Content: "", ToolCalls: "{not json"},
			}, "", false, true)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("tool_calls JSON"))
		})

		It("fails on an orphan tool message, naming its index", func() {
			// A role:tool message with no preceding assistant tool_calls turn
			// would be silently dropped by the jinja; we fail loud instead.
			_, err := RenderGemma4([]*pb.Message{
				{Role: "user", Content: "hi"},
				{Role: "tool", Content: `{"temp": 20}`, ToolCallId: "call_1"},
			}, "", false, true)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("orphan tool message 1"))
		})

		It("fails on trailing garbage after the tools JSON array", func() {
			_, err := RenderGemma4([]*pb.Message{
				{Role: "user", Content: "hi"},
			}, "[] junk", false, true)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("tools JSON"))
		})

		It("fails when the tools JSON is not an array", func() {
			_, err := RenderGemma4([]*pb.Message{
				{Role: "user", Content: "hi"},
			}, `{"type":"function"}`, false, true)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("tools JSON is not an array"))
		})

		It("fails when a tools array element is not an object", func() {
			_, err := RenderGemma4([]*pb.Message{
				{Role: "user", Content: "hi"},
			}, `[42]`, false, true)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("tools[0] is not an object"))
		})

		It("rejects a nil message via the unknown-role check", func() {
			// Pins current behavior: pb getters are nil-safe, so a nil message
			// reads as role "" and trips the fail-loud unknown-role guard.
			_, err := RenderGemma4([]*pb.Message{nil}, "", false, true)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(`unknown role "" in message 0`))
		})
	})
})
