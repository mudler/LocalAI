package functions

import (
	"strings"
	"testing"

	"github.com/mudler/LocalAI/pkg/utils"
	. "github.com/onsi/gomega"
)

// FuzzStreamFunctionCallParser checks the Anthropic stream parse against
// ParseFunctionCall of the whole output, at every token.
func FuzzStreamFunctionCallParser(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s, []byte{2, 5, 1})
	}
	configs := append(xmlConfigs(), markerConfigs()...)
	configs = append(configs, FunctionsConfig{DisablePEGParser: true})
	f.Fuzz(func(t *testing.T, text string, cuts []byte) {
		g := NewWithT(t)
		cfg := configs[len(text)%len(configs)]
		p := NewStreamFunctionCallParser(cfg)
		var output strings.Builder
		for _, tok := range fuzzTokens(text, cuts) {
			p.Write(tok)
			output.WriteString(tok)
			g.Expect(p.Parse()).To(Equal(ParseFunctionCall(output.String(), cfg)), output.String())
		}
	})
}

// FuzzNewlineEscaper checks the escape of a growing output against
// utils.EscapeNewLines.
func FuzzNewlineEscaper(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s, []byte{1, 4})
	}
	f.Fuzz(func(t *testing.T, text string, cuts []byte) {
		g := NewWithT(t)
		var e newlineEscaper
		var output strings.Builder
		for _, tok := range fuzzTokens(text, cuts) {
			e.write(tok)
			output.WriteString(tok)
			g.Expect(e.String()).To(Equal(utils.EscapeNewLines(output.String())), output.String())
		}
	})
}
