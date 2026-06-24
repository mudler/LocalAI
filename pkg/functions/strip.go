package functions

import "strings"

// StripToolCallMarkup extracts the non-tool-call content from a string
// by reusing the iterative XML parser which already separates content
// from tool calls. Returns the remaining text, trimmed.
func StripToolCallMarkup(content string) string {
	for _, fmtPreset := range getAllXMLFormats() {
		if fmtPreset.format == nil {
			continue
		}
		if pr, ok := tryParseXMLFromScopeStart(content, fmtPreset.format, false); ok && len(pr.ToolCalls) > 0 {
			return strings.TrimSpace(pr.Content)
		}
	}
	return strings.TrimSpace(content)
}
