package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerConfigTools(s *mcp.Server, client LocalAIClient, opts Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolGetModelConfig,
		Description: "Read the YAML/JSON config of an installed model. Use this before edit_model_config to show the user a diff.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		Name string `json:"name" jsonschema:"The installed model name."`
	}) (*mcp.CallToolResult, any, error) {
		if args.Name == "" {
			return errorResultf("name is required"), nil, nil
		}
		cfg, err := client.GetModelConfig(ctx, args.Name)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(cfg), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolVRAMEstimate,
		Description: "Estimate VRAM usage for an installed model under a given config (context size, GPU layers, KV cache quantization).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args VRAMEstimateRequest) (*mcp.CallToolResult, any, error) {
		if args.ModelName == "" {
			return errorResultf("model_name is required"), nil, nil
		}
		est, err := client.VRAMEstimate(ctx, args)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(est), nil, nil
	})

	if opts.DisableMutating {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolEditModelConfig,
		Description: "Patch (deep-merge) JSON into an installed model's config. Requires user confirmation per safety rule 1; show a diff first.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		Name  string         `json:"name"  jsonschema:"The installed model name."`
		Patch map[string]any `json:"patch" jsonschema:"Deep-merge JSON patch — only the changed keys."`
	}) (*mcp.CallToolResult, any, error) {
		if args.Name == "" {
			return errorResultf("name is required"), nil, nil
		}
		if len(args.Patch) == 0 {
			return errorResultf("patch is required"), nil, nil
		}
		if err := client.EditModelConfig(ctx, args.Name, args.Patch); err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]any{"patched": args.Name}), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolReloadModels,
		Description: "Reload all model configs from disk so changes (e.g. from edit_model_config) take effect. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		if err := client.ReloadModels(ctx); err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]any{"reloaded": true}), nil, nil
	})
}
