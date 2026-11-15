package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerAliasTools wires the conversational alias-management tools. An
// alias redirects all traffic for one model name to another configured
// model; list_aliases enumerates them, set_alias creates or swaps the
// target. Deletion reuses the existing delete_model tool, which works on
// any config including an alias.
func registerAliasTools(s *mcp.Server, client LocalAIClient, opts Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolListAliases,
		Description: "List every configured model alias and the target model it routes to.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		aliases, err := client.ListAliases(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(aliases), nil, nil
	})

	if opts.DisableMutating {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolSetAlias,
		Description: "Create a model alias (name -> target) or swap an existing alias's target. The target must be an existing, non-alias, enabled model. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		Name   string `json:"name"   jsonschema:"The alias name clients will call."`
		Target string `json:"target" jsonschema:"The existing model the alias routes to."`
	}) (*mcp.CallToolResult, any, error) {
		if args.Name == "" {
			return errorResultf("name is required"), nil, nil
		}
		if args.Target == "" {
			return errorResultf("target is required"), nil, nil
		}
		if err := client.SetAlias(ctx, args.Name, args.Target); err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(AliasInfo{Name: args.Name, Target: args.Target}), nil, nil
	})
}
