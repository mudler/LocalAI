package main

// Parser specs for Gemma4Parser (model output text -> pb.ChatDelta events).
//
// Fixture provenance:
//   - Entries marked "vLLM: <name>" are direct ports of the named test from
//     vLLM PR #45163, tests/tool_parsers/test_gemma4_tool_parser.py (the
//     authoritative test-suite for the gemma4 tool-call wire format). The
//     streaming tests' chunk lists are reused verbatim as Feed fragments.
//   - Decoder entries port the TestParseGemma4Args / TestParseGemma4Array
//     classes from the same file (non-partial mode only; this parser never
//     decodes partial payloads, see the divergence note in gemma4_parser.go).
//   - Channel/turn-marker expectations come from the chat template embedded
//     in gemma4_renderer.go (tpl L356-L362 generation prompt, L148-L158
//     strip_thinking) and vLLM's Gemma4ReasoningParser
//     (vllm/reasoning/gemma4_reasoning_parser.py).

import (
	"encoding/json"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// flatGemma4Tool is one accumulated tool call, mirroring how LocalAI core
// folds ToolCallDelta streams (pkg/functions/chat_deltas.go
// ToolCallsFromChatDeltas: name/id latch on first non-empty, arguments
// concatenate per index). Tests flatten through the same rules so they
// assert exactly what core will reconstruct.
type flatGemma4Tool struct {
	id   string
	name string
	args string
}

func flattenGemma4Deltas(deltas []*pb.ChatDelta) (string, string, []flatGemma4Tool) {
	var content, reasoning strings.Builder
	byIndex := map[int32]*flatGemma4Tool{}
	maxIdx := int32(-1)
	for _, d := range deltas {
		content.WriteString(d.GetContent())
		reasoning.WriteString(d.GetReasoningContent())
		for _, tc := range d.GetToolCalls() {
			acc, ok := byIndex[tc.GetIndex()]
			if !ok {
				acc = &flatGemma4Tool{}
				byIndex[tc.GetIndex()] = acc
			}
			if tc.GetName() != "" {
				acc.name = tc.GetName()
			}
			if tc.GetId() != "" {
				acc.id = tc.GetId()
			}
			acc.args += tc.GetArguments()
			if tc.GetIndex() > maxIdx {
				maxIdx = tc.GetIndex()
			}
		}
	}
	var tools []flatGemma4Tool
	for i := int32(0); i <= maxIdx; i++ {
		if acc, ok := byIndex[i]; ok {
			tools = append(tools, *acc)
		}
	}
	return content.String(), reasoning.String(), tools
}

type wantGemma4Tool struct {
	name     string
	argsJSON string // compared with MatchJSON (key order irrelevant)
}

type parseGemma4Case struct {
	startInThought bool
	fragments      []string
	wantContent    string
	wantReasoning  string
	wantTools      []wantGemma4Tool
}

func parseGemma4Fragments(startInThought bool, fragments []string) []*pb.ChatDelta {
	p := NewGemma4Parser(startInThought)
	var all []*pb.ChatDelta
	for _, f := range fragments {
		all = append(all, p.Feed(f)...)
	}
	return append(all, p.Close()...)
}

var _ = Describe("Gemma4Parser", func() {
	DescribeTable("parses streamed gemma4 output into ChatDeltas",
		func(c parseGemma4Case) {
			content, reasoning, tools := flattenGemma4Deltas(parseGemma4Fragments(c.startInThought, c.fragments))
			Expect(content).To(Equal(c.wantContent))
			Expect(reasoning).To(Equal(c.wantReasoning))
			Expect(tools).To(HaveLen(len(c.wantTools)))
			seenIDs := map[string]bool{}
			for i, want := range c.wantTools {
				Expect(tools[i].name).To(Equal(want.name), "tool %d name", i)
				Expect(tools[i].args).To(MatchJSON(want.argsJSON), "tool %d arguments", i)
				Expect(tools[i].id).ToNot(BeEmpty(), "tool %d id", i)
				Expect(seenIDs).ToNot(HaveKey(tools[i].id), "tool %d id must be unique", i)
				seenIDs[tools[i].id] = true
			}
		},

		// --- (1) pure content -------------------------------------------------
		// vLLM: test_no_tool_calls
		Entry("pure content, single fragment", parseGemma4Case{
			fragments:   []string{"Hello, how can I help you today?"},
			wantContent: "Hello, how can I help you today?",
		}),

		// --- (2) thought -> final transition ----------------------------------
		// enable_thinking render: prompt ends at <|turn>model\n and the model
		// opens/closes its own thought channel in the OUTPUT (vLLM
		// Gemma4ReasoningParser docstring; tpl L356-L362). The "thought\n"
		// role label after <|channel> is structural and must be stripped
		// (vLLM _THOUGHT_PREFIX handling).
		Entry("thought channel then final content", parseGemma4Case{
			fragments:     []string{"<|channel>thought\nLet me think about this.\n<channel|>The answer is 42."},
			wantReasoning: "Let me think about this.\n",
			wantContent:   "The answer is 42.",
		}),

		// --- (3) startInThought both ways -------------------------------------
		Entry("startInThought=true routes initial text to reasoning until <channel|>", parseGemma4Case{
			startInThought: true,
			fragments:      []string{"I am thinking hard.<channel|>Done."},
			wantReasoning:  "I am thinking hard.",
			wantContent:    "Done.",
		}),
		// A stray <channel|> with no open channel is swallowed, matching the
		// template's strip_thinking (tpl L148-L158: the marker is dropped,
		// text on both sides is kept).
		Entry("startInThought=false keeps the same text as content, stray <channel|> swallowed", parseGemma4Case{
			startInThought: false,
			fragments:      []string{"I am thinking hard.<channel|>Done."},
			wantContent:    "I am thinking hard.Done.",
		}),

		// --- (4) one tool call, full payload type zoo --------------------------
		Entry("single tool call: strings, numbers, bools, null, nested object and array", parseGemma4Case{
			fragments: []string{`<|tool_call>call:complex_function{text:<|"|>with, comma and {braces}<|"|>,count:42,score:3.14,yes:true,no:false,nothing:null,obj:{inner:<|"|>v<|"|>,k:1},arr:[<|"|>a<|"|>,2,true]}<tool_call|>`},
			wantTools: []wantGemma4Tool{{
				name:     "complex_function",
				argsJSON: `{"text":"with, comma and {braces}","count":42,"score":3.14,"yes":true,"no":false,"nothing":null,"obj":{"inner":"v","k":1},"arr":["a",2,true]}`,
			}},
		}),

		// --- (5) payload split across 3 fragments ------------------------------
		Entry("tool-call payload split across three fragments", parseGemma4Case{
			fragments: []string{
				"<|tool_call>call:get_weather{loc",
				`ation:<|"|>Paris, Fra`,
				`nce<|"|>}<tool_call|>`,
			},
			wantTools: []wantGemma4Tool{{name: "get_weather", argsJSON: `{"location":"Paris, France"}`}},
		}),

		// --- (6) marker split across fragments ----------------------------------
		Entry("tool-call open marker split across fragments", parseGemma4Case{
			fragments: []string{
				"<|tool_ca",
				`ll>call:get_weather{location:<|"|>London<|"|>}<tool_call|>`,
			},
			wantTools: []wantGemma4Tool{{name: "get_weather", argsJSON: `{"location":"London"}`}},
		}),
		Entry("channel open marker split across fragments", parseGemma4Case{
			fragments: []string{
				"<|chan",
				"nel>thought\ndeep thought<channel|>final",
			},
			wantReasoning: "deep thought",
			wantContent:   "final",
		}),

		// --- (7) trailing partial marker held, flushed by Close -----------------
		Entry("trailing partial marker is held back and flushed by Close", parseGemma4Case{
			fragments:   []string{"Hello <|tool"},
			wantContent: "Hello <|tool",
		}),

		// --- (8) malformed/incomplete payload -> content fallback ---------------
		// vLLM: test_incomplete_tool_call (no end marker: the whole text stays
		// content, never silently dropped).
		Entry("incomplete tool payload at Close is emitted as raw content", parseGemma4Case{
			fragments:   []string{`<|tool_call>call:get_weather{location:<|"|>London`},
			wantContent: `<|tool_call>call:get_weather{location:<|"|>London`,
		}),
		Entry("malformed complete payload is emitted as raw content, parsing continues", parseGemma4Case{
			fragments:   []string{"<|tool_call>oops no call syntax<tool_call|> done"},
			wantContent: "<|tool_call>oops no call syntax<tool_call|> done",
		}),

		// --- (9) <turn|> ends the turn -------------------------------------------
		Entry("text after <turn|> is ignored, including later fragments", parseGemma4Case{
			fragments: []string{
				"before<turn|>after",
				`more <|tool_call>call:f{}<tool_call|>`,
			},
			wantContent: "before",
		}),
		Entry("<turn|> inside a thought channel ends the turn", parseGemma4Case{
			startInThought: true,
			fragments:      []string{"thinking<turn|>ignored"},
			wantReasoning:  "thinking",
		}),

		// --- (10) ported vLLM non-streaming cases ---------------------------------
		// vLLM: test_single_tool_call
		Entry("vLLM: test_single_tool_call", parseGemma4Case{
			fragments: []string{`<|tool_call>call:get_weather{location:<|"|>London<|"|>}<tool_call|>`},
			wantTools: []wantGemma4Tool{{name: "get_weather", argsJSON: `{"location":"London"}`}},
		}),
		// vLLM: test_multiple_arguments
		Entry("vLLM: test_multiple_arguments", parseGemma4Case{
			fragments: []string{`<|tool_call>call:get_weather{location:<|"|>San Francisco<|"|>,unit:<|"|>celsius<|"|>}<tool_call|>`},
			wantTools: []wantGemma4Tool{{name: "get_weather", argsJSON: `{"location":"San Francisco","unit":"celsius"}`}},
		}),
		// vLLM: test_text_before_tool_call. DIVERGENCE: vLLM's non-streaming
		// extractor trims the content ("...you."); a streaming parser cannot
		// retroactively trim already-emitted text, so the trailing space is
		// kept (vLLM's own streaming path keeps it too, see
		// test_streaming_text_before_tool_call which only checks a prefix).
		Entry("vLLM: test_text_before_tool_call (streaming semantics: no trim)", parseGemma4Case{
			fragments:   []string{`Let me check the weather for you. <|tool_call>call:get_weather{location:<|"|>Paris<|"|>}<tool_call|>`},
			wantContent: "Let me check the weather for you. ",
			wantTools:   []wantGemma4Tool{{name: "get_weather", argsJSON: `{"location":"Paris"}`}},
		}),
		// vLLM: test_multiple_tool_calls (also covers case 11: multi-tool sequence)
		Entry("vLLM: test_multiple_tool_calls", parseGemma4Case{
			fragments: []string{`<|tool_call>call:get_weather{location:<|"|>London<|"|>}<tool_call|><|tool_call>call:get_time{location:<|"|>London<|"|>}<tool_call|>`},
			wantTools: []wantGemma4Tool{
				{name: "get_weather", argsJSON: `{"location":"London"}`},
				{name: "get_time", argsJSON: `{"location":"London"}`},
			},
		}),
		// vLLM: test_nested_arguments
		Entry("vLLM: test_nested_arguments", parseGemma4Case{
			fragments: []string{`<|tool_call>call:complex_function{nested:{inner:<|"|>value<|"|>},list:[<|"|>a<|"|>,<|"|>b<|"|>]}<tool_call|>`},
			wantTools: []wantGemma4Tool{{name: "complex_function", argsJSON: `{"nested":{"inner":"value"},"list":["a","b"]}`}},
		}),
		// vLLM: test_tool_call_with_number_and_boolean
		Entry("vLLM: test_tool_call_with_number_and_boolean", parseGemma4Case{
			fragments: []string{`<|tool_call>call:set_status{is_active:true,count:42,score:3.14}<tool_call|>`},
			wantTools: []wantGemma4Tool{{name: "set_status", argsJSON: `{"is_active":true,"count":42,"score":3.14}`}},
		}),
		// vLLM: test_hyphenated_function_name
		Entry("vLLM: test_hyphenated_function_name", parseGemma4Case{
			fragments: []string{`<|tool_call>call:get-weather{location:<|"|>London<|"|>}<tool_call|>`},
			wantTools: []wantGemma4Tool{{name: "get-weather", argsJSON: `{"location":"London"}`}},
		}),
		// vLLM: test_dotted_function_name
		Entry("vLLM: test_dotted_function_name", parseGemma4Case{
			fragments: []string{`<|tool_call>call:weather.get{location:<|"|>London<|"|>}<tool_call|>`},
			wantTools: []wantGemma4Tool{{name: "weather.get", argsJSON: `{"location":"London"}`}},
		}),
		// vLLM: test_no_arguments
		Entry("vLLM: test_no_arguments", parseGemma4Case{
			fragments: []string{"<|tool_call>call:get_status{}<tool_call|>"},
			wantTools: []wantGemma4Tool{{name: "get_status", argsJSON: `{}`}},
		}),

		// --- ported vLLM streaming cases (chunk lists reused as fragments) --------
		// vLLM: test_basic_streaming_single_tool
		Entry("vLLM: test_basic_streaming_single_tool", parseGemma4Case{
			fragments: []string{
				"<|tool_call>",
				"call:get_weather{",
				`location:<|"|>Paris`,
				", France",
				`<|"|>}`,
				"<tool_call|>",
			},
			wantTools: []wantGemma4Tool{{name: "get_weather", argsJSON: `{"location":"Paris, France"}`}},
		}),
		// vLLM: test_streaming_multi_arg
		Entry("vLLM: test_streaming_multi_arg", parseGemma4Case{
			fragments: []string{
				"<|tool_call>",
				"call:get_weather{",
				`location:<|"|>Tokyo<|"|>,`,
				`unit:<|"|>celsius<|"|>}`,
				"<tool_call|>",
			},
			wantTools: []wantGemma4Tool{{name: "get_weather", argsJSON: `{"location":"Tokyo","unit":"celsius"}`}},
		}),
		// vLLM: test_streaming_text_before_tool_call
		Entry("vLLM: test_streaming_text_before_tool_call", parseGemma4Case{
			fragments: []string{
				"Let me check ",
				"the weather. ",
				"<|tool_call>",
				"call:get_weather{",
				`location:<|"|>London<|"|>}`,
				"<tool_call|>",
			},
			wantContent: "Let me check the weather. ",
			wantTools:   []wantGemma4Tool{{name: "get_weather", argsJSON: `{"location":"London"}`}},
		}),
		// vLLM: test_streaming_numeric_args
		Entry("vLLM: test_streaming_numeric_args", parseGemma4Case{
			fragments: []string{
				"<|tool_call>",
				"call:set_config{",
				"count:42,",
				"active:true}",
				"<tool_call|>",
			},
			wantTools: []wantGemma4Tool{{name: "set_config", argsJSON: `{"count":42,"active":true}`}},
		}),
		// vLLM: test_streaming_boolean_split_across_chunks
		Entry("vLLM: test_streaming_boolean_split_across_chunks", parseGemma4Case{
			fragments: []string{
				"<|tool_call>",
				"call:search{input:{all:tru",
				"e}}",
				"<tool_call|>",
			},
			wantTools: []wantGemma4Tool{{name: "search", argsJSON: `{"input":{"all":true}}`}},
		}),
		// vLLM: test_streaming_false_split_across_chunks
		Entry("vLLM: test_streaming_false_split_across_chunks", parseGemma4Case{
			fragments: []string{
				"<|tool_call>",
				"call:set{flag:fals",
				"e}",
				"<tool_call|>",
			},
			wantTools: []wantGemma4Tool{{name: "set", argsJSON: `{"flag":false}`}},
		}),
		// vLLM: test_streaming_number_split_across_chunks
		Entry("vLLM: test_streaming_number_split_across_chunks", parseGemma4Case{
			fragments: []string{
				"<|tool_call>",
				"call:set{count:4",
				"2}",
				"<tool_call|>",
			},
			wantTools: []wantGemma4Tool{{name: "set", argsJSON: `{"count":42}`}},
		}),
		// vLLM: test_streaming_empty_args
		Entry("vLLM: test_streaming_empty_args", parseGemma4Case{
			fragments: []string{
				"<|tool_call>",
				"call:get_status{}",
				"<tool_call|>",
			},
			wantTools: []wantGemma4Tool{{name: "get_status", argsJSON: `{}`}},
		}),
		// vLLM: test_streaming_split_delimiter_no_invalid_json (string
		// delimiter <|"|> split across fragments must not leak fragments).
		Entry("vLLM: test_streaming_split_delimiter_no_invalid_json", parseGemma4Case{
			fragments: []string{
				"<|tool_call>",
				"call:todowrite{",
				`content:<|"|>Buy milk<|`,
				`"|>}`,
				"<tool_call|>",
			},
			wantTools: []wantGemma4Tool{{name: "todowrite", argsJSON: `{"content":"Buy milk"}`}},
		}),
		// vLLM: test_streaming_does_not_duplicate_plain_text_after_tool_call
		Entry("vLLM: test_streaming_does_not_duplicate_plain_text_after_tool_call", parseGemma4Case{
			fragments: []string{
				"<|tool_call>",
				"call:get_weather{",
				`location:<|"|>Paris<|"|>}`,
				"<tool_call|><",
				"div>",
			},
			wantContent: "<div>",
			wantTools:   []wantGemma4Tool{{name: "get_weather", argsJSON: `{"location":"Paris"}`}},
		}),
		// vLLM: test_streaming_html_argument_does_not_duplicate_tag_prefixes
		Entry("vLLM: test_streaming_html_argument_does_not_duplicate_tag_prefixes", parseGemma4Case{
			fragments: []string{
				"<|tool_call>",
				"call:write_file{",
				`path:<|"|>index.html<|"|>,`,
				`content:<|"|><!DOCTYPE html>` + "\n<",
				`html lang="zh-CN">` + "\n<",
				"head>\n    <",
				`meta charset="UTF-8">` + "\n    <",
				`meta name="viewport" content="width=device-width">` + "\n",
				`<|"|>}`,
				"<tool_call|>",
			},
			wantTools: []wantGemma4Tool{{
				name:     "write_file",
				argsJSON: `{"path":"index.html","content":"<!DOCTYPE html>\n<html lang=\"zh-CN\">\n<head>\n    <meta charset=\"UTF-8\">\n    <meta name=\"viewport\" content=\"width=device-width\">\n"}`,
			}},
		}),
		// vLLM: test_streaming_single_chunk_complete_tool_call
		Entry("vLLM: test_streaming_single_chunk_complete_tool_call", parseGemma4Case{
			fragments: []string{`<|tool_call>call:name_a_color{color_hex:<|"|>00ff11<|"|>}<tool_call|>`},
			wantTools: []wantGemma4Tool{{name: "name_a_color", argsJSON: `{"color_hex":"00ff11"}`}},
		}),
		// vLLM: test_streaming_multi_chunk_batched_tool_calls (two complete
		// calls in ONE fragment; both must come out with distinct indices)
		Entry("vLLM: test_streaming_multi_chunk_batched_tool_calls", parseGemma4Case{
			fragments: []string{
				`<|tool_call>call:get_weather{location:<|"|>London<|"|>}<tool_call|>` +
					`<|tool_call>call:get_time{timezone:<|"|>GMT<|"|>}<tool_call|>`,
			},
			wantTools: []wantGemma4Tool{
				{name: "get_weather", argsJSON: `{"location":"London"}`},
				{name: "get_time", argsJSON: `{"timezone":"GMT"}`},
			},
		}),
		// vLLM: test_streaming_trailing_bare_bool_not_duplicated
		Entry("vLLM: test_streaming_trailing_bare_bool_not_duplicated", parseGemma4Case{
			fragments: []string{
				"<|tool_call>",
				"call:Edit{",
				`file_path:<|"|>src/env.py<|"|>,`,
				`old_string:<|"|>old_val<|"|>,`,
				`new_string:<|"|>new_val<|"|>,`,
				"replace_all:",
				"false}",
				"<tool_call|>",
			},
			wantTools: []wantGemma4Tool{{
				name:     "Edit",
				argsJSON: `{"file_path":"src/env.py","old_string":"old_val","new_string":"new_val","replace_all":false}`,
			}},
		}),

		// --- implicit reasoning end on <|tool_call> (vLLM is_reasoning_end:
		// a tool_call token means reasoning is over) -----------------------------
		Entry("tool call inside an open thought channel ends the reasoning", parseGemma4Case{
			startInThought: true,
			fragments:      []string{`need the weather<|tool_call>call:get_weather{location:<|"|>Rome<|"|>}<tool_call|>`},
			wantReasoning:  "need the weather",
			wantTools:      []wantGemma4Tool{{name: "get_weather", argsJSON: `{"location":"Rome"}`}},
		}),

		// --- (12) empty fragments are no-ops --------------------------------------
		Entry("empty fragments are no-ops", parseGemma4Case{
			fragments:   []string{"", "Hello", "", "", " world", ""},
			wantContent: "Hello world",
		}),
	)

	It("returns no deltas for an empty fragment and after Close", func() {
		p := NewGemma4Parser(false)
		Expect(p.Feed("")).To(BeEmpty())
		Expect(p.Feed("hi")).ToNot(BeEmpty())
		Expect(p.Close()).To(BeEmpty()) // nothing held back
		// The parser is finished after Close: further input is dropped.
		Expect(p.Feed("more")).To(BeEmpty())
		Expect(p.Close()).To(BeEmpty())
	})

	It("generates index-based tool call ids (call_<index>)", func() {
		// Mirrors the index-based id convention of pkg/grpc/rich_test.go and
		// keeps ids deterministic for the split-invariance property below.
		deltas := parseGemma4Fragments(false, []string{
			`<|tool_call>call:a{}<tool_call|><|tool_call>call:b{}<tool_call|>`,
		})
		_, _, tools := flattenGemma4Deltas(deltas)
		Expect(tools).To(HaveLen(2))
		Expect(tools[0].id).To(Equal("call_0"))
		Expect(tools[1].id).To(Equal("call_1"))
	})

	// Property: for a fixed full output, EVERY 2-split position must yield
	// exactly the same flattened result as the unsplit parse. This kills
	// fragment-boundary bugs (mid-marker, mid-delimiter, mid-payload splits).
	DescribeTable("2-split fragment invariance",
		func(startInThought bool, full string) {
			refContent, refReasoning, refTools := flattenGemma4Deltas(
				parseGemma4Fragments(startInThought, []string{full}))
			for i := 0; i <= len(full); i++ {
				content, reasoning, tools := flattenGemma4Deltas(
					parseGemma4Fragments(startInThought, []string{full[:i], full[i:]}))
				Expect(content).To(Equal(refContent), fmt.Sprintf("content diverged at split %d", i))
				Expect(reasoning).To(Equal(refReasoning), fmt.Sprintf("reasoning diverged at split %d", i))
				Expect(tools).To(Equal(refTools), fmt.Sprintf("tool calls diverged at split %d", i))
			}
		},
		Entry("thought + content + two tool calls + turn end", false,
			"<|channel>thought\nPondering the request...\n<channel|>Sure - calling tools now. "+
				`<|tool_call>call:get_weather{location:<|"|>Paris, France<|"|>,unit:<|"|>celsius<|"|>,days:3,detailed:true}<tool_call|>`+
				`<|tool_call>call:get_time{timezone:<|"|>Europe/Lisbon<|"|>,nested:{flag:false,vals:[1,2.5,<|"|>x<|"|>]}}<tool_call|>`+
				"Done.<turn|>ignored tail"),
		Entry("startInThought + tool call + trailing partial marker", true,
			`Deep thought<channel|>final answer <|tool_call>call:noop{}<tool_call|> trailing <|tool`),
		Entry("malformed payload fallback", false,
			`pre <|tool_call>not a call<tool_call|> post`),
	)
})

// Decoder-level ports of vLLM's TestParseGemma4Args / TestParseGemma4Array
// (non-partial mode; the partial-withholding tests do not apply because this
// parser only ever decodes COMPLETE payloads, see gemma4_parser.go).
var _ = Describe("decodeGemma4Args", func() {
	DescribeTable("decodes the gemma4 call syntax into JSON arguments",
		func(in, wantJSON string) {
			Expect(decodeGemma4Args(in, 0)).To(MatchJSON(wantJSON))
		},
		// vLLM: test_empty_string / test_whitespace_only
		Entry("empty string", "", `{}`),
		Entry("whitespace only", "   ", `{}`),
		// vLLM: test_single_string_value
		Entry("single string value", `location:<|"|>Paris<|"|>`, `{"location":"Paris"}`),
		// vLLM: test_string_value_with_comma
		Entry("string value with comma", `location:<|"|>Paris, France<|"|>`, `{"location":"Paris, France"}`),
		// vLLM: test_multiple_string_values
		Entry("multiple string values", `location:<|"|>San Francisco<|"|>,unit:<|"|>celsius<|"|>`, `{"location":"San Francisco","unit":"celsius"}`),
		// vLLM: test_integer_value / test_float_value
		Entry("integer value", "count:42", `{"count":42}`),
		Entry("float value", "score:3.14", `{"score":3.14}`),
		// vLLM: test_boolean_true / test_boolean_false
		Entry("boolean true", "flag:true", `{"flag":true}`),
		Entry("boolean false", "flag:false", `{"flag":false}`),
		// vLLM: test_null_value (bare null must become JSON null, not "null")
		Entry("null value", "param:null", `{"param":null}`),
		// vLLM: test_mixed_types
		Entry("mixed types", `name:<|"|>test<|"|>,count:42,active:true,score:3.14`,
			`{"name":"test","count":42,"active":true,"score":3.14}`),
		// vLLM: test_nested_object
		Entry("nested object", `nested:{inner:<|"|>value<|"|>}`, `{"nested":{"inner":"value"}}`),
		// vLLM: test_array_of_strings
		Entry("array of strings", `items:[<|"|>a<|"|>,<|"|>b<|"|>]`, `{"items":["a","b"]}`),
		// vLLM: test_unterminated_string (take everything after the delimiter)
		Entry("unterminated string", `key:<|"|>unterminated`, `{"key":"unterminated"}`),
		// vLLM: test_empty_value (key with no value after colon)
		Entry("empty value", "key:", `{"key":""}`),
		// vLLM: test_trailing_dot_float_partial_withheld, non-partial branch
		// (trailing-dot floats parse normally outside streaming).
		Entry("trailing dot float, complete payload", "left:108.,right:22.8", `{"left":108.0,"right":22.8}`),
	)

	It("terminates and yields valid JSON on malformed input", func() {
		// vLLM: test_malformed_partial_array (the assertion there is only
		// "returns a dict without hanging"; ours is "valid JSON object").
		out := decodeGemma4Args(":[t:[]", 0)
		var v map[string]any
		Expect(json.Unmarshal([]byte(out), &v)).To(Succeed())
	})

	It("degrades nesting beyond the recursion cap to a string value", func() {
		// 200 levels of a:{a:{...a:1...}}. Without the depth cap the mutual
		// recursion would grow the stack with the model's output; a Go stack
		// overflow is a fatal process kill, so levels past gemma4MaxArgsDepth
		// must gracefully fall back to the raw inner text as a JSON string.
		const depth = 200
		body := strings.Repeat("a:{", depth-1) + "a:1" + strings.Repeat("}", depth-1)
		out := decodeGemma4Args(body, 0)
		var v map[string]any
		Expect(json.Unmarshal([]byte(out), &v)).To(Succeed())
		levels := 0
		var cur any = v
		for {
			m, ok := cur.(map[string]any)
			if !ok {
				break
			}
			Expect(m).To(HaveKey("a"))
			cur = m["a"]
			levels++
		}
		Expect(levels).To(Equal(gemma4MaxArgsDepth + 1))
		Expect(cur).To(BeAssignableToTypeOf(""))
		Expect(cur).To(ContainSubstring("a:{"))
	})
})

var _ = Describe("decodeGemma4Array", func() {
	DescribeTable("decodes gemma4 array bodies into JSON arrays",
		func(in, wantJSON string) {
			Expect(decodeGemma4Array(in, 0)).To(MatchJSON(wantJSON))
		},
		// vLLM: test_string_array / test_empty_array / test_bare_values
		Entry("string array", `<|"|>a<|"|>,<|"|>b<|"|>`, `["a","b"]`),
		Entry("empty array", "", `[]`),
		Entry("bare values", "42,true,3.14", `[42,true,3.14]`),
		// vLLM: test_string_element_with_closing_bracket (a ']' inside a
		// delimited string must not close the array)
		Entry("string element with closing bracket", `[<|"|>a]b<|"|>,<|"|>c<|"|>],<|"|>tail<|"|>`, `[["a]b","c"],"tail"]`),
		// vLLM: test_stray_closing_bracket (no-progress abort, keep prefix)
		Entry("stray closing bracket", "42,]trailing", `[42]`),
	)
})
