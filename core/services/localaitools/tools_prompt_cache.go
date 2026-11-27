package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerPromptCacheTools(s *mcp.Server, client LocalAIClient, opts Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolGetPromptCache,
		Description: "Disk prompt cache of llama.cpp: size, free disk, effective cap and state (on, paused_low_disk, disabled_disk_full) of each node, the size of each model, and the total. A node that did not answer has an error; a node that is off has off_reason. Read-only.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		st, err := client.GetPromptCache(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(st), nil, nil
	})

	if opts.DisableMutating {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolClearPromptCache,
		Description: "Delete the disk prompt cache of one model, or of every model when model is empty, on every node (a node that is offline deletes it when it is back). Later requests compute their prompts again. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args PromptCacheClearRequest) (*mcp.CallToolResult, any, error) {
		res, err := client.ClearPromptCache(ctx, args.Model)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(res), nil, nil
	})
}
