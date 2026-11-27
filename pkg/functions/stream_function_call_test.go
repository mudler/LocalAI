package functions

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/pkg/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("StreamFunctionCallParser", func() {
	It("escape the newlines of a growing output as EscapeNewLines does", func() {
		r := rand.New(rand.NewSource(25))
		pool := append(slices.Clone(codeFragments), "\"", "\"", "\\", "\\\"", "\n", "\r")
		for run := range 5000 {
			var e newlineEscaper
			var output strings.Builder
			prev := ""
			for i, tok := range randomTokens(r, pool, 30, 0) {
				output.WriteString(tok)
				at := e.write(tok)
				want := utils.EscapeNewLines(output.String())
				where := fmt.Sprintf("run %d step %d output %q", run, i, output.String())
				Expect(e.String()).To(Equal(want), where)
				// Only the bytes from the reported point on changed.
				if at < 0 {
					Expect(strings.HasPrefix(want, prev)).To(BeTrue(), where)
				} else {
					Expect(want[:at]).To(Equal(prev[:at]), where)
				}
				prev = want
			}
		}
	})

	It("find the function calls ParseFunctionCall finds, at every token", func() {
		r := rand.New(rand.NewSource(26))
		configs := append(xmlConfigs(), markerConfigs()...)
		configs = append(configs,
			FunctionsConfig{DisablePEGParser: true},
			FunctionsConfig{FunctionNameKey: "function", FunctionArgumentsKey: "params"},
			FunctionsConfig{AutomaticToolParsingFallback: true},
			FunctionsConfig{ToolFormatMarkers: &ToolFormatMarkers{FormatType: "tag_with_json", FuncNamePrefix: "functions.", FuncNameSuffix: ":"}},
			FunctionsConfig{ReplaceFunctionResults: []ReplaceResult{{Key: `x`, Value: "{"}}},
			FunctionsConfig{ReplaceLLMResult: []ReplaceResult{{Key: `(?s)^Here`, Value: "<tool_call>"}}},
			FunctionsConfig{JSONRegexMatch: []string{`(?s)<json>(.*?)</json>`}},
		)
		pool := append(slices.Clone(codeFragments),
			`{"function": "f", "params": {"a": 1}}`, `{"name": "g", "arguments": "not json"}`, "\"text\nwith newline\"",
			"[TOOL_CALLS]", "</s>", "functions.f:", "<json>", "</json>")
		for run := range 3000 {
			cfg := configs[run%len(configs)]
			tokens := randomTokens(r, pool, 40, 5)
			p := NewStreamFunctionCallParser(cfg)
			var output strings.Builder
			for i, tok := range tokens {
				p.Write(tok)
				output.WriteString(tok)
				want := ParseFunctionCall(CleanupLLMResult(output.String(), cfg), cfg)
				at := fmt.Sprintf("run %d config %d step %d output %q", run, run%len(configs), i, output.String())
				Expect(p.Parse()).To(Equal(want), at)
			}
		}
	})

	It("find the calls of an answer with code that ends in a tool call", func() {
		calls := []string{
			"<tool_call>\n<function=get_weather>\n<parameter=city>\nRome\n</parameter>\n</function>\n</tool_call>",
			"<tool_call>\n{\"name\": \"get_weather\", \"arguments\": {\"city\": \"Rome\"}}\n</tool_call>",
			"<function=get_weather>{\"city\": \"Rome\"}</function>",
			"{\"name\": \"get_weather\", \"arguments\": {\"city\": \"Rome\\n\\\"x\\\"\"}}",
			"<minimax:tool_call>\n<invoke name=\"get_weather\">\n<parameter name=\"city\">Rome</parameter>\n</invoke>\n</minimax:tool_call>",
			"[TOOL_CALLS]{\"name\": \"get_weather\", \"arguments\": {}}</s>",
		}
		configs := append(xmlConfigs(), markerConfigs()...)
		configs = append(configs, FunctionsConfig{DisablePEGParser: true})
		prose := strings.Join(codeAnswerTokens(120), "")
		for c, call := range calls {
			text := prose + "\n" + call
			var tokens []string
			for rest := text; rest != ""; {
				n := min(len(rest), 3)
				tokens = append(tokens, rest[:n])
				rest = rest[n:]
			}
			for i, cfg := range configs {
				where := fmt.Sprintf("call %d config %d", c, i)
				checkToolCallParser(cfg, tokens, where)
				p := NewStreamFunctionCallParser(cfg)
				var output strings.Builder
				for _, tok := range tokens {
					p.Write(tok)
					output.WriteString(tok)
				}
				Expect(p.Parse()).To(Equal(ParseFunctionCall(output.String(), cfg)), where)
			}
		}
	})

	It("skip the markup parsers on an answer with code blocks", func() {
		p := NewStreamFunctionCallParser(FunctionsConfig{})
		Expect(p.incremental).To(BeTrue())
		for _, tok := range codeAnswerTokens(2048) {
			p.Write(tok)
			Expect(p.Parse()).To(BeEmpty())
		}
		Expect(p.watch.seen).To(BeFalse())
		Expect(p.legacy.cur.offset).To(BeNumerically(">", len(p.escaped.String())-64))
	})
})
