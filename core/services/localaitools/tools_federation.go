package localaitools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerFederationTools wires the federation tools. list_federation shows
// the LocalAI instances and the groups (one model name served by several
// instances); manage_federation_group changes a group with an action
// parameter, since every action edits the same group record.
func registerFederationTools(s *mcp.Server, client LocalAIClient, opts Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolListFederation,
		Description: "List the LocalAI instances that can serve a federation group (this server, id local, and the LocalAI remotes with their P2P path and models) and the groups with the health, drain flag and requests in flight of each member.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		v, err := client.GetFederation(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(v), nil, nil
	})

	if opts.DisableMutating {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolManageFederationGroup,
		Description: "Change a federation group: create (name, members, optional policy least_busy or round_robin), update (policy and/or members), delete (the members stay), drain or undrain one member (model). Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args FederationGroupAction) (*mcp.CallToolResult, any, error) {
		if err := checkFederationAction(args); err != nil {
			return errorResult(err), nil, nil
		}
		g, err := client.ManageFederationGroup(ctx, args)
		if err != nil {
			return errorResult(err), nil, nil
		}
		if g == nil {
			return jsonResult(map[string]any{"name": args.Name, "deleted": true}), nil, nil
		}
		return jsonResult(g), nil, nil
	})
}

// checkFederationAction checks the fields each action needs before the
// client is called.
func checkFederationAction(a FederationGroupAction) error {
	if a.Name == "" {
		return fmt.Errorf("name is required")
	}
	switch a.Action {
	case FederationActionCreate:
		if len(a.Members) < 2 {
			return fmt.Errorf("create needs at least two members")
		}
	case FederationActionUpdate:
		if a.Policy == "" && a.Members == nil {
			return fmt.Errorf("update needs policy or members")
		}
	case FederationActionDelete:
	case FederationActionDrain, FederationActionUndrain:
		if a.Model == "" {
			return fmt.Errorf("%s needs model", a.Action)
		}
	default:
		return fmt.Errorf("action must be create, update, delete, drain or undrain")
	}
	return nil
}
