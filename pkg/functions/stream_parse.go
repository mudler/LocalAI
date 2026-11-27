package functions

import (
	"strings"
)

// toolCallTriggers are the bytes without which the partial parse finds no tool
// call: a JSON call needs an object, and every XML format starts its scope or
// its call with a tag. Until one arrives, parsing the growing output again for
// each token finds nothing, at a cost that grows with the output.
const toolCallTriggers = "<{["

// StreamToolCalls is the result of the partial tool-call parse of a streamed
// output. XML holds the calls when the XML parse found some. Otherwise JSON and
// JSONErr hold the result of the JSON parse.
type StreamToolCalls struct {
	XML     []FuncCallResults
	JSON    []map[string]any
	JSONErr error
}

// StreamToolCallParser runs the partial tool-call parse of a streamed output
// as the output grows: CleanupLLMResult, ParseXMLIterative, and
// ParseJSONIterative when the XML parse finds nothing, on the whole output. It
// skips the parse when its result cannot have changed, when nothing was
// written since the last parse, and returns no calls without parsing while the
// output has no byte that can start a tool call.
//
// Without replacement rules the output is only appended to, and two parses
// keep their work across tokens, with the same result as a parse of the whole
// output: the XML parse is skipped until the output holds a start tag of the
// format (xmlStreamGate), and the JSON parse of an output that does not open
// with an object or array resumes from its last stable step (legacyJSONScan).
type StreamToolCallParser struct {
	config    FunctionsConfig
	xmlFormat *XMLToolCallFormat

	output     strings.Builder
	hasTrigger bool
	dirty      bool
	last       StreamToolCalls

	// incremental is set when the cleaned output is the output itself.
	incremental bool
	xmlGate     xmlStreamGate
	legacy      legacyJSONScan
}

// NewStreamToolCallParser returns a parser for the given function config.
func NewStreamToolCallParser(config FunctionsConfig) *StreamToolCallParser {
	p := &StreamToolCallParser{config: config}
	if config.XMLFormat != nil {
		p.xmlFormat = config.XMLFormat
	} else if config.XMLFormatPreset != "" {
		p.xmlFormat = GetXMLFormatPreset(config.XMLFormatPreset)
	}
	p.incremental = len(config.ReplaceLLMResult) == 0
	if p.incremental {
		p.xmlGate = newXMLStreamGate(p.xmlFormat)
	}
	return p
}

// Write appends a token to the output.
func (p *StreamToolCallParser) Write(token string) {
	if token == "" {
		return
	}
	p.output.WriteString(token)
	p.dirty = true
	if !p.hasTrigger && strings.ContainsAny(token, toolCallTriggers) {
		p.hasTrigger = true
	}
}

// Reset clears the output, as for a retried generation.
func (p *StreamToolCallParser) Reset() {
	p.output.Reset()
	p.hasTrigger = false
	p.dirty = false
	p.last = StreamToolCalls{}
	p.xmlGate.reset()
	p.legacy.reset()
}

// Parse returns the partial tool calls of the output so far.
func (p *StreamToolCallParser) Parse() StreamToolCalls {
	if !p.dirty {
		return p.last
	}
	p.dirty = false
	// A replacement rule can write any text, triggers included, so the skip
	// holds only for the output as the model wrote it.
	if !p.hasTrigger && len(p.config.ReplaceLLMResult) == 0 {
		p.last = StreamToolCalls{}
		return p.last
	}
	cleaned := p.Cleaned()
	if calls, err := p.parseXML(cleaned); err == nil && len(calls) > 0 {
		p.last = StreamToolCalls{XML: calls}
		return p.last
	}
	calls, err := p.parseJSON(cleaned, true)
	p.last = StreamToolCalls{JSON: calls, JSONErr: err}
	return p.last
}

// Cleaned returns CleanupLLMResult of the output so far.
func (p *StreamToolCallParser) Cleaned() string {
	if p.incremental {
		return p.output.String()
	}
	return CleanupLLMResult(p.output.String(), p.config)
}

// ParseXML returns ParseXMLIterative(p.Cleaned(), format, true), with the
// format of the config.
func (p *StreamToolCallParser) ParseXML() ([]FuncCallResults, error) {
	return p.parseXML(p.Cleaned())
}

// ParseJSON returns ParseJSONIterative(p.Cleaned(), isPartial).
func (p *StreamToolCallParser) ParseJSON(isPartial bool) ([]map[string]any, error) {
	return p.parseJSON(p.Cleaned(), isPartial)
}

func (p *StreamToolCallParser) parseXML(cleaned string) ([]FuncCallResults, error) {
	if p.incremental && p.xmlGate.empty(cleaned) {
		return []FuncCallResults{}, nil
	}
	return ParseXMLIterative(cleaned, p.xmlFormat, true)
}

func (p *StreamToolCallParser) parseJSON(cleaned string, isPartial bool) ([]map[string]any, error) {
	if p.incremental {
		// ParseJSONIterative starts with the iterative parser at the first
		// byte, which gives up on one that opens no object or array, and the
		// parse is then parseJSONLegacy of the whole output, as it is for
		// every output that starts like this one.
		if opens, known := opensWithJSON(cleaned); known && !opens {
			return p.legacy.scan(cleaned)
		}
	}
	return ParseJSONIterative(cleaned, isPartial)
}

// StreamOutput keeps the output of a stream for the endpoints that parse it
// for tool calls on every token, and tells when a parse cannot find one yet.
type StreamOutput struct {
	b          strings.Builder
	hasTrigger bool
}

// Write appends a token.
func (o *StreamOutput) Write(token string) {
	o.b.WriteString(token)
	if !o.hasTrigger && strings.ContainsAny(token, toolCallTriggers) {
		o.hasTrigger = true
	}
}

// String returns the output so far.
func (o *StreamOutput) String() string { return o.b.String() }

// Len returns the length of the output so far.
func (o *StreamOutput) Len() int { return o.b.Len() }

// MayHoldPartialToolCall reports whether CleanupLLMResult followed by
// ParseXMLIterative or ParseJSONIterative can find a tool call in the output.
// Without a '<', '{' or '[' they cannot, unless a replacement rule can write
// one or a custom XML format starts its calls with another byte.
func (o *StreamOutput) MayHoldPartialToolCall(config FunctionsConfig) bool {
	return o.hasTrigger || len(config.ReplaceLLMResult) > 0 || config.XMLFormat != nil
}

// MayHoldFunctionCall reports whether CleanupLLMResult followed by
// ParseFunctionCall can find a tool call in the output. It answers yes for
// every config that can match text without a '<', '{' or '[': replacement
// rules, response regexes, JSON match regexes, a custom XML format, and
// backend markers that can match without one (markersMatchWithoutTrigger).
func (o *StreamOutput) MayHoldFunctionCall(config FunctionsConfig) bool {
	return o.MayHoldPartialToolCall(config) ||
		len(config.ReplaceFunctionResults) > 0 ||
		len(config.ResponseRegex) > 0 ||
		len(config.JSONRegexMatch) > 0 ||
		markersMatchWithoutTrigger(config.ToolFormatMarkers)
}

// markersMatchWithoutTrigger reports whether the parser built from a
// backend's tool-format markers can find a call in output that has no '<',
// '{' or '['. A call needs its start marker (the section, per-call or
// function-name prefix), and a JSON call needs an object, so it can only when
// a start marker lacks those bytes, or a tag format has no start marker.
func markersMatchWithoutTrigger(m *ToolFormatMarkers) bool {
	if m == nil {
		return false
	}
	hasStart := false
	for _, start := range []string{m.SectionStart, m.PerCallStart, m.FuncNamePrefix} {
		if start == "" {
			continue
		}
		hasStart = true
		if !strings.ContainsAny(start, toolCallTriggers) {
			return true
		}
	}
	return !hasStart && m.FormatType != "json_native"
}
