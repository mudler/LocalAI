package functions

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"unicode"

	"github.com/mudler/xlog"
)

// ChatMsgPartialException represents a partial parsing exception (recoverable)
type ChatMsgPartialException struct {
	Message string
}

func (e *ChatMsgPartialException) Error() string {
	return e.Message
}

// StringRange represents a range of characters in the input string
type StringRange struct {
	Begin int
	End   int
}

// FindLiteralResult represents the result of finding a literal in the input
type FindLiteralResult struct {
	Prelude string
	Groups  []StringRange
}

// ChatMsgParser is an iterative parser similar to llama.cpp's common_chat_msg_parser
// It tracks position in the input and can parse incrementally, supporting partial parsing
type ChatMsgParser struct {
	input         string
	isPartial     bool
	pos           int
	healingMarker string
	content       strings.Builder
	reasoning     strings.Builder
	toolCalls     []FuncCallResults
}

// NewChatMsgParser creates a new iterative parser
func NewChatMsgParser(input string, isPartial bool) *ChatMsgParser {
	// Generate a unique healing marker (similar to llama.cpp)
	healingMarker := generateHealingMarker(input)

	return &ChatMsgParser{
		input:         input,
		isPartial:     isPartial,
		pos:           0,
		healingMarker: healingMarker,
		toolCalls:     make([]FuncCallResults, 0),
	}
}

// generateHealingMarker generates a unique marker that doesn't appear in the input
func generateHealingMarker(input string) string {
	for {
		id := fmt.Sprintf("%d", rand.Int63())
		if !strings.Contains(input, id) {
			return id
		}
	}
}

// Input returns the input string
func (p *ChatMsgParser) Input() string {
	return p.input
}

// Pos returns the current position in the input
func (p *ChatMsgParser) Pos() int {
	return p.pos
}

// IsPartial returns whether this is a partial parse
func (p *ChatMsgParser) IsPartial() bool {
	return p.isPartial
}

// HealingMarker returns the healing marker used for partial JSON
func (p *ChatMsgParser) HealingMarker() string {
	return p.healingMarker
}

// MoveTo moves the parser position to a specific index
func (p *ChatMsgParser) MoveTo(pos int) error {
	if pos < 0 || pos > len(p.input) {
		return fmt.Errorf("invalid position: %d (input length: %d)", pos, len(p.input))
	}
	p.pos = pos
	return nil
}

// MoveBack moves the parser position back by n characters
func (p *ChatMsgParser) MoveBack(n int) error {
	if p.pos < n {
		return fmt.Errorf("can't move back %d characters from position %d", n, p.pos)
	}
	p.pos -= n
	return nil
}

// Str returns the substring at the given range
func (p *ChatMsgParser) Str(rng StringRange) string {
	if rng.Begin < 0 || rng.End > len(p.input) || rng.Begin > rng.End {
		return ""
	}
	return p.input[rng.Begin:rng.End]
}

// ConsumeRest returns the remaining input from current position to end
func (p *ChatMsgParser) ConsumeRest() string {
	if p.pos >= len(p.input) {
		return ""
	}
	result := p.input[p.pos:]
	p.pos = len(p.input)
	return result
}

// AddContent appends content to the result
func (p *ChatMsgParser) AddContent(content string) {
	p.content.WriteString(content)
}

// AddReasoningContent appends reasoning content to the result
func (p *ChatMsgParser) AddReasoningContent(reasoning string) {
	p.reasoning.WriteString(reasoning)
}

// AddToolCall adds a tool call to the result
func (p *ChatMsgParser) AddToolCall(name, id, arguments string) bool {
	if name == "" {
		return false
	}
	p.toolCalls = append(p.toolCalls, FuncCallResults{
		Name:      name,
		Arguments: arguments,
	})
	return true
}

// ToolCalls returns the parsed tool calls
func (p *ChatMsgParser) ToolCalls() []FuncCallResults {
	return p.toolCalls
}

// Content returns the parsed content
func (p *ChatMsgParser) Content() string {
	return p.content.String()
}

// Reasoning returns the parsed reasoning content
func (p *ChatMsgParser) Reasoning() string {
	return p.reasoning.String()
}

// ClearTools clears all parsed tool calls
func (p *ChatMsgParser) ClearTools() {
	p.toolCalls = p.toolCalls[:0]
}

// TryConsumeLiteral attempts to consume a literal string at the current position
// Returns true if the literal was found and consumed, false otherwise
func (p *ChatMsgParser) TryConsumeLiteral(literal string) bool {
	if len(literal) == 0 {
		return true
	}
	if p.pos+len(literal) > len(p.input) {
		return false
	}
	if p.input[p.pos:p.pos+len(literal)] == literal {
		p.pos += len(literal)
		return true
	}
	return false
}

// ConsumeLiteral consumes a literal string, throwing an error if not found
func (p *ChatMsgParser) ConsumeLiteral(literal string) error {
	if !p.TryConsumeLiteral(literal) {
		return &ChatMsgPartialException{Message: fmt.Sprintf("Expected literal: %s", literal)}
	}
	return nil
}

// TryFindLiteral finds a literal string starting from the current position
// Returns the result if found, nil otherwise
// Similar to llama.cpp's try_find_literal
func (p *ChatMsgParser) TryFindLiteral(literal string) *FindLiteralResult {
	if len(literal) == 0 {
		return nil
	}

	// Search for the literal starting from current position
	idx := strings.Index(p.input[p.pos:], literal)
	if idx == -1 {
		// If partial parsing is enabled, try to find partial matches
		if p.isPartial {
			partialIdx := stringFindPartialStop(p.input[p.pos:], literal)
			if partialIdx != -1 && partialIdx >= 0 {
				result := &FindLiteralResult{
					Prelude: p.input[p.pos : p.pos+partialIdx],
					Groups: []StringRange{
						{Begin: p.pos + partialIdx, End: len(p.input)},
					},
				}
				p.pos = len(p.input)
				return result
			}
		}
		return nil
	}

	idx += p.pos
	result := &FindLiteralResult{
		Prelude: p.input[p.pos:idx],
		Groups: []StringRange{
			{Begin: idx, End: idx + len(literal)},
		},
	}
	p.pos = idx + len(literal)
	return result
}

// stringFindPartialStop finds where a partial string match might stop
// This is used for streaming/partial parsing
func stringFindPartialStop(s, needle string) int {
	if len(needle) == 0 || len(s) == 0 {
		return -1
	}
	// Check if s ends with a prefix of needle
	for i := len(needle); i > 0; i-- {
		if len(s) >= i && s[len(s)-i:] == needle[:i] {
			return len(s) - i
		}
	}
	return -1
}

// ConsumeSpaces consumes whitespace characters
func (p *ChatMsgParser) ConsumeSpaces() bool {
	consumed := false
	for p.pos < len(p.input) && unicode.IsSpace(rune(p.input[p.pos])) {
		p.pos++
		consumed = true
	}
	return consumed
}

// AllSpace checks if a string contains only whitespace
func AllSpace(s string) bool {
	return strings.TrimSpace(s) == ""
}

// TryConsumeJSON attempts to consume a JSON value from the current position
// Returns the parsed JSON and whether it's partial
// Improved to better match llama.cpp's JSON parsing with healing support
func (p *ChatMsgParser) TryConsumeJSON() (map[string]any, bool, error) {
	// Skip whitespace
	p.ConsumeSpaces()

	if p.pos >= len(p.input) {
		return nil, false, errors.New("end of input")
	}

	// Try to parse JSON starting from current position
	jsonStart := p.pos
	if p.input[p.pos] != '{' && p.input[p.pos] != '[' {
		return nil, false, errors.New("not a JSON object or array")
	}

	// Try parsing complete JSON first using decoder to get exact position
	decoder := json.NewDecoder(strings.NewReader(p.input[jsonStart:]))
	var jsonValue map[string]any
	if err := decoder.Decode(&jsonValue); err == nil {
		// Complete JSON parsed successfully
		// Calculate position after JSON using decoder's input offset
		p.pos = jsonStart + int(decoder.InputOffset())
		return jsonValue, false, nil
	}

	// If parsing failed, try to find where JSON might end
	// Find matching brace/bracket
	depth := 0
	inString := false
	escape := false
	jsonEnd := -1

	for i := p.pos; i < len(p.input); i++ {
		ch := p.input[i]

		if escape {
			escape = false
			continue
		}

		if ch == '\\' {
			escape = true
			continue
		}

		if ch == '"' {
			inString = !inString
			continue
		}

		if inString {
			continue
		}

		if ch == '{' || ch == '[' {
			depth++
		} else if ch == '}' || ch == ']' {
			depth--
			if depth == 0 {
				jsonEnd = i + 1
				break
			}
		}
	}

	if jsonEnd == -1 {
		// Incomplete JSON (partial)
		if p.isPartial {
			// Try healing: add healing marker and closing braces
			// This is a simplified version - full implementation would track stack
			healedJSON := p.input[jsonStart:] + `"` + p.healingMarker + `"`
			// Try to close any open objects/arrays
			openBraces := strings.Count(p.input[jsonStart:], "{") - strings.Count(p.input[jsonStart:], "}")
			openBrackets := strings.Count(p.input[jsonStart:], "[") - strings.Count(p.input[jsonStart:], "]")
			for i := 0; i < openBraces; i++ {
				healedJSON += "}"
			}
			for i := 0; i < openBrackets; i++ {
				healedJSON += "]"
			}

			var healedValue map[string]any
			if err := json.Unmarshal([]byte(healedJSON), &healedValue); err == nil {
				// Successfully healed - remove healing marker from result
				cleaned := removeHealingMarkerFromJSON(healedValue, p.healingMarker)
				p.pos = len(p.input)
				return cleaned, true, nil
			}
		}
		return nil, true, errors.New("incomplete JSON")
	}

	// Parse complete JSON
	jsonStr := p.input[jsonStart:jsonEnd]
	if err := json.Unmarshal([]byte(jsonStr), &jsonValue); err != nil {
		return nil, false, err
	}

	p.pos = jsonEnd
	return jsonValue, false, nil
}

// removeHealingMarkerFromJSON removes healing markers from a parsed JSON structure
func removeHealingMarkerFromJSON(value map[string]any, marker string) map[string]any {
	result := make(map[string]any)
	for k, v := range value {
		if str, ok := v.(string); ok {
			if idx := strings.Index(str, marker); idx != -1 {
				v = str[:idx]
			}
		} else if nestedMap, ok := v.(map[string]any); ok {
			v = removeHealingMarkerFromJSON(nestedMap, marker)
		}
		result[k] = v
	}
	return result
}

// removeHealingMarker removes the healing marker from a JSON string
func removeHealingMarker(jsonStr string) string {
	// This is a simplified version - in practice, we'd need to properly clean JSON
	pos := strings.LastIndex(jsonStr, "XML_TOOL_CALL_PARTIAL_FLAG")
	if pos == -1 {
		return jsonStr
	}
	// Check that only valid JSON characters follow
	for i := pos + len("XML_TOOL_CALL_PARTIAL_FLAG"); i < len(jsonStr); i++ {
		ch := jsonStr[i]
		if ch != '\'' && ch != '"' && ch != '}' && ch != ':' && ch != ']' && !unicode.IsSpace(rune(ch)) {
			return jsonStr
		}
	}
	if pos > 0 && jsonStr[pos-1] == '"' {
		pos--
	}
	return jsonStr[:pos]
}

// TryConsumeXMLToolCalls attempts to parse XML tool calls using the iterative parser
// Returns true if tool calls were found and parsed, false otherwise
// Similar to llama.cpp's parse_xml_tool_calls
func (p *ChatMsgParser) TryConsumeXMLToolCalls(format *XMLToolCallFormat) (bool, error) {
	if format == nil {
		return false, errors.New("format is required")
	}

	// Handle Functionary format (JSON parameters inside XML tags) - use regex parser
	if format.KeyStart == "" && format.ToolStart == "<function=" {
		// Fall back to regex-based parser for Functionary format
		results, err := parseFunctionaryFormat(p.input[p.pos:], format)
		if err != nil || len(results) == 0 {
			return false, nil
		}
		for _, result := range results {
			p.AddToolCall(result.Name, "", result.Arguments)
		}
		return true, nil
	}

	// Handle JSON-like formats (Apriel-1.5, Xiaomi-MiMo) - use regex parser
	if format.ToolStart != "" && strings.Contains(format.ToolStart, "{\"name\"") {
		results, err := parseJSONLikeXMLFormat(p.input[p.pos:], format)
		if err != nil || len(results) == 0 {
			return false, nil
		}
		for _, result := range results {
			p.AddToolCall(result.Name, "", result.Arguments)
		}
		return true, nil
	}

	// Validate required fields for standard XML formats
	if format.ToolStart == "" || format.KeyStart == "" || format.KeyValSep == "" ||
		format.ValEnd == "" || format.ToolEnd == "" {
		return false, errors.New("required format fields missing")
	}

	startPos := p.pos
	recovery := true

	// Helper to return error with optional recovery
	returnError := func(err error, canRecover bool) (bool, error) {
		xlog.Debug("Failed to parse XML tool call", "error", err, "position", p.pos)
		if canRecover && recovery {
			p.MoveTo(startPos)
			return false, nil
		}
		return false, fmt.Errorf("tool call parsing failed with unrecoverable errors: %w", err)
	}

	// Helper to find val_end or last_val_end
	tryFindValEnd := func() (int, *FindLiteralResult) {
		savedPos := p.pos
		tc := p.TryFindLiteral(format.ValEnd)
		valEndSize := len(format.ValEnd)

		if format.LastValEnd != nil {
			p.MoveTo(savedPos)
			tc2 := p.tryFind2LiteralSplitBySpaces(*format.LastValEnd, format.ToolEnd)
			if format.LastToolEnd != nil {
				p.MoveTo(savedPos)
				tc3 := p.tryFind2LiteralSplitBySpaces(*format.LastValEnd, *format.LastToolEnd)
				if tc3 != nil && (tc2 == nil || len(tc2.Prelude) > len(tc3.Prelude)) {
					tc2 = tc3
				}
			}
			if tc2 != nil && (tc == nil || len(tc.Prelude) > len(tc2.Prelude)) {
				tc = tc2
				if tc.Groups[0].End > len(p.input) {
					tc.Groups[0].End = len(p.input)
				}
				if tc.Groups[0].Begin+len(*format.LastValEnd) < len(p.input) {
					tc.Groups[0].End = tc.Groups[0].Begin + len(*format.LastValEnd)
				}
				p.MoveTo(tc.Groups[0].End)
				valEndSize = len(*format.LastValEnd)
			} else {
				p.MoveTo(savedPos)
			}
		}
		return valEndSize, tc
	}

	// Helper to find tool_end or last_tool_end
	tryFindToolEnd := func() (int, *FindLiteralResult) {
		savedPos := p.pos
		tc := p.TryFindLiteral(format.ToolEnd)
		toolEndSize := len(format.ToolEnd)

		if format.LastToolEnd != nil {
			p.MoveTo(savedPos)
			tc2 := p.tryFind2LiteralSplitBySpaces(*format.LastToolEnd, format.ScopeEnd)
			if tc2 != nil && (tc == nil || len(tc.Prelude) > len(tc2.Prelude)) {
				tc = tc2
				if tc.Groups[0].End > len(p.input) {
					tc.Groups[0].End = len(p.input)
				}
				if tc.Groups[0].Begin+len(*format.LastToolEnd) < len(p.input) {
					tc.Groups[0].End = tc.Groups[0].Begin + len(*format.LastToolEnd)
				}
				p.MoveTo(tc.Groups[0].End)
				toolEndSize = len(*format.LastToolEnd)
			} else {
				p.MoveTo(savedPos)
			}
		}
		return toolEndSize, tc
	}

	// Parse scope_start if present
	if format.ScopeStart != "" && !AllSpace(format.ScopeStart) {
		tc := p.TryFindLiteral(format.ScopeStart)
		if tc == nil {
			return false, nil
		}
		if !AllSpace(tc.Prelude) {
			p.MoveTo(startPos)
			return false, nil
		}
		// Validate size match (partial detection)
		if len(tc.Groups) > 0 {
			matchedSize := tc.Groups[0].End - tc.Groups[0].Begin
			if matchedSize != len(format.ScopeStart) {
				return false, &ChatMsgPartialException{Message: fmt.Sprintf("Partial literal: %s", format.ScopeStart)}
			}
		}
	}

	// Parse tool calls
	for {
		tc := p.TryFindLiteral(format.ToolStart)
		if tc == nil {
			break
		}

		if !AllSpace(tc.Prelude) {
			// Non-whitespace before tool_start, stop parsing
			p.MoveTo(tc.Groups[0].Begin - len(tc.Prelude))
			break
		}

		// Find function name
		var funcName *FindLiteralResult
		if AllSpace(format.ToolSep) {
			// GLM 4.5 format: function name is between tool_start and key_start
			funcName = p.TryFindLiteral(format.KeyStart)
		} else {
			// Standard format: function name is between tool_start and tool_sep
			funcName = p.TryFindLiteral(format.ToolSep)
		}

		if funcName == nil {
			// Try to find tool_end instead (empty tool call)
			_, toolEnd := tryFindToolEnd()
			if toolEnd != nil {
				// Empty tool call - extract function name from between tool_start and tool_end
				nameStart := tc.Groups[0].End
				nameEnd := toolEnd.Groups[0].Begin
				functionName := ""
				if nameEnd > nameStart {
					functionName = strings.TrimSpace(p.input[nameStart:nameEnd])
				}
				argsJSON, _ := json.Marshal(map[string]any{})
				p.AddToolCall(functionName, "", string(argsJSON))
				recovery = false
				continue
			}
			// Partial tool name not supported
			return false, &ChatMsgPartialException{Message: "incomplete tool_call"}
		}

		// Check if tool_end appears in function name prelude (empty tool call)
		functionNamePrelude := funcName.Prelude
		if strings.Contains(functionNamePrelude, format.ToolEnd) ||
			(format.LastToolEnd != nil && strings.Contains(functionNamePrelude, *format.LastToolEnd)) {
			// Empty tool call - function name is empty, tool_end is in the prelude
			// Move back to start of tool_start and find tool_end
			p.MoveTo(tc.Groups[0].Begin)
			_, toolEnd := tryFindToolEnd()
			if toolEnd != nil {
				// Extract function name from between tool_start and tool_end
				nameStart := tc.Groups[0].End
				nameEnd := toolEnd.Groups[0].Begin
				functionName := ""
				if nameEnd > nameStart {
					functionName = strings.TrimSpace(p.input[nameStart:nameEnd])
					// Remove tool_sep if present
					if !AllSpace(format.ToolSep) && strings.HasSuffix(functionName, format.ToolSep) {
						functionName = strings.TrimSpace(functionName[:len(functionName)-len(format.ToolSep)])
					}
				}
				argsJSON, _ := json.Marshal(map[string]any{})
				p.AddToolCall(functionName, "", string(argsJSON))
				recovery = false
				continue
			}
		}

		// Extract function name from prelude
		// Move to appropriate position based on format
		if AllSpace(format.ToolSep) {
			// GLM 4.5 format: function name is on a separate line after tool_start, before key_start
			// The prelude contains the function name
			p.MoveTo(funcName.Groups[0].Begin)
		} else {
			// Standard format: function name is before tool_sep
			p.MoveTo(funcName.Groups[0].End)
		}
		functionName := strings.TrimSpace(funcName.Prelude)

		// Handle Kimi-K2 function name stripping
		if strings.HasPrefix(functionName, "functions.") {
			functionName = functionName[10:]
			if idx := strings.LastIndex(functionName, ":"); idx != -1 {
				suffix := functionName[idx+1:]
				allDigits := true
				for _, r := range suffix {
					if r < '0' || r > '9' {
						allDigits = false
						break
					}
				}
				if allDigits {
					functionName = functionName[:idx]
				}
			}
		}

		// Parse arguments
		arguments := make(map[string]any)

		for {
			keyStart := p.TryFindLiteral(format.KeyStart)
			if keyStart == nil {
				break
			}

			if !AllSpace(keyStart.Prelude) {
				// Non-whitespace before key_start, stop parsing parameters
				p.MoveTo(keyStart.Groups[0].Begin - len(keyStart.Prelude))
				break
			}

			// Validate size match (partial detection)
			if len(keyStart.Groups) > 0 {
				matchedSize := keyStart.Groups[0].End - keyStart.Groups[0].Begin
				if matchedSize != len(format.KeyStart) {
					// Partial key_start, emit tool call with current args
					argsJSON, _ := json.Marshal(arguments)
					if len(argsJSON) > 0 && argsJSON[len(argsJSON)-1] == '}' {
						argsJSON = argsJSON[:len(argsJSON)-1]
					}
					p.AddToolCall(functionName, "", string(argsJSON))
					return false, &ChatMsgPartialException{Message: fmt.Sprintf("Partial literal: %s", format.KeyStart)}
				}
			}

			// Find key_val_sep
			keyValSep := p.TryFindLiteral(format.KeyValSep)
			if keyValSep == nil {
				// Generate partial args
				rest := p.ConsumeRest()
				arguments[rest+"XML_TOOL_CALL_PARTIAL_FLAG"] = ""
				argsJSON, _ := json.Marshal(arguments)
				toolStr := string(argsJSON)
				if cleaned, isPartial := partialJSON(toolStr); isPartial {
					p.AddToolCall(functionName, "", cleaned)
				} else {
					p.AddToolCall(functionName, "", toolStr)
				}
				return false, &ChatMsgPartialException{
					Message: fmt.Sprintf("Expected %s after %s", format.KeyValSep, format.KeyStart),
				}
			}

			// Validate size match
			if len(keyValSep.Groups) > 0 {
				matchedSize := keyValSep.Groups[0].End - keyValSep.Groups[0].Begin
				if matchedSize != len(format.KeyValSep) {
					// Partial key_val_sep
					rest := keyValSep.Prelude
					arguments[rest+"XML_TOOL_CALL_PARTIAL_FLAG"] = ""
					argsJSON, _ := json.Marshal(arguments)
					toolStr := string(argsJSON)
					if cleaned, isPartial := partialJSON(toolStr); isPartial {
						p.AddToolCall(functionName, "", cleaned)
					} else {
						p.AddToolCall(functionName, "", toolStr)
					}
					return false, &ChatMsgPartialException{Message: fmt.Sprintf("Partial literal: %s", format.KeyValSep)}
				}
			}

			key := strings.TrimSpace(keyValSep.Prelude)
			recovery = false

			// Handle key_val_sep2 if present (GLM 4.5 format)
			// For GLM 4.5, key_val_sep2 is "</arg_key>\n<arg_value>"
			// We need to consume it but it's optional - if not found, the value might be empty
			if format.KeyValSep2 != nil {
				// Try to consume it, but don't fail if not found (might be empty value)
				p.TryConsumeLiteral(*format.KeyValSep2)
			}

			// Find val_end
			valEndSize, valEnd := tryFindValEnd()
			if valEnd == nil {
				// Partial value
				rest := p.ConsumeRest()
				if format.TrimRawArgVal {
					rest = strings.TrimSpace(rest)
				}
				arguments[key] = rest + "XML_TOOL_CALL_PARTIAL_FLAG"
				argsJSON, _ := json.Marshal(arguments)
				toolStr := string(argsJSON)
				if cleaned, isPartial := partialJSON(toolStr); isPartial {
					p.AddToolCall(functionName, "", cleaned)
				} else {
					p.AddToolCall(functionName, "", toolStr)
				}
				return false, &ChatMsgPartialException{
					Message: fmt.Sprintf("Expected %s after %s", format.ValEnd, format.KeyValSep),
				}
			}

			// Validate size match
			if len(valEnd.Groups) > 0 {
				matchedSize := valEnd.Groups[0].End - valEnd.Groups[0].Begin
				if matchedSize != valEndSize {
					// Partial val_end
					rest := valEnd.Prelude
					if format.TrimRawArgVal {
						rest = strings.TrimSpace(rest)
					}
					arguments[key] = rest + "XML_TOOL_CALL_PARTIAL_FLAG"
					argsJSON, _ := json.Marshal(arguments)
					toolStr := string(argsJSON)
					if cleaned, isPartial := partialJSON(toolStr); isPartial {
						p.AddToolCall(functionName, "", cleaned)
					} else {
						p.AddToolCall(functionName, "", toolStr)
					}
					return false, &ChatMsgPartialException{Message: fmt.Sprintf("Partial literal: %s", format.ValEnd)}
				}
			}

			// Parse value using parseParameterValue to match regex parser behavior
			// This handles JSON-first parsing correctly
			valueStr := strings.TrimSpace(valEnd.Prelude)
			value := parseParameterValue(valueStr, format)
			arguments[key] = value
		}

		// Find tool_end
		toolEndSize, toolEnd := tryFindToolEnd()
		if toolEnd == nil {
			// Partial tool call
			argsJSON, _ := json.Marshal(arguments)
			toolStr := string(argsJSON)
			if len(toolStr) > 0 && toolStr[len(toolStr)-1] == '}' {
				toolStr = toolStr[:len(toolStr)-1]
			}
			p.AddToolCall(functionName, "", toolStr)
			return false, &ChatMsgPartialException{Message: "incomplete tool_call"}
		}

		if !AllSpace(toolEnd.Prelude) {
			return returnError(errors.New("non-whitespace before tool_end"), recovery)
		}

		// Validate size match
		if len(toolEnd.Groups) > 0 {
			matchedSize := toolEnd.Groups[0].End - toolEnd.Groups[0].Begin
			if matchedSize == toolEndSize {
				// Complete tool call
				argsJSON, _ := json.Marshal(arguments)
				if !p.AddToolCall(functionName, "", string(argsJSON)) {
					return false, &ChatMsgPartialException{Message: "Failed to add XML tool call"}
				}
				recovery = false
				continue
			}
		}

		// Partial tool_end
		argsJSON, _ := json.Marshal(arguments)
		toolStr := string(argsJSON)
		if len(toolStr) > 0 && toolStr[len(toolStr)-1] == '}' {
			toolStr = toolStr[:len(toolStr)-1]
		}
		p.AddToolCall(functionName, "", toolStr)
		return false, &ChatMsgPartialException{Message: "incomplete tool_call"}
	}

	// Parse scope_end if present
	if format.ScopeEnd != "" {
		tc := p.TryFindLiteral(format.ScopeEnd)
		if tc == nil {
			// Expected scope_end but not found
			if !p.isPartial {
				return returnError(errors.New("expected scope_end"), recovery)
			}
		} else if !AllSpace(tc.Prelude) {
			return returnError(errors.New("non-whitespace before scope_end"), recovery)
		}
	}

	return len(p.toolCalls) > 0, nil
}

// tryFind2LiteralSplitBySpaces finds two literals separated by spaces
func (p *ChatMsgParser) tryFind2LiteralSplitBySpaces(literal1, literal2 string) *FindLiteralResult {
	savedPos := p.pos

	// Try to find first literal
	tc1 := p.TryFindLiteral(literal1)
	if tc1 == nil {
		p.MoveTo(savedPos)
		return nil
	}

	// Consume spaces
	p.ConsumeSpaces()

	// Try to find second literal
	tc2 := p.TryFindLiteral(literal2)
	if tc2 == nil {
		p.MoveTo(savedPos)
		return nil
	}

	// Combine results - extract the text between the two literals
	betweenText := p.input[tc1.Groups[0].End:tc2.Groups[0].Begin]
	return &FindLiteralResult{
		Prelude: tc1.Prelude + strings.TrimSpace(betweenText) + tc2.Prelude,
		Groups: []StringRange{
			{Begin: tc1.Groups[0].Begin, End: tc2.Groups[0].End},
		},
	}
}
