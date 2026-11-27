package functions

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"
)

// fuzzTokens cuts text into tokens whose sizes come from cuts, in turn.
func fuzzTokens(text string, cuts []byte) []string {
	var tokens []string
	for i := 0; text != ""; i++ {
		n := 1
		if len(cuts) > 0 {
			n = 1 + int(cuts[i%len(cuts)]%16)
		}
		n = min(n, len(text))
		tokens = append(tokens, text[:n])
		text = text[n:]
	}
	return tokens
}

var fuzzSeeds = []string{
	"Here is code:\n```go\nfunc f() { m := map[string]int{\"a\": 1}; x := a[0] }\n```\nand <div>{x}</div>",
	"Some text \"quoted\nwith newline\" then {\"name\": \"f\", \"arguments\": {\"a\": \"b\\n\"}} done",
	"prose <tool_call>\n<function=get_weather>\n<parameter=city>\nRome\n</parameter>\n</function>\n</tool_call>",
	"Answer [1, 2, {\"b\": null}] tru 12 3.5e2 \"open string",
	"text <minimax:tool_call><invoke name=\"f\"><parameter name=\"a\">1</parameter></invoke></minimax:tool_call>",
	"  \t<tool",
}

// FuzzStreamToolCallParser checks the chat and Responses stream parses
// against a parse of the whole output, at every token.
func FuzzStreamToolCallParser(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s, []byte{3, 1, 7})
	}
	configs := xmlConfigs()
	f.Fuzz(func(t *testing.T, text string, cuts []byte) {
		g := NewWithT(t)
		cfg := configs[len(text)%len(configs)]
		p := NewStreamToolCallParser(cfg)
		var output strings.Builder
		for _, tok := range fuzzTokens(text, cuts) {
			p.Write(tok)
			output.WriteString(tok)
			s := output.String()
			wantXML, wantXMLErr := ParseXMLIterative(s, p.xmlFormat, true)
			gotXML, gotXMLErr := p.ParseXML()
			g.Expect(gotXML).To(Equal(wantXML), s)
			g.Expect(errText(gotXMLErr)).To(Equal(errText(wantXMLErr)), s)
			for _, partial := range []bool{true, false} {
				want, wantErr := ParseJSONIterative(s, partial)
				got, gotErr := p.ParseJSON(partial)
				g.Expect(got).To(Equal(want), s)
				g.Expect(errText(gotErr)).To(Equal(errText(wantErr)), s)
			}
		}
	})
}
