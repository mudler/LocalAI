package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerVoiceProfileTools(s *mcp.Server, client LocalAIClient, opts Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolListVoiceProfiles,
		Description: "List reusable voice-cloning profiles. Returns stable voice URI values suitable for TTSRequest.voice; filesystem paths are never exposed.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		profiles, err := client.ListVoiceProfiles(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(profiles), nil, nil
	})

	if opts.DisableMutating {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolCreateVoiceProfile,
		Description: "Create a reusable voice-cloning profile from a base64 16-bit PCM WAV (mono 24 kHz recommended) and its exact transcript. consent_confirmed must be true. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args CreateVoiceProfileRequest) (*mcp.CallToolResult, any, error) {
		if args.Name == "" || args.Transcript == "" || args.AudioBase64 == "" {
			return errorResultf("name, transcript, and audio_base64 are required"), nil, nil
		}
		if !args.ConsentConfirmed {
			return errorResultf("consent_confirmed must be true"), nil, nil
		}
		profile, err := client.CreateVoiceProfile(ctx, args)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(profile), nil, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolDeleteVoiceProfile,
		Description: "Permanently delete a reusable voice-cloning profile by opaque UUID. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args DeleteVoiceProfileRequest) (*mcp.CallToolResult, any, error) {
		if args.ID == "" {
			return errorResultf("id is required"), nil, nil
		}
		if err := client.DeleteVoiceProfile(ctx, args.ID); err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]any{"deleted": true, "id": args.ID}), nil, nil
	})
}
