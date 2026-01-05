package functions

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

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

// rstrip removes trailing whitespace from a string
func rstrip(s string) string {
	return strings.TrimRightFunc(s, unicode.IsSpace)
}

// eraseSpaces erases a substring and surrounding spaces, replacing with newlines
// Reference: llama.cpp/common/chat-parser-xml-toolcall.cpp lines 659-668
func eraseSpaces(str string, l, r int) (string, int) {
	if l < 0 || r < 0 || l > len(str) || r > len(str) || l > r {
		return str, l
	}
	// Move l left to include leading spaces
	for l > 0 && l < len(str) && unicode.IsSpace(rune(str[l-1])) {
		l--
	}
	// Move r right to include trailing spaces
	for r < len(str) && unicode.IsSpace(rune(str[r])) {
		r++
	}
	// Replace with newlines
	result := str[:l]
	if l < r {
		result += "\n"
		if l+1 < r {
			result += "\n"
		}
	}
	newL := l
	if newL != 0 {
		newL += 2
	}
	if newL < len(str) && newL <= r {
		result += str[r:]
	} else if newL < len(str) {
		result += str[newL:]
	}
	return result, newL
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
// Returns the parsed JSON (can be object, array, or any JSON type), whether it's partial,
// and the jsonDumpMarker (non-empty if JSON was healed)
// Matches llama.cpp's try_consume_json() which returns common_json containing any JSON type and healing_marker
func (p *ChatMsgParser) TryConsumeJSON() (any, bool, string, error) {
	// Skip whitespace
	p.ConsumeSpaces()

	if p.pos >= len(p.input) {
		return nil, false, "", errors.New("end of input")
	}

	// Try to parse JSON starting from current position
	jsonStart := p.pos
	if p.input[p.pos] != '{' && p.input[p.pos] != '[' {
		return nil, false, "", errors.New("not a JSON object or array")
	}

	// Try parsing complete JSON first using decoder to get exact position
	// Use any to support objects, arrays, and other JSON types (matching llama.cpp)
	decoder := json.NewDecoder(strings.NewReader(p.input[jsonStart:]))
	var jsonValue any
	if err := decoder.Decode(&jsonValue); err == nil {
		// Complete JSON parsed successfully
		// Calculate position after JSON using decoder's input offset
		p.pos = jsonStart + int(decoder.InputOffset())
		return jsonValue, false, "", nil
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
			// Use stack-based healing matching llama.cpp's implementation
			partialInput := p.input[jsonStart:]
			healedValue, wasHealed, jsonDumpMarker, err := parseJSONWithStack(partialInput, p.healingMarker)
			if err == nil && wasHealed {
				// Successfully healed - remove healing marker from result
				cleaned := removeHealingMarkerFromJSONAny(healedValue, p.healingMarker)
				p.pos = len(p.input)
				return cleaned, true, jsonDumpMarker, nil
			}
		}
		return nil, true, "", errors.New("incomplete JSON")
	}

	// Parse complete JSON
	jsonStr := p.input[jsonStart:jsonEnd]
	if err := json.Unmarshal([]byte(jsonStr), &jsonValue); err != nil {
		return nil, false, "", err
	}

	p.pos = jsonEnd
	return jsonValue, false, "", nil
}

// tryConsumeJSONPrimitive attempts to consume a JSON primitive (null, true, false, or number)
// This is a fallback when TryConsumeJSON fails because it only accepts objects/arrays
// Reference: llama.cpp/common/chat-parser-xml-toolcall.cpp lines 506-520
func (p *ChatMsgParser) tryConsumeJSONPrimitive() (any, bool) {
	// Consume spaces first
	p.ConsumeSpaces()
	if p.pos >= len(p.input) {
		return nil, false
	}

	// Get UTF-8 safe view of remaining input
	remaining := p.input[p.pos:]
	safeView := utf8TruncateSafeView(remaining)

	// Check for null, true, false (minimum 4 chars needed)
	if len(safeView) >= 4 {
		prefix := safeView
		if len(prefix) > 6 {
			prefix = prefix[:6]
		}
		if strings.HasPrefix(prefix, "null") {
			// Check if it's complete "null" (followed by space, comma, }, ], or end)
			if len(safeView) >= 4 {
				if len(safeView) == 4 || isJSONTerminator(safeView[4]) {
					p.pos += 4
					return nil, false
				}
			}
		} else if strings.HasPrefix(prefix, "true") {
			if len(safeView) >= 4 {
				if len(safeView) == 4 || isJSONTerminator(safeView[4]) {
					p.pos += 4
					return true, false
				}
			}
		} else if strings.HasPrefix(prefix, "false") {
			if len(safeView) >= 5 {
				if len(safeView) == 5 || isJSONTerminator(safeView[5]) {
					p.pos += 5
					return false, false
				}
			}
		}
	}

	// Check for number: [0-9-][0-9]*(\.\d*)?([eE][+-]?\d*)?
	// Use regex to match number pattern
	numberRegex := regexp.MustCompile(`^[0-9-][0-9]*(\.\d*)?([eE][+-]?\d*)?`)
	if match := numberRegex.FindString(safeView); match != "" {
		// Try to parse as number
		var numValue float64
		if _, err := fmt.Sscanf(match, "%f", &numValue); err == nil {
			// Check if match is followed by a JSON terminator or end of input
			if len(safeView) == len(match) || isJSONTerminator(safeView[len(match)]) {
				p.pos += len(match)
				return numValue, false
			}
		}
	}

	return nil, false
}

// isJSONTerminator checks if a character is a valid JSON terminator
func isJSONTerminator(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' ||
		ch == ',' || ch == '}' || ch == ']' || ch == ':' || ch == '<'
}

// utf8TruncateSafeView truncates a string at a safe UTF-8 boundary
// This is a helper function to avoid importing from parse.go
func utf8TruncateSafeView(s string) string {
	if len(s) == 0 {
		return s
	}
	// Check if the string ends at a valid UTF-8 boundary
	// If not, truncate to the last valid boundary
	for i := len(s); i > 0 && i > len(s)-4; i-- {
		if utf8.ValidString(s[:i]) {
			return s[:i]
		}
	}
	// If we can't find a valid boundary in the last 4 bytes, truncate conservatively
	if len(s) > 3 {
		return s[:len(s)-3]
	}
	return ""
}

// isJSONObjectOrArray checks if a value is a JSON object or array
func isJSONObjectOrArray(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return true
	default:
		return false
	}
}

// isJSONString checks if a value is a JSON string
func isJSONString(v any) bool {
	_, ok := v.(string)
	return ok
}

// trimPotentialPartialWord removes partial XML tags from the end of content
// This prevents emitting incomplete tags during streaming
// Reference: llama.cpp/common/chat-parser-xml-toolcall.cpp lines 684-692
func trimPotentialPartialWord(content string, format *XMLToolCallFormat, startThink, endThink string) string {
	patterns := []string{
		startThink,
		endThink,
		format.ScopeStart,
		format.ToolStart,
		format.ToolSep,
		format.KeyStart,
		format.KeyValSep,
	}
	if format.KeyValSep2 != nil {
		patterns = append(patterns, *format.KeyValSep2)
	}
	patterns = append(patterns, format.ValEnd)
	if format.LastValEnd != nil {
		patterns = append(patterns, *format.LastValEnd)
	}
	patterns = append(patterns, format.ToolEnd)
	if format.LastToolEnd != nil {
		patterns = append(patterns, *format.LastToolEnd)
	}
	patterns = append(patterns, format.ScopeEnd)

	bestMatch := len(content)
	for _, pattern := range patterns {
		if len(pattern) == 0 {
			continue
		}
		// Check for suffix matches from end of content backwards
		maxStart := len(content) - len(pattern)
		if maxStart < 0 {
			maxStart = 0
		}
		for matchIdx := len(content); matchIdx > maxStart; matchIdx-- {
			matchLen := len(content) - matchIdx
			if matchLen > 0 && matchIdx < len(content) {
				// Check if pattern matches as suffix starting at matchIdx
				if matchIdx+matchLen <= len(content) {
					substr := content[matchIdx : matchIdx+matchLen]
					if len(substr) <= len(pattern) && strings.HasPrefix(pattern, substr) {
						if matchIdx < bestMatch {
							bestMatch = matchIdx
						}
					}
				}
			}
		}
	}

	if len(content) > bestMatch {
		return content[:bestMatch]
	}
	return content
}

// removeHealingMarkerFromJSON removes healing markers from a parsed JSON structure (objects only)
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

// removeHealingMarkerFromJSONAny removes healing markers from any JSON type (objects, arrays, etc.)
func removeHealingMarkerFromJSONAny(value any, marker string) any {
	switch v := value.(type) {
	case map[string]any:
		return removeHealingMarkerFromJSON(v, marker)
	case []any:
		result := make([]any, len(v))
		for i, item := range v {
			result[i] = removeHealingMarkerFromJSONAny(item, marker)
		}
		return result
	case string:
		if idx := strings.Index(v, marker); idx != -1 {
			return v[:idx]
		}
		return v
	default:
		return v
	}
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

	// Parse multiple scopes (for formats like qwen3-coder that can have multiple <tool_call> blocks)
	// Continue parsing until no more scopes are found
	for {
		// Parse scope_start if present
		if format.ScopeStart != "" && !AllSpace(format.ScopeStart) {
			tc := p.TryFindLiteral(format.ScopeStart)
			if tc == nil {
				// No more scopes found, break
				break
			}
			if !AllSpace(tc.Prelude) {
				// Non-whitespace before scope_start, stop parsing
				p.MoveTo(tc.Groups[0].Begin - len(tc.Prelude))
				break
			}
			// Validate size match (partial detection)
			if len(tc.Groups) > 0 {
				matchedSize := tc.Groups[0].End - tc.Groups[0].Begin
				if matchedSize != len(format.ScopeStart) {
					return false, &ChatMsgPartialException{Message: fmt.Sprintf("Partial literal: %s", format.ScopeStart)}
				}
			}
		}

		// Parse tool calls within this scope
		scopeToolCallsFound := false
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

				// Save position before attempting JSON parsing
				// Reference: llama.cpp/common/chat-parser-xml-toolcall.cpp lines 499-555
				valStart := p.pos

				// Try to parse JSON first (if raw_argval is false/null)
				// This matches llama.cpp's approach: try JSON before finding val_end
				var jsonValue any
				var jsonHealingMarker string
				jsonParsed := false

				if format.RawArgVal == nil || !*format.RawArgVal {
					// Try JSON parsing (objects/arrays)
					jsonVal, _, jsonDumpMarker, err := p.TryConsumeJSON()
					if err == nil {
						jsonValue = jsonVal
						jsonHealingMarker = jsonDumpMarker
						jsonParsed = true
					} else {
						// Try primitive fallback (null, true, false, numbers)
						primitiveVal, found := p.tryConsumeJSONPrimitive()
						if found {
							jsonValue = primitiveVal
							jsonParsed = true
						} else {
							// Reset position if JSON parsing failed
							p.MoveTo(valStart)
						}
					}
				}

				// If JSON was parsed, check if val_end follows
				if jsonParsed {
					jsonEnd := p.pos
					p.ConsumeSpaces()

					// Check if at end of input (partial case)
					if p.pos >= len(p.input) {
						// Partial JSON - handle based on format and JSON type
						if format.RawArgVal != nil && !*format.RawArgVal {
							// raw_argval is false - only JSON allowed
							if isJSONObjectOrArray(jsonValue) || isJSONString(jsonValue) {
								arguments[key] = jsonValue
								argsJSON, _ := json.Marshal(arguments)
								toolStr := string(argsJSON)

								// Use jsonDumpMarker to cut precisely (matching llama.cpp lines 532-538)
								if jsonHealingMarker != "" {
									// Find jsonDumpMarker in the JSON string and cut there
									// Matching llama.cpp: GGML_ASSERT(std::string::npos != json_str.rfind(...))
									idx := strings.LastIndex(toolStr, jsonHealingMarker)
									if idx != -1 {
										toolStr = toolStr[:idx]
									} else {
										// Marker should always be found if it was returned from parseJSONWithStack
										// Log warning but continue with fallback
										jsonPreview := toolStr
										if len(jsonPreview) > 100 {
											jsonPreview = jsonPreview[:100]
										}
										xlog.Debug("jsonDumpMarker not found in JSON string, using fallback", "marker", jsonHealingMarker, "json", jsonPreview)
										// Fallback: remove trailing } if present
										if len(toolStr) > 0 && toolStr[len(toolStr)-1] == '}' {
											toolStr = toolStr[:len(toolStr)-1]
										}
									}
								} else {
									// Remove trailing } if present (matching llama.cpp line 537)
									if len(toolStr) > 0 && toolStr[len(toolStr)-1] == '}' {
										toolStr = toolStr[:len(toolStr)-1]
									}
								}
								p.AddToolCall(functionName, "", toolStr)
								return false, &ChatMsgPartialException{
									Message: "JSON arg_value detected. Waiting for more tokens for validations.",
								}
							}
						}
						// Generate partial args
						genPartialArgs := func(needle string) {
							arguments[key] = needle
							argsJSON, _ := json.Marshal(arguments)
							toolStr := string(argsJSON)
							if cleaned, isPartial := partialJSON(toolStr); isPartial {
								p.AddToolCall(functionName, "", cleaned)
							} else {
								p.AddToolCall(functionName, "", toolStr)
							}
						}
						genPartialArgs("XML_TOOL_CALL_PARTIAL_FLAG")
						return false, &ChatMsgPartialException{
							Message: "JSON arg_value detected. Waiting for more tokens for validations.",
						}
					}

					// Rewind to json_end and check if val_end follows
					p.MoveTo(jsonEnd)
					valEndSize, valEnd := tryFindValEnd()
					if valEnd != nil && AllSpace(valEnd.Prelude) && jsonHealingMarker == "" {
						// val_end follows JSON
						if len(valEnd.Groups) > 0 {
							matchedSize := valEnd.Groups[0].End - valEnd.Groups[0].Begin
							if matchedSize == valEndSize {
								// Complete val_end - use JSON value
								arguments[key] = jsonValue
							} else {
								// Partial val_end
								genPartialArgs := func(needle string) {
									arguments[key] = needle
									argsJSON, _ := json.Marshal(arguments)
									toolStr := string(argsJSON)
									if cleaned, isPartial := partialJSON(toolStr); isPartial {
										p.AddToolCall(functionName, "", cleaned)
									} else {
										p.AddToolCall(functionName, "", toolStr)
									}
								}
								genPartialArgs("XML_TOOL_CALL_PARTIAL_FLAG")
								return false, &ChatMsgPartialException{
									Message: fmt.Sprintf("Partial literal: %s", format.ValEnd),
								}
							}
						}
					} else {
						// val_end doesn't follow - rewind and parse as text
						p.MoveTo(valStart)
						jsonParsed = false
					}
				}

				// If JSON wasn't parsed or val_end didn't follow, parse as plain text
				if !jsonParsed {
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
					// This handles JSON-first parsing correctly for text fallback
					valueStr := strings.TrimSpace(valEnd.Prelude)
					value := parseParameterValue(valueStr, format)
					arguments[key] = value
				}
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

		// Parse scope_end if present (for this scope)
		if format.ScopeEnd != "" {
			tc := p.TryFindLiteral(format.ScopeEnd)
			if tc == nil {
				// Expected scope_end but not found
				if !p.isPartial {
					// If we found tool calls in this scope, it's okay to not have scope_end
					// (might be multiple scopes or incomplete)
					if !scopeToolCallsFound {
						return returnError(errors.New("expected scope_end"), recovery)
					}
					break
				}
				break
			} else if !AllSpace(tc.Prelude) {
				// Non-whitespace before scope_end - this might be another scope_start
				// Check if it's actually another scope_start
				if format.ScopeStart != "" {
					// Check if the non-whitespace is actually another scope_start
					testPos := tc.Groups[0].Begin - len(tc.Prelude)
					if testPos >= 0 && testPos < len(p.input) {
						testInput := p.input[testPos:]
						if strings.HasPrefix(testInput, format.ScopeStart) {
							// It's another scope_start, break to continue outer loop
							p.MoveTo(testPos)
							break
						}
					}
				}
				return returnError(errors.New("non-whitespace before scope_end"), recovery)
			}
			// Successfully found scope_end, continue to next scope if any
			scopeToolCallsFound = true
		} else {
			// No scope_end defined, we're done after parsing tool calls
			break
		}
	}

	return len(p.toolCalls) > 0, nil
}

// ParseMsgWithXMLToolCalls parses content with reasoning blocks and XML tool calls
// This matches llama.cpp's parse_msg_with_xml_tool_calls function
// Reference: llama.cpp/common/chat-parser-xml-toolcall.cpp lines 654-872
func (p *ChatMsgParser) ParseMsgWithXMLToolCalls(format *XMLToolCallFormat, startThink, endThink string) error {
	if format == nil {
		return errors.New("format is required")
	}

	// Default reasoning tags if not provided
	if startThink == "" {
		startThink = "<think>"
	}
	if endThink == "" {
		endThink = "</think>"
	}

	// Trim leading spaces without affecting keyword matching
	p.ConsumeSpaces()

	// Parse content
	reasoningUnclosed := false // TODO: support thinking_forced_open from syntax
	unclosedReasoningContent := ""

	for {
		// Find scope_start + tool_start using tryFind2LiteralSplitBySpaces
		tc := p.tryFind2LiteralSplitBySpaces(format.ScopeStart, format.ToolStart)
		var content string
		var toolCallStart string

		if tc != nil {
			content = tc.Prelude
			toolCallStart = p.Str(tc.Groups[0])
		} else {
			content = p.ConsumeRest()
			content = utf8TruncateSafeView(content)
		}

		// Handle unclosed think block
		if reasoningUnclosed {
			pos := strings.Index(content, endThink)
			if pos == -1 && p.pos != len(p.input) {
				unclosedReasoningContent += content
				if !(format.AllowToolcallInThink && tc != nil) {
					unclosedReasoningContent += toolCallStart
					continue
				}
			} else {
				reasoningUnclosed = false
				var reasoningContent string
				if pos == -1 {
					reasoningContent = content
					content = ""
				} else {
					reasoningContent = content[:pos]
					content = content[pos+len(endThink):]
				}
				if p.pos == len(p.input) && AllSpace(content) {
					reasoningContent = rstrip(reasoningContent)
					reasoningContent = trimPotentialPartialWord(reasoningContent, format, startThink, endThink)
					reasoningContent = rstrip(reasoningContent)
					if reasoningContent == "" {
						unclosedReasoningContent = rstrip(unclosedReasoningContent)
						unclosedReasoningContent = trimPotentialPartialWord(unclosedReasoningContent, format, startThink, endThink)
						unclosedReasoningContent = rstrip(unclosedReasoningContent)
						if unclosedReasoningContent == "" {
							continue
						}
					}
				}
				// TODO: Handle reasoning_format and reasoning_in_content from syntax
				// For now, always add to reasoning content
				p.AddReasoningContent(unclosedReasoningContent)
				p.AddReasoningContent(reasoningContent)
				unclosedReasoningContent = ""
			}
		}

		// Handle multiple think blocks
		toolcallInThink := false
		thinkStart := strings.Index(content, startThink)
		for thinkStart != -1 {
			thinkEnd := strings.Index(content[thinkStart+len(startThink):], endThink)
			if thinkEnd != -1 {
				thinkEnd += thinkStart + len(startThink)
				// Extract reasoning content
				reasoningContent := content[thinkStart+len(startThink) : thinkEnd]
				p.AddReasoningContent(reasoningContent)
				// Erase the reasoning block from content
				content, _ = eraseSpaces(content, thinkStart, thinkEnd+len(endThink)-1)
				thinkStart = strings.Index(content, startThink)
			} else {
				// Unclosed reasoning block
				if format.AllowToolcallInThink {
					unclosedReasoningContent = content[thinkStart+len(startThink):]
				} else {
					unclosedReasoningContent = content[thinkStart+len(startThink):] + toolCallStart
				}
				reasoningUnclosed = true
				content = content[:thinkStart]
				toolcallInThink = true
				break
			}
		}

		// TODO: Handle reasoning_format and reasoning_in_content
		// For now, strip content and handle unclosed end_think tokens
		content = rstrip(content)
		pos := strings.LastIndex(content, endThink)
		for pos != -1 {
			content, pos = eraseSpaces(content, pos, pos+len(endThink)-1)
			pos = strings.LastIndex(content, endThink)
		}
		// Strip leading whitespace if needed
		content = strings.TrimLeftFunc(content, unicode.IsSpace)

		// Remove potential partial suffix
		if p.pos == len(p.input) {
			if unclosedReasoningContent == "" {
				content = rstrip(content)
				content = trimPotentialPartialWord(content, format, startThink, endThink)
				content = rstrip(content)
			} else {
				unclosedReasoningContent = rstrip(unclosedReasoningContent)
				unclosedReasoningContent = trimPotentialPartialWord(unclosedReasoningContent, format, startThink, endThink)
				unclosedReasoningContent = rstrip(unclosedReasoningContent)
			}
		}

		// Consume unclosed_reasoning_content if allow_toolcall_in_think is set
		if format.AllowToolcallInThink && unclosedReasoningContent != "" {
			// TODO: Handle reasoning_format
			p.AddReasoningContent(unclosedReasoningContent)
			unclosedReasoningContent = ""
		}

		// Add content
		if content != "" {
			// TODO: Handle reasoning_format for multiple content blocks
			if p.content.Len() > 0 {
				p.AddContent("\n\n")
			}
			p.AddContent(content)
		}

		// Skip tool call if it's in thinking block and allow_toolcall_in_think is not set
		if toolcallInThink && !format.AllowToolcallInThink {
			continue
		}

		// No tool call found, break
		if tc == nil {
			break
		}

		// Parse tool calls
		p.MoveTo(tc.Groups[0].Begin)
		success, err := p.TryConsumeXMLToolCalls(format)
		if err != nil {
			// Check if it's a partial exception
			if _, ok := err.(*ChatMsgPartialException); ok {
				// Partial parse, continue
				continue
			}
			return err
		}
		if success {
			endOfTool := p.pos
			p.ConsumeSpaces()
			if p.pos != len(p.input) {
				p.MoveTo(endOfTool)
				if p.content.Len() > 0 {
					p.AddContent("\n\n")
				}
			}
		} else {
			// Tool call parsing failed, add next character as content
			if p.pos < len(p.input) {
				nextChar := string(p.input[p.pos])
				nextChar = rstrip(nextChar)
				p.AddContent(nextChar)
				p.pos++
			}
		}
	}

	return nil
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
