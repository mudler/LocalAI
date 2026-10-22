package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerBackendTools(s *mcp.Server, client LocalAIClient, opts Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolListBackends,
		Description: "List installed backends.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		backends, err := client.ListBackends(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(backends), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolListKnownBackends,
		Description: "List backends available to install from configured backend galleries.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		backends, err := client.ListKnownBackends(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(backends), nil, nil
	})

	if opts.DisableMutating {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolInstallBackend,
		Description: "Install a backend from a backend gallery. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args InstallBackendRequest) (*mcp.CallToolResult, any, error) {
		if args.BackendName == "" {
			return errorResultf("backend_name is required"), nil, nil
		}
		jobID, err := client.InstallBackend(ctx, args)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]any{"job_id": jobID}), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolInstallLibraryBackend,
		Description: "Install a backend from an add-on library entry (a backend entry of a library source). The library resolves the image and checks its signature, so the call names only the source, the backend and the version. The backend gallery is not changed and a backend runs native code. The call returns when the install is done. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args InstallLibraryBackendRequest) (*mcp.CallToolResult, any, error) {
		if args.SourceID == "" || args.Name == "" || args.Version == "" {
			return errorResultf("source_id, name and version are required"), nil, nil
		}
		res, err := client.InstallLibraryBackend(ctx, args)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(res), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolUpgradeBackend,
		Description: "Upgrade an installed backend by name. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		Name string `json:"name" jsonschema:"The installed backend name."`
	}) (*mcp.CallToolResult, any, error) {
		if args.Name == "" {
			return errorResultf("name is required"), nil, nil
		}
		jobID, err := client.UpgradeBackend(ctx, args.Name)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]any{"job_id": jobID}), nil, nil
	})
}
