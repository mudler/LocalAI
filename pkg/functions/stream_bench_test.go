package functions

import (
	"fmt"
	"strings"
	"testing"
)

// streamParsers are the per-token parses of the three endpoints: as each
// endpoint ran them on the whole output, and with the stream parsers.
var streamParsers = []struct {
	name        string
	full        func(cfg FunctionsConfig) func(output string)
	incremental func(cfg FunctionsConfig) func(token string)
}{
	{
		"chat",
		func(cfg FunctionsConfig) func(string) {
			format := GetXMLFormatPreset(cfg.XMLFormatPreset)
			return func(output string) { directParse(output, cfg, format) }
		},
		func(cfg FunctionsConfig) func(string) {
			p := NewStreamToolCallParser(cfg)
			return func(token string) { p.Write(token); p.Parse() }
		},
	},
	{
		"anthropic",
		func(cfg FunctionsConfig) func(string) {
			return func(output string) { ParseFunctionCall(CleanupLLMResult(output, cfg), cfg) }
		},
		func(cfg FunctionsConfig) func(string) {
			p := NewStreamFunctionCallParser(cfg)
			return func(token string) { p.Write(token); p.Parse() }
		},
	},
	{
		"responses",
		func(cfg FunctionsConfig) func(string) {
			format := GetXMLFormatPreset(cfg.XMLFormatPreset)
			return func(output string) {
				cleaned := CleanupLLMResult(output, cfg)
				if calls, err := ParseXMLIterative(cleaned, format, true); err == nil && len(calls) > 0 {
					return
				}
				_, _ = ParseJSONIterative(cleaned, false)
			}
		},
		func(cfg FunctionsConfig) func(string) {
			p := NewStreamToolCallParser(cfg)
			return func(token string) {
				p.Write(token)
				if calls, err := p.ParseXML(); err == nil && len(calls) > 0 {
					return
				}
				_, _ = p.ParseJSON(false)
			}
		},
	},
}

// BenchmarkStreamCodeAnswer parses an answer with code blocks for tool calls
// at every token, as the endpoints do for a request that offers tools.
func BenchmarkStreamCodeAnswer(b *testing.B) {
	cfg := FunctionsConfig{}
	for _, sp := range streamParsers {
		for _, n := range []int{512, 2048, 8192} {
			tokens := codeAnswerTokens(n)
			b.Run(fmt.Sprintf("%s/full/tokens=%d", sp.name, n), func(b *testing.B) {
				for b.Loop() {
					parse := sp.full(cfg)
					var out strings.Builder
					for _, tok := range tokens {
						out.WriteString(tok)
						parse(out.String())
					}
				}
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*n), "ns/token")
			})
			b.Run(fmt.Sprintf("%s/incremental/tokens=%d", sp.name, n), func(b *testing.B) {
				for b.Loop() {
					parse := sp.incremental(cfg)
					for _, tok := range tokens {
						parse(tok)
					}
				}
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*n), "ns/token")
			})
		}
	}
}
