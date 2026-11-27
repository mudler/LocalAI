package functions

import "strings"

// newlineEscaper keeps utils.EscapeNewLines of an output that grows.
//
// EscapeNewLines replaces each '\r' and '\n' inside a double-quoted string
// with the two bytes `\n`, and leaves the rest. Its pattern matches a string
// from a quote to the next quote that no backslash escapes. A quote whose
// string does not close is left as it is, and so is everything after it: any
// later quote is escaped inside that string, and a match from it would also
// run to the end. So the result changes in place only when a string closes,
// and only from its opening quote on.
type newlineEscaper struct {
	out      *strings.Builder // replaced, never copied, when a string closes
	inString bool
	escaped  bool
	start    int  // where the open string starts in out
	newline  bool // the open string holds a '\r' or '\n'
}

func (e *newlineEscaper) reset() {
	e.out = nil
	e.inString, e.escaped, e.start, e.newline = false, false, 0, false
}

func (e *newlineEscaper) String() string {
	if e.out == nil {
		return ""
	}
	return e.out.String()
}

// write appends a token. It returns the first byte of the result that
// changed in place, or -1 when the result only grew.
func (e *newlineEscaper) write(token string) int {
	if e.out == nil {
		e.out = new(strings.Builder)
	}
	changed := -1
	for i := 0; i < len(token); i++ {
		c := token[i]
		e.out.WriteByte(c)
		if !e.inString {
			if c == '"' {
				e.inString, e.escaped, e.newline = true, false, false
				e.start = e.out.Len() - 1
			}
			continue
		}
		switch {
		case e.escaped:
			e.escaped = false
		case c == '\\':
			e.escaped = true
		case c == '"':
			e.inString = false
			if e.newline {
				e.escapeOpenString()
				if changed < 0 {
					changed = e.start
				}
			}
			continue
		}
		if c == '\r' || c == '\n' {
			e.newline = true
		}
	}
	return changed
}

// escapeOpenString rewrites the string that just closed. It builds a new
// buffer: strings returned before keep their bytes.
func (e *newlineEscaper) escapeOpenString() {
	old := e.out.String()
	b := new(strings.Builder)
	b.Grow(len(old) + 64)
	b.WriteString(old[:e.start])
	for i := e.start; i < len(old); i++ {
		if c := old[i]; c == '\r' || c == '\n' {
			b.WriteString(`\n`)
		} else {
			b.WriteByte(c)
		}
	}
	e.out = b
}

// functionCallAnchors returns strings without which ParseFunctionCall finds
// no call in markup: the PEG parsers it tries and the XML fallback. Every PEG
// parser it builds reads content up to a delimiter and needs the delimiter to
// start a call, and the XML fallback runs only on output with "<tool_call>"
// or "<function=". It returns false when a parser has no delimiter.
func functionCallAnchors(cfg FunctionsConfig) ([]string, bool) {
	anchors := []string{"<tool_call>", "<function="}
	if cfg.DisablePEGParser {
		return anchors, true
	}
	if m := cfg.ToolFormatMarkers; m != nil {
		switch m.FormatType {
		case "tag_with_json", "tag_with_tagged":
			start, _ := effectiveScope(m)
			if start == "" {
				start = strings.TrimRight(m.FuncNamePrefix, " \t\n")
			}
			if start == "" {
				return nil, false
			}
			anchors = append(anchors, start)
		case "json_native":
			// Without both section markers there is no parser.
			if start, end := effectiveScope(m); start != "" && end != "" {
				anchors = append(anchors, start)
			}
		}
	}
	formats := []*XMLToolCallFormat{cfg.XMLFormat}
	if cfg.XMLFormatPreset != "" {
		formats = append(formats, GetXMLFormatPreset(cfg.XMLFormatPreset))
	}
	for _, preset := range getAllXMLFormats() {
		formats = append(formats, preset.format)
	}
	for _, f := range formats {
		if f == nil {
			continue
		}
		start := getScopeOrToolStart(f)
		if start == "" {
			return nil, false
		}
		anchors = append(anchors, start)
	}
	return anchors, true
}

// StreamFunctionCallParser returns ParseFunctionCall(CleanupLLMResult(output))
// of an output that grows, as the Anthropic stream parses it after each
// token.
//
// For a config without replacement rules or regexes, and an output that does
// not open with a JSON object or array, the parse is the JSON calls of
// parseJSONLegacy(EscapeNewLines(output)) and the calls of the markup parsers.
// The parser keeps EscapeNewLines and the JSON scan across tokens, and runs the
// markup parsers only once the output holds one of their delimiters.
type StreamFunctionCallParser struct {
	config      FunctionsConfig
	incremental bool

	output  strings.Builder
	escaped newlineEscaper
	legacy  legacyJSONScan
	watch   anchorWatch

	// calls holds the calls of the stable objects of the JSON scan, and
	// callsAt[k] how many of them the first k objects give.
	calls   []FuncCallResults
	callsAt []int
}

// NewStreamFunctionCallParser returns a parser for the given function config.
func NewStreamFunctionCallParser(config FunctionsConfig) *StreamFunctionCallParser {
	p := &StreamFunctionCallParser{config: config, legacy: legacyJSONScan{keepMarks: true}}
	anchors, ok := functionCallAnchors(config)
	p.incremental = ok &&
		len(config.ReplaceLLMResult) == 0 &&
		len(config.ReplaceFunctionResults) == 0 &&
		len(config.JSONRegexMatch) == 0 &&
		len(config.ResponseRegex) == 0
	p.watch = newAnchorWatch(anchors)
	return p
}

// Write appends a token to the output.
func (p *StreamFunctionCallParser) Write(token string) {
	p.output.WriteString(token)
	if !p.incremental {
		return
	}
	if at := p.escaped.write(token); at >= 0 {
		p.legacy.rewind(at)
		if k := p.legacy.stable(); k < len(p.callsAt)-1 {
			p.callsAt = p.callsAt[:k+1]
			p.calls = p.calls[:p.callsAt[k]]
		}
	}
}

// Reset clears the output, as for a retried generation.
func (p *StreamFunctionCallParser) Reset() {
	p.output.Reset()
	p.escaped.reset()
	p.legacy.reset()
	p.watch.reset()
	p.calls, p.callsAt = nil, nil
}

// Parse returns the function calls of the output so far.
func (p *StreamFunctionCallParser) Parse() []FuncCallResults {
	output := p.output.String()
	if !p.incremental {
		return ParseFunctionCall(CleanupLLMResult(output, p.config), p.config)
	}
	// EscapeNewLines keeps the bytes before the first quote, so the escaped
	// output opens as the output does.
	if opens, known := opensWithJSON(output); opens || !known {
		return ParseFunctionCall(output, p.config)
	}
	// ParseJSON returns parseJSONLegacy of such an output, as
	// ParseJSONIterative does and then its own fallback.
	objs, _ := p.legacy.scan(p.escaped.String())
	if p.callsAt == nil {
		p.callsAt = []int{0}
	}
	for k := len(p.callsAt) - 1; k < p.legacy.stable(); k++ {
		p.calls = appendFunctionCalls(p.calls, objs[k:k+1], p.config)
		p.callsAt = append(p.callsAt, len(p.calls))
	}
	results := make([]FuncCallResults, len(p.calls), len(p.calls)+1)
	copy(results, p.calls)
	results = appendFunctionCalls(results, objs[p.legacy.stable():], p.config)
	p.watch.update(output)
	if !p.watch.seen {
		return results
	}
	return mergeMarkupFunctionCalls(output, p.config, results)
}
