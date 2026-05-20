package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerBrandingTools(s *mcp.Server, client LocalAIClient, opts Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolGetBranding,
		Description: "Read the configured instance branding (name, tagline, logo URLs, favicon URL).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		b, err := client.GetBranding(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(b), nil, nil
	})

	if opts.DisableMutating {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolSetBranding,
		Description: "Set the instance branding name and/or tagline. Both fields optional — nil leaves the existing value alone, an empty string resets to default. Requires user confirmation per safety rule 1. To replace the logo or favicon, point the user at the Branding section of the Settings page (file upload is intentionally not exposed over MCP).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args SetBrandingRequest) (*mcp.CallToolResult, any, error) {
		if args.InstanceName == nil && args.InstanceTagline == nil {
			return errorResultf("at least one of instance_name or instance_tagline must be provided"), nil, nil
		}
		b, err := client.SetBranding(ctx, args)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(b), nil, nil
	})
}
