package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerModelTools(s *mcp.Server, client LocalAIClient, opts Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolGallerySearch,
		Description: "Search configured galleries for installable models. Returns name, gallery, description, license and tags. Always run this before install_model.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args GallerySearchQuery) (*mcp.CallToolResult, any, error) {
		hits, err := client.GallerySearch(ctx, args)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(hits), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolListInstalledModels,
		Description: "List models currently installed on this LocalAI. Optional capability filter (chat, completion, embeddings, image, tts, transcript, rerank, vad).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		Capability Capability `json:"capability,omitempty" jsonschema:"Filter to models advertising this capability. One of: chat, completion, embeddings, image, tts, transcript, rerank, vad. Empty value = no filter."`
	}) (*mcp.CallToolResult, any, error) {
		models, err := client.ListInstalledModels(ctx, args.Capability)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(models), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolListGalleries,
		Description: "List configured model galleries.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		galleries, err := client.ListGalleries(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(galleries), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolGetJobStatus,
		Description: "Poll the status of an install/delete/upgrade job by id. Returns processed, progress, message, and error fields.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		JobID string `json:"job_id" jsonschema:"The job id returned by install_model / install_backend / upgrade_backend / delete_model."`
	}) (*mcp.CallToolResult, any, error) {
		if args.JobID == "" {
			return errorResultf("job_id is required"), nil, nil
		}
		status, err := client.GetJobStatus(ctx, args.JobID)
		if err != nil {
			return errorResult(err), nil, nil
		}
		if status == nil {
			return errorResultf("no job with id %q", args.JobID), nil, nil
		}
		return jsonResult(status), nil, nil
	})

	if opts.DisableMutating {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolInstallModel,
		Description: "Install a model from a gallery. Requires explicit user confirmation per safety rule 1. Returns a job id; poll with get_job_status.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args InstallModelRequest) (*mcp.CallToolResult, any, error) {
		// Empty-string check at the tool layer: the SDK schema validator
		// only enforces presence, not non-empty, and we want a consistent
		// error regardless of which LocalAIClient backs the tool.
		if args.ModelName == "" {
			return errorResultf("model_name is required"), nil, nil
		}
		jobID, err := client.InstallModel(ctx, args)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]any{"job_id": jobID}), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolImportModelURI,
		Description: "Import a model from a URI (HuggingFace link, OCI image, file path, or HTTP URL). The importer auto-detects the backend; when multiple backends could handle the source, the response sets ambiguous_backend=true and lists candidates. Surface them to the user, then call again with backend_preference set. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args ImportModelURIRequest) (*mcp.CallToolResult, any, error) {
		if args.URI == "" {
			return errorResultf("uri is required"), nil, nil
		}
		resp, err := client.ImportModelURI(ctx, args)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(resp), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolDeleteModel,
		Description: "Delete an installed model by name. Requires explicit user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		Name string `json:"name" jsonschema:"The installed model name."`
	}) (*mcp.CallToolResult, any, error) {
		if args.Name == "" {
			return errorResultf("name is required"), nil, nil
		}
		if err := client.DeleteModel(ctx, args.Name); err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]any{"deleted": args.Name}), nil, nil
	})
}
