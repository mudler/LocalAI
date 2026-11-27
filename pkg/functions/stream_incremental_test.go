package functions

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// codeFragments build answers with code blocks: prose, code in several
// languages, JSON with nested braces and escaped quotes, HTML tags, markdown
// links, numbers and literals cut at any point.
var codeFragments = []string{
	"Here", " is", " the", " code", ":\n\n", "```go\n", "```json\n", "```\n", "\n", " ", "\t", "  ",
	"func f() {", "}", "{", "[", "]", "(", ")", "<", ">", "</", "/>", "<div>", "</div>", "<ul className=\"x\">",
	"{\"a\": 1}", "{\"a\": [1, 2, {\"b\": null}]}", "{\"s\": \"he said \\\"hi\\\"\"}", "{\"k\": \"v\\n\"}",
	"\"", "\\\"", "\\", "\\n", "'", ":", ",", "a < b", "x > 3", "[link](url)", "arr[i]", "m := map[string]int{",
	"\"a\": 1,", "\"name\": \"f\"", "\"arguments\": {}", "12", "3.5e", "2", "-", "tru", "e", "fals", "null", "nul",
	"ü", "日本", "\xc3", "\xa0", " ", "\r\n", "n", "r", "\\r",
}

// markupFragments are the tags of the XML formats and pieces of them, so an
// anchor can arrive whole, cut across tokens, or at the very end.
var markupFragments = []string{
	"<tool_call>", "<tool", "_call>", "</tool_call>", "<function=", "<func", "tion=", "get_weather>", "</function>",
	"<parameter=", "city>", "Rome", "</parameter>", "<invoke name=\"", "f\">", "</invoke>",
	"<minimax:tool_call>", "</minimax:tool_call>", "<arg_key>", "k</arg_key>", "<arg_value>", "v</arg_value>",
	"<|tool_calls_section_begin|>", "<|tool_call_begin|>", "functions.f:0", "<|tool_call_argument_begin|>",
	"<|tool_call_end|>", "<|tool_calls_section_end|>", "<tool_calls>[", "]</tool_calls>",
	"<tool_call>\n{\"name\": \"", "f\", \"arguments\": {", "}\n</tool_call>",
	`{"name": "get_weather", "arguments": {"city": "Rome"}}`, `{"name": "`, `search", "arguments": {"q": "x"`, `}}`,
}

// codeAnswerPieces is a model answer with prose and code blocks: '<', '{' and
// '[' in plenty, and no tool call.
var codeAnswerPieces = []string{
	"Here is how you can do it. First, define the handler:\n\n",
	"```go\nfunc handle(w http.ResponseWriter, r *http.Request) {\n\tif r.Method != \"POST\" {\n\t\thttp.Error(w, \"bad\", 405)\n\t\treturn\n\t}\n\tvar req struct{ Name string `json:\"name\"` }\n\t_ = json.NewDecoder(r.Body).Decode(&req)\n\tm := map[string]int{\"a\": 1, \"b\": 2}\n\tfor k, v := range m {\n\t\tfmt.Println(k, v, items[0])\n\t}\n}\n```\n\n",
	"The response is a JSON object such as:\n\n```json\n{\n  \"status\": \"ok\",\n  \"items\": [1, 2, 3],\n  \"meta\": {\"page\": 2, \"next\": null}\n}\n```\n\n",
	"In the page, render it with a component:\n\n```jsx\nexport function List({ items }) {\n  return (\n    <ul className=\"list\">\n      {items.map((x) => <li key={x.id}>{x.name}</li>)}\n    </ul>\n  );\n}\n```\n\n",
	"Note that `a < b` holds when the list `[x, y]` is sorted, and that {braces} in prose are fine. ",
	"The template is <div class=\"row\"><span>{{ .Name }}</span></div>, and 5 > 3.\n\n",
}

// codeAnswerTokens cuts a code answer into n tokens of about four bytes.
func codeAnswerTokens(n int) []string {
	var text strings.Builder
	for i := 0; text.Len() < n*4; i++ {
		text.WriteString(codeAnswerPieces[i%len(codeAnswerPieces)])
	}
	s := text.String()
	tokens := make([]string, 0, n)
	for i := 0; len(tokens) < n; i += 4 {
		tokens = append(tokens, s[i:i+4])
	}
	return tokens
}

// randomTokens returns 1 to max tokens, mostly from pool and sometimes from
// markupFragments.
func randomTokens(r *rand.Rand, pool []string, maxTokens int, markupEvery int) []string {
	tokens := make([]string, 1+r.Intn(maxTokens))
	for i := range tokens {
		p := pool
		if markupEvery > 0 && r.Intn(markupEvery) == 0 {
			p = markupFragments
		}
		tokens[i] = p[r.Intn(len(p))]
	}
	return tokens
}

// customXMLFormats are formats a model config can set, beside the presets:
// one that misses a required field, one with a blank scope, and one whose
// tags hold no '<'.
func customXMLFormats() []*XMLToolCallFormat {
	return []*XMLToolCallFormat{
		{ToolStart: "<call>"},
		{ScopeStart: " ", ToolStart: "<call ", ToolSep: ">", KeyStart: "<k=", KeyValSep: ">", ValEnd: "</k>", ToolEnd: "</call>"},
		{ScopeStart: "BEGIN", ToolStart: "call:", ToolSep: "(", KeyStart: "@", KeyValSep: "=", ValEnd: ";", ToolEnd: ")", ScopeEnd: "END"},
	}
}

func xmlConfigs() []FunctionsConfig {
	configs := []FunctionsConfig{{}}
	for _, preset := range getAllXMLFormats() {
		configs = append(configs, FunctionsConfig{XMLFormatPreset: preset.name})
	}
	for _, f := range customXMLFormats() {
		configs = append(configs, FunctionsConfig{XMLFormat: f})
	}
	return configs
}

func errText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// checkToolCallParser compares each parse of p with the parse of the whole
// output after each token.
func checkToolCallParser(cfg FunctionsConfig, tokens []string, where string) {
	p := NewStreamToolCallParser(cfg)
	var output strings.Builder
	for i, tok := range tokens {
		p.Write(tok)
		output.WriteString(tok)
		at := fmt.Sprintf("%s step %d output %q", where, i, output.String())
		cleaned := CleanupLLMResult(output.String(), cfg)

		wantXML, wantXMLErr := ParseXMLIterative(cleaned, p.xmlFormat, true)
		gotXML, gotXMLErr := p.ParseXML()
		Expect(gotXML).To(Equal(wantXML), at)
		Expect(errText(gotXMLErr)).To(Equal(errText(wantXMLErr)), at)

		for _, partial := range []bool{true, false} {
			want, wantErr := ParseJSONIterative(cleaned, partial)
			got, gotErr := p.ParseJSON(partial)
			Expect(got).To(Equal(want), at)
			Expect(errText(gotErr)).To(Equal(errText(wantErr)), at)
		}
	}
}

var _ = Describe("Incremental stream parses", func() {
	It("scan the legacy JSON of a growing output as a parse of the whole output does", func() {
		r := rand.New(rand.NewSource(21))
		pools := [][]string{legacyFragments, codeFragments}
		for run := range 3000 {
			tokens := randomTokens(r, pools[run%2], 40, 0)
			var l legacyJSONScan
			var output strings.Builder
			for i, tok := range tokens {
				output.WriteString(tok)
				s := output.String()
				want, wantErr := parseJSONLegacy(s)
				got, gotErr := l.scan(s)
				at := fmt.Sprintf("run %d step %d output %q", run, i, s)
				Expect(got).To(Equal(want), at)
				Expect(errText(gotErr)).To(Equal(errText(wantErr)), at)
			}
		}
	})

	It("rewind the legacy JSON scan to the bytes that changed", func() {
		r := rand.New(rand.NewSource(22))
		for run := range 2000 {
			l := legacyJSONScan{keepMarks: true}
			s := ""
			for i := range 1 + r.Intn(40) {
				s += codeFragments[r.Intn(len(codeFragments))]
				if r.Intn(5) == 0 && len(s) > 0 {
					// Change the bytes from a random point, as the newline
					// escape of a string that closes does.
					at := r.Intn(len(s))
					s = s[:at] + strings.ToUpper(s[at:])
					l.rewind(at)
				}
				want, wantErr := parseJSONLegacy(s)
				got, gotErr := l.scan(s)
				where := fmt.Sprintf("run %d step %d output %q", run, i, s)
				Expect(got).To(Equal(want), where)
				Expect(errText(gotErr)).To(Equal(errText(wantErr)), where)
			}
		}
	})

	It("find the XML and JSON calls a parse of the whole output finds, at every token", func() {
		r := rand.New(rand.NewSource(23))
		configs := append(xmlConfigs(), FunctionsConfig{ReplaceLLMResult: []ReplaceResult{{Key: `(?s)^Here`, Value: "<tool_call>"}}})
		for run := range 3000 {
			cfg := configs[run%len(configs)]
			pool := codeFragments
			if run%3 == 0 {
				pool = streamFragments
			}
			tokens := randomTokens(r, pool, 40, 6)
			checkToolCallParser(cfg, tokens, fmt.Sprintf("run %d config %d tokens %q", run, run%len(configs), tokens))
		}
	})

	It("see a start tag that arrives cut across tokens", func() {
		for _, cfg := range xmlConfigs() {
			p := NewStreamToolCallParser(cfg)
			anchors, ok := xmlParseAnchors(p.xmlFormat)
			if !ok {
				Expect(p.xmlGate.empty("some text")).To(BeFalse())
				continue
			}
			for _, anchor := range anchors {
				for cut := 1; cut < len(anchor); cut++ {
					p.Reset()
					p.Write("Some prose that comes first. ")
					if !slices.ContainsFunc(anchors, func(a string) bool { return strings.Contains(p.Cleaned(), a) }) {
						Expect(p.xmlGate.empty(p.Cleaned())).To(BeTrue(), anchor)
					}
					p.Write(anchor[:cut])
					p.xmlGate.empty(p.Cleaned())
					p.Write(anchor[cut:])
					Expect(p.xmlGate.empty(p.Cleaned())).To(BeFalse(), fmt.Sprintf("%q cut at %d", anchor, cut))
				}
			}
		}
	})

	It("follow an answer with code blocks cut into tokens of any size", func() {
		r := rand.New(rand.NewSource(24))
		text := strings.Join(codeAnswerTokens(600), "")
		for run := range 40 {
			var tokens []string
			for rest := text; rest != ""; {
				n := min(len(rest), 1+r.Intn(12))
				tokens = append(tokens, rest[:n])
				rest = rest[n:]
			}
			cfg := xmlConfigs()[run%len(xmlConfigs())]
			checkToolCallParser(cfg, tokens, fmt.Sprintf("run %d", run))
		}
	})

	It("keep their work across the tokens of an answer with code blocks", func() {
		p := NewStreamToolCallParser(FunctionsConfig{})
		for _, tok := range codeAnswerTokens(2048) {
			p.Write(tok)
			p.Parse()
		}
		Expect(p.Parse().XML).To(BeEmpty())
		// The XML parse never ran, and the JSON scan stopped short of the
		// end only by the last step.
		Expect(p.xmlGate.empty(p.Cleaned())).To(BeTrue())
		Expect(p.legacy.cur.offset).To(BeNumerically(">", p.output.Len()-64))
	})
})
