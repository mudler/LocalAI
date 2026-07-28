package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerPIITools(s *mcp.Server, client LocalAIClient, _ Options) {
	// The regex pattern tools (list/test/set/persist) were removed with
	// the regex tier. Detection policy now lives on each detector model's
	// pii_detection block (managed via the model config tools/UI), so the
	// only PII tool is the read-only audit-event view.
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolGetPIIEvents,
		Description: "Recent PII redaction events. Filter by correlation_id (joins to a usage record), user_id, or pattern_id (e.g. ner:EMAIL). Events never carry the matched value — only an 8-char sha256 prefix so admins can dedupe recurring leaks.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args PIIEventsQuery) (*mcp.CallToolResult, any, error) {
		events, err := client.GetPIIEvents(ctx, args)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(events), nil, nil
	})
}
