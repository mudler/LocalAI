package functions

import (
	"regexp"
	"strings"
)

// toolCallBlockRe matches closed <tool_call>...</tool_call> blocks
var toolCallBlockRe = regexp.MustCompile(`(?s)<tool_call>.*?</tool_call>`)

// functionBlockRe matches closed <function=...>...</function> blocks
var functionBlockRe = regexp.MustCompile(`(?s)<function=[^>]*>.*?</function>`)

// openToolCallRe matches an open-ended <tool_call> at the end of string (no closing tag)
var openToolCallRe = regexp.MustCompile(`(?s)<tool_call>[^<]*$`)

// openFunctionRe matches an open-ended <function=...> at the end of string (no closing tag)
var openFunctionRe = regexp.MustCompile(`(?s)<function=[^>]*>[^<]*$`)

// StripToolCallMarkup removes tool call markup blocks from content.
// It removes closed <tool_call>...</tool_call> and <function=...>...</function> blocks,
// plus open-ended ones at the end of the string.
// Returns the remaining text, trimmed of whitespace.
func StripToolCallMarkup(content string) string {
	// Remove closed blocks
	content = toolCallBlockRe.ReplaceAllString(content, "")
	content = functionBlockRe.ReplaceAllString(content, "")

	// Remove open-ended blocks at end of string
	content = openToolCallRe.ReplaceAllString(content, "")
	content = openFunctionRe.ReplaceAllString(content, "")

	return strings.TrimSpace(content)
}
