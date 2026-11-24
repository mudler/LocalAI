package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/LocalAI/pkg/enginesettings"
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

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolParseEngineCommand,
		Description: "Read a pasted command line of an engine (engine llama-cpp for llama-server) and return the model config keys and options it maps to, plus the flags that have no first-class key (unmapped) and any warnings. Read-only: the command is only tokenised, never run. To apply the result, use edit_model_config after the user confirms.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		Engine  string `json:"engine" jsonschema:"The engine id, for example llama-cpp."`
		Command string `json:"command" jsonschema:"The command line."`
	}) (*mcp.CallToolResult, any, error) {
		if args.Engine == "" || args.Command == "" {
			return errorResultf("engine and command are required"), nil, nil
		}
		res, err := client.ParseEngineCommand(ctx, args.Engine, args.Command)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(res), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolPreviewEngineCommand,
		Description: "Write the command line of an engine (engine llama-cpp for llama-server) that matches a partial model config (context_size, gpu_layers, parameters, options and so on). Read-only; the text is for display.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		Engine string                `json:"engine" jsonschema:"The engine id, for example llama-cpp."`
		Config enginesettings.Config `json:"config" jsonschema:"A partial model config."`
	}) (*mcp.CallToolResult, any, error) {
		if args.Engine == "" {
			return errorResultf("engine is required"), nil, nil
		}
		res, err := client.PreviewEngineCommand(ctx, args.Engine, args.Config)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(res), nil, nil
	})

	if opts.DisableMutating {
		return
	}

	mcp.AddTool(s, &mcp.Tool{Name: ToolCancelModelLoad, Description: "Cancel one distributed model load, named by the job_id from the model's load-status. Requires user confirmation per safety rule 1. State `stopping` means the cancel is recorded and the stop is pending, not that the work stopped; the model is released after retry_after seconds regardless."}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		Model string `json:"model" jsonschema:"The model whose load to cancel."`
		JobID string `json:"job_id" jsonschema:"The exact load attempt, from load-status. Never guess it."`
	}) (*mcp.CallToolResult, any, error) {
		if args.Model == "" || args.JobID == "" {
			return errorResultf("model and job_id are required"), nil, nil
		}
		result, err := client.CancelModelLoad(ctx, args.Model, args.JobID)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(result), nil, nil
	})

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
