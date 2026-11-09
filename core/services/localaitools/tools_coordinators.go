package localaitools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerCoordinatorTools exposes the administrator's oversight of the
// coordinator agents: list them, pause and resume one, read the audit log.
// Making or editing a coordinator is a user act on a feature route (the
// agents feature), not an administrator route, so it has no tool here.
func registerCoordinatorTools(s *mcp.Server, client LocalAIClient, opts Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolListCoordinators,
		Description: "List the coordinator agents of every user: owner, whether it is paused and by whom, the next scheduled wake, and the runs and messages it used today.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		out, err := client.ListCoordinators(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(out), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: ToolListCoordinatorAudit,
		Description: "Read the audit log of the coordinators, newest first: messages sent, attempts denied or blocked (with the reason), wakes, pauses and helper events. " +
			"Holds the length and hash of a message, never its text. Filters by coordinator, kind or outcome.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args CoordinatorAuditQuery) (*mcp.CallToolResult, any, error) {
		if args.Limit <= 0 {
			args.Limit = 50
		}
		args.Limit = min(args.Limit, 500)
		out, err := client.ListCoordinatorAudit(ctx, args)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(out), nil, nil
	})

	if opts.DisableMutating {
		return
	}
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolPauseCoordinator,
		Description: "Pause a coordinator: it stops all its outbound messages, its run and helpers are cancelled, and messages to it are held until resume. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args CoordinatorRef) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(args.ID) == "" {
			return errorResultf("id is required"), nil, nil
		}
		out, err := client.PauseCoordinator(ctx, args.ID)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(out), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolResumeCoordinator,
		Description: "Resume a paused coordinator: the messages held meanwhile start as one turn. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args CoordinatorRef) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(args.ID) == "" {
			return errorResultf("id is required"), nil, nil
		}
		out, err := client.ResumeCoordinator(ctx, args.ID)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(out), nil, nil
	})
}
