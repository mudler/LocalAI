package functions

import (
	"strings"

	"github.com/mudler/LocalAI/pkg/functions/peg"
	"github.com/mudler/xlog"
)

// PEGFormatType identifies the format type for PEG parsing.
type PEGFormatType int

const (
	FormatJSONNative    PEGFormatType = iota
	FormatTagWithJSON                 // <function=name>{"key": "val"}</function>
	FormatTagWithTagged               // <function=name><param=key>value</param></function>
)

// ParseFunctionCallPEG attempts to parse tool calls using the PEG parser.
// Returns nil if no tool calls were found.
func ParseFunctionCallPEG(llmresult string, config FunctionsConfig) []FuncCallResults {
	xlog.Debug("[PEG] starting PEG tool call parsing")

	// If auto-detected markers from the C++ backend are available, use them first
	if config.ToolFormatMarkers != nil {
		m := config.ToolFormatMarkers
		xlog.Debug("[PEG] using auto-detected markers from C++ backend",
			"format_type", m.FormatType,
			"section_start", m.SectionStart,
			"section_end", m.SectionEnd,
			"per_call_start", m.PerCallStart,
			"per_call_end", m.PerCallEnd,
			"func_name_prefix", m.FuncNamePrefix,
			"func_name_suffix", m.FuncNameSuffix,
			"func_close", m.FuncClose,
			"arg_name_prefix", m.ArgNamePrefix,
			"arg_name_suffix", m.ArgNameSuffix,
			"arg_value_prefix", m.ArgValuePrefix,
			"arg_value_suffix", m.ArgValueSuffix,
			"arg_separator", m.ArgSeparator,
			"name_field", m.NameField,
			"args_field", m.ArgsField,
			"id_field", m.IDField,
			"reasoning_start", m.ReasoningStart,
			"reasoning_end", m.ReasoningEnd,
		)
		arena := BuildPEGParserFromMarkers(config.ToolFormatMarkers)
		if arena != nil {
			results := parsePEG(arena, llmresult)
			if len(results) > 0 {
				xlog.Debug("[PEG] markers-based parser matched", "count", len(results))
				return results
			}
			xlog.Debug("[PEG] markers-based parser found no tool calls")
		} else {
			xlog.Debug("[PEG] failed to build parser from markers")
		}
	}

	// If a specific XML format preset is set, use its PEG format
	if config.XMLFormatPreset != "" {
		xlog.Debug("[PEG] trying XML format preset", "preset", config.XMLFormatPreset)
		preset := GetXMLFormatPreset(config.XMLFormatPreset)
		if preset != nil {
			pegType := classifyXMLFormat(preset)
			xlog.Debug("[PEG] classified preset", "preset", config.XMLFormatPreset, "peg_type", pegTypeName(pegType))
			arena := BuildPEGParserFromFormat(preset, pegType)
			if arena != nil {
				results := parsePEG(arena, llmresult)
				if len(results) > 0 {
					xlog.Debug("[PEG] preset parser matched", "preset", config.XMLFormatPreset, "count", len(results))
					return results
				}
				xlog.Debug("[PEG] preset parser found no tool calls", "preset", config.XMLFormatPreset)
			}
		} else {
			xlog.Debug("[PEG] unknown preset name", "preset", config.XMLFormatPreset)
		}
	}

	// If a custom XML format is set, classify and try it
	if config.XMLFormat != nil {
		pegType := classifyXMLFormat(config.XMLFormat)
		xlog.Debug("[PEG] trying custom XML format", "peg_type", pegTypeName(pegType))
		arena := BuildPEGParserFromFormat(config.XMLFormat, pegType)
		if arena != nil {
			results := parsePEG(arena, llmresult)
			if len(results) > 0 {
				xlog.Debug("[PEG] custom format parser matched", "count", len(results))
				return results
			}
			xlog.Debug("[PEG] custom format parser found no tool calls")
		}
	}

	// Auto-detect: try all three format types
	xlog.Debug("[PEG] auto-detecting format across all presets")
	for _, pegType := range []PEGFormatType{FormatJSONNative, FormatTagWithJSON, FormatTagWithTagged} {
		for _, preset := range getAllXMLFormats() {
			classified := classifyXMLFormat(preset.format)
			if classified != pegType {
				continue
			}
			arena := BuildPEGParserFromFormat(preset.format, pegType)
			if arena == nil {
				continue
			}
			results := parsePEG(arena, llmresult)
			if len(results) > 0 {
				xlog.Debug("[PEG] auto-detect matched", "preset", preset.name, "peg_type", pegTypeName(pegType), "count", len(results))
				return results
			}
		}
	}

	xlog.Debug("[PEG] no tool calls found by any format")
	return nil
}

func pegTypeName(t PEGFormatType) string {
	switch t {
	case FormatJSONNative:
		return "json_native"
	case FormatTagWithJSON:
		return "tag_with_json"
	case FormatTagWithTagged:
		return "tag_with_tagged"
	default:
		return "unknown"
	}
}

// classifyXMLFormat determines the PEG format type from an XML format config.
func classifyXMLFormat(f *XMLToolCallFormat) PEGFormatType {
	// If there's an explicit function opener like "<function=", it's a tag-based format
	hasTagOpener := f.ToolStart != "" && f.ToolSep != ""

	if f.RawArgVal != nil && !*f.RawArgVal {
		// JSON-only args
		if hasTagOpener {
			return FormatTagWithJSON
		}
		if f.KeyStart == "" || f.KeyStart == "\"" {
			return FormatJSONNative
		}
		return FormatTagWithJSON
	}
	if f.KeyStart != "" {
		return FormatTagWithTagged
	}
	return FormatTagWithJSON
}

// BuildPEGParserFromFormat builds a PEG parser arena from an XML format config.
func BuildPEGParserFromFormat(f *XMLToolCallFormat, pegType PEGFormatType) *peg.Arena {
	switch pegType {
	case FormatTagWithTagged, FormatTagWithJSON:
		return buildTaggedPEGParser(f)
	case FormatJSONNative:
		return buildJSONNativePEGParser(f)
	default:
		return nil
	}
}

func buildTaggedPEGParser(f *XMLToolCallFormat) *peg.Arena {
	markers := map[string]string{}

	funcOpener := f.ToolStart
	funcNameSuffix := f.ToolSep
	funcCloser := f.ToolEnd

	hasScope := f.ScopeStart != ""

	if hasScope {
		markers["tool_call_start_marker"] = f.ScopeStart
		markers["tool_call_end_marker"] = f.ScopeEnd
	}

	markers["function_opener"] = funcOpener
	markers["function_name_suffix"] = funcNameSuffix
	markers["function_closer"] = funcCloser

	// Always set parameter markers explicitly to avoid relying on defaults.
	// Formats without tagged params (e.g., functionary) need empty strings.
	markers["parameter_key_prefix"] = f.KeyStart
	markers["parameter_key_suffix"] = f.KeyValSep
	markers["parameter_closer"] = f.ValEnd

	// Determine what to use as the content delimiter
	contentDelim := f.ScopeStart
	if contentDelim == "" {
		contentDelim = f.ToolStart
	}

	return peg.BuildChatPegParser(func(p *peg.ChatBuilder) peg.ParserID {
		tools := []peg.ToolDef{} // empty = accept anything
		content := p.Content(p.Until(contentDelim))

		if hasScope {
			// With scope markers: use StandardConstructedTools which wraps in scope
			toolCall := p.StandardConstructedTools(markers, tools, true, false)
			return p.Seq(content, p.Optional(p.Seq(p.Space(), toolCall)), p.End())
		}

		// No scope markers (e.g., functionary): build tool parser directly without scope wrapper
		hasTaggedParams := f.KeyStart != ""
		var args peg.ParserID
		if hasTaggedParams {
			paramKeyPrefix := f.KeyStart
			paramKeySuffix := f.KeyValSep
			paramCloser := f.ValEnd
			argRule := p.ToolArg(p.Seq(
				p.ToolArgOpen(p.Literal(paramKeyPrefix)),
				p.ToolArgName(p.Until(paramKeySuffix)),
				p.Literal(paramKeySuffix),
				p.ToolArgValue(p.Until(paramCloser)),
				p.ToolArgClose(p.Literal(paramCloser)),
			))
			args = p.ToolArgs(p.ZeroOrMore(p.Seq(argRule, p.Space())))
		} else {
			// JSON arguments
			args = p.ToolArgs(p.Until(funcCloser))
		}

		toolParser := p.Tool(p.Seq(
			p.ToolOpen(p.Seq(
				p.Literal(funcOpener),
				p.ToolName(p.Until(funcNameSuffix)),
				p.Literal(funcNameSuffix),
			)),
			p.Space(),
			args,
			p.Space(),
			p.ToolClose(p.Literal(funcCloser)),
		))

		toolCall := p.TriggerRule("tool-call", p.OneOrMore(p.Seq(toolParser, p.Space())))
		return p.Seq(content, p.Optional(p.Seq(p.Space(), toolCall)), p.End())
	})
}

func buildJSONNativePEGParser(f *XMLToolCallFormat) *peg.Arena {
	sectionStart := f.ScopeStart
	sectionEnd := f.ScopeEnd

	if sectionStart == "" && f.ToolStart != "" {
		sectionStart = f.ToolStart
	}
	if sectionEnd == "" && f.ToolEnd != "" {
		sectionEnd = f.ToolEnd
	}

	if sectionStart == "" || sectionEnd == "" {
		return nil
	}

	return peg.BuildChatPegParser(func(p *peg.ChatBuilder) peg.ParserID {
		// For JSON native, tool call is { "name": ..., "arguments": ... }
		// Build a generic parser that accepts any JSON tool call
		toolCall := p.TriggerRule("tool-call", p.Seq(
			p.Literal(sectionStart), p.Space(),
			p.Tool(p.Seq(
				p.ToolOpen(p.Literal("{")), p.Space(),
				p.ZeroOrMore(p.Seq(
					p.Choice(
						p.Seq(
							p.Literal("\"name\""), p.Space(), p.Literal(":"), p.Space(),
							p.Literal("\""), p.ToolName(p.JSONString()), p.Literal("\""),
						),
						p.Seq(
							p.Literal("\"arguments\""), p.Space(), p.Literal(":"), p.Space(),
							p.ToolArgs(p.JSON()),
						),
						p.Seq(
							p.Literal("\""), p.JSONString(), p.Literal("\""), p.Space(),
							p.Literal(":"), p.Space(), p.JSON(),
						),
					),
					p.Optional(p.Seq(p.Space(), p.Literal(","), p.Space())),
				)),
				p.Space(), p.ToolClose(p.Literal("}")),
			)),
			p.Space(), p.Literal(sectionEnd),
		))

		content := p.Content(p.Until(sectionStart))
		return p.Seq(content, p.Optional(p.Seq(p.Space(), toolCall)), p.End())
	})
}

// BuildPEGParserFromMarkers builds a PEG parser from auto-detected C++ autoparser markers.
func BuildPEGParserFromMarkers(m *ToolFormatMarkers) *peg.Arena {
	switch m.FormatType {
	case "tag_with_json":
		return buildPEGFromMarkersTagJSON(m)
	case "tag_with_tagged":
		return buildPEGFromMarkersTagTagged(m)
	case "json_native":
		return buildPEGFromMarkersJSONNative(m)
	default:
		return nil
	}
}

func buildPEGFromMarkersTagJSON(m *ToolFormatMarkers) *peg.Arena {
	markers := map[string]string{}

	// Use section markers if available, otherwise fall back to per-call markers
	scopeStart, scopeEnd := effectiveScope(m)

	if scopeStart != "" {
		markers["tool_call_start_marker"] = scopeStart
		markers["tool_call_end_marker"] = scopeEnd
	}

	markers["function_opener"] = strings.TrimRight(m.FuncNamePrefix, " \t\n")
	markers["function_name_suffix"] = strings.TrimRight(m.FuncNameSuffix, " \t\n")
	markers["function_closer"] = strings.TrimRight(m.FuncClose, " \t\n")
	markers["parameter_key_prefix"] = ""
	markers["parameter_key_suffix"] = ""
	markers["parameter_closer"] = ""

	if m.CallIDPosition == "between_func_and_args" {
		markers["call_id_prefix"] = m.CallIDPrefix
		markers["call_id_suffix"] = m.CallIDSuffix
	}

	contentDelim := scopeStart
	if contentDelim == "" {
		contentDelim = strings.TrimRight(m.FuncNamePrefix, " \t\n")
	}

	return peg.BuildChatPegParser(func(p *peg.ChatBuilder) peg.ParserID {
		tools := []peg.ToolDef{}
		content := p.Content(p.Until(contentDelim))

		if scopeStart != "" {
			toolCall := p.StandardConstructedTools(markers, tools, true, false)
			return p.Seq(content, p.Optional(p.Seq(p.Space(), toolCall)), p.End())
		}

		// No scope: build tool parser directly
		funcOpener := m.FuncNamePrefix
		funcNameSuffix := m.FuncNameSuffix
		funcCloser := m.FuncClose

		args := p.ToolArgs(p.Until(funcCloser))

		// Build call ID section if detected
		callIDSection := buildCallIDParser(p, m)

		toolParser := p.Tool(p.Seq(
			p.ToolOpen(p.Seq(
				p.Literal(funcOpener),
				p.ToolName(p.Until(funcNameSuffix)),
				p.Literal(funcNameSuffix),
			)),
			callIDSection,
			p.Space(),
			args,
			p.Space(),
			p.ToolClose(p.Literal(funcCloser)),
		))
		toolCall := p.TriggerRule("tool-call", p.OneOrMore(p.Seq(toolParser, p.Space())))
		return p.Seq(content, p.Optional(p.Seq(p.Space(), toolCall)), p.End())
	})
}

func buildPEGFromMarkersTagTagged(m *ToolFormatMarkers) *peg.Arena {
	markers := map[string]string{}

	// Use section markers if available, otherwise fall back to per-call markers
	scopeStart, scopeEnd := effectiveScope(m)

	if scopeStart != "" {
		markers["tool_call_start_marker"] = scopeStart
		markers["tool_call_end_marker"] = scopeEnd
	}

	// Trim trailing whitespace from markers — the PEG Space() parser
	// handles whitespace between elements, so baked-in \n would cause mismatches.
	markers["function_opener"] = strings.TrimRight(m.FuncNamePrefix, " \t\n")
	markers["function_name_suffix"] = strings.TrimRight(m.FuncNameSuffix, " \t\n")
	markers["function_closer"] = strings.TrimRight(m.FuncClose, " \t\n")
	markers["parameter_key_prefix"] = strings.TrimRight(m.ArgNamePrefix, " \t\n")
	markers["parameter_key_suffix"] = strings.TrimRight(m.ArgNameSuffix, " \t\n")
	markers["parameter_closer"] = strings.TrimRight(m.ArgValueSuffix, " \t\n")

	if m.CallIDPosition == "between_func_and_args" {
		markers["call_id_prefix"] = m.CallIDPrefix
		markers["call_id_suffix"] = m.CallIDSuffix
	}

	contentDelim := scopeStart
	if contentDelim == "" {
		contentDelim = strings.TrimRight(m.FuncNamePrefix, " \t\n")
	}

	return peg.BuildChatPegParser(func(p *peg.ChatBuilder) peg.ParserID {
		tools := []peg.ToolDef{}
		content := p.Content(p.Until(contentDelim))
		toolCall := p.StandardConstructedTools(markers, tools, true, false)
		return p.Seq(content, p.Optional(p.Seq(p.Space(), toolCall)), p.End())
	})
}

func buildPEGFromMarkersJSONNative(m *ToolFormatMarkers) *peg.Arena {
	sectionStart, sectionEnd := effectiveScope(m)

	if sectionStart == "" || sectionEnd == "" {
		return nil
	}

	nameKey := m.NameField
	if nameKey == "" {
		nameKey = "name"
	}
	argsKey := m.ArgsField
	if argsKey == "" {
		argsKey = "arguments"
	}

	idField := m.IDField
	genIDField := m.GenIDField

	return peg.BuildChatPegParser(func(p *peg.ChatBuilder) peg.ParserID {
		// Build field matchers for known keys
		knownFields := []peg.ParserID{
			p.Seq(
				p.Literal("\""+nameKey+"\""), p.Space(), p.Literal(":"), p.Space(),
				p.Literal("\""), p.ToolName(p.JSONString()), p.Literal("\""),
			),
			p.Seq(
				p.Literal("\""+argsKey+"\""), p.Space(), p.Literal(":"), p.Space(),
				p.ToolArgs(p.JSON()),
			),
		}

		// Add ID field matching if detected
		if idField != "" {
			knownFields = append(knownFields, p.Seq(
				p.Literal("\""+idField+"\""), p.Space(), p.Literal(":"), p.Space(),
				p.Literal("\""), p.ToolID(p.JSONString()), p.Literal("\""),
			))
		}
		if genIDField != "" && genIDField != idField {
			knownFields = append(knownFields, p.Seq(
				p.Literal("\""+genIDField+"\""), p.Space(), p.Literal(":"), p.Space(),
				p.Literal("\""), p.ToolID(p.JSONString()), p.Literal("\""),
			))
		}

		// Catch-all for unknown JSON fields
		knownFields = append(knownFields, p.Seq(
			p.Literal("\""), p.JSONString(), p.Literal("\""), p.Space(),
			p.Literal(":"), p.Space(), p.JSON(),
		))

		// Build a generic JSON tool call parser that accepts any tool
		toolCall := p.TriggerRule("tool-call", p.Seq(
			p.Literal(sectionStart), p.Space(),
			p.Tool(p.Seq(
				p.ToolOpen(p.Literal("{")), p.Space(),
				p.ZeroOrMore(p.Seq(
					p.Choice(knownFields...),
					p.Optional(p.Seq(p.Space(), p.Literal(","), p.Space())),
				)),
				p.Space(), p.ToolClose(p.Literal("}")),
			)),
			p.Space(), p.Literal(sectionEnd),
		))
		content := p.Content(p.Until(sectionStart))
		return p.Seq(content, p.Optional(p.Seq(p.Space(), toolCall)), p.End())
	})
}

// effectiveScope returns the scope start/end markers to use.
// Prefers section markers, falls back to per-call markers, stripping trailing
// whitespace so the PEG Space() parser can handle it flexibly.
func effectiveScope(m *ToolFormatMarkers) (string, string) {
	if m.SectionStart != "" {
		return strings.TrimRight(m.SectionStart, " \t\n"), strings.TrimRight(m.SectionEnd, " \t\n")
	}
	if m.PerCallStart != "" {
		return strings.TrimRight(m.PerCallStart, " \t\n"), strings.TrimRight(m.PerCallEnd, " \t\n")
	}
	return "", ""
}

// buildCallIDParser creates a parser for call ID markers based on position.
// Currently only BETWEEN_FUNC_AND_ARGS is supported (matching llama.cpp behavior).
func buildCallIDParser(p *peg.ChatBuilder, m *ToolFormatMarkers) peg.ParserID {
	if m.CallIDPosition == "between_func_and_args" && m.CallIDPrefix != "" && m.CallIDSuffix != "" {
		return p.Optional(p.Seq(
			p.Literal(m.CallIDPrefix),
			p.ToolID(p.Until(m.CallIDSuffix)),
			p.Literal(m.CallIDSuffix),
		))
	}
	return p.Eps()
}

// parsePEG runs the PEG parser and extracts tool call results.
func parsePEG(arena *peg.Arena, input string) []FuncCallResults {
	ctx := peg.NewParseContext(input, false)
	result := arena.Parse(ctx)

	if result.Type != peg.Success {
		inputPreview := input
		if len(inputPreview) > 200 {
			inputPreview = inputPreview[:200] + "..."
		}
		xlog.Debug("[PEG] parse did not succeed", "result_type", result.Type, "input_preview", inputPreview)
		return nil
	}

	mapper := &peg.ChatPegMapper{}
	mapper.FromAST(&ctx.Ast, &result)
	msg := mapper.Result

	xlog.Debug("[PEG] parse succeeded", "content_len", len(msg.Content), "reasoning_len", len(msg.ReasoningContent), "tool_calls", len(msg.ToolCalls))

	if len(msg.ToolCalls) == 0 {
		return nil
	}

	var results []FuncCallResults
	for _, tc := range msg.ToolCalls {
		xlog.Debug("[PEG] extracted tool call", "name", tc.Name, "id", tc.ID, "args_len", len(tc.Arguments))
		results = append(results, FuncCallResults{
			Name:      tc.Name,
			Arguments: tc.Arguments,
			ID:        tc.ID,
		})
	}
	return results
}
