package functions

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("StreamOutput", func() {
	It("holds no tool call that the parses find while it says so", func() {
		r := rand.New(rand.NewSource(11))
		configs := []FunctionsConfig{{}}
		for _, preset := range getAllXMLFormats() {
			configs = append(configs, FunctionsConfig{XMLFormatPreset: preset.name})
		}
		for run := range 400 {
			cfg := configs[run%len(configs)]
			var o StreamOutput
			for range 1 + r.Intn(30) {
				pool := proseFragments
				if r.Intn(10) == 0 {
					pool = streamFragments
				}
				o.Write(pool[r.Intn(len(pool))])
				at := fmt.Sprintf("run %d config %+v output %q", run, cfg, o.String())
				Expect(o.String()).To(HaveLen(o.Len()), at)
				if o.MayHoldFunctionCall(cfg) {
					Expect(o.MayHoldPartialToolCall(cfg)).To(BeTrue(), at)
					continue
				}
				Expect(o.MayHoldPartialToolCall(cfg)).To(BeFalse(), at)
				cleaned := CleanupLLMResult(o.String(), cfg)
				Expect(ParseFunctionCall(cleaned, cfg)).To(BeEmpty(), at)
				Expect(noCalls(directParse(o.String(), cfg, GetXMLFormatPreset(cfg.XMLFormatPreset)))).To(BeTrue(), at)
			}
		}
	})

	It("always parses for a config that can match without a trigger byte", func() {
		var o StreamOutput
		o.Write("plain text")
		Expect(o.MayHoldFunctionCall(FunctionsConfig{})).To(BeFalse())
		for _, cfg := range []FunctionsConfig{
			{ReplaceLLMResult: []ReplaceResult{{Key: "x", Value: "{"}}},
			{ReplaceFunctionResults: []ReplaceResult{{Key: "x", Value: "{"}}},
			{ResponseRegex: []string{`(?P<name>\w+)`}},
			{JSONRegexMatch: []string{`(.*)`}},
			{XMLFormat: &XMLToolCallFormat{ToolStart: "call:"}},
			{ToolFormatMarkers: &ToolFormatMarkers{}},
		} {
			Expect(o.MayHoldFunctionCall(cfg)).To(BeTrue(), fmt.Sprintf("%+v", cfg))
		}
		Expect(o.MayHoldPartialToolCall(FunctionsConfig{ResponseRegex: []string{`x`}})).To(BeFalse())
		o.Write(" then {")
		Expect(o.MayHoldPartialToolCall(FunctionsConfig{})).To(BeTrue())
	})
})

// BenchmarkStreamFunctionCallParse compares CleanupLLMResult and
// ParseFunctionCall on the whole output for each token, as the Anthropic
// stream did, with the StreamOutput gate, on a prose answer to a request that
// offered tools.
func BenchmarkStreamFunctionCallParse(b *testing.B) {
	cfg := FunctionsConfig{}
	tokens := proseTokens(512)
	b.Run("every-token", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var out strings.Builder
			for _, tok := range tokens {
				out.WriteString(tok)
				ParseFunctionCall(CleanupLLMResult(out.String(), cfg), cfg)
			}
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(tokens)), "ns/token")
	})
	b.Run("gated", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var out StreamOutput
			for _, tok := range tokens {
				out.Write(tok)
				if out.MayHoldFunctionCall(cfg) {
					ParseFunctionCall(CleanupLLMResult(out.String(), cfg), cfg)
				}
			}
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(tokens)), "ns/token")
	})
}
