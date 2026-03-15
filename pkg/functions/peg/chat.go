package peg

import "encoding/json"

// Tag constants matching llama.cpp
const (
	TagReasoningBlock = "reasoning-block"
	TagReasoning      = "reasoning"
	TagContent        = "content"
	TagTool           = "tool"
	TagToolOpen       = "tool-open"
	TagToolClose      = "tool-close"
	TagToolID         = "tool-id"
	TagToolName       = "tool-name"
	TagToolArgs       = "tool-args"
	TagToolArg        = "tool-arg"
	TagToolArgOpen    = "tool-arg-open"
	TagToolArgClose   = "tool-arg-close"
	TagToolArgName    = "tool-arg-name"
	TagToolArgValue   = "tool-arg-value"
	TagToolArgStrVal  = "tool-arg-string-value"
)

// ChatBuilder extends Builder with chat-specific tag helpers.
type ChatBuilder struct {
	*Builder
}

func NewChatBuilder() *ChatBuilder {
	return &ChatBuilder{Builder: NewBuilder()}
}

// Semantic tag wrappers
func (cb *ChatBuilder) ReasoningBlock(child ParserID) ParserID {
	return cb.Tag(TagReasoningBlock, child)
}
func (cb *ChatBuilder) Reasoning(child ParserID) ParserID {
	return cb.Tag(TagReasoning, child)
}
func (cb *ChatBuilder) Content(child ParserID) ParserID {
	return cb.Tag(TagContent, child)
}
func (cb *ChatBuilder) Tool(child ParserID) ParserID {
	return cb.Tag(TagTool, child)
}
func (cb *ChatBuilder) ToolOpen(child ParserID) ParserID {
	return cb.Atomic(cb.Tag(TagToolOpen, child))
}
func (cb *ChatBuilder) ToolClose(child ParserID) ParserID {
	return cb.Atomic(cb.Tag(TagToolClose, child))
}
func (cb *ChatBuilder) ToolID(child ParserID) ParserID {
	return cb.Atomic(cb.Tag(TagToolID, child))
}
func (cb *ChatBuilder) ToolName(child ParserID) ParserID {
	return cb.Atomic(cb.Tag(TagToolName, child))
}
func (cb *ChatBuilder) ToolArgs(child ParserID) ParserID {
	return cb.Tag(TagToolArgs, child)
}
func (cb *ChatBuilder) ToolArg(child ParserID) ParserID {
	return cb.Tag(TagToolArg, child)
}
func (cb *ChatBuilder) ToolArgOpen(child ParserID) ParserID {
	return cb.Atomic(cb.Tag(TagToolArgOpen, child))
}
func (cb *ChatBuilder) ToolArgClose(child ParserID) ParserID {
	return cb.Atomic(cb.Tag(TagToolArgClose, child))
}
func (cb *ChatBuilder) ToolArgName(child ParserID) ParserID {
	return cb.Atomic(cb.Tag(TagToolArgName, child))
}
func (cb *ChatBuilder) ToolArgValue(child ParserID) ParserID {
	return cb.Tag(TagToolArgValue, child)
}
func (cb *ChatBuilder) ToolArgStringValue(child ParserID) ParserID {
	return cb.Tag(TagToolArgStrVal, child)
}
func (cb *ChatBuilder) ToolArgJSONValue(child ParserID) ParserID {
	return cb.Atomic(cb.Tag(TagToolArgValue, child))
}

// TagWithSafeContent creates content parsing that avoids a marker string.
func (cb *ChatBuilder) TagWithSafeContent(tagName, marker string, p ParserID) ParserID {
	if marker == "" {
		return cb.ZeroOrMore(cb.Choice(p,
			cb.Rule(tagName, cb.Content(cb.Any())),
		))
	}
	contentChunk := cb.Rule(tagName,
		cb.Content(cb.Seq(
			cb.Negate(cb.Literal(marker)),
			cb.Any(),
			cb.Until(marker),
		)),
	)
	return cb.ZeroOrMore(cb.Choice(p, contentChunk))
}

// ToolDef holds a tool definition used to build parsers.
type ToolDef struct {
	Name       string
	Properties map[string]PropDef
}

// PropDef holds a property definition for tool arguments.
type PropDef struct {
	Type string
}

// StandardConstructedTools builds XML/tagged-style tool parsers.
func (cb *ChatBuilder) StandardConstructedTools(
	markers map[string]string,
	tools []ToolDef,
	parallelToolCalls bool,
	forceToolCalls bool,
) ParserID {
	getMarker := func(key, defaultVal string) string {
		if v, ok := markers[key]; ok {
			return v
		}
		return defaultVal
	}

	sectionStart := getMarker("tool_call_start_marker", "<tool_call>")
	sectionEnd := getMarker("tool_call_end_marker", "</tool_call>")
	funcOpener := getMarker("function_opener", "<function=")
	funcNameSuffix := getMarker("function_name_suffix", ">")
	funcCloser := getMarker("function_closer", "</function>")
	paramKeyPrefix := getMarker("parameter_key_prefix", "<param=")
	paramKeySuffix := getMarker("parameter_key_suffix", ">")
	paramCloser := getMarker("parameter_closer", "</param>")
	callIDPrefix := getMarker("call_id_prefix", "")
	callIDSuffix := getMarker("call_id_suffix", "")

	hasTaggedParams := paramKeyPrefix != ""

	var toolChoices []ParserID

	if len(tools) == 0 {
		// Generic parser: accept any function name
		var args ParserID
		if hasTaggedParams {
			// Tagged parameters: <param=key>value</param>
			argRule := cb.ToolArg(cb.Seq(
				cb.ToolArgOpen(cb.Literal(paramKeyPrefix)),
				cb.ToolArgName(cb.Until(paramKeySuffix)),
				cb.Literal(paramKeySuffix),
				cb.ToolArgValue(cb.Until(paramCloser)),
				cb.ToolArgClose(cb.Literal(paramCloser)),
			))
			args = cb.ToolArgs(cb.ZeroOrMore(cb.Seq(argRule, cb.Space())))
		} else {
			// JSON arguments: {"key": "val"}
			args = cb.ToolArgs(cb.Until(funcCloser))
		}

		// Build optional call ID section (between function name and args)
		callIDSection := cb.Eps()
		if callIDPrefix != "" && callIDSuffix != "" {
			callIDSection = cb.Optional(cb.Seq(
				cb.Literal(callIDPrefix),
				cb.ToolID(cb.Until(callIDSuffix)),
				cb.Literal(callIDSuffix),
			))
		}

		toolParser := cb.Tool(cb.Seq(
			cb.ToolOpen(cb.Seq(
				cb.Literal(funcOpener),
				cb.ToolName(cb.Until(funcNameSuffix)),
				cb.Literal(funcNameSuffix),
			)),
			callIDSection,
			cb.Space(),
			args,
			cb.Space(),
			cb.ToolClose(cb.Literal(funcCloser)),
		))

		toolChoices = append(toolChoices, cb.Rule("tool-generic", toolParser))
	} else {
		for _, tool := range tools {
			// Build argument parsers
			args := cb.Eps()
			if hasTaggedParams && len(tool.Properties) > 0 {
				var argParsers []ParserID
				for propName := range tool.Properties {
					argNameParser := cb.Choice(
						cb.Literal(propName),
						cb.Literal("\""+propName+"\""),
						cb.Literal("'"+propName+"'"),
					)

					argRule := cb.ToolArg(cb.Seq(
						cb.ToolArgOpen(cb.Literal(paramKeyPrefix)),
						cb.ToolArgName(argNameParser),
						cb.Literal(paramKeySuffix),
						cb.ToolArgValue(cb.Until(paramCloser)),
						cb.ToolArgClose(cb.Literal(paramCloser)),
					))
					argParsers = append(argParsers, argRule)
				}
				argChoice := cb.Choice(argParsers...)
				args = cb.ZeroOrMore(cb.Seq(argChoice, cb.Space()))
			} else if !hasTaggedParams {
				// JSON arguments
				args = cb.Until(funcCloser)
			}

			// Build optional call ID section
			toolCallIDSection := cb.Eps()
			if callIDPrefix != "" && callIDSuffix != "" {
				toolCallIDSection = cb.Optional(cb.Seq(
					cb.Literal(callIDPrefix),
					cb.ToolID(cb.Until(callIDSuffix)),
					cb.Literal(callIDSuffix),
				))
			}

			// Build function parser
			toolParser := cb.Tool(cb.Seq(
				cb.ToolOpen(cb.Seq(
					cb.Literal(funcOpener),
					cb.ToolName(cb.Literal(tool.Name)),
					cb.Literal(funcNameSuffix),
				)),
				toolCallIDSection,
				cb.Space(),
				cb.ToolArgs(args),
				cb.Space(),
				cb.ToolClose(cb.Literal(funcCloser)),
			))

			toolChoices = append(toolChoices, cb.Rule("tool-"+tool.Name, toolParser))
		}
	}

	toolChoice := cb.Choice(toolChoices...)

	var section ParserID
	if parallelToolCalls {
		section = cb.TriggerRule("tool-call", cb.Seq(
			cb.Literal(sectionStart), cb.Space(),
			cb.OneOrMore(cb.Seq(toolChoice, cb.Space())),
			cb.Literal(sectionEnd),
		))
	} else {
		section = cb.TriggerRule("tool-call", cb.Seq(
			cb.Literal(sectionStart), cb.Space(),
			toolChoice, cb.Space(),
			cb.Literal(sectionEnd),
		))
	}

	if forceToolCalls {
		return section
	}
	return cb.Optional(section)
}

// StandardJSONToolsOpts holds options for building JSON tool call parsers.
type StandardJSONToolsOpts struct {
	SectionStart    string
	SectionEnd      string
	Tools           []ToolDef
	ParallelCalls   bool
	ForceToolCalls  bool
	NameKey         string
	ArgsKey         string
	ArrayWrapped    bool
	FunctionIsKey   bool
	CallIDKey       string
	GenCallIDKey    string
	ParametersOrder []string
}

// StandardJSONTools builds JSON-format tool call parsers.
func (cb *ChatBuilder) StandardJSONTools(opts StandardJSONToolsOpts) ParserID {
	if len(opts.Tools) == 0 {
		return cb.Eps()
	}

	effectiveNameKey := opts.NameKey
	if effectiveNameKey == "" {
		effectiveNameKey = "name"
	}
	effectiveArgsKey := opts.ArgsKey
	if effectiveArgsKey == "" {
		effectiveArgsKey = "arguments"
	}

	var toolChoices ParserID
	if opts.FunctionIsKey {
		toolChoices = cb.buildJSONToolsFunctionIsKey(opts.Tools, opts.ArgsKey, effectiveArgsKey, opts.CallIDKey, opts.GenCallIDKey)
	} else {
		nameSpec := parseKeySpec(effectiveNameKey)
		argsSpec := parseKeySpec(effectiveArgsKey)
		if nameSpec.prefix != "" || argsSpec.prefix != "" {
			toolChoices = cb.buildJSONToolsNestedKeys(opts.Tools, effectiveNameKey, effectiveArgsKey, opts.CallIDKey, opts.GenCallIDKey)
		} else {
			toolChoices = cb.buildJSONToolsFlatKeys(opts.Tools, effectiveNameKey, effectiveArgsKey, opts.CallIDKey, opts.GenCallIDKey, opts.ParametersOrder)
		}
	}

	toolCalls := toolChoices
	if opts.ParallelCalls {
		toolCalls = cb.Seq(
			toolChoices,
			cb.ZeroOrMore(cb.Seq(cb.Space(), cb.Literal(","), cb.Space(), toolChoices)),
		)
	}

	if opts.ArrayWrapped {
		toolCalls = cb.Seq(cb.Literal("["), cb.Space(), toolCalls, cb.Space(), cb.Literal("]"))
	}

	section := cb.TriggerRule("tool-call", cb.Seq(
		cb.Literal(opts.SectionStart), cb.Space(),
		toolCalls, cb.Space(),
		cb.Literal(opts.SectionEnd),
	))

	if opts.ForceToolCalls {
		return section
	}
	return cb.Optional(section)
}

func (cb *ChatBuilder) buildJSONToolsFunctionIsKey(
	tools []ToolDef,
	argsKey, effectiveArgsKey, callIDKey, genCallIDKey string,
) ParserID {
	var toolChoices []ParserID

	for _, tool := range tools {
		var innerFields []ParserID

		if callIDKey != "" {
			idParser := cb.Atomic(cb.Seq(
				cb.Literal("\""+callIDKey+"\""), cb.Space(), cb.Literal(":"), cb.Space(),
				cb.Literal("\""), cb.ToolID(cb.JSONString()), cb.Literal("\""),
			))
			innerFields = append(innerFields, cb.Optional(cb.Seq(idParser, cb.Space(), cb.Optional(cb.Seq(cb.Literal(","), cb.Space())))))
		}

		if genCallIDKey != "" {
			genIDParser := cb.Atomic(cb.Seq(
				cb.Literal("\""+genCallIDKey+"\""), cb.Space(), cb.Literal(":"), cb.Space(),
				cb.Choice(
					cb.Seq(cb.Literal("\""), cb.ToolID(cb.JSONString()), cb.Literal("\"")),
					cb.ToolID(cb.JSONNumber()),
				),
			))
			innerFields = append(innerFields, cb.Optional(cb.Seq(genIDParser, cb.Space(), cb.Optional(cb.Seq(cb.Literal(","), cb.Space())))))
		}

		// Arguments
		var argsParser ParserID
		if argsKey == "" {
			argsParser = cb.ToolArgs(cb.JSON())
		} else {
			argsParser = cb.Seq(
				cb.Literal("\""+effectiveArgsKey+"\""), cb.Space(), cb.Literal(":"), cb.Space(),
				cb.ToolArgs(cb.JSON()),
			)
		}
		innerFields = append(innerFields, argsParser)

		// Build inner object
		var innerObject ParserID
		if argsKey == "" && len(innerFields) == 1 {
			innerObject = innerFields[0]
		} else {
			innerObject = cb.Literal("{")
			for i, f := range innerFields {
				innerObject = cb.Seq(innerObject, cb.Space(), f)
				if i < len(innerFields)-1 {
					innerObject = cb.Seq(innerObject, cb.Space())
				}
			}
			innerObject = cb.Seq(innerObject, cb.Space(), cb.Literal("}"))
		}

		toolParser := cb.Tool(cb.Seq(
			cb.ToolOpen(cb.Literal("{")), cb.Space(),
			cb.Literal("\""), cb.ToolName(cb.Literal(tool.Name)), cb.Literal("\""),
			cb.Space(), cb.Literal(":"), cb.Space(),
			innerObject,
			cb.Space(), cb.ToolClose(cb.Literal("}")),
		))

		toolChoices = append(toolChoices, cb.Rule("tool-"+tool.Name, toolParser))
	}

	return cb.Choice(toolChoices...)
}

// keySpec represents a dot-notation key split into prefix and field.
type keySpec struct {
	prefix string
	field  string
}

func parseKeySpec(key string) keySpec {
	for i, c := range key {
		if c == '.' {
			return keySpec{prefix: key[:i], field: key[i+1:]}
		}
	}
	return keySpec{field: key}
}

func (cb *ChatBuilder) buildJSONToolsNestedKeys(
	tools []ToolDef,
	effectiveNameKey, effectiveArgsKey, callIDKey, genCallIDKey string,
) ParserID {
	var toolChoices []ParserID

	nameSpec := parseKeySpec(effectiveNameKey)
	argsSpec := parseKeySpec(effectiveArgsKey)

	nestedPrefix := nameSpec.prefix
	if nestedPrefix == "" {
		nestedPrefix = argsSpec.prefix
	}
	nestedNameField := nameSpec.field
	if nameSpec.prefix == "" {
		nestedNameField = effectiveNameKey
	}
	nestedArgsField := argsSpec.field
	if argsSpec.prefix == "" {
		nestedArgsField = effectiveArgsKey
	}

	for _, tool := range tools {
		nestedName := cb.Seq(
			cb.Literal("\""+nestedNameField+"\""), cb.Space(), cb.Literal(":"), cb.Space(),
			cb.Literal("\""), cb.ToolName(cb.Literal(tool.Name)), cb.Literal("\""),
		)
		nestedArgs := cb.Seq(
			cb.Literal("\""+nestedArgsField+"\""), cb.Space(), cb.Literal(":"), cb.Space(),
			cb.ToolArgs(cb.JSON()),
		)
		nestedObject := cb.Seq(
			cb.Literal("{"), cb.Space(),
			nestedName, cb.Space(), cb.Literal(","), cb.Space(),
			nestedArgs,
			cb.Space(), cb.Literal("}"),
		)

		toolParserBody := cb.ToolOpen(cb.Literal("{"))

		if callIDKey != "" {
			idSpec := parseKeySpec(callIDKey)
			if idSpec.prefix == "" {
				idParser := cb.Atomic(cb.Seq(
					cb.Literal("\""+callIDKey+"\""), cb.Space(), cb.Literal(":"), cb.Space(),
					cb.Literal("\""), cb.ToolID(cb.JSONString()), cb.Literal("\""),
				))
				toolParserBody = cb.Seq(toolParserBody, cb.Space(),
					cb.Optional(cb.Seq(idParser, cb.Space(), cb.Literal(","), cb.Space())))
			}
		}

		if genCallIDKey != "" {
			genIDSpec := parseKeySpec(genCallIDKey)
			if genIDSpec.prefix == "" {
				genIDParser := cb.Atomic(cb.Seq(
					cb.Literal("\""+genCallIDKey+"\""), cb.Space(), cb.Literal(":"), cb.Space(),
					cb.Choice(
						cb.Seq(cb.Literal("\""), cb.ToolID(cb.JSONString()), cb.Literal("\"")),
						cb.ToolID(cb.JSONNumber()),
					),
				))
				toolParserBody = cb.Seq(toolParserBody, cb.Space(),
					cb.Optional(cb.Seq(genIDParser, cb.Space(), cb.Literal(","), cb.Space())))
			}
		}

		nestedField := cb.Seq(
			cb.Literal("\""+nestedPrefix+"\""), cb.Space(), cb.Literal(":"), cb.Space(),
			nestedObject,
		)
		toolParserBody = cb.Seq(toolParserBody, cb.Space(), nestedField, cb.Space(), cb.ToolClose(cb.Literal("}")))

		toolChoices = append(toolChoices, cb.Rule("tool-"+tool.Name, cb.Tool(toolParserBody)))
	}

	return cb.Choice(toolChoices...)
}

func (cb *ChatBuilder) buildJSONToolsFlatKeys(
	tools []ToolDef,
	effectiveNameKey, effectiveArgsKey, callIDKey, genCallIDKey string,
	parametersOrder []string,
) ParserID {
	var toolChoices []ParserID
	nameKeyParser := cb.Literal("\"" + effectiveNameKey + "\"")
	argsKeyParser := cb.Literal("\"" + effectiveArgsKey + "\"")

	for _, tool := range tools {
		toolNameP := cb.Seq(
			nameKeyParser, cb.Space(), cb.Literal(":"), cb.Space(),
			cb.Literal("\""), cb.ToolName(cb.Literal(tool.Name)), cb.Literal("\""),
		)
		toolArgsP := cb.Seq(
			argsKeyParser, cb.Space(), cb.Literal(":"), cb.Space(),
			cb.ToolArgs(cb.JSON()),
		)

		pairs := []parserPair{
			{toolNameP, effectiveNameKey},
			{toolArgsP, effectiveArgsKey},
		}

		if callIDKey != "" {
			idParser := cb.Atomic(cb.Seq(
				cb.Literal("\""+callIDKey+"\""), cb.Space(), cb.Literal(":"), cb.Space(),
				cb.Choice(
					cb.Seq(cb.Literal("\""), cb.ToolID(cb.JSONString()), cb.Literal("\"")),
					cb.ToolID(cb.JSONNumber()),
				),
			))
			pairs = append(pairs, parserPair{cb.Optional(idParser), callIDKey})
		}

		if genCallIDKey != "" {
			genIDParser := cb.Atomic(cb.Seq(
				cb.Literal("\""+genCallIDKey+"\""), cb.Space(), cb.Literal(":"), cb.Space(),
				cb.Choice(
					cb.Seq(cb.Literal("\""), cb.ToolID(cb.JSONString()), cb.Literal("\"")),
					cb.ToolID(cb.JSONNumber()),
				),
			))
			pairs = append(pairs, parserPair{cb.Optional(genIDParser), genCallIDKey})
		}

		// Sort by parameters_order if provided
		if len(parametersOrder) > 0 {
			sortPairsByOrder(pairs, parametersOrder)
		}

		orderedBody := cb.ToolOpen(cb.Literal("{"))
		for i, p := range pairs {
			orderedBody = cb.Seq(orderedBody, cb.Space(), p.parser)
			if i < len(pairs)-1 {
				orderedBody = cb.Seq(orderedBody, cb.Space(), cb.Literal(","), cb.Space())
			}
		}
		orderedBody = cb.Seq(orderedBody, cb.Space(), cb.ToolClose(cb.Literal("}")))

		toolChoices = append(toolChoices, cb.Rule("tool-"+tool.Name, cb.Tool(orderedBody)))
	}

	return cb.Choice(toolChoices...)
}

type parserPair struct {
	parser ParserID
	key    string
}

func sortPairsByOrder(pairs []parserPair, order []string) {
	indexOf := func(key string) int {
		for i, o := range order {
			if o == key {
				return i
			}
		}
		return len(order)
	}
	// Simple insertion sort (small N)
	for i := 1; i < len(pairs); i++ {
		for j := i; j > 0 && indexOf(pairs[j].key) < indexOf(pairs[j-1].key); j-- {
			pairs[j], pairs[j-1] = pairs[j-1], pairs[j]
		}
	}
}

// BuildChatPegParser is a convenience function to build a chat parser.
func BuildChatPegParser(fn func(cb *ChatBuilder) ParserID) *Arena {
	cb := NewChatBuilder()
	root := fn(cb)
	cb.SetRoot(root)
	return cb.Build()
}

// ToolCall represents a parsed tool call.
type ToolCall struct {
	Name      string
	Arguments string
	ID        string
}

// ChatMsg represents a parsed chat message.
type ChatMsg struct {
	Content          string
	ReasoningContent string
	ToolCalls        []ToolCall
}

// ChatPegMapper maps AST nodes to a ChatMsg.
type ChatPegMapper struct {
	Result ChatMsg

	pendingToolCall  *ToolCall
	currentTool      *ToolCall
	argCount         int
	closingQuotePend bool
	argsBuffer       string
}

func (m *ChatPegMapper) argsTarget() *string {
	if m.currentTool != nil && m.currentTool.Name != "" {
		return &m.currentTool.Arguments
	}
	return &m.argsBuffer
}

// FromAST populates the ChatMsg from parse results.
func (m *ChatPegMapper) FromAST(ast *AstArena, result *ParseResult) {
	ast.VisitResult(result, func(node *AstNode) {
		m.mapNode(node)
	})

	// Flush pending tool call
	if m.pendingToolCall != nil && m.pendingToolCall.Name != "" {
		if m.argsBuffer != "" {
			m.pendingToolCall.Arguments = m.argsBuffer
		}
		if m.closingQuotePend && m.pendingToolCall.Arguments != "" {
			m.pendingToolCall.Arguments += "\""
		}
		m.Result.ToolCalls = append(m.Result.ToolCalls, *m.pendingToolCall)
		m.pendingToolCall = nil
	}
}

func (m *ChatPegMapper) mapNode(node *AstNode) {
	switch node.Tag {
	case TagReasoning:
		m.Result.ReasoningContent += node.Text

	case TagContent:
		m.Result.Content += node.Text

	case TagToolOpen:
		tc := ToolCall{}
		m.pendingToolCall = &tc
		m.currentTool = m.pendingToolCall
		m.argCount = 0
		m.argsBuffer = ""
		m.closingQuotePend = false

	case TagToolID:
		if m.currentTool != nil {
			text := trimTrailingSpace(node.Text)
			if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
				text = text[1 : len(text)-1]
			}
			m.currentTool.ID = text
		}

	case TagToolName:
		if m.currentTool != nil {
			m.currentTool.Name = trimTrailingSpace(node.Text)
			if m.argsBuffer != "" {
				m.currentTool.Arguments = m.argsBuffer
				m.argsBuffer = ""
			} else if m.currentTool.Arguments == "" {
				m.currentTool.Arguments = "{"
			}
			// Add tool call to results for streaming
			if m.pendingToolCall != nil {
				m.Result.ToolCalls = append(m.Result.ToolCalls, *m.pendingToolCall)
				m.pendingToolCall = nil
				m.currentTool = &m.Result.ToolCalls[len(m.Result.ToolCalls)-1]
			}
		}

	case TagToolArgs:
		if m.currentTool != nil {
			text := trimTrailingSpace(node.Text)
			if len(text) > 0 && text[0] == '{' {
				*m.argsTarget() = text
			}
		}

	case TagToolArgOpen:
		m.closingQuotePend = false

	case TagToolArgName:
		if m.currentTool != nil {
			argEntry := ""
			if m.argCount > 0 {
				argEntry = ","
			}
			trimmed := trimSpace(node.Text)
			escapedKey := escapeJSONString(trimmed)
			argEntry += escapedKey + ":"
			m.argCount++

			target := m.argsTarget()
			if *target == "" {
				*target = "{"
			}
			*target += argEntry
		}

	case TagToolArgStrVal:
		if m.currentTool != nil {
			content := trimOneSpace(node.Text)
			var valueToAdd string
			if content == "" {
				valueToAdd = "\""
				m.closingQuotePend = true
			} else {
				if !m.closingQuotePend {
					valueToAdd = "\""
					m.closingQuotePend = true
				}
				valueToAdd += EscapeJSONStringInner(content)
			}
			*m.argsTarget() += valueToAdd
		}

	case TagToolArgValue:
		if m.currentTool != nil {
			content := trimOneSpace(node.Text)
			var valueToAdd string
			if content != "" {
				isPotentialContainer := content[0] == '[' || content[0] == '{'
				if isPotentialContainer {
					content = NormalizeQuotesToJSON(content)
				}

				// Try to parse as JSON
				var parsed json.RawMessage
				if err := json.Unmarshal([]byte(content), &parsed); err == nil {
					// Check if it's a string
					var s string
					if err2 := json.Unmarshal(parsed, &s); err2 == nil {
						// It's a string — strip closing quote for monotonic streaming
						escaped, _ := json.Marshal(s)
						str := string(escaped)
						if len(str) > 0 && str[len(str)-1] == '"' {
							str = str[:len(str)-1]
						}
						valueToAdd = str
						m.closingQuotePend = true
					} else {
						// Non-string: use raw content
						valueToAdd = content
					}
				} else {
					if node.IsPartial && isPotentialContainer {
						valueToAdd = content
					} else {
						if !m.closingQuotePend {
							valueToAdd = "\""
							m.closingQuotePend = true
						}
						valueToAdd += EscapeJSONStringInner(content)
					}
				}
			}
			*m.argsTarget() += valueToAdd
		}

	case TagToolArgClose:
		if m.currentTool != nil {
			if m.closingQuotePend {
				*m.argsTarget() += "\""
				m.closingQuotePend = false
			}
		}

	case TagToolClose:
		if m.currentTool != nil {
			// Flush buffer if tool name was never seen
			if m.currentTool.Name == "" && m.argsBuffer != "" {
				m.currentTool.Arguments = m.argsBuffer
				m.argsBuffer = ""
			}
			if m.closingQuotePend {
				m.currentTool.Arguments += "\""
				m.closingQuotePend = false
			}
			// Close unclosed braces
			for depth := jsonBraceDepth(m.currentTool.Arguments); depth > 0; depth-- {
				m.currentTool.Arguments += "}"
			}
			// Add if pending and named
			if m.pendingToolCall != nil {
				if m.currentTool.Name != "" {
					m.Result.ToolCalls = append(m.Result.ToolCalls, *m.pendingToolCall)
				}
				m.pendingToolCall = nil
			}
		}
	}
}

// NormalizeQuotesToJSON converts Python-style single-quoted strings to JSON double-quoted.
func NormalizeQuotesToJSON(input string) string {
	result := make([]byte, 0, len(input)+16)

	inSingleQuoted := false
	inDoubleQuoted := false

	for i := 0; i < len(input); i++ {
		c := input[i]

		if c == '\\' && i+1 < len(input) {
			next := input[i+1]

			if inSingleQuoted {
				if next == '\'' {
					result = append(result, '\'')
					i++
					continue
				}
				if next == '"' {
					result = append(result, '\\', '"')
					i++
					continue
				}
				result = append(result, c, next)
				i++
				continue
			}

			if inDoubleQuoted {
				result = append(result, c, next)
				i++
				continue
			}

			result = append(result, c)
			continue
		}

		if c == '"' {
			if inSingleQuoted {
				result = append(result, '\\', '"')
			} else {
				inDoubleQuoted = !inDoubleQuoted
				result = append(result, c)
			}
		} else if c == '\'' {
			if inDoubleQuoted {
				result = append(result, c)
			} else if inSingleQuoted {
				inSingleQuoted = false
				result = append(result, '"')
			} else {
				inSingleQuoted = true
				result = append(result, '"')
			}
		} else {
			result = append(result, c)
		}
	}

	return string(result)
}

// EscapeJSONStringInner JSON-escapes a string and returns the inner content (without surrounding quotes).
func EscapeJSONStringInner(s string) string {
	escaped, err := json.Marshal(s)
	if err != nil {
		return s
	}
	str := string(escaped)
	if len(str) >= 2 && str[0] == '"' && str[len(str)-1] == '"' {
		return str[1 : len(str)-1]
	}
	return str
}

func escapeJSONString(s string) string {
	escaped, err := json.Marshal(s)
	if err != nil {
		return "\"" + s + "\""
	}
	return string(escaped)
}

func jsonBraceDepth(s string) int {
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && inString {
			escaped = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if !inString {
			if c == '{' {
				depth++
			} else if c == '}' {
				depth--
			}
		}
	}
	return depth
}

func trimTrailingSpace(s string) string {
	end := len(s)
	for end > 0 && isWhitespace(s[end-1]) {
		end--
	}
	return s[:end]
}

func trimLeadingSpace(s string, max int) string {
	start := 0
	count := 0
	for start < len(s) && isWhitespace(s[start]) {
		if max >= 0 && count >= max {
			break
		}
		start++
		count++
	}
	return s[start:]
}

func trimSpace(s string) string {
	s = trimLeadingSpace(s, 1)
	return trimTrailingSpace(s)
}

func trimOneSpace(s string) string {
	s = trimLeadingSpace(s, 1)
	end := len(s)
	count := 0
	for end > 0 && isWhitespace(s[end-1]) && count < 1 {
		end--
		count++
	}
	return s[:end]
}
