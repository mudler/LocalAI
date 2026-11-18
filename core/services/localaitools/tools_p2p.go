package localaitools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerP2PTools wires the P2P tools: the state of the layer, the paired
// devices, the invites and the relays, and revoking a device or canceling an
// invite. Minting an invite, adding or removing a relay and a new identity are not
// tools: a link and a claim code are credentials, and a new identity unpairs
// everything (see adminRoutesWithoutTool).
func registerP2PTools(s *mcp.Server, client LocalAIClient, opts Options) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolGetP2PStatus,
		Description: "Show the P2P mode (off, connect, share), the peer ID, reachability and the number of paired devices and pending invites.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		st, err := client.GetP2PStatus(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(st), nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolListP2PDevices,
		Description: "List the devices paired over P2P and how each one is connected now.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		devs, err := client.ListP2PDevices(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(devs), nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolListP2PInvites,
		Description: "List the P2P invites and their state (pending, used, expired, canceled), without the links.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		invs, err := client.ListP2PInvites(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(invs), nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolListP2PRelays,
		Description: "List the P2P relays the server knows, with their mode and sync state.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		rs, err := client.ListP2PRelays(ctx)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(rs), nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolGetP2PRelayLimits,
		Description: "Read the limits of one P2P relay: the data, the time and the bandwidth of one circuit (zero means no limit), and whether this server may change them.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		ID string `json:"id" jsonschema:"relay ID from list_p2p_relays"`
	}) (*mcp.CallToolResult, any, error) {
		info, err := client.GetP2PRelayLimits(ctx, args.ID)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(info), nil, nil
	})

	if opts.DisableMutating {
		return
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolRevokeP2PDevice,
		Description: "Revoke a P2P device: it is cut off at once and must pair again with a new invite. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		ID string `json:"id" jsonschema:"device ID from list_p2p_devices"`
	}) (*mcp.CallToolResult, any, error) {
		if err := client.RevokeP2PDevice(ctx, args.ID); err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]any{"id": args.ID, "revoked": true}), nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{
		Name:        ToolCancelP2PInvite,
		Description: "Cancel a pending P2P invite so its link no longer works. Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args struct {
		ID string `json:"id" jsonschema:"invite ID from list_p2p_invites"`
	}) (*mcp.CallToolResult, any, error) {
		if err := client.CancelP2PInvite(ctx, args.ID); err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(map[string]any{"id": args.ID, "canceled": true}), nil, nil
	})
	mcp.AddTool(s, &mcp.Tool{
		Name: ToolSetP2PRelayLimits,
		Description: "Change the data, time or bandwidth limit of one circuit on a P2P relay this server administers; a limit left out stays, zero removes it. A new bandwidth limit applies to the next circuit; a new data or time limit restarts the relay's circuit service, so its reservations drop and devices reserve again within moments. " +
			"Requires user confirmation per safety rule 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args SetP2PRelayLimitsRequest) (*mcp.CallToolResult, any, error) {
		info, err := client.SetP2PRelayLimits(ctx, args.ID, args)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return jsonResult(info), nil, nil
	})
}
