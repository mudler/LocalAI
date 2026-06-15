package functions_test

import (
	. "github.com/mudler/LocalAI/pkg/functions"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// LFM2 / LFM2.5 emit tool calls in a Pythonic syntax wrapped in special tokens:
//
//	<|tool_call_start|>[func_name(arg1="value1", arg2="value2")]<|tool_call_end|>
//
// See backend/cpp/llama-cpp/llama.cpp/common/chat.cpp:1277 (common_chat_params_init_lfm2)
// and https://docs.liquid.ai/lfm/key-concepts/tool-use. The format is auto-detected
// by upstream llama.cpp when the chat template contains <|tool_list_start|>/<|tool_list_end|>.
//
// The tests below pin the LocalAI-side parser config (response_regex + argument_regex)
// that the lfm gallery template ships, so configurations relying on the gRPC backend
// returning raw text (rather than pre-parsed tool_calls via use_jinja) still work.
var _ = Describe("LFM2 Pythonic tool-call parsing", func() {
	// Matches the markers exactly; non-greedy `arguments` so the closing `)]` of one
	// call doesn't swallow trailing content that happens to share characters.
	const lfm2ResponseRegex = `<\|tool_call_start\|>\[(?P<name>\w+)\((?P<arguments>.*?)\)\]<\|tool_call_end\|>`

	// Two argument extractors: quoted strings and bare scalars (numbers / true / false / null).
	// ParseFunctionCallArgs runs every regex in order, so later matches with the same key
	// would overwrite earlier ones — which is fine here because the patterns are disjoint.
	var lfm2ArgRegex = []string{
		`(?P<key>\w+)\s*=\s*"(?P<value>[^"]*)"`,
		`(?P<key>\w+)\s*=\s*(?P<value>-?\d+(?:\.\d+)?|true|false|null)`,
	}

	cfg := func() FunctionsConfig {
		return FunctionsConfig{
			ResponseRegex:      []string{lfm2ResponseRegex},
			ArgumentRegex:      lfm2ArgRegex,
			ArgumentRegexKey:   "key",
			ArgumentRegexValue: "value",
		}
	}

	It("parses a single string-arg call", func() {
		input := `<|tool_call_start|>[get_weather(city="Berlin")]<|tool_call_end|>`
		results := ParseFunctionCall(input, cfg())
		Expect(results).To(HaveLen(1))
		Expect(results[0].Name).To(Equal("get_weather"))
		Expect(results[0].Arguments).To(Equal(`{"city":"Berlin"}`))
	})

	It("parses multiple string args", func() {
		input := `<|tool_call_start|>[search(query="hello world", source="web")]<|tool_call_end|>`
		results := ParseFunctionCall(input, cfg())
		Expect(results).To(HaveLen(1))
		Expect(results[0].Name).To(Equal("search"))
		// argument map ordering is not stable; check content as JSON
		Expect(results[0].Arguments).To(SatisfyAny(
			Equal(`{"query":"hello world","source":"web"}`),
			Equal(`{"source":"web","query":"hello world"}`),
		))
	})

	It("parses numeric and boolean args", func() {
		input := `<|tool_call_start|>[set_volume(level=42, mute=false)]<|tool_call_end|>`
		results := ParseFunctionCall(input, cfg())
		Expect(results).To(HaveLen(1))
		Expect(results[0].Name).To(Equal("set_volume"))
		// ArgumentRegex always emits string values; the JSON we produce represents
		// them as strings. A typed parser is a future enhancement (PEG parser).
		Expect(results[0].Arguments).To(SatisfyAny(
			Equal(`{"level":"42","mute":"false"}`),
			Equal(`{"mute":"false","level":"42"}`),
		))
	})

	It("parses a no-args call", func() {
		input := `<|tool_call_start|>[get_time()]<|tool_call_end|>`
		results := ParseFunctionCall(input, cfg())
		Expect(results).To(HaveLen(1))
		Expect(results[0].Name).To(Equal("get_time"))
		Expect(results[0].Arguments).To(Equal(`{}`))
	})

	It("ignores surrounding text", func() {
		input := `Sure, let me check.
<|tool_call_start|>[get_weather(city="Paris")]<|tool_call_end|>
Standby.`
		results := ParseFunctionCall(input, cfg())
		Expect(results).To(HaveLen(1))
		Expect(results[0].Name).To(Equal("get_weather"))
		Expect(results[0].Arguments).To(Equal(`{"city":"Paris"}`))
	})

	It("returns no results when the markers are absent", func() {
		input := `Plain text response with no tool call.`
		results := ParseFunctionCall(input, cfg())
		Expect(results).To(BeEmpty())
	})

	It("preserves quoted argument values that contain spaces and equals signs", func() {
		input := `<|tool_call_start|>[search(query="x = y + 1")]<|tool_call_end|>`
		results := ParseFunctionCall(input, cfg())
		Expect(results).To(HaveLen(1))
		Expect(results[0].Name).To(Equal("search"))
		Expect(results[0].Arguments).To(Equal(`{"query":"x = y + 1"}`))
	})
})
