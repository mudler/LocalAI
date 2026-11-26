package functions

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// streamFragments build random outputs: prose, JSON calls, the tags of the XML
// formats, and pieces of them.
var streamFragments = []string{
	"Sure", ", here", " is", " the answer.", "\n", " ", "a=b", "x > y", "\\", "\"", ":", ",",
	`{"name": "get_weather", "arguments": {"city": "Rome"}}`, `{"name": "`, `search", "arguments": {"q": "x"`, `}}`,
	"[", "]", "{", "}", `"name"`,
	"<tool_call>", "</tool_call>", "\n{\"name\": \"f\", \"arguments\": {}}\n",
	"<function=", "get_weather>", "<parameter=", "city>", "Rome", "</parameter>", "</function>",
	"<invoke name=\"f\">", "</invoke>", "<minimax:tool_call>", "</minimax:tool_call>",
	"<arg_key>", "k</arg_key>", "<arg_value>", "v</arg_value>",
	"<|tool_calls_section_begin|>", "<|tool_call_begin|>", "functions.f:0", "<|tool_call_argument_begin|>", "<|tool_call_end|>",
	"<tool_calls>[", "]</tool_calls>", "ü", "日本",
}

// proseFragments hold no byte that can start a tool call.
var proseFragments = []string{
	"Sure", ", here", " is", " the answer.", "\n", " ", "a=b", "x > y", "\\", "\"", ":", ",",
	"function", " name", " arguments", "parameter=", "invoke", "|", "tool_call", "ü",
}

func directParse(output string, cfg FunctionsConfig, format *XMLToolCallFormat) StreamToolCalls {
	cleaned := CleanupLLMResult(output, cfg)
	if calls, err := ParseXMLIterative(cleaned, format, true); err == nil && len(calls) > 0 {
		return StreamToolCalls{XML: calls}
	}
	calls, err := ParseJSONIterative(cleaned, true)
	return StreamToolCalls{JSON: calls, JSONErr: err}
}

// noCalls tells whether the streaming worker emits nothing for r.
func noCalls(r StreamToolCalls) bool {
	return len(r.XML) == 0 && (r.JSONErr != nil || len(r.JSON) == 0)
}

func checkStream(cfg FunctionsConfig, tokens []string, where string) {
	p := NewStreamToolCallParser(cfg)
	var output strings.Builder
	for i, tok := range tokens {
		p.Write(tok)
		output.WriteString(tok)
		got := p.Parse()
		want := directParse(output.String(), cfg, p.xmlFormat)
		at := fmt.Sprintf("%s step %d output %q", where, i, output.String())
		if !strings.ContainsAny(output.String(), toolCallTriggers) && len(cfg.ReplaceLLMResult) == 0 {
			Expect(noCalls(want)).To(BeTrue(), "the direct parse found a call without a trigger: "+at)
			Expect(noCalls(got)).To(BeTrue(), at)
			continue
		}
		Expect(got.XML).To(Equal(want.XML), at)
		Expect(got.JSON).To(Equal(want.JSON), at)
		Expect(got.JSONErr == nil).To(Equal(want.JSONErr == nil), at)
	}
}

var _ = Describe("StreamToolCallParser", func() {
	It("finds what a parse of the whole output finds, at every token", func() {
		r := rand.New(rand.NewSource(1))
		configs := []FunctionsConfig{
			{},
			{XMLFormatPreset: "qwen3-coder"},
			{ReplaceLLMResult: []ReplaceResult{{Key: `(?s)^Sure`, Value: "{"}}},
		}
		for run := range 600 {
			cfg := configs[run%len(configs)]
			pool := streamFragments
			if run%4 == 0 {
				pool = proseFragments
			}
			tokens := make([]string, 1+r.Intn(30))
			for i := range tokens {
				if r.Intn(8) == 0 {
					continue // an empty token, as the autoparser path sends
				}
				tokens[i] = pool[r.Intn(len(pool))]
			}
			checkStream(cfg, tokens, fmt.Sprintf("run %d config %d tokens %q", run, run%len(configs), tokens))
		}
	})

	It("starts over after a reset", func() {
		p := NewStreamToolCallParser(FunctionsConfig{})
		p.Write(`{"name": "f", "arguments": {}}`)
		Expect(p.Parse().JSON).To(HaveLen(1))
		p.Reset()
		p.Write("plain text")
		Expect(noCalls(p.Parse())).To(BeTrue())
	})
})

func proseTokens(n int) []string {
	tokens := make([]string, n)
	for i := range tokens {
		tokens[i] = fmt.Sprintf(" word%d", i)
	}
	return tokens
}

// BenchmarkStreamToolCallParse compares a parse of the whole output for each
// token, as the stream worker did, with StreamToolCallParser, on a prose
// answer to a request that offered tools.
func BenchmarkStreamToolCallParse(b *testing.B) {
	cfg := FunctionsConfig{}
	for _, n := range []int{1024, 4096, 16384} {
		tokens := proseTokens(n)
		b.Run(fmt.Sprintf("whole-output/tokens=%d", n), func(b *testing.B) {
			for b.Loop() {
				result := ""
				for _, tok := range tokens {
					result += tok
					directParse(result, cfg, nil)
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*n), "ns/token")
		})
		b.Run(fmt.Sprintf("stream-parser/tokens=%d", n), func(b *testing.B) {
			for b.Loop() {
				p := NewStreamToolCallParser(cfg)
				for _, tok := range tokens {
					p.Write(tok)
					p.Parse()
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*n), "ns/token")
		})
	}
}
