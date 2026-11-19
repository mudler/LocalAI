package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/LocalAI/core/services/modelupdates"
)

func registerModelUpdateTools(s *mcp.Server, client LocalAIClient, opts Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name: ToolModelUpdates,
		Description: "Installed gallery models with state (up-to-date, update-available, edited, unknown, gone, held, failed), edited keys, last upgrade and source state. " +
			"action=list: last check; check: read the galleries now, installs nothing. Models never update by themselves: use upgrade_model.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		Action string `json:"action,omitempty" jsonschema:"list (default) or check."`
	}) (*mcp.CallToolResult, any, error) {
		var (
			snap *modelupdates.Snapshot
			err  error
		)
		switch args.Action {
		case "", "list":
			snap, err = client.ModelUpdates(ctx)
		case "check":
			snap, err = client.CheckModelUpdates(ctx)
		default:
			return errorResultf("action must be list or check"), nil, nil
		}
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(snap), nil, nil
	})

	if opts.DisableMutating {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolUpgradeModel,
		Description: "Upgrade a gallery model (upgrade, config_only, retry), remove the old files a confirmed upgrade kept (remove_old), hold it at its version (hold, unhold), or track a model from an earlier release (adopt; gallery, entry when asked). Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args UpgradeModelRequest) (*mcp.CallToolResult, any, error) {
		if args.Name == "" {
			return errorResultf("name is required"), nil, nil
		}
		if len(args.Choices) > 0 && args.Action != UpgradeModelActionUpgrade && args.Action != UpgradeModelActionConfigOnly {
			return errorResultf("choices apply only to upgrade and config_only"), nil, nil
		}
		if (args.Gallery != "" || args.Entry != "") && args.Action != UpgradeModelActionAdopt {
			return errorResultf("gallery and entry apply only to adopt"), nil, nil
		}
		var (
			out any
			err error
		)
		switch args.Action {
		case UpgradeModelActionUpgrade, UpgradeModelActionConfigOnly:
			var job string
			job, err = client.UpgradeModel(ctx, args.Name, args.Action == UpgradeModelActionConfigOnly, args.Choices)
			out = map[string]any{"job_id": job}
		case UpgradeModelActionRetry:
			var job string
			job, err = client.RetryModelUpgrade(ctx, args.Name)
			out = map[string]any{"job_id": job}
		case UpgradeModelActionRemoveOld:
			out, err = client.RemoveOldModelVersion(ctx, args.Name)
		case UpgradeModelActionHold, UpgradeModelActionUnhold:
			out, err = client.SetModelHold(ctx, args.Name, args.Action == UpgradeModelActionHold)
		case UpgradeModelActionAdopt:
			out, err = client.AdoptModel(ctx, args.Name, args.Gallery, args.Entry)
		default:
			return errorResultf("action must be one of upgrade, config_only, retry, remove_old, hold, unhold, adopt"), nil, nil
		}
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(out), nil, nil
	})
}
