package localaitools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The shared folders are /shared/<name> folders of the chats' workspace that
// users share. A member (a user, or @everyone) can read or edit a folder;
// members find its pages under Pages, "Shared with you", and their chats see
// it at /workspace/shared-<name>.
func registerSharedFolderTools(s *mcp.Server, client LocalAIClient, opts Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolListSharedFolders,
		Description: "List the shared folders with their id, path, page count and members. A member is a user ID (with the user's name) or @everyone, with the mode read or write.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		out, err := client.ListSharedFolders(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(out), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolListSharedFolderEvents,
		Description: "Read the audit of the shared folders, newest first: who created, renamed, removed or restored a folder and who gave or took a member's access.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args SharedFolderEventsQuery) (*mcp.CallToolResult, any, error) {
		limit := args.Limit
		if limit <= 0 {
			limit = 50
		}
		out, err := client.ListSharedFolderEvents(ctx, min(limit, 500))
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(out), nil, nil
	})

	if opts.DisableMutating {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolCreateSharedFolder,
		Description: "Create the shared folder /shared/<name> with no members. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args CreateSharedFolderRequest) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(args.Name) == "" {
			return errorResultf("name is required"), nil, nil
		}
		out, err := client.CreateSharedFolder(ctx, args.Name)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(out), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolRenameSharedFolder,
		Description: "Rename a shared folder by id. Its members and pages stay. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args RenameSharedFolderRequest) (*mcp.CallToolResult, any, error) {
		if args.ID <= 0 || strings.TrimSpace(args.Name) == "" {
			return errorResultf("id and name are required"), nil, nil
		}
		out, err := client.RenameSharedFolder(ctx, args.ID, args.Name)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(out), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolDeleteSharedFolder,
		Description: "Remove a shared folder by id. Its files go to the trash; the answer's undo_rev restores it with restore_shared_folder. Every user's consent to its pages is dropped. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args SharedFolderRef) (*mcp.CallToolResult, any, error) {
		if args.ID <= 0 {
			return errorResultf("id is required"), nil, nil
		}
		out, err := client.DeleteSharedFolder(ctx, args.ID)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(out), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolRestoreSharedFolder,
		Description: "Restore a removed shared folder with the undo_rev that delete_shared_folder returned. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args RestoreSharedFolderRequest) (*mcp.CallToolResult, any, error) {
		if args.Rev <= 0 {
			return errorResultf("rev is required"), nil, nil
		}
		out, err := client.RestoreSharedFolder(ctx, args.Rev)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(out), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolSetSharedFolderMember,
		Description: "Give a user (ID, email or name) or @everyone access to a shared folder, or change it: mode read (can read) or write (can edit). Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args SetSharedFolderMemberRequest) (*mcp.CallToolResult, any, error) {
		if args.ID <= 0 || strings.TrimSpace(args.Member) == "" {
			return errorResultf("id and member are required"), nil, nil
		}
		if args.Mode != "read" && args.Mode != "write" {
			return errorResultf("mode must be read or write"), nil, nil
		}
		out, err := client.SetSharedFolderMember(ctx, args.ID, args.Member, args.Mode)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(out), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolRemoveSharedFolderMember,
		Description: "Take a member's access to a shared folder away. The folder and its pages disappear for that user. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args RemoveSharedFolderMemberRequest) (*mcp.CallToolResult, any, error) {
		if args.ID <= 0 || strings.TrimSpace(args.Member) == "" {
			return errorResultf("id and member are required"), nil, nil
		}
		out, err := client.RemoveSharedFolderMember(ctx, args.ID, args.Member)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(out), nil, nil
	})
}
