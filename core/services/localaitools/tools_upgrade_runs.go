package localaitools

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Item kinds an upgrade run takes from the assistant. Add-ons are not among
// them: an add-on upgrade needs the administrator's own browser session and
// the add-on owner's approval, which an assistant tool cannot give (see
// .agents/localai-assistant-mcp.md).
const (
	UpgradeRunKindGallery = "gallery"
	UpgradeRunKindLibrary = "library"
	UpgradeRunKindModel   = "model"
	upgradeRunKindAddon   = "addon"
)

// errAddonRunItem is the refusal of an add-on item.
const errAddonRunItem = "add-ons are not updated by the assistant: an add-on upgrade needs your own browser session; upgrade it from the Updates page or the add-on page"

// CheckUpgradeRunItems refuses items the assistant may not start: an add-on,
// an unknown kind or an empty name. Both clients and the tool use it.
func CheckUpgradeRunItems(items []UpgradeRunItemRef) error {
	if len(items) == 0 {
		return errors.New("items is required")
	}
	for _, it := range items {
		switch it.Kind {
		case UpgradeRunKindGallery, UpgradeRunKindLibrary, UpgradeRunKindModel:
		case upgradeRunKindAddon:
			return fmt.Errorf("%s (%s)", errAddonRunItem, it.Name)
		default:
			return fmt.Errorf("kind must be gallery, library or model (%q)", it.Kind)
		}
		if it.Name == "" {
			return errors.New("name is required for every item")
		}
	}
	return nil
}

func registerUpgradeRunTools(s *mcp.Server, client LocalAIClient, opts Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolUpgradeRunStatus,
		Description: "Read an upgrade run by id, or with no id the running run and the recent ones: each item with phase, progress, error and job id.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		ID string `json:"id,omitempty" jsonschema:"Run id; omit for the running and recent runs."`
	}) (*mcp.CallToolResult, any, error) {
		runs, err := client.UpgradeRuns(ctx, args.ID)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]any{"runs": runs}), nil, nil
	})

	if opts.DisableMutating {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolUpgradeRunStart,
		Description: "Update several backends and gallery models in one server-owned run (backends first, then models one at a time). Not add-ons. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args UpgradeRunStartRequest) (*mcp.CallToolResult, any, error) {
		if err := CheckUpgradeRunItems(args.Items); err != nil {
			return errorResult(err), nil, nil
		}
		run, err := client.StartUpgradeRun(ctx, args)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(run), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolUpgradeRunCancel,
		Description: "Cancel an upgrade run, or one item of it (key). Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		ID  string `json:"id"            jsonschema:"Run id (from upgrade_run_status)."`
		Key string `json:"key,omitempty" jsonschema:"Item key <kind>:<name> to cancel one item; omit to cancel the run."`
	}) (*mcp.CallToolResult, any, error) {
		if args.ID == "" {
			return errorResultf("id is required"), nil, nil
		}
		run, err := client.CancelUpgradeRun(ctx, args.ID, args.Key)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(run), nil, nil
	})
}
