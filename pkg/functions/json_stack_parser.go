package functions

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode"
)

// JSONStackElementType represents the type of JSON stack element
type JSONStackElementType int

const (
	JSONStackElementObject JSONStackElementType = iota
	JSONStackElementKey
	JSONStackElementArray
)

// JSONStackElement represents an element in the JSON parsing stack
type JSONStackElement struct {
	Type JSONStackElementType
	Key  string
}

// JSONErrorLocator tracks JSON parsing state and errors
type JSONErrorLocator struct {
	position         int
	foundError       bool
	lastToken        string
	exceptionMessage string
	stack            []JSONStackElement
}

// parseJSONWithStack parses JSON with stack tracking, matching llama.cpp's common_json_parse
// Returns the parsed JSON value, whether it was healed, and any error
func parseJSONWithStack(input string, healingMarker string) (any, bool, string, error) {
	if healingMarker == "" {
		// No healing marker, just try to parse normally
		var result any
		if err := json.Unmarshal([]byte(input), &result); err != nil {
			return nil, false, "", err
		}
		return result, false, "", nil
	}

	// Try to parse complete JSON first
	var result any
	if err := json.Unmarshal([]byte(input), &result); err == nil {
		return result, false, "", nil
	}

	// Parsing failed, need to track stack and heal
	errLoc := &JSONErrorLocator{
		position:   0,
		foundError: false,
		stack:      make([]JSONStackElement, 0),
	}

	// Parse with stack tracking to find where error occurs
	errorPos, err := parseJSONWithStackTracking(input, errLoc)
	if err == nil && !errLoc.foundError {
		// No error found, should have parsed successfully
		var result any
		if err := json.Unmarshal([]byte(input), &result); err != nil {
			return nil, false, "", err
		}
		return result, false, "", nil
	}

	if !errLoc.foundError || len(errLoc.stack) == 0 {
		// Can't heal without stack information
		return nil, false, "", errors.New("incomplete JSON")
	}

	// Build closing braces/brackets from stack
	closing := ""
	for i := len(errLoc.stack) - 1; i >= 0; i-- {
		el := errLoc.stack[i]
		if el.Type == JSONStackElementObject {
			closing += "}"
		} else if el.Type == JSONStackElementArray {
			closing += "]"
		}
		// Keys don't add closing characters
	}

	// Get the partial input up to error position
	partialInput := input
	if errorPos > 0 && errorPos < len(input) {
		partialInput = input[:errorPos]
	}

	// Find last non-space character
	lastNonSpacePos := strings.LastIndexFunc(partialInput, func(r rune) bool {
		return !unicode.IsSpace(r)
	})
	if lastNonSpacePos == -1 {
		return nil, false, "", errors.New("cannot heal a truncated JSON that stopped in an unknown location")
	}
	lastNonSpaceChar := rune(partialInput[lastNonSpacePos])

	// Check if we stopped on a number
	wasMaybeNumber := func() bool {
		if len(partialInput) > 0 && unicode.IsSpace(rune(partialInput[len(partialInput)-1])) {
			return false
		}
		return unicode.IsDigit(lastNonSpaceChar) ||
			lastNonSpaceChar == '.' ||
			lastNonSpaceChar == 'e' ||
			lastNonSpaceChar == 'E' ||
			lastNonSpaceChar == '-'
	}

	// Check for partial unicode escape sequences
	partialUnicodeRegex := regexp.MustCompile(`\\u(?:[0-9a-fA-F](?:[0-9a-fA-F](?:[0-9a-fA-F](?:[0-9a-fA-F])?)?)?)?$`)
	unicodeMarkerPadding := "udc00"
	lastUnicodeMatch := partialUnicodeRegex.FindStringSubmatch(partialInput)
	if lastUnicodeMatch != nil {
		// Pad the escape sequence
		unicodeMarkerPadding = strings.Repeat("0", 6-len(lastUnicodeMatch[0]))
		// Check if it's a high surrogate
		if len(lastUnicodeMatch[0]) >= 4 {
			seq := lastUnicodeMatch[0]
			if seq[0] == '\\' && seq[1] == 'u' {
				third := strings.ToLower(string(seq[2]))
				if third == "d" {
					fourth := strings.ToLower(string(seq[3]))
					if fourth == "8" || fourth == "9" || fourth == "a" || fourth == "b" {
						// High surrogate, add low surrogate
						unicodeMarkerPadding += "\\udc00"
					}
				}
			}
		}
	}

	canParse := func(str string) bool {
		var test any
		return json.Unmarshal([]byte(str), &test) == nil
	}

	// Heal based on stack top element type
	healedJSON := partialInput
	jsonDumpMarker := ""
	topElement := errLoc.stack[len(errLoc.stack)-1]

	if topElement.Type == JSONStackElementKey {
		// We're inside an object value
		if lastNonSpaceChar == ':' && canParse(healedJSON+"1"+closing) {
			jsonDumpMarker = "\"" + healingMarker
			healedJSON += jsonDumpMarker + "\"" + closing
		} else if canParse(healedJSON + ": 1" + closing) {
			jsonDumpMarker = ":\"" + healingMarker
			healedJSON += jsonDumpMarker + "\"" + closing
		} else if lastNonSpaceChar == '{' && canParse(healedJSON+closing) {
			jsonDumpMarker = "\"" + healingMarker
			healedJSON += jsonDumpMarker + "\": 1" + closing
		} else if canParse(healedJSON + "\"" + closing) {
			jsonDumpMarker = healingMarker
			healedJSON += jsonDumpMarker + "\"" + closing
		} else if len(healedJSON) > 0 && healedJSON[len(healedJSON)-1] == '\\' && canParse(healedJSON+"\\\""+closing) {
			jsonDumpMarker = "\\" + healingMarker
			healedJSON += jsonDumpMarker + "\"" + closing
		} else if canParse(healedJSON + unicodeMarkerPadding + "\"" + closing) {
			jsonDumpMarker = unicodeMarkerPadding + healingMarker
			healedJSON += jsonDumpMarker + "\"" + closing
		} else {
			// Find last colon and cut back
			lastColon := strings.LastIndex(healedJSON, ":")
			if lastColon == -1 {
				return nil, false, "", errors.New("cannot heal a truncated JSON that stopped in an unknown location")
			}
			jsonDumpMarker = "\"" + healingMarker
			healedJSON = healedJSON[:lastColon+1] + jsonDumpMarker + "\"" + closing
		}
	} else if topElement.Type == JSONStackElementArray {
		// We're inside an array
		if (lastNonSpaceChar == ',' || lastNonSpaceChar == '[') && canParse(healedJSON+"1"+closing) {
			jsonDumpMarker = "\"" + healingMarker
			healedJSON += jsonDumpMarker + "\"" + closing
		} else if canParse(healedJSON + "\"" + closing) {
			jsonDumpMarker = healingMarker
			healedJSON += jsonDumpMarker + "\"" + closing
		} else if len(healedJSON) > 0 && healedJSON[len(healedJSON)-1] == '\\' && canParse(healedJSON+"\\\""+closing) {
			jsonDumpMarker = "\\" + healingMarker
			healedJSON += jsonDumpMarker + "\"" + closing
		} else if canParse(healedJSON + unicodeMarkerPadding + "\"" + closing) {
			jsonDumpMarker = unicodeMarkerPadding + healingMarker
			healedJSON += jsonDumpMarker + "\"" + closing
		} else if !wasMaybeNumber() && canParse(healedJSON+", 1"+closing) {
			jsonDumpMarker = ",\"" + healingMarker
			healedJSON += jsonDumpMarker + "\"" + closing
		} else {
			lastBracketOrComma := strings.LastIndexAny(healedJSON, "[,")
			if lastBracketOrComma == -1 {
				return nil, false, "", errors.New("cannot heal a truncated JSON array stopped in an unknown location")
			}
			jsonDumpMarker = "\"" + healingMarker
			healedJSON = healedJSON[:lastBracketOrComma+1] + jsonDumpMarker + "\"" + closing
		}
	} else if topElement.Type == JSONStackElementObject {
		// We're inside an object (expecting a key)
		if (lastNonSpaceChar == '{' && canParse(healedJSON+closing)) ||
			(lastNonSpaceChar == ',' && canParse(healedJSON+"\"\": 1"+closing)) {
			jsonDumpMarker = "\"" + healingMarker
			healedJSON += jsonDumpMarker + "\": 1" + closing
		} else if !wasMaybeNumber() && canParse(healedJSON+",\"\": 1"+closing) {
			jsonDumpMarker = ",\"" + healingMarker
			healedJSON += jsonDumpMarker + "\": 1" + closing
		} else if canParse(healedJSON + "\": 1" + closing) {
			jsonDumpMarker = healingMarker
			healedJSON += jsonDumpMarker + "\": 1" + closing
		} else if len(healedJSON) > 0 && healedJSON[len(healedJSON)-1] == '\\' && canParse(healedJSON+"\\\": 1"+closing) {
			jsonDumpMarker = "\\" + healingMarker
			healedJSON += jsonDumpMarker + "\": 1" + closing
		} else if canParse(healedJSON + unicodeMarkerPadding + "\": 1" + closing) {
			jsonDumpMarker = unicodeMarkerPadding + healingMarker
			healedJSON += jsonDumpMarker + "\": 1" + closing
		} else {
			lastColon := strings.LastIndex(healedJSON, ":")
			if lastColon == -1 {
				return nil, false, "", errors.New("cannot heal a truncated JSON object stopped in an unknown location")
			}
			jsonDumpMarker = "\"" + healingMarker
			healedJSON = healedJSON[:lastColon+1] + jsonDumpMarker + "\"" + closing
		}
	} else {
		return nil, false, "", errors.New("cannot heal a truncated JSON object stopped in an unknown location")
	}

	// Try to parse the healed JSON
	var healedValue any
	if err := json.Unmarshal([]byte(healedJSON), &healedValue); err != nil {
		return nil, false, "", err
	}

	// Remove healing marker from result
	cleaned := removeHealingMarkerFromJSONAny(healedValue, healingMarker)
	return cleaned, true, jsonDumpMarker, nil
}

// parseJSONWithStackTracking parses JSON while tracking the stack structure
// Returns the error position and any error encountered
// This implements stack tracking similar to llama.cpp's json_error_locator
func parseJSONWithStackTracking(input string, errLoc *JSONErrorLocator) (int, error) {
	// First, try to parse to get exact error position
	decoder := json.NewDecoder(strings.NewReader(input))
	var test any
	err := decoder.Decode(&test)
	if err != nil {
		errLoc.foundError = true
		errLoc.exceptionMessage = err.Error()

		var errorPos int
		if syntaxErr, ok := err.(*json.SyntaxError); ok {
			errorPos = int(syntaxErr.Offset)
			errLoc.position = errorPos
		} else {
			// Fallback: use end of input
			errorPos = len(input)
			errLoc.position = errorPos
		}

		// Now build the stack by parsing up to the error position
		// This matches llama.cpp's approach of tracking stack during SAX parsing
		partialInput := input
		if errorPos > 0 && errorPos < len(input) {
			partialInput = input[:errorPos]
		}

		// Track stack by parsing character by character up to error
		pos := 0
		inString := false
		escape := false
		keyStart := -1
		keyEnd := -1

		for pos < len(partialInput) {
			ch := partialInput[pos]

			if escape {
				escape = false
				pos++
				continue
			}

			if ch == '\\' {
				escape = true
				pos++
				continue
			}

			if ch == '"' {
				if !inString {
					// Starting a string
					inString = true
					// Check if we're in an object context (expecting a key)
					if len(errLoc.stack) > 0 {
						top := errLoc.stack[len(errLoc.stack)-1]
						if top.Type == JSONStackElementObject {
							// This could be a key
							keyStart = pos + 1 // Start after quote
						}
					}
				} else {
					// Ending a string
					inString = false
					if keyStart != -1 {
						// This was potentially a key, extract it
						keyEnd = pos
						key := partialInput[keyStart:keyEnd]

						// Look ahead to see if next non-whitespace is ':'
						nextPos := pos + 1
						for nextPos < len(partialInput) && unicode.IsSpace(rune(partialInput[nextPos])) {
							nextPos++
						}
						if nextPos < len(partialInput) && partialInput[nextPos] == ':' {
							// This is a key, add it to stack
							errLoc.stack = append(errLoc.stack, JSONStackElement{Type: JSONStackElementKey, Key: key})
						}
						keyStart = -1
						keyEnd = -1
					}
				}
				pos++
				continue
			}

			if inString {
				pos++
				continue
			}

			// Handle stack operations (outside strings)
			if ch == '{' {
				errLoc.stack = append(errLoc.stack, JSONStackElement{Type: JSONStackElementObject})
			} else if ch == '}' {
				// Pop object and any key on top (keys are popped when value starts, but handle here too)
				for len(errLoc.stack) > 0 {
					top := errLoc.stack[len(errLoc.stack)-1]
					errLoc.stack = errLoc.stack[:len(errLoc.stack)-1]
					if top.Type == JSONStackElementObject {
						break
					}
				}
			} else if ch == '[' {
				errLoc.stack = append(errLoc.stack, JSONStackElement{Type: JSONStackElementArray})
			} else if ch == ']' {
				// Pop array
				for len(errLoc.stack) > 0 {
					top := errLoc.stack[len(errLoc.stack)-1]
					errLoc.stack = errLoc.stack[:len(errLoc.stack)-1]
					if top.Type == JSONStackElementArray {
						break
					}
				}
			} else if ch == ':' {
				// Colon means we're starting a value, pop the key if it's on stack
				if len(errLoc.stack) > 0 && errLoc.stack[len(errLoc.stack)-1].Type == JSONStackElementKey {
					errLoc.stack = errLoc.stack[:len(errLoc.stack)-1]
				}
			}
			// Note: commas and whitespace don't affect stack structure

			pos++
		}

		return errorPos, err
	}

	// No error, parse was successful - build stack anyway for completeness
	// (though we shouldn't need healing in this case)
	pos := 0
	inString := false
	escape := false

	for pos < len(input) {
		ch := input[pos]

		if escape {
			escape = false
			pos++
			continue
		}

		if ch == '\\' {
			escape = true
			pos++
			continue
		}

		if ch == '"' {
			inString = !inString
			pos++
			continue
		}

		if inString {
			pos++
			continue
		}

		if ch == '{' {
			errLoc.stack = append(errLoc.stack, JSONStackElement{Type: JSONStackElementObject})
		} else if ch == '}' {
			for len(errLoc.stack) > 0 {
				top := errLoc.stack[len(errLoc.stack)-1]
				errLoc.stack = errLoc.stack[:len(errLoc.stack)-1]
				if top.Type == JSONStackElementObject {
					break
				}
			}
		} else if ch == '[' {
			errLoc.stack = append(errLoc.stack, JSONStackElement{Type: JSONStackElementArray})
		} else if ch == ']' {
			for len(errLoc.stack) > 0 {
				top := errLoc.stack[len(errLoc.stack)-1]
				errLoc.stack = errLoc.stack[:len(errLoc.stack)-1]
				if top.Type == JSONStackElementArray {
					break
				}
			}
		}

		pos++
	}

	return len(input), nil
}
