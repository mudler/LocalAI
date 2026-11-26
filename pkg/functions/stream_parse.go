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
type StreamToolCallParser struct {
	config    FunctionsConfig
	xmlFormat *XMLToolCallFormat

	output     strings.Builder
	hasTrigger bool
	dirty      bool
	last       StreamToolCalls
}

// NewStreamToolCallParser returns a parser for the given function config.
func NewStreamToolCallParser(config FunctionsConfig) *StreamToolCallParser {
	p := &StreamToolCallParser{config: config}
	if config.XMLFormat != nil {
		p.xmlFormat = config.XMLFormat
	} else if config.XMLFormatPreset != "" {
		p.xmlFormat = GetXMLFormatPreset(config.XMLFormatPreset)
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
	cleaned := CleanupLLMResult(p.output.String(), p.config)
	if calls, err := ParseXMLIterative(cleaned, p.xmlFormat, true); err == nil && len(calls) > 0 {
		p.last = StreamToolCalls{XML: calls}
		return p.last
	}
	calls, err := ParseJSONIterative(cleaned, true)
	p.last = StreamToolCalls{JSON: calls, JSONErr: err}
	return p.last
}
