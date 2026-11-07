package localaitools

import (
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// errorResult builds an MCP CallToolResult that surfaces err to the LLM via
// the standard IsError + TextContent convention. The LLM is instructed (in
// 10_safety.md) to surface tool errors verbatim.
func errorResult(err error) *mcp.CallToolResult {
	r := &mcp.CallToolResult{}
	r.SetError(err)
	return r
}

// errorResultf is the printf cousin of errorResult.
func errorResultf(format string, args ...any) *mcp.CallToolResult {
	return errorResult(fmt.Errorf(format, args...))
}

// jsonResult marshals v as pretty JSON and returns it as the tool's
// TextContent payload. Errors during marshalling become tool errors.
func jsonResult(v any) *mcp.CallToolResult {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errorResult(fmt.Errorf("marshal tool result: %w", err))
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(data)}},
	}
}
